package graph

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/repo"
)

// app is a repository with a worktree, worked in from its checkout. Its
// sibling is another repository at /work/site.
var app = family.Project{Path: "/work/app", Members: []agent.Project{
	{Path: "/work/app"}, {Path: "/work/app/.claude/worktrees/calm-river"},
}}

// at is the machine's answer for a path that exists where it was asked.
func at(p, fam string) Place {
	return Place{Path: p, Exists: true, Family: family.Family{Name: fam, Evidence: family.Repository}}
}

// away builds one sitting of the given turns in app's checkout, with the
// machine's answers, and returns what it recorded as done elsewhere.
func away(t *testing.T, e Elsewhere, turns ...agent.Turn) (Goal, Graph) {
	t.Helper()
	opt := DefaultOptions(made)
	opt.Now = fixedNow
	opt.Elsewhere = e
	g := Build(app, []agent.Session{{ID: "s", Dir: "/work/app", Turns: turns}}, opt)
	if len(g.Goals) != 1 {
		t.Fatalf("%d goals, want one sitting", len(g.Goals))
	}
	return g.Goals[0], g
}

func places(ps map[string]Place) Elsewhere { return Elsewhere{Places: ps} }

func TestAnEditInASiblingFamilyIsRecorded(t *testing.T) {
	work := turn(1, 0, longRequest, "/work/app/main.go", "/work/site/index.html", "/work/site/index.html", "/work/site/style.css")
	goal, g := away(t, places(map[string]Place{
		"/work/app/main.go":     at("/work/app/main.go", "/work/app"),
		"/work/site/index.html": at("/work/site/index.html", "/work/site"),
		"/work/site/style.css":  at("/work/site/style.css", "/work/site"),
	}), work)

	want := []Visit{{Family: "/work/site", Path: "/work/site", Files: []FileCount{
		{Path: "/work/site/index.html", Edits: 2}, {Path: "/work/site/style.css", Edits: 1},
	}}}
	if !reflect.DeepEqual(goal.Elsewhere, want) {
		t.Errorf("elsewhere = %+v\nwant %+v", goal.Elsewhere, want)
	}
	if len(g.Links) != 0 || g.Totals.Edits != 4 {
		t.Errorf("recording the visit changed the project's own measures: %+v", g.Totals)
	}
}

// The project's own directories are its own, a worktree included.
func TestAnEditInTheProjectsOwnFamilyIsNotRecorded(t *testing.T) {
	const file = "/work/app/.claude/worktrees/calm-river/export.go"
	goal, _ := away(t, places(map[string]Place{file: at(file, "/work/app")}), turn(1, 0, longRequest, file, file))
	if len(goal.Elsewhere) != 0 {
		t.Errorf("elsewhere = %+v, want nothing for the project's own worktree", goal.Elsewhere)
	}
}

func TestACommitAfterMovingIntoAnotherRepositoryIsRecorded(t *testing.T) {
	work := turn(1, 0, longRequest)
	work.Committed = []agent.Commit{
		{SHA: "1111111", Kind: "committed", At: work.At, Dir: "/work/site"},
		{SHA: "2222222", Kind: "committed", At: work.At.Add(time.Hour), Dir: "/work/site"},
		{SHA: "3333333", Kind: "committed", At: work.At},
	}
	goal, g := away(t, Elsewhere{
		Places: map[string]Place{"/work/site": at("/work/site", "/work/site")},
		Repos: map[string]repo.History{"/work/site": {Read: true, Commits: []repo.Commit{
			{SHA: "1111111", Subject: "fix the header", When: work.At.Add(2 * time.Second), Added: 3},
		}}},
	}, work)

	want := []Visit{{Family: "/work/site", Path: "/work/site", RepoRead: true, Commits: []Commit{
		{SHA: "1111111", Kind: "committed", Subject: "fix the header", Added: 3},
		// Checked against the sibling's repository, which cannot reach it.
		{Kind: "committed"},
	}}}
	if !reflect.DeepEqual(goal.Elsewhere, want) {
		t.Errorf("elsewhere = %+v\nwant %+v", goal.Elsewhere, want)
	}
	// Still not the project's own.
	if len(g.Totals.Commits) != 1 || g.Totals.Commits[0].SHA != "3333333" {
		t.Errorf("project commits = %+v, want only the one made at home", g.Totals.Commits)
	}
}

