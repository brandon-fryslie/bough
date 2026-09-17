package family

import (
	"regexp"
	"strings"
	"testing"

	"github.com/nickelsec/bough/internal/agent"
)

// disk is a machine described by hand: which directories exist, and which
// repository each existing one sits in.
type disk struct {
	t     *testing.T
	dirs  []string          // directories that exist
	repos map[string]string // repository root, an existing directory, to its main working tree
}

func (d disk) Exists(dir string) bool {
	for _, have := range d.dirs {
		if have == dir {
			return true
		}
	}
	return false
}

func (d disk) MainTree(dir string) string {
	// The seam promises to ask only about directories that exist. A resolver
	// that asked about a deleted one would be reading an answer git could
	// never give.
	if !d.Exists(dir) {
		d.t.Errorf("MainTree(%q) asked about a directory that does not exist", dir)
	}
	for root, tree := range d.repos {
		if dir == root || strings.HasPrefix(dir, root+"/") {
			return tree
		}
	}
	return ""
}

// mangled writes a path the way one agent names it in its own storage, every
// separator and punctuation mark folded to a dash. It stands in for the
// agent's matcher here: the resolver never sees this function, only the
// question it answers, which is the whole point of the seam.
func mangled(p string) string {
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(p, "-")
}

// scratchpadFor is the record an agent leaves on a scratchpad: the project's
// name in the agent's own lossy form.
func scratchpadFor(name string) func(string) bool {
	return func(candidate string) bool { return mangled(candidate) == name }
}

// worktreeOf is the record an agent leaves on a worktree it made under a
// project: the project's path, which it knows outright.
func worktreeOf(parent string) func(string) bool {
	return func(candidate string) bool {
		return agent.NormalisePath(candidate) == agent.NormalisePath(parent)
	}
}

func project(path string, serves func(string) bool) agent.Project {
	return agent.Project{Name: path[strings.LastIndex(path, "/")+1:], Path: path, Serves: serves}
}

// A history written by hand. The names are real projects; the paths and their
// arrangement are the cases the rules have to hold for.
var (
	history = []agent.Project{
		// Both have history and contain everything below them.
		project("/Users/bmf", nil),
		project("/Users/bmf/code", nil),

		// A repository and a subdirectory a session started in.
		project("/Users/bmf/code/textual-js", nil),
		project("/Users/bmf/code/textual-js/visual-tests", nil),

		// A fork with a shared name, and its worktree, which has been deleted.
		project("/Users/bmf/code/happy", nil),
		project("/Users/bmf/code/brandon-fryslie_happy", nil),
		project("/Users/bmf/code/happy/.claude/worktrees/calm-sparking-floyd",
			worktreeOf("/Users/bmf/code/happy")),

		// Codex saw a directory whose name mangles the same as the fork.
		project("/Users/bmf/code/brandon-fryslie/happy", nil),

		// A linked worktree whose main tree has no history of its own.
		project("/Users/bmf/wt/low-talker-fix", nil),

		// Scratchpads: one names textual-js, one names the fork ambiguously,
		// one names the deleted worktree. Another agent started inside the
		// first and recorded nothing about it.
		project("/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad",
			scratchpadFor("-Users-bmf-code-textual-js")),
		project("/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad/probe", nil),
		project("/private/tmp/claude-501/-Users-bmf-code-brandon-fryslie-happy/8074a0b7-19ec-4b75-bd91-a09e32227e4d/scratchpad",
			scratchpadFor("-Users-bmf-code-brandon-fryslie-happy")),
		project("/private/tmp/claude-501/-Users-bmf-code-happy--claude-worktrees-calm-sparking-floyd/859818fc-1f79-47fa-9a8b-12b41eeb2b0e/scratchpad",
			scratchpadFor("-Users-bmf-code-happy--claude-worktrees-calm-sparking-floyd")),

		// Deleted, with no repository anywhere above it.
		project("/Users/bmf/code/gone", nil),

		// A project whose path fell back to the agent's own storage.
		project("/Users/bmf/.claude/projects/-Users-bmf-Desktop-notes", nil),

		// Two installs of one plugin.
		project("/Users/bmf/.claude/plugins/cache/memento/memento/0.1.2/skills/address-pr-reviews", nil),
		project("/Users/bmf/.claude/plugins/cache/memento/memento/0.3.0/skills/address-pr-reviews", nil),

		// Two unrelated directories called docs.
		project("/Users/bmf/code/docs", nil),
		project("/Users/bmf/writing/docs", nil),

		// A deleted directory on a Windows drive, and a worktree made under it.
		project(`D:\work\site\gone`, nil),
		project(`D:\work\site\gone\.claude\worktrees\w`, worktreeOf(`D:\work\site\gone`)),
	}

	exists = []string{
		"/", "/Users", "/Users/bmf", "/Users/bmf/code",
		"/Users/bmf/code/textual-js", "/Users/bmf/code/textual-js/visual-tests",
		"/Users/bmf/code/happy", "/Users/bmf/code/happy/.claude",
		"/Users/bmf/code/brandon-fryslie_happy",
		"/Users/bmf/code/brandon-fryslie", "/Users/bmf/code/brandon-fryslie/happy",
		"/Users/bmf/code/low-talker", "/Users/bmf/wt", "/Users/bmf/wt/low-talker-fix",
		"/Users/bmf/code/deps", "/Users/bmf/code/deps/vendorlib", "/Users/bmf/code/deps/vendorlib/src",
		"/private", "/private/tmp", "/private/tmp/claude-501",
		"/private/tmp/claude-501/-Users-bmf-code-textual-js",
		"/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c",
		"/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad",
		"/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad/repro",
		"D:/", "D:/work", "D:/work/site",
		"/Users/bmf/.claude", "/Users/bmf/.claude/projects", "/Users/bmf/.claude/projects/-Users-bmf-Desktop-notes",
		"/Users/bmf/.claude/plugins", "/Users/bmf/.claude/plugins/cache",
		"/Users/bmf/.claude/plugins/cache/memento", "/Users/bmf/.claude/plugins/cache/memento/memento",
		"/Users/bmf/.claude/plugins/cache/memento/memento/0.1.2",
		"/Users/bmf/.claude/plugins/cache/memento/memento/0.1.2/skills",
		"/Users/bmf/.claude/plugins/cache/memento/memento/0.1.2/skills/address-pr-reviews",
		"/Users/bmf/.claude/plugins/cache/memento/memento/0.3.0",
		"/Users/bmf/.claude/plugins/cache/memento/memento/0.3.0/skills",
		"/Users/bmf/.claude/plugins/cache/memento/memento/0.3.0/skills/address-pr-reviews",
		"/Users/bmf/code/docs", "/Users/bmf/writing", "/Users/bmf/writing/docs",
	}

	repos = map[string]string{
		"/Users/bmf/code/textual-js":            "/Users/bmf/code/textual-js",
		"/Users/bmf/code/happy":                 "/Users/bmf/code/happy",
		"/Users/bmf/code/brandon-fryslie_happy": "/Users/bmf/code/brandon-fryslie_happy",
		"/Users/bmf/code/brandon-fryslie/happy": "/Users/bmf/code/brandon-fryslie/happy",
		"/Users/bmf/code/low-talker":            "/Users/bmf/code/low-talker",
		"/Users/bmf/wt/low-talker-fix":          "/Users/bmf/code/low-talker",
		"/Users/bmf/code/deps/vendorlib":        "/Users/bmf/code/deps/vendorlib",
		"D:/work/site":                          "D:/work/site",

		// A throwaway repository a session made inside its scratchpad.
		"/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad/repro": "/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad/repro",
	}
)

