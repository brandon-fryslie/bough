package claude

import (
	"regexp"
	"strings"

	"github.com/nickelsec/bough/internal/agent"
)

// Claude Code makes two kinds of directory on a project's behalf, and a
// session can start in either, which leaves the project's history spread over
// directories that are not projects. Both carry the project's name in the
// path; this reads it back out as a question the core can ask of the projects
// it knows, since the name is written in a form that cannot be reversed.

// scratchpad is a session's scratch space, /tmp/claude-<uid>/<mangled
// cwd>/<session id>/scratchpad, sometimes with a subdirectory beneath. The
// project it belongs to is named in the same form as the history directory.
var scratchpad = regexp.MustCompile(`/claude-\d+/([^/]+)/[0-9a-fA-F-]{36}/scratchpad(?:/|$)`)

// worktree is a checkout Claude Code made under a project's own .claude
// directory. Here the project is the path above it, written out in full.
var worktree = regexp.MustCompile(`^(.*)/\.claude/worktrees/[^/]+(?:/|$)`)

// mangle writes a path the way Claude Code names its history directory for
// it: every character that is not a letter or digit becomes a dash. It is
// lossy (docs/format.md), which is why the core is handed a comparison and
// not a path.
var mangle = regexp.MustCompile(`[^A-Za-z0-9]`)

// serves is the record Claude Code left in a working directory of which
// project it was made for, as a question about a candidate, or nil for a
// directory that is a project in its own right.
func serves(path string) func(string) bool {
	path = strings.ReplaceAll(path, `\`, "/")
	if m := scratchpad.FindStringSubmatch(path); m != nil {
		name := m[1]
		return func(candidate string) bool {
			// Both spellings of a Windows drive letter are written the same
			// way by the agent, and the transcript is not consistent about
			// which it records.
			return strings.EqualFold(mangle.ReplaceAllString(candidate, "-"), name)
		}
	}
	if m := worktree.FindStringSubmatch(path); m != nil {
		parent := agent.NormalisePath(m[1])
		return func(candidate string) bool {
			return agent.NormalisePath(candidate) == parent
		}
	}
	return nil
}
