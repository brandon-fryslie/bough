package codex

import (
	"cmp"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/agent/transcript"
)

var exitCodeRegex = regexp.MustCompile(`(?:Process exited with code|Command failed with exit code|Exit code:?)\s*(\d+)`)
var commitShaRegex = regexp.MustCompile(`\[[\w/-]+\s+([0-9a-f]{7,40})\]`)
var patchFileRegex = regexp.MustCompile(`(?m)^\*\*\*\s*(?:Add|Update|Delete)\s*File:\s*([^\r\n]+)`)
var execCmdRegex = regexp.MustCompile(`cmd:\s*"((?:\\.|[^"\\])*)"`)
var execWorkdirRegex = regexp.MustCompile(`workdir:\s*"((?:\\.|[^"\\])*)"`)

// ExtractTurns parses Codex rollout records into normalised agent.Turn instances.
// Records are deduplicated by item ID to prevent inflated counts upon session replay.
func ExtractTurns(recs []*Record) []agent.Turn {
	var turns []agent.Turn
	var cur *agent.Turn
	var currentModel string
	commits := transcript.Commits{}

	seenItems := make(map[string]bool)
	seenUsage := make(map[string]bool)

	// Whether this session uses the newer usage records decides which stream to
	// believe, and that has to be known before the first one is read.
	haveRecords := false
	for _, r := range recs {
		if r.Type == "token_usage_record" {
			haveRecords = true
			break
		}
	}

	for _, r := range recs {
		if r.Type == "turn_context" {
			var tc TurnContext
			if err := json.Unmarshal(r.Payload, &tc); err == nil && tc.Model != "" {
				currentModel = tc.Model
			}
			continue
		}

		// A turn opens on a request, and a request reaches an agent one of two
		// ways: a person types it, or another agent delegates it. A sub-agent
		// only ever gets the second kind, so counting the first alone leaves its
		// rollout empty and its work uncounted.
		if r.IsHumanPrompt() || r.IsNewTask() {
			var item ResponseItem
			_ = json.Unmarshal(r.Payload, &item)
			if item.ID != "" && seenItems[item.ID] {
				continue
			}
			if item.ID != "" {
				seenItems[item.ID] = true
			}

			// A sub-agent's request was handed over, not typed, so it has a
			// task name and no prompt. The two readers each answer empty for
			// the kind of request they do not recognise.
			turns = append(turns, agent.Turn{
				At:       r.Time(),
				Text:     r.PromptText(),
				TaskName: r.TaskName(),
				Tools:    map[string]int{},
				Files:    map[string]int{},
				Edits:    map[string]int{},
				Lines:    map[string]int{},
				Models:   map[string]int{},
			})
			cur = &turns[len(turns)-1]
			continue
		}

		if cur == nil {
			continue
		}

		if r.Type == "event_msg" || r.Type == "token_usage_record" {
			handleTokenCount(cur, r, currentModel, seenUsage, haveRecords)
			continue
		}

		if r.Type == "response_item" {
			var item ResponseItem
			if err := json.Unmarshal(r.Payload, &item); err != nil {
				continue
			}
			if item.ID != "" && seenItems[item.ID] {
				continue
			}
			if item.ID != "" {
				seenItems[item.ID] = true
			}

			switch item.Type {
			case "function_call", "custom_tool_call":
				handleToolCall(cur, &item, len(turns)-1, commits)
			case "function_call_output", "custom_tool_call_output":
				handleToolOutput(cur, &item, r.Time(), turns, commits)
			}
		}
	}

	return turns
}

// handleTokenCount adds one response's usage to the turn.
//
// Two streams carry the same numbers. Older rollouts report usage in an
// event_msg of type "token_count", under info.last_token_usage. Newer ones also
// write a token_usage_record line of their own, carrying "usage" for the
// response that just finished.
//
// Where both appear, only the record counts. They report identical figures, and
// adding both doubles every total in the graph. The record is preferred because
// it carries a response_id, which is what lets a replayed response be
// recognised as one already counted; the event carries no id at all.
//
// The per-response figure is the one added. A running total is also available
// and is the wrong thing to sum: adding it once per response counts the first
// response as many times as there are responses.
func handleTokenCount(cur *agent.Turn, r *Record, currentModel string, seen map[string]bool, haveRecords bool) {
	var usage tokenUsage
	var id string

	switch r.Type {
	case "token_usage_record":
		var rec struct {
			ResponseID string     `json:"response_id"`
			Usage      tokenUsage `json:"usage"`
		}
		if err := json.Unmarshal(r.Payload, &rec); err != nil {
			return
		}
		usage, id = rec.Usage, rec.ResponseID
	case "event_msg":
		if haveRecords {
			return
		}
		var evt struct {
			Type string `json:"type"`
			Info struct {
				LastTokenUsage tokenUsage `json:"last_token_usage"`
			} `json:"info"`
		}
		if err := json.Unmarshal(r.Payload, &evt); err != nil || evt.Type != "token_count" {
			return
		}
		usage = evt.Info.LastTokenUsage
	default:
		return
	}

	if id != "" {
		if seen[id] {
			return
		}
		seen[id] = true
	}

	// Codex's input_tokens is the whole input, cached part included, which is
	// not what agent.Tokens means by Input: there the four fields are disjoint
	// and Total adds them up. Adding both fields as they arrive counted the
	// cached tokens twice and roughly doubled every Codex figure. One real
	// record read 28,739 input against 28,032 cached with a total of 28,750,
	// so the cache is 97% of the input rather than something beside it.
	//
	// Claude's API reports these already separated, which is the shape the
	// core expects, so the subtraction happens here rather than the meaning
	// changing for everyone.
	fresh := usage.InputTokens - usage.CachedInputTokens
	if fresh < 0 {
		// Only reachable if a record disagrees with itself. Trusting the
		// smaller number keeps the total honest rather than negative.
		fresh = 0
	}
	cur.Tokens.Input += fresh
	cur.Tokens.CacheRead += usage.CachedInputTokens
	cur.Tokens.Output += usage.OutputTokens
	if currentModel != "" && usage.OutputTokens > 0 {
		cur.Models[currentModel] += usage.OutputTokens
	}
}

