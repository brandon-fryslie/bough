// Package codex reads OpenAI Codex CLI session history.
//
// Codex stores conversation transcripts as JSONL rollout files under
// ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl.
package codex

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/nickelsec/bough/internal/agent/transcript"
)

// Record is one line of a Codex rollout transcript.
type Record struct {
	Timestamp string          `json:"timestamp"`
	Ordinal   int             `json:"ordinal"`
	Type      string          `json:"type"` // "session_meta", "response_item", "event_msg", "turn_context"
	Payload   json.RawMessage `json:"payload"`
}

// SessionMeta holds session initialization metadata.
//
// ID names this rollout. SessionID names the conversation it belongs to, and
// the two differ in two quite different situations, which is why both are read
// rather than one being preferred outright.
//
// Resuming a session writes a new rollout carrying the original SessionID, and
// those really are one session: the new file replays the earlier items under
// the ids they already had, and grouping on SessionID is what stops the replay
// being counted twice.
//
// Spawning a subagent also writes a rollout carrying the parent's SessionID,
// and that is a different thing wearing the same clothes. The subagent has its
// own prompts, its own tools and its own token spend, and folding it into the
// parent hides work that did happen rather than removing work that did not.
// ParentThreadID is what separates the two cases.
type SessionMeta struct {
	ID             string `json:"id"`
	SessionID      string `json:"session_id"`
	ParentThreadID string `json:"parent_thread_id"`
	Cwd            string `json:"cwd"`
	Timestamp      string `json:"timestamp"`
	Model          string `json:"model"`
}

// ResponseItem represents messages, tool calls, and tool outputs.
type ResponseItem struct {
	Type      string          `json:"type"` // "message", "function_call", "custom_tool_call", "function_call_output", "custom_tool_call_output", "reasoning"
	ID        string          `json:"id"`
	Role      string          `json:"role"` // "user", "assistant", "developer"
	Name      string          `json:"name"` // "exec", "apply_patch", "exec_command", "shell_command", "spawn_agent"
	Input     string          `json:"input"`
	Arguments json.RawMessage `json:"arguments"`
	Content   []ContentItem   `json:"content"`
	Output    json.RawMessage `json:"output"`
	CallID    string          `json:"call_id"`
}

// ContentItem is one chunk of content in a message or tool output.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// TurnContext carries context including the active model.
type TurnContext struct {
	Model string `json:"model"`
	Cwd   string `json:"cwd"`
}

// tokenUsage is what one model response cost. Both the event_msg and the
// token_usage_record shapes spell the fields the same way.
type tokenUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

// TokenCountInfo holds token usage metrics from an event_msg.
type TokenCountInfo struct {
	Type string `json:"type"`
	Info struct {
		LastTokenUsage tokenUsage `json:"last_token_usage"`
	} `json:"info"`
}

// Time parses the timestamp on the record.
func (r *Record) Time() time.Time {
	return transcript.Time(r.Timestamp)
}

var commandTagRegex = regexp.MustCompile(`(?s)<command>\s*(.*?)\s*</command>`)

// IsHumanPrompt reports whether this record represents a human request.
func (r *Record) IsHumanPrompt() bool {
	return r.PromptText() != ""
}

// PromptText returns what a person typed, omitting system injections and
// context headers. Any record that is not a user message has none.
func (r *Record) PromptText() string {
	item, ok := r.item("message")
	if !ok || item.Role != "user" {
		return ""
	}
	return cleanPrompt(r.extractUserText(&item))
}

// item reads the record as a response item of one type.
func (r *Record) item(kind string) (ResponseItem, bool) {
	var item ResponseItem
	if r.Type != "response_item" || json.Unmarshal(r.Payload, &item) != nil || item.Type != kind {
		return ResponseItem{}, false
	}
	return item, true
}

