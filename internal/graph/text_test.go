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
