package claude

import (
	"testing"
)

func TestServesReadsTheProjectOutOfAScratchpad(t *testing.T) {
	made, ok := serves("/private/tmp/claude-501/-Users-bmf-code-promptctl-laws/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad/probe")
	if !ok {
		t.Fatal("a scratchpad path was not recognised")
	}
	ask := made.For
	// Both of these mangle to the name the scratchpad carries, which is the
	// ambiguity the core has to be handed rather than have hidden from it.
	for _, p := range []string{"/Users/bmf/code/promptctl-laws", "/Users/bmf/code/promptctl_laws", "/users/BMF/code/promptctl-laws"} {
		if !ask(p) {
			t.Errorf("%q should match the scratchpad's project", p)
		}
	}
	for _, p := range []string{"/Users/bmf/code/promptctl", "/Users/bmf/code/promptctl-laws/plugins"} {
		if ask(p) {
			t.Errorf("%q should not match the scratchpad's project", p)
		}
	}
}

func TestServesReadsTheProjectOutOfAWorktree(t *testing.T) {
	made, ok := serves("/Users/bmf/code/happy/.claude/worktrees/calm-sparking-floyd/environments/data/envs/bold-reef/project")
	if !ok {
		t.Fatal("a worktree path was not recognised")
	}
	ask := made.For
	if !ask("/Users/bmf/code/happy") || !ask(`\Users\bmf\code\HAPPY`) {
		t.Error("the directory the worktree sits under should match, however spelled")
	}
	if ask("/Users/bmf/code/happy/.claude") || ask("/Users/bmf/code") {
		t.Error("neither the .claude directory nor the parent of the project is the project")
	}
}

// A project in its own right, and the storage directory a project falls back
// to when no record names its cwd, record nothing.
func TestServesIsNilForAProjectOfItsOwn(t *testing.T) {
	for _, p := range []string{
		"/Users/bmf/code/happy",
		"/Users/bmf/.claude/projects/-Users-bmf-code-happy",
		"/Users/bmf/.claude/plugins/cache/memento/memento/0.1.2/skills/address-pr-reviews",
		"/Users/bmf/.claude/plans/tidy-otter.md",
		"/private/tmp/happy-testing-ground-17cbe9ce",
	} {
		if _, ok := serves(p); ok {
			t.Errorf("%q should record no project", p)
		}
	}
}

// The agent hands the record to the core, and a project it detects carries
// nothing agent-specific.
func TestAgentReadsWhichProjectADirectoryWasMadeFor(t *testing.T) {
	made, ok := Agent().MadeFor("/private/tmp/claude-501/-Users-bmf-code-happy/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad/probe.go")
	if !ok || !made.For("/Users/bmf/code/happy") {
		t.Error("a file in a scratchpad should name the project the scratchpad was made for")
	}
}

// What lies below a directory the agent made is read from the directory down,
// so where the directory sits says nothing about the files in it. A worktree
// made inside another is read from the inner one.
func TestServesSaysWhatLiesBelowTheDirectory(t *testing.T) {
	for p, want := range map[string]string{
		"/Users/bmf/code/happy/.claude/worktrees/calm-sparking-floyd":                                 "",
		"/Users/bmf/code/happy/.claude/worktrees/calm-sparking-floyd/internal/memory/store.go":        "/internal/memory/store.go",
		`C:\Users\bmf\code\happy\.claude\worktrees\calm-sparking-floyd\.claude\settings.json`:         "/.claude/settings.json",
		"/Users/bmf/code/happy/.claude/worktrees/outer-name/.claude/worktrees/inner-name/cmd/main.go": "/cmd/main.go",
		// Only the worktree command writes .claude/worktrees/<name>, so one in
		// the agent's own directory is a checkout of a repository kept at home.
		"/Users/bmf/.claude/worktrees/tidy-otter/src/main.go":                                                "/src/main.go",
		"/private/tmp/claude-501/-Users-bmf-code-happy/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad":      "",
		"/private/tmp/claude-501/-Users-bmf-code-happy/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad/a.go": "/a.go",
	} {
		made, ok := serves(p)
		if !ok {
			t.Errorf("%q was not recognised", p)
			continue
		}
		if made.Within != want {
			t.Errorf("below %q: got %q, want %q", p, made.Within, want)
		}
	}
}
