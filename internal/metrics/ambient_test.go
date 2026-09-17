package metrics

import (
	"strings"
	"testing"

	"github.com/nickelsec/bough/internal/agent"
)

// worktree is an agent that made one directory, read the way agent.MadeFor
// promises: anywhere inside it, with what lies below.
func worktree(dir string) agent.MadeFor {
	return func(p string) (agent.Made, bool) {
		rest, ok := strings.CutPrefix(p, dir)
		if !ok || (rest != "" && !strings.HasPrefix(rest, "/")) {
			return agent.Made{}, false
		}
		return agent.Made{For: func(string) bool { return false }, Within: rest}, true
	}
}

func TestAmbient(t *testing.T) {
	ambience := Ambience{Made: []agent.MadeFor{
		worktree("/home/x/code/app/.claude/worktrees/calm-river"),
		// A repository kept at the home directory gets its worktrees beside
		// the agent's own plans, and they are still checkouts of it.
		worktree("/home/x/.claude/worktrees/tidy-otter"),
		worktree("C:/Users/x/code/app/.claude/worktrees/calm-river"),
	}}

	for p, want := range map[string]bool{
		// The agent's own directory, wherever the file sits in it.
		"/home/x/.claude/plans/session.md":                         true,
		"/home/x/.claude/projects/-home-x-code-app/memory/note.md": true,
		`C:\Users\x\.claude\projects\p\memory\notes.md`:            true,

		// A repository's own agent settings.
		"/home/x/code/app/.claude/settings.json": true,

		// Named bookkeeping, in a repository or a worktree of it.
		"/proj/CHANGELOG.md": true,
		"/proj/memory.md":    true,
		"/proj/Cargo.lock":   true,
		"/proj/CLAUDE.md":    true,
		"/home/x/code/app/.claude/worktrees/calm-river/CLAUDE.md":             true,
		"/home/x/code/app/.claude/worktrees/calm-river/.claude/settings.json": true,

		// The user's work.
		"/proj/src/main.go":                         false,
		"/proj/ui/hero.css":                         false,
		"/proj/Cargo.toml":                          false,
		"/proj/docs/format.md":                      false,
		"/home/x/code/app/internal/memory/store.go": false,

		// The user's work in a worktree, which sits under a .claude directory.
		"/home/x/code/app/.claude/worktrees/calm-river/internal/metrics/ambient.go": false,
		"/home/x/code/app/.claude/worktrees/calm-river/internal/memory/store.go":    false,
		`C:\Users\x\code\app\.claude\worktrees\calm-river\cmd\main.go`:              false,
		"/home/x/.claude/worktrees/tidy-otter/src/main.go":                          false,
	} {
		if got := ambience.Ambient(p); got != want {
			t.Errorf("Ambient(%q) = %v, want %v", p, got, want)
		}
	}
}