// A commit made after climbing out of the session's directory is placed from
// that directory.
func TestACommitThroughARelativePathIsPlaced(t *testing.T) {
	work := turn(1, 0, longRequest)
	work.Committed = []agent.Commit{{Kind: "committed", At: work.At, Dir: "../site"}}
	if got := Visited(app, []agent.Session{{Dir: "/work/app", Turns: []agent.Turn{work}}}).Dirs; !slices.Equal(got, []string{"/work/site"}) {
		t.Errorf("visited dirs = %q, want the sibling", got)
	}
	goal, _ := away(t, places(map[string]Place{"/work/site": at("/work/site", "/work/site")}), work)
	if len(goal.Elsewhere) != 1 || len(goal.Elsewhere[0].Commits) != 1 {
		t.Errorf("elsewhere = %+v, want the sibling's commit", goal.Elsewhere)
	}
}

// A directory that has gone cannot be placed in any repository, whatever
// its nearest surviving ancestor is.
func TestACommitInADeletedDirectoryIsNotRecorded(t *testing.T) {
	work := turn(1, 0, longRequest)
	work.Committed = []agent.Commit{{Kind: "committed", At: work.At, Dir: "/work/site/.claude/worktrees/gone"}}
	gone := at("/work/site/.claude/worktrees/gone", "/work/site")
	gone.Exists = false
	goal, _ := away(t, places(map[string]Place{"/work/site/.claude/worktrees/gone": gone}), work)
	if len(goal.Elsewhere) != 0 {
		t.Errorf("elsewhere = %+v, want nothing for a deleted directory", goal.Elsewhere)
	}
}

func TestARelativePathIsJoinedToTheSessionsDirectory(t *testing.T) {
	work := turn(1, 0, longRequest, "../site/index.html", "../site/index.html")
	sessions := []agent.Session{{Dir: "/work/app", Turns: []agent.Turn{work}}}
	if got := Visited(app, sessions).Files; !slices.Equal(got, []string{"/work/site/index.html"}) {
		t.Errorf("visited files = %q, want the path joined to the session's directory", got)
	}
	goal, _ := away(t, places(map[string]Place{"/work/site/index.html": at("/work/site/index.html", "/work/site")}), work)
	if len(goal.Elsewhere) != 1 || goal.Elsewhere[0].Files[0].Edits != 2 {
		t.Errorf("elsewhere = %+v, want the two edits in the sibling", goal.Elsewhere)
	}
}

// A scratchpad made for the sibling is still a scratchpad, and a symlink that
// leads into a temporary directory leads nowhere lasting.
func TestTemporaryWorkIsNotRecorded(t *testing.T) {
	const pad = "/tmp/claude-501/-work-site/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad/probe.go"
	const link = "/work/elsewhere/probe.go"
	recorded := at(pad, "/work/site")
	recorded.Family.Evidence = family.Recorded
	goal, _ := away(t, Elsewhere{
		Places: map[string]Place{
			pad:  recorded,
			link: at("/private/tmp/throwaway/probe.go", "/private/tmp/throwaway"),
		},
		Temporary: []string{"/tmp", "/private/tmp/"},
	}, turn(1, 0, longRequest, pad, pad, link, link))
	if len(goal.Elsewhere) != 0 {
		t.Errorf("elsewhere = %+v, want nothing done in a temporary directory", goal.Elsewhere)
	}
}

