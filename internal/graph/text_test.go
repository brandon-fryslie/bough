package graph

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/agent"
)

// A hand-off shows every fact the agent recorded, and a task another agent
// handed over is marked as one. When one field stood in for another, a task
// name was printed where the reader expects their own words.
func TestTextShowsEachRecordedFactForWhatItIs(t *testing.T) {
	prompt := spent(30, "make me a site", 100, 10)
	prompt.Delegated = []agent.Delegation{
		{Kind: "Explore", Description: "Research the fonts"},
		{Name: "pixel_art"},
		{},
	}
	orphan := agent.Session{ID: "C", ParentID: "missing", Turns: []agent.Turn{handed(31, "/root/sprites", 40, 4)}}

	opt := DefaultOptions(made)
	opt.Now = func() time.Time { return minute(50) }
	g := Build(alone(agent.Project{Name: "site", Path: "/work/site"}), []agent.Session{{ID: "S", Turns: []agent.Turn{prompt}}, orphan}, opt)

	var out strings.Builder
	if err := WriteText(&out, g, true, named); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"handed off: Explore · Research the fonts\n",
		"handed off: pixel_art\n",
		"handed off: (unnamed)\n",
		"task from an agent: /root/sprites\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// A reading that did not consult the repository says so.
//
// A commit hash means two things. Read the repository and a hash is one it
// confirmed, with anything stale cleared. Do not read it and every hash is
// whatever the transcript claimed, unchecked. "Not a git repository", "git is
// not installed" and --no-repo all arrive at the same place, and without a
// word about it they look exactly like a clean confirmation.
func TestTextSaysWhenTheRepositoryWasNotRead(t *testing.T) {
	g := Graph{
		Project: Project{Name: "x", RepoRead: false},
		Totals:  Stats{Commits: []Commit{{SHA: "abc1234"}}},
	}
	var b bytes.Buffer
	if err := WriteText(&b, g, false, named); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "the repository was not read") {
		t.Errorf("nothing said the repository went unread:\n%s", b.String())
	}

	g.Project.RepoRead = true
	b.Reset()
	if err := WriteText(&b, g, false, named); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "the repository was not read") {
		t.Errorf("a confirmed reading claimed it was not read:\n%s", b.String())
	}
}

// A project read from several directories says so, and names them when every
// prompt is asked for too. One read from a single directory says nothing more.
func TestTextSaysWhenAProjectSpansDirectories(t *testing.T) {
	for _, c := range []struct {
		dirs    []string
		verbose bool
		want    []string
		not     []string
	}{
		{[]string{"/work/app"}, true, nil, []string{"directories"}},
		{[]string{"/work/app", "/work/app/.claude/worktrees/w"}, false, []string{"across 2 directories"}, []string{"  /work/app/.claude/worktrees/w"}},
		{[]string{"/work/app", "/work/app/.claude/worktrees/w"}, true, []string{"across 2 directories", "  /work/app/.claude/worktrees/w"}, nil},
	} {
		var out strings.Builder
		g := Graph{Project: Project{Name: "app", Path: "/work/app", Directories: c.dirs}}
		if err := WriteText(&out, g, c.verbose, named); err != nil {
			t.Fatal(err)
		}
		for _, want := range c.want {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%d directories, verbose %v: missing %q in\n%s", len(c.dirs), c.verbose, want, out.String())
			}
		}
		for _, not := range c.not {
			if strings.Contains(out.String(), not) {
				t.Errorf("%d directories, verbose %v: unexpected %q in\n%s", len(c.dirs), c.verbose, not, out.String())
			}
		}
	}
}

// A sitting that worked in another project says so under its own lines, one
// line for each project, in the words the page uses for the same work: the
// project by its directory's name, how many files changed there and how many
// commits were made.
func TestTextSaysWhatASittingDidElsewhere(t *testing.T) {
	files := func(n int) []FileCount {
		out := make([]FileCount, n)
		for i := range out {
			out[i] = FileCount{Path: "/work/site/f" + string(rune('a'+i)), Edits: 3}
		}
		return out
	}
	commits := func(n int) []Commit {
		out := make([]Commit, n)
		for i := range out {
			out[i] = Commit{SHA: "abc123" + string(rune('0'+i))}
		}
		return out
	}
	site := func(f, c int, read bool) Visit {
		return Visit{Family: "/work/site", Path: "/work/site", Files: files(f), Commits: commits(c), RepoRead: read}
	}

	for _, c := range []struct {
		what   string
		visits []Visit
		lines  []string
	}{
		{"edits and commits", []Visit{site(12, 2, true)}, []string{"also changed 12 files in site, 2 commits"}},
		{"edits only", []Visit{site(3, 0, true)}, []string{"also changed 3 files in site"}},
		{"commits only", []Visit{site(0, 2, true)}, []string{"also 2 commits in site"}},
		{"one file and one commit", []Visit{site(1, 1, true)}, []string{"also changed 1 file in site, 1 commit"}},
		{"one commit alone", []Visit{site(0, 1, true)}, []string{"also 1 commit in site"}},
		{"no work elsewhere", nil, nil},
		{"a repository that was not read", []Visit{site(2, 2, false)}, []string{
			"also changed 2 files in site, 2 commits, as the transcript recorded them: the repository was not read",
		}},
		{"two projects", []Visit{site(4, 0, true), {Family: "/work/docs", Path: "/work/docs", Commits: commits(1), RepoRead: true}}, []string{
			"also changed 4 files in site",
			"also 1 commit in docs",
		}},
	} {
		g := Graph{
			Project: Project{Name: "app", Path: "/work/app", Agents: []string{"claude-code"}, RepoRead: true},
			Goals:   []Goal{{ID: "g1", Agent: "claude-code", Period: "Sat 1 Aug", Label: "ship it", Elsewhere: c.visits}},
		}
		var out strings.Builder
		if err := WriteText(&out, g, false, named); err != nil {
			t.Fatal(err)
		}

		var also []string
		for _, line := range strings.Split(out.String(), "\n") {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "also ") {
				also = append(also, trimmed)
				// Under the sitting, indented the way its other lines are.
				if !strings.HasPrefix(line, strings.Repeat(" ", 15)+"also ") {
					t.Errorf("%s: %q is not indented under the sitting", c.what, line)
				}
			}
		}
		if strings.Join(also, "\n") != strings.Join(c.lines, "\n") {
			t.Errorf("%s: lines are\n%s\nwant\n%s", c.what, strings.Join(also, "\n"), strings.Join(c.lines, "\n"))
			continue
		}
		for i, v := range c.visits {
			if also[i] != "also "+DoneIn(v) {
				t.Errorf("%s: the text says %q where the page is handed %q", c.what, also[i], DoneIn(v))
			}
		}
		// The project is named, never its whole path, and no hash is printed
		// for work elsewhere, least of all one nothing checked.
		if strings.Contains(out.String(), "/work/site") || strings.Contains(out.String(), "abc123") {
			t.Errorf("%s: the output prints a path or a hash:\n%s", c.what, out.String())
		}
	}
}