func TestResolve(t *testing.T) {
	cases := []struct {
		name string
		path string
		want Family
	}{
		{"a subdirectory shares its repository",
			"/Users/bmf/code/textual-js/visual-tests",
			Family{"/Users/bmf/code/textual-js", Repository}},
		{"a linked worktree joins its main tree",
			"/Users/bmf/wt/low-talker-fix",
			Family{"/Users/bmf/code/low-talker", Repository}},
		{"a linked worktree joins a main tree that has no history",
			"/Users/bmf/wt/low-talker-fix/src/main.go",
			Family{"/Users/bmf/code/low-talker", Repository}},
		{"a scratchpad joins the one project its name matches",
			"/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad",
			Family{"/Users/bmf/code/textual-js", Recorded}},
		{"a file inside a scratchpad goes where the scratchpad goes, past a project that recorded nothing",
			"/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad/probe/x.go",
			Family{"/Users/bmf/code/textual-js", Recorded}},
		{"a throwaway repository inside a scratchpad is still the scratchpad's project",
			"/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad/repro/x.go",
			Family{"/Users/bmf/code/textual-js", Recorded}},
		{"a scratchpad whose name matches two projects stays alone",
			"/private/tmp/claude-501/-Users-bmf-code-brandon-fryslie-happy/8074a0b7-19ec-4b75-bd91-a09e32227e4d/scratchpad",
			Family{"/private/tmp/claude-501/-Users-bmf-code-brandon-fryslie-happy/8074a0b7-19ec-4b75-bd91-a09e32227e4d/scratchpad", Project}},
		{"a deleted directory joins the repository above it",
			"/Users/bmf/code/happy/environments/data/envs/bold-reef/project",
			Family{"/Users/bmf/code/happy", Repository}},
		{"a deleted worktree the agent recorded joins by its record",
			"/Users/bmf/code/happy/.claude/worktrees/calm-sparking-floyd",
			Family{"/Users/bmf/code/happy", Recorded}},
		{"a deleted directory with no repository above it stays alone",
			"/Users/bmf/code/gone",
			Family{"/Users/bmf/code/gone", Project}},
		{"a scratchpad naming a deleted worktree joins that repository",
			"/private/tmp/claude-501/-Users-bmf-code-happy--claude-worktrees-calm-sparking-floyd/859818fc-1f79-47fa-9a8b-12b41eeb2b0e/scratchpad",
			Family{"/Users/bmf/code/happy", Recorded}},
		{"a project under the agent's own storage stays alone",
			"/Users/bmf/.claude/projects/-Users-bmf-Desktop-notes",
			Family{"/Users/bmf/.claude/projects/-Users-bmf-Desktop-notes", Project}},
		{"a path inside a repository with no history is that repository",
			"/Users/bmf/code/deps/vendorlib/src/lib.go",
			Family{"/Users/bmf/code/deps/vendorlib", Repository}},
		{"containment alone does not join: the home directory",
			"/Users/bmf",
			Family{"/Users/bmf", Project}},
		{"containment alone does not join: a directory inside it with history",
			"/Users/bmf/code",
			Family{"/Users/bmf/code", Project}},
		{"a shared name does not join a fork",
			"/Users/bmf/code/brandon-fryslie_happy",
			Family{"/Users/bmf/code/brandon-fryslie_happy", Repository}},
		{"a shared name does not join two directories called docs",
			"/Users/bmf/writing/docs",
			Family{"/Users/bmf/writing/docs", Project}},
		{"a plugin cache copy is its own install",
			"/Users/bmf/.claude/plugins/cache/memento/memento/0.3.0/skills/address-pr-reviews",
			Family{"/Users/bmf/.claude/plugins/cache/memento/memento/0.3.0/skills/address-pr-reviews", Project}},
		{"a path nothing is known about is its own family",
			"/opt/elsewhere/thing.txt",
			Family{"/opt/elsewhere/thing.txt", None}},
		{"a Windows spelling walks the same way",
			`\Users\bmf\code\textual-js\visual-tests\index.html`,
			Family{"/Users/bmf/code/textual-js", Repository}},
		{"a deleted directory on a Windows drive walks to the drive's root",
			`D:\work\site\gone\index.html`,
			Family{"D:/work/site", Repository}},
		{"a worktree recorded under a deleted Windows directory follows it to the repository",
			`D:\work\site\gone\.claude\worktrees\w`,
			Family{"D:/work/site", Recorded}},
		{"a relative path is nowhere in particular",
			"src/main.go",
			Family{"src/main.go", None}},
		{"an empty path is nowhere in particular",
			"",
			Family{".", None}},
	}

	r := New(history, disk{t: t, dirs: exists, repos: repos})
	if !r.RepositoryConsulted {
		t.Fatal("a resolver given a disk should say the repository was consulted")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := r.Resolve(c.path); got != c.want {
				t.Errorf("Resolve(%q)\n got %+v\nwant %+v", c.path, got, c.want)
			}
		})
	}
}