// The agent's own notes are judged as recorded, before a symlink leads them
// into a dotfiles checkout.
func TestTheAgentsOwnFilesAreNotRecorded(t *testing.T) {
	const note = "/home/me/.claude/CLAUDE.md"
	goal, _ := away(t, places(map[string]Place{note: at("/home/me/dotfiles/claude/CLAUDE.md", "/home/me/dotfiles")}),
		turn(1, 0, longRequest, note, note))
	if len(goal.Elsewhere) != 0 {
		t.Errorf("elsewhere = %+v, want nothing for the agent's own file", goal.Elsewhere)
	}
}

func TestASymlinkedPathIsRecordedWhereItLeads(t *testing.T) {
	const link = "/home/me/.config/tool/config.toml"
	goal, _ := away(t, places(map[string]Place{link: at(`C:\Users\Me\Dotfiles\Config\tool\config.toml`, "C:/Users/Me/Dotfiles")}),
		turn(1, 0, longRequest, link, link))
	// Spelled as the disk led there, with one separator.
	want := []Visit{{Family: "c:/users/me/dotfiles", Path: "C:/Users/Me/Dotfiles", Files: []FileCount{
		{Path: "C:/Users/Me/Dotfiles/Config/tool/config.toml", Edits: 2},
	}}}
	if !reflect.DeepEqual(goal.Elsewhere, want) {
		t.Errorf("elsewhere = %+v\nwant %+v", goal.Elsewhere, want)
	}
}

// One edit is a passing visit. A commit is never incidental.
func TestAOneEditVisitIsNotRecorded(t *testing.T) {
	const file = "/work/site/index.html"
	e := places(map[string]Place{file: at(file, "/work/site"), "/work/site": at("/work/site", "/work/site")})
	if goal, _ := away(t, e, turn(1, 0, longRequest, file)); len(goal.Elsewhere) != 0 {
		t.Errorf("elsewhere = %+v, want nothing for one edit", goal.Elsewhere)
	}

	committed := turn(1, 0, longRequest, file)
	committed.Committed = []agent.Commit{{Kind: "committed", At: committed.At, Dir: "/work/site"}}
	if goal, _ := away(t, e, committed); len(goal.Elsewhere) != 1 || len(goal.Elsewhere[0].Files) != 1 {
		t.Errorf("elsewhere = %+v, want the edit and the commit", goal.Elsewhere)
	}
}

// A repository's own machinery is not work, and a path in no family and no
// repository belongs to nobody to record it against.
func TestPathsThatAreNobodysWorkAreNotRecorded(t *testing.T) {
	const msg = "/work/site/.git/COMMIT_EDITMSG"
	const loose = "/work/notes/today.txt"
	goal, _ := away(t, places(map[string]Place{
		msg:   at(msg, "/work/site"),
		loose: {Path: loose, Exists: true, Family: family.Family{Name: loose, Evidence: family.None}},
	}), turn(1, 0, longRequest, msg, msg, loose, loose))
	if len(goal.Elsewhere) != 0 {
		t.Errorf("elsewhere = %+v, want nothing", goal.Elsewhere)
	}
}

// Several families come most edited first, whichever order they were found in.
func TestVisitsAreMostEditedFirst(t *testing.T) {
	work := turn(1, 0, longRequest, "/work/b/x", "/work/b/x", "/work/a/x", "/work/a/x", "/work/a/y")
	goal, _ := away(t, places(map[string]Place{
		"/work/a/x": at("/work/a/x", "/work/a"), "/work/a/y": at("/work/a/y", "/work/a"),
		"/work/b/x": at("/work/b/x", "/work/b"),
	}), work)
	if len(goal.Elsewhere) != 2 || goal.Elsewhere[0].Family != "/work/a" || goal.Elsewhere[1].Family != "/work/b" {
		t.Errorf("elsewhere = %+v, want /work/a then /work/b", goal.Elsewhere)
	}
}