func handleToolCall(cur *agent.Turn, item *ResponseItem, turnIdx int, commits transcript.Commits) {
	cur.Tools[item.Name]++

	switch item.Name {
	case "apply_patch":
		applyPatch(cur, item.Input)
	case "exec":
		handleExec(item.Input, item.CallID, turnIdx, commits)
	case "exec_command", "shell_command":
		handleCommand(item.Arguments, item.CallID, turnIdx, commits)
	case "spawn_agent":
		handleSpawnAgent(cur, item.Arguments)
	}
}

// applyPatch reads a Codex patch and credits each file with what changed in it.
//
// A patch may carry several files, each opened by its own "*** Add File:"
// header, and the lines that follow belong to whichever header came last.
// Counting the whole patch and putting the total on the first file named it a
// rewrite and left the others looking untouched, which is exactly the wrong
// answer for a score that reads how much a file moved.
//
// Removals count as well as additions. Deleting code is work, and the Claude
// side has always counted both, so a file changed by the same amount should
// read the same whichever agent did it.
func applyPatch(cur *agent.Turn, input string) {
	at := patchFileRegex.FindAllStringSubmatchIndex(input, -1)
	if len(at) == 0 {
		return
	}

	for i, m := range at {
		file := agent.NormalisePath(strings.TrimSpace(input[m[2]:m[3]]))
		if file == "" {
			continue
		}
		cur.Files[file]++
		cur.Edits[file]++

		// This file's hunk runs to the next header, or to the end.
		to := len(input)
		if i+1 < len(at) {
			to = at[i+1][0]
		}
		if n := transcript.ChangedLines(strings.Split(input[m[1]:to], "\n")); n > 0 {
			cur.Lines[file] += n
		}
	}
}

func handleExec(input, callID string, turnIdx int, commits transcript.Commits) {
	call := transcript.Call{Turn: turnIdx, ID: callID}
	if m := execCmdRegex.FindStringSubmatch(input); len(m) > 1 {
		call.Command = strings.ReplaceAll(m[1], `\"`, `"`)
	}
	if m := execWorkdirRegex.FindStringSubmatch(input); len(m) > 1 {
		call.Workdir = strings.ReplaceAll(m[1], `\"`, `"`)
	}
	commits.Call(call)
}

func handleCommand(args json.RawMessage, callID string, turnIdx int, commits transcript.Commits) {
	var parsed struct {
		Cmd     string `json:"cmd"`
		Command string `json:"command"`
		Workdir string `json:"workdir"`
		Cwd     string `json:"cwd"`
	}
	if len(args) > 0 {
		var rawStr string
		if err := json.Unmarshal(args, &rawStr); err == nil {
			_ = json.Unmarshal([]byte(rawStr), &parsed)
		} else {
			_ = json.Unmarshal(args, &parsed)
		}
	}
	commits.Call(transcript.Call{
		Turn:    turnIdx,
		ID:      callID,
		Command: cmp.Or(parsed.Cmd, parsed.Command),
		Workdir: cmp.Or(parsed.Workdir, parsed.Cwd),
	})
}

func handleSpawnAgent(cur *agent.Turn, args json.RawMessage) {
	var parsed struct {
		AgentType string `json:"agent_type"`
		TaskName  string `json:"task_name"`
		Message   string `json:"message"`
	}
	var rawStr string
	if err := json.Unmarshal(args, &rawStr); err == nil {
		_ = json.Unmarshal([]byte(rawStr), &parsed)
	} else {
		_ = json.Unmarshal(args, &parsed)
	}
	// The brief is encrypted when one agent spawns another, so there is nothing
	// to show. The delegation is still recorded: that the work was handed off is
	// worth knowing even when what was asked for is not readable.
	desc := parsed.Message
	if Encrypted(desc) {
		desc = ""
	}
	cur.Delegated = append(cur.Delegated, agent.Delegation{
		Kind:        parsed.AgentType,
		Name:        parsed.TaskName,
		Description: desc,
	})
}

func handleToolOutput(cur *agent.Turn, item *ResponseItem, at time.Time, turns []agent.Turn, commits transcript.Commits) {
	outputStr := ""
	var rawStr string
	if err := json.Unmarshal(item.Output, &rawStr); err == nil {
		outputStr = rawStr
	} else {
		var items []ContentItem
		if err := json.Unmarshal(item.Output, &items); err == nil {
			var sb strings.Builder
			for _, ci := range items {
				sb.WriteString(ci.Text)
			}
			outputStr = sb.String()
		}
	}

	isError := false
	if m := exitCodeRegex.FindStringSubmatch(outputStr); len(m) > 1 {
		code, _ := strconv.Atoi(m[1])
		if code != 0 {
			isError = true
		}
	}

	if isError {
		cur.Errors++
	}

	// Codex keeps git's printout and nothing parsed from it, so the hash is
	// read back out of that. It never says which kind of commit it saw.
	res := transcript.Result{CallID: item.CallID, At: at, Failed: isError}
	if sm := commitShaRegex.FindStringSubmatch(outputStr); len(sm) > 1 {
		res.Reported.SHA = sm[1]
	}
	commits.Settle(turns, res)
}
