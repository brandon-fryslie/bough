package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServesReadsTheProjectOutOfAScratchpad(t *testing.T) {
	ask := serves("/private/tmp/claude-501/-Users-bmf-code-promptctl-laws/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad/probe")
	if ask == nil {
		t.Fatal("a scratchpad path was not recognised")
	}
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
	ask := serves("/Users/bmf/code/happy/.claude/worktrees/calm-sparking-floyd/environments/data/envs/bold-reef/project")
	if ask == nil {
		t.Fatal("a worktree path was not recognised")
	}
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
		"/private/tmp/happy-testing-ground-17cbe9ce",
	} {
		if serves(p) != nil {
			t.Errorf("%q should record no project", p)
		}
	}
}

// Detect carries the record out on the project it reports.
func TestDetectRecordsWhichProjectAScratchpadServes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-private-tmp-claude-501--Users-bmf-code-happy-1d56911b-b2f0-46e1-96a3-e1622bc1875c-scratchpad")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "1d56911b-b2f0-46e1-96a3-e1622bc1875c.jsonl"),
		`{"uuid":"a","type":"user","promptId":"p1",`+
			`"cwd":"/private/tmp/claude-501/-Users-bmf-code-happy/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad",`+
			`"message":{"role":"user","content":[{"type":"text","text":"text-a1b2"}]}}`)

	projects, err := Source{Root: root}.Detect()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Serves == nil {
		t.Fatalf("got %d projects, want one that records its project", len(projects))
	}
	if !projects[0].Serves("/Users/bmf/code/happy") {
		t.Error("the scratchpad should say it serves the project its path names")
	}
}