// extractUserText joins the chunks of a user message.
//
// Chunks are separated rather than run together, because a record holding an
// injected block followed by a typed line would otherwise present the two as
// one string with no boundary between them.
func (r *Record) extractUserText(item *ResponseItem) string {
	var parts []string
	for _, c := range item.Content {
		if c.Type == "input_text" || c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// injectedTag opens a block the harness put in front of the model rather than
// something a person typed.
//
// These arrive as ordinary user messages, and the desktop app sends several in
// one record: a plugin catalogue, then the environment, then whatever else the
// session needs. Matching only the first one was enough while a record held a
// single block, and it stopped being enough when the app began concatenating
// them. A record whose first chunk was the plugin list read as a prompt and was
// counted as one, which is where the extra prompt in every session came from.
//
// So the test is per block rather than on the joined text, and a record is a
// prompt only if something survives once every injected block is removed.
// The block is matched from its opening tag to its own closing tag. Go's
// regexp has no backreferences, so the closing tag is spelled out per block
// rather than tied back to the opening one. A loose `</[^>]+>` would stop at
// the first nested close, since these blocks do carry markup inside them, and
// leave a dangling fragment behind.
var injectedTag = regexp.MustCompile(`(?s)<(recommended_plugins)>.*?</recommended_plugins>` +
	`|(?s)<(environment_context)>.*?</environment_context>` +
	`|(?s)<(permissions instructions)>.*?</permissions instructions>` +
	`|(?s)<(app-context)>.*?</app-context>` +
	`|(?s)<(skills_instructions)>.*?</skills_instructions>` +
	`|(?s)<(collaboration_mode)>.*?</collaboration_mode>` +
	`|(?s)<(multi_agent_role)>.*?</multi_agent_role>` +
	`|(?s)<(multi_agent_mode)>.*?</multi_agent_mode>` +
	`|(?s)<(user_instructions)>.*?</user_instructions>` +
	`|(?s)<(INSTRUCTIONS)>.*?</INSTRUCTIONS>`)

// encrypted matches a payload Codex stored as ciphertext rather than text.
//
// The multi-agent feature encrypts what one agent sends another: the brief in
// a spawn_agent call, and the task an agent_message delivers. Both are Fernet
// tokens, base64url with a fixed version byte, so they begin gAAAAA and run for
// a few hundred characters with no spaces in them.
//
// bough cannot read these and should not try. What it must not do is print one
// as though it were something a person wrote, which is how a wall of ciphertext
// ended up as the label on a goal.
var encrypted = regexp.MustCompile(`^gAAAAA[A-Za-z0-9_-]{32,}={0,2}$`)

// Encrypted reports whether text is a stored ciphertext rather than prose.
func Encrypted(s string) bool {
	return encrypted.MatchString(strings.TrimSpace(s))
}

// injectedPrefix marks text that opens a harness block but never closes it.
// Not every injection is a well formed pair, and an opening alone is enough to
// know the block was not typed by a person.
var injectedPrefix = []string{
	"# AGENTS.md",
	"<INSTRUCTIONS>",
	"<environment_context>",
	"<permissions instructions>",
	"<recommended_plugins>",
	"<app-context>",
	"<skills_instructions>",
	"<collaboration_mode>",
	"<multi_agent_role>",
	"<multi_agent_mode>",
	"Reviewed Codex session id:",
	"The Codex agent has requested the following action:",
}

func cleanPrompt(text string) string {
	text = strings.TrimSpace(injectedTag.ReplaceAllString(text, ""))
	if text == "" {
		return ""
	}
	for _, p := range injectedPrefix {
		if strings.HasPrefix(text, p) {
			return ""
		}
	}
	// Ciphertext is not a prompt. Showing it would put a few hundred characters
	// of base64 where the reader expects the thing they asked for.
	if Encrypted(text) {
		return ""
	}
	if m := commandTagRegex.FindStringSubmatch(text); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return text
}

// taskName picks the task out of the envelope wrapped round a delegated brief.
// The gap after the colon stays on its line, so an envelope with the name left
// blank does not read the next header as the name.
var taskName = regexp.MustCompile(`(?m)^Task name:[ \t]*(.+)$`)

// IsNewTask reports whether this record is a fresh brief handed to a sub-agent.
//
// A sub-agent's rollout has no user message in it at all. The work it was asked
// to do arrives as an agent_message from whoever spawned it, so treating only
// user messages as prompts leaves the whole session with nothing in it, and a
// session with no turns is dropped. That is how a spawned agent's work went
// missing rather than merely being mislabelled.
func (r *Record) IsNewTask() bool {
	_, ok := r.newTask()
	return ok
}

// TaskName names the task a sub-agent was given, and is empty for any record
// that is not a fresh brief or whose envelope names nothing.
//
// The brief itself is encrypted, but the envelope round it is not, and it
// carries the task name: what the work was called by the agent that handed it
// over. It is not a prompt, since nobody typed it.
func (r *Record) TaskName() string {
	envelope, ok := r.newTask()
	if !ok {
		return ""
	}
	if m := taskName.FindStringSubmatch(envelope); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// newTask returns the envelope of a fresh brief handed to a sub-agent.
//
// Agents message each other in both directions, and a reply is not new work.
// Counting every message opened a turn on the parent each time a sub-agent
// reported back, inventing a request nobody made.
func (r *Record) newTask() (string, bool) {
	item, ok := r.item("agent_message")
	if !ok {
		return "", false
	}
	envelope := r.extractUserText(&item)
	return envelope, strings.Contains(envelope, "Message Type: NEW_TASK")
}
