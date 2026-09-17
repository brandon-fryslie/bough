package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/family"
)

// codexSitting writes a Codex rollout of one session that ran in cwd, holding
// as many prompts as asked for, an hour apart, the first at 09:00 plus from
// minutes on 1 August, each editing edit.
func codexSitting(t *testing.T, root, cwd, id string, from, prompts int, edit string) {
	t.Helper()
	dir := filepath.Join(root, "2026", "08", "01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	at := func(hour, minute int) string {
		return fmt.Sprintf("2026-08-01T%02d:%02d:00Z", 9+hour+(from+minute)/60, (from+minute)%60)
	}
	lines := []string{
		`{"timestamp":"` + at(0, 0) + `","type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `","timestamp":"` + at(0, 0) + `"}}`,
	}
	for i := 0; i < prompts; i++ {
		lines = append(lines,
			`{"timestamp":"`+at(i, 0)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"a request long enough to count as one, number `+strconv.Itoa(i)+`"}]}}`,
			`{"timestamp":"`+at(i, 5)+`","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Update File: `+edit+`\n@@\n-old\n+new\n*** End Patch\n"}}`,
			`{"timestamp":"`+at(i, 6)+`","type":"response_item","payload":{"type":"custom_tool_call_output","output":"Success"}}`,
		)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-"+id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bothAgents is a history root where Claude Code and Codex both worked in
// /work/app at the same time, Claude Code with three prompts from 09:00 to
// 11:00 and Codex with two from 09:30 to 10:30, and Claude Code alone worked
// in /writing/draft.
func bothAgents(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	sitting(t, root, app, 3, app+"/main.go")
	codexSitting(t, root, app, "codex-app", 30, 2, app+"/main.go")
	sitting(t, root, loneDraft, 5, loneDraft+"/chapter.md")
	return root
}

// ran runs bough and hands back what it wrote.
func ran(t *testing.T, args ...string) string {
	t.Helper()
	var out, errs bytes.Buffer
	if err := run(args, &out, &errs); err != nil {
		t.Fatalf("bough %q: %v\n%s", args, err, errs.String())
	}
	return out.String()
}

// rowsOf is a listing's project rows, with the columns' padding folded away.
func rowsOf(listing string) []string {
	var rows []string
	for _, line := range strings.Split(strings.TrimRight(listing, "\n"), "\n") {
		if !strings.HasPrefix(line, " ") {
			rows = append(rows, filepath.ToSlash(strings.Join(strings.Fields(line), " ")))
		}
	}
	return rows
}

// A directory both agents worked in is one project, listed once with each
// agent's own count.
func TestListShowsBothAgentsOnOneRow(t *testing.T) {
	root := bothAgents(t)
	got := rowsOf(ran(t, "--list", "--no-repo", "--root", root))
	want := []string{
		"app /work/app Claude Code 3 prompts and Codex 2 prompts",
		"draft /writing/draft Claude Code 5 prompts",
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows are\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The directories a project spans are counted once each, however many agents
// worked in them, and each directory names the agents that worked there.
func TestListCountsADirectoryOnceAcrossAgents(t *testing.T) {
	root := bothAgents(t)
	tree := app + "/.claude/worktrees/calm-river"
	codexSitting(t, root, tree, "codex-tree", 0, 1, tree+"/export.go")
	listing := ran(t, "--list", "-v", "--no-repo", "--root", root)

	if got := rowsOf(listing)[0]; got != "app /work/app Claude Code 3 prompts and Codex 3 prompts in 2 directories" {
		t.Errorf("app's row is %q", got)
	}
	folded := filepath.ToSlash(strings.Join(strings.Fields(listing), " "))
	for _, dir := range []string{app + " Claude Code 3 prompts and Codex 2 prompts", tree + " Codex 1 prompts"} {
		if !strings.Contains(folded, dir) {
			t.Errorf("the listing does not show %q:\n%s", dir, listing)
		}
	}
}

// --agent still narrows everything to one agent's history: the listing and
// the project it opens.
func TestAgentFlagNarrowsAFamilyToOneAgent(t *testing.T) {
	root := bothAgents(t)
	if got, want := rowsOf(ran(t, "--list", "--no-repo", "--agent", "codex", "--root", root)), []string{"app /work/app Codex 2 prompts"}; !slices.Equal(got, want) {
		t.Errorf("rows are %q, want %q", got, want)
	}

	g := agentGraph(t, ran(t, "app", "--json", "--no-repo", "--agent", "codex", "--root", root))
	if !slices.Equal(g.Project.Agents, []string{"codex"}) || g.Totals.Turns != 2 {
		t.Errorf("--agent codex opened agents %q with %d prompts, want Codex's 2", g.Project.Agents, g.Totals.Turns)
	}
	for _, goal := range g.Goals {
		if goal.Agent != "codex" {
			t.Errorf("--agent codex drew a sitting of %q", goal.Agent)
		}
	}
}

// A family both agents worked in opens as one graph, by name or by path, with
// both agents' sittings, each carrying its agent. The two sessions overlapped
// and stay two sittings.
func TestAFamilyOpensWithBothAgentsSittings(t *testing.T) {
	root := bothAgents(t)
	for _, arg := range []string{"app", app} {
		g := agentGraph(t, ran(t, arg, "--json", "--no-repo", "--root", root))
		if !slices.Equal(g.Project.Agents, []string{"claude-code", "codex"}) || g.Totals.Turns != 5 {
			t.Errorf("%s opened agents %q with %d prompts, want both agents' 5", arg, g.Project.Agents, g.Totals.Turns)
		}
		var whose []string
		for _, goal := range g.Goals {
			whose = append(whose, goal.Agent)
		}
		if !slices.Equal(whose, []string{"claude-code", "codex"}) {
			t.Errorf("%s drew sittings of %q, want Claude Code's and then Codex's, one each", arg, whose)
		}
	}
}

// The terminal marks each sitting with its agent, in time order across both.
// A family one agent worked in names its agent once and marks no sitting.
func TestTextMarksEachAgentsSittings(t *testing.T) {
	root := bothAgents(t)
	both := ran(t, "app", "--text", "--no-repo", "--root", root)
	claude, codex := strings.Index(both, "[Claude Code] "), strings.Index(both, "[Codex] ")
	if !strings.Contains(both, "from Claude Code and Codex") || claude < 0 || codex < claude {
		t.Errorf("want both agents named and their sittings marked in time order:\n%s", both)
	}

	one := ran(t, "draft", "--text", "--no-repo", "--root", root)
	if !strings.Contains(one, "from Claude Code\n") || strings.Contains(one, "[Claude Code]") {
		t.Errorf("want one agent named once and no sitting marked:\n%s", one)
	}
}

type agentJSON struct {
	Project struct {
		Agents []string `json:"agents"`
	} `json:"project"`
	Goals []struct {
		Agent string `json:"agent"`
	} `json:"goals"`
	Totals struct {
		Turns int `json:"turns"`
	} `json:"totals"`
}

func agentGraph(t *testing.T, out string) agentJSON {
	t.Helper()
	var g agentJSON
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	return g
}

// The chooser names every agent that worked on a project with its own share of
// the history, and counts a directory both worked in once.
func TestTheChooserNamesEveryAgentsShare(t *testing.T) {
	p := family.Project{Path: app, Members: []agent.Project{
		{Path: app, Source: "claude-code", Bytes: 3 << 20},
		{Path: app, Source: "codex", Bytes: 12 << 10},
		{Path: app + "/web", Source: "codex", Bytes: 4 << 10},
	}}
	if got, want := describe(p), "Claude Code 3 MB and Codex 16 KB, 2 directories"; got != want {
		t.Errorf("describe = %q, want %q", got, want)
	}
}