// Under --no-repo git is never asked, so only what the agent recorded joins
// anything, and the answer says so.
func TestResolveWithoutRepository(t *testing.T) {
	cases := []struct {
		name string
		path string
		want Family
	}{
		{"a scratchpad still finds its project",
			"/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad",
			Family{"/Users/bmf/code/textual-js", Recorded}},
		{"a worktree the agent made still finds its project",
			"/Users/bmf/code/happy/.claude/worktrees/calm-sparking-floyd",
			Family{"/Users/bmf/code/happy", Recorded}},
		{"a scratchpad naming that worktree follows it home",
			"/private/tmp/claude-501/-Users-bmf-code-happy--claude-worktrees-calm-sparking-floyd/859818fc-1f79-47fa-9a8b-12b41eeb2b0e/scratchpad",
			Family{"/Users/bmf/code/happy", Recorded}},
		{"a subdirectory has no repository to share",
			"/Users/bmf/code/textual-js/visual-tests",
			Family{"/Users/bmf/code/textual-js/visual-tests", Project}},
		{"a linked worktree has no main tree to join",
			"/Users/bmf/wt/low-talker-fix",
			Family{"/Users/bmf/wt/low-talker-fix", Project}},
	}

	r := WithoutRepository(history)
	if r.RepositoryConsulted {
		t.Fatal("a resolver without a repository should say so")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := r.Resolve(c.path); got != c.want {
				t.Errorf("Resolve(%q)\n got %+v\nwant %+v", c.path, got, c.want)
			}
		})
	}
}

// Two directories recorded as made for each other must not chase one another.
func TestResolveStopsFollowingACycle(t *testing.T) {
	r := WithoutRepository([]agent.Project{
		project("/a", worktreeOf("/b")),
		project("/b", worktreeOf("/a")),
	})
	// The record is followed once around and stops where it began.
	if got := r.Resolve("/a"); got != (Family{"/a", Recorded}) {
		t.Errorf("Resolve(/a) = %+v, want to end where it began", got)
	}
}

// One family is one key, however its members were reached.
func TestKeyIsTheSameAcrossAFamily(t *testing.T) {
	r := New(history, disk{t: t, dirs: exists, repos: repos})
	root := r.Resolve("/Users/bmf/code/textual-js").Key()
	for _, member := range []string{
		"/Users/bmf/code/textual-js/visual-tests",
		"/private/tmp/claude-501/-Users-bmf-code-textual-js/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad",
	} {
		if got := r.Resolve(member).Key(); got != root {
			t.Errorf("Resolve(%q).Key() = %q, want %q", member, got, root)
		}
	}
}
