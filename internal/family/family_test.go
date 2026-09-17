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

// fixture is a directory in the hand-written history: a project a session
// started in, or a directory an agent made that no session started in, and
// what the agent wrote into its path.
type fixture struct {
	path    string
	serves  func(string) bool
	history bool
}

func project(path string, serves func(string) bool) fixture {
	return fixture{path: path, serves: serves, history: true}
}

func madeOnly(path string, serves func(string) bool) fixture {
	return fixture{path: path, serves: serves}
}

func projectsOf(fs []fixture) []agent.Project {
	var out []agent.Project
	for _, f := range fs {
		if f.history {
			out = append(out, agent.Project{Name: f.path[strings.LastIndex(f.path, "/")+1:], Path: f.path})
		}
	}
	return out
}

// recordsOf is one agent's reading of the directories it made: the record on
// whichever fixture the path lies in.
func recordsOf(fs []fixture) []agent.MadeFor {
	return []agent.MadeFor{func(p string) func(string) bool {
		p = agent.NormalisePath(p)
		for _, f := range fs {
			dir := agent.NormalisePath(f.path)
			if f.serves != nil && (p == dir || strings.HasPrefix(p, dir+"/")) {
				return f.serves
			}
		}
		return nil
	}}
}

// A history written by hand. The names are real projects; the paths and their
// arrangement are the cases the rules have to hold for.
var (
	history = []fixture{
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

		// Directories an agent made that no session started in: a scratchpad a
		// session in happy wrote into, and a textual-js worktree since deleted.
		madeOnly("/private/tmp/claude-501/-Users-bmf-code-happy/5e0c1a2b-7d3f-4e5a-9b8c-0d1e2f3a4b5c/scratchpad",
			scratchpadFor("-Users-bmf-code-happy")),
		madeOnly("/Users/bmf/code/textual-js/.claude/worktrees/brisk-dune",
			worktreeOf("/Users/bmf/code/textual-js")),

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
		"//wsl$/Ubuntu", "//wsl$/Ubuntu/home", "//wsl$/Ubuntu/home/me", "//wsl$/Ubuntu/home/me/proj",
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
		"/Users/bmf/Downloads",
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
		"//wsl$/Ubuntu/home/me/proj":            "//wsl$/Ubuntu/home/me/proj",

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
		{"a file in a scratchpad no session started in goes to the scratchpad's project",
			"/private/tmp/claude-501/-Users-bmf-code-happy/5e0c1a2b-7d3f-4e5a-9b8c-0d1e2f3a4b5c/scratchpad/probe.go",
			Family{"/Users/bmf/code/happy", Recorded}},
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
		{"containment alone does not join: a deleted directory with no history under a project with history",
			"/Users/bmf/code/never-had-history",
			Family{"/Users/bmf/code/never-had-history", None}},
		{"containment alone does not join: a file with no history under the home directory",
			"/Users/bmf/Downloads/notes.txt",
			Family{"/Users/bmf/Downloads/notes.txt", None}},
		{"containment alone does not join: a file inside a project that is no repository",
			"/Users/bmf/writing/docs/draft.md",
			Family{"/Users/bmf/writing/docs/draft.md", None}},
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
		{"a deleted directory on a network share joins the repository above it",
			`\\wsl$\Ubuntu\home\me\proj\gone\x.go`,
			Family{"//wsl$/Ubuntu/home/me/proj", Repository}},
		{"the walk up a network share ends at the share",
			`\\wsl$\Ubuntu\tmp\gone`,
			Family{"//wsl$/Ubuntu/tmp/gone", None}},
		{"a relative path is nowhere in particular",
			"src/main.go",
			Family{"src/main.go", None}},
		{"an empty path is nowhere in particular",
			"",
			Family{".", None}},
	}

	r := New(projectsOf(history), recordsOf(history), disk{t: t, dirs: exists, repos: repos})
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
		{"a deleted worktree no session started in still finds its project",
			"/Users/bmf/code/textual-js/.claude/worktrees/brisk-dune/src/app.ts",
			Family{"/Users/bmf/code/textual-js", Recorded}},
		{"a subdirectory has no repository to share",
			"/Users/bmf/code/textual-js/visual-tests",
			Family{"/Users/bmf/code/textual-js/visual-tests", Project}},
		{"a file in a subdirectory joins nothing by containment",
			"/Users/bmf/code/textual-js/visual-tests/snap.png",
			Family{"/Users/bmf/code/textual-js/visual-tests/snap.png", None}},
		{"a linked worktree has no main tree to join",
			"/Users/bmf/wt/low-talker-fix",
			Family{"/Users/bmf/wt/low-talker-fix", Project}},
	}

	r := WithoutRepository(projectsOf(history), recordsOf(history))
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

// One directory recorded under two spellings is asked about in both, so which
// source came first cannot decide whether a record finds it.
func TestResolveAsksARecordAboutEverySpelling(t *testing.T) {
	scratchpad := madeOnly("/private/tmp/claude-501/D--work-site/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad",
		scratchpadFor("D--work-site"))
	for _, order := range [][]fixture{
		{project("/d/work/site", nil), project(`D:\work\site`, nil), scratchpad},
		{project(`D:\work\site`, nil), project("/d/work/site", nil), scratchpad},
	} {
		r := WithoutRepository(projectsOf(order), recordsOf(order))
		if got := r.Resolve(scratchpad.path); got.Evidence != Recorded {
			t.Errorf("with %q first, the scratchpad resolved to %+v, want its project", order[0].path, got)
		}
	}
}

// Two directories recorded as made for each other must not chase one another.
func TestResolveStopsFollowingACycle(t *testing.T) {
	cycle := []fixture{
		project("/a", worktreeOf("/b")),
		project("/b", worktreeOf("/a")),
	}
	r := WithoutRepository(projectsOf(cycle), recordsOf(cycle))
	// The record is followed once around and stops where it began.
	if got := r.Resolve("/a"); got != (Family{"/a", Recorded}) {
		t.Errorf("Resolve(/a) = %+v, want to end where it began", got)
	}
}

// One family is one key, however its members were reached.
func TestKeyIsTheSameAcrossAFamily(t *testing.T) {
	r := New(projectsOf(history), recordsOf(history), disk{t: t, dirs: exists, repos: repos})
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
