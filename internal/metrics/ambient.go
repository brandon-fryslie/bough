package metrics

import (
	"path"
	"strings"

	"github.com/nickelsec/bough/internal/agent"
)

// Ambience tells the files an agent keeps from the ones the user was working
// on.
//
// Plan files, memory notes and changelogs get rewritten constantly as a side
// effect of how the agent works, so they float to the top of any measure based
// on how often a file changed. On the history this was built against, the most
// rewritten file in nearly every project was the agent's own plan: 48 rewrites
// in one, 29 in another. Counting those as effort says the user struggled with
// a scratchpad.
//
// The user's own README or changelog does get filtered out along with them.
// That is the right trade: someone reading their history wants to see the work,
// and documentation churn is rarely the part they remember.
type Ambience struct {
	// Made is every agent's reading of the directories it makes on a
	// project's behalf, whichever agent's history is being read: a session of
	// one agent can work in a directory another made.
	//
	// A file in one of those is judged from that directory down. Claude Code
	// puts a worktree under the project's .claude directory, which is where
	// the agent's own bookkeeping lives too, and judging the whole path threw
	// away every edit made in a worktree as the agent's own.
	Made []agent.MadeFor
}

// Ambient reports whether a file is one the agent keeps rather than one the
// user was working on.
//
// Paths come out of transcripts rather than off this machine, so a Windows
// path has to be read the same way on a Linux box as on the machine that
// wrote it. path/filepath would only split on the host's own separator.
func (a Ambience) Ambient(p string) bool {
	within := strings.ToLower(a.within(strings.ReplaceAll(p, `\`, "/")))

	if ambientNames[path.Base(within)] {
		return true
	}
	// Plan files are named after the session that made them and live in the
	// agent's own directory, so the name is unpredictable but the location is not.
	for _, marker := range ambientDirs {
		if strings.Contains(within, marker.dir) && strings.HasSuffix(within, marker.suffix) {
			return true
		}
	}
	return false
}

// within is the part of a path that says what kind of file it is: below the
// directory an agent made it in, or the whole path when it is in none.
func (a Ambience) within(p string) string {
	for _, madeFor := range a.Made {
		if made, ok := madeFor(p); ok {
			return made.Within
		}
	}
	return p
}

var ambientNames = map[string]bool{
	"memory.md":         true,
	"changelog.md":      true,
	"claude.md":         true,
	"agents.md":         true,
	"todo.md":           true,
	"notes.md":          true,
	"cargo.lock":        true,
	"package-lock.json": true,
	"pnpm-lock.yaml":    true,
	"yarn.lock":         true,
	"go.sum":            true,
	"poetry.lock":       true,
}

// ambientDirs are locations that only ever hold an agent's own bookkeeping,
// each with the ending of the files it keeps there.
//
// A memory directory holds notes. A package named memory holds code, and
// counting every directory of that name hid a repository's storage layer
// along with the agent's notes, so only the notes are matched there.
// [LAW:dataflow-not-control-flow] Every marker is the same check; an empty
// suffix matches any file.
var ambientDirs = []struct{ dir, suffix string }{
	{"/.claude/", ""},
	{"/.cursor/", ""},
	{"/.codex/", ""},
	{"/memory/", ".md"},
}
