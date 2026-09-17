package graph

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/repo"
)

var fixedNow = func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }

// made is an agent's record of the worktrees it makes, written by hand:
// <project>/.claude/worktrees/<name>. Every build here is given it, as the
// command gives every build every agent's record.
var made = []agent.MadeFor{func(p string) (agent.Made, bool) {
	m := regexp.MustCompile(`^(.*)/\.claude/worktrees/[^/]+(/.*)?$`).FindStringSubmatch(p)
	if m == nil {
		return agent.Made{}, false
	}
	return agent.Made{For: func(c string) bool { return c == m[1] }, Within: m[2]}, true
}}

// alone is a project worked in the one directory it is known by.
func alone(p agent.Project) family.Project {
	return family.Project{Path: p.Path, Agent: p.Source, Members: []agent.Project{p}}
}

func turn(day, min int, text string, edits ...string) agent.Turn {
	t := agent.Turn{
		At:    time.Date(2026, 8, day, 9, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute),
		Text:  text,
		Tools: map[string]int{},
		Files: map[string]int{},
		Edits: map[string]int{},
	}
	for _, f := range edits {
		t.Files[f]++
		t.Edits[f]++
	}
	return t
}

const longRequest = "rework the export path so a document with an embedded font renders its first page"
const otherRequest = "the search index needs to rebuild itself whenever a document is deleted from disk"

func sample() (agent.Project, []agent.Session) {
	p := agent.Project{Name: "example", Path: "/work/example", Source: "claude-code"}
	s := []agent.Session{{
		ID:    "s1",
		Title: "Working on the exporter",
		Turns: []agent.Turn{
			turn(1, 0, longRequest, "export.go"),
			turn(1, 20, "keep going", "export.go"),
			turn(2, 0, otherRequest, "search.go"),
			turn(2, 30, "now handle the empty case too", "search.go", "export.go"),
		},
	}}
	return p, s
}

func TestBuildProducesTheHierarchy(t *testing.T) {
	p, sessions := sample()
	opt := DefaultOptions(made)
	opt.Now = fixedNow

	g := Build(alone(p), sessions, opt)

	if g.Schema != SchemaVersion {
		t.Errorf("schema = %d, want %d", g.Schema, SchemaVersion)
	}
	if g.Project.Name != "example" || g.Project.Agent != "claude-code" {
		t.Errorf("project = %+v", g.Project)
	}
	// Two days of work, so two sittings.
	if len(g.Goals) != 2 {
		t.Fatalf("got %d goals, want 2", len(g.Goals))
	}
	if g.Goals[0].ID != "g1" || g.Goals[1].ID != "g2" {
		t.Errorf("ids = %q, %q", g.Goals[0].ID, g.Goals[1].ID)
	}
	if g.Goals[0].Title != "Working on the exporter" {
		t.Errorf("title = %q, want the session's own name", g.Goals[0].Title)
	}
	if g.Totals.Turns != 4 {
		t.Errorf("totals.turns = %d, want 4", g.Totals.Turns)
	}
	// Task ids have to say which goal they belong to.
	if got := g.Goals[0].Tasks[0].ID; got != "g1.t1" {
		t.Errorf("task id = %q, want g1.t1", got)
	}
}

// The same history has to produce byte-identical output, or nobody can diff two
// runs to see what a change actually did.
func TestBuildIsDeterministic(t *testing.T) {
	p, sessions := sample()
	opt := DefaultOptions(made)
	opt.Now = fixedNow

	first, err := json.Marshal(Build(alone(p), sessions, opt))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		again, err := json.Marshal(Build(alone(p), sessions, opt))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("output changed between runs\nfirst: %s\nagain: %s", first, again)
		}
	}
}

// Nothing about drawing belongs in the core's output. If one of these ever
// appears, the renderer has started bending the shape of the data.
func TestGraphCarriesNoPresentation(t *testing.T) {
	p, sessions := sample()
	opt := DefaultOptions(made)
	opt.Now = fixedNow

	body, err := json.Marshal(Build(alone(p), sessions, opt))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{
		`"x"`, `"y"`, `"color"`, `"colour"`, `"width"`, `"height"`,
		`"radius"`, `"size"`, `"collapsed"`, `"expanded"`, `"zoom"`, `"font"`,
	} {
		if bytes.Contains(body, []byte(banned)) {
			t.Errorf("graph contains %s, which is a rendering concern", banned)
		}
	}
}

// Every turn has to survive into the output. A view that quietly drops work is
// worse than no view.
func TestBuildLosesNoTurns(t *testing.T) {
	p, sessions := sample()
	opt := DefaultOptions(made)
	opt.Now = fixedNow

	g := Build(alone(p), sessions, opt)

	counted := 0
	for _, goal := range g.Goals {
		for _, task := range goal.Tasks {
			counted += len(task.Turns)
		}
	}
	want := 0
	for _, s := range sessions {
		want += len(s.Turns)
	}
	if counted != want {
		t.Errorf("graph holds %d turns, history had %d", counted, want)
	}
}

// Prompts are the user's own writing and the reason to click into anything, so
// they are never trimmed on the way out.
func TestTurnTextIsNotTruncated(t *testing.T) {
	long := longRequest + " " + longRequest + " " + longRequest
	p := agent.Project{Name: "x", Source: "claude-code"}
	sessions := []agent.Session{{Turns: []agent.Turn{turn(1, 0, long)}}}

	opt := DefaultOptions(made)
	opt.Now = fixedNow
	g := Build(alone(p), sessions, opt)

	if got := g.Goals[0].Tasks[0].Turns[0].Text; got != long {
		t.Errorf("prompt was altered on the way out:\n got %q\nwant %q", got, long)
	}
}

// A project with several sessions should read as one run of work, since
// sessions are how the agent stores things rather than how the work happened.
func TestBuildOrdersGoalsAcrossSessions(t *testing.T) {
	p := agent.Project{Name: "x", Source: "claude-code"}
	sessions := []agent.Session{
		{ID: "later", Turns: []agent.Turn{turn(9, 0, longRequest)}},
		{ID: "earlier", Turns: []agent.Turn{turn(2, 0, otherRequest)}},
	}
	opt := DefaultOptions(made)
	opt.Now = fixedNow

	g := Build(alone(p), sessions, opt)

	if len(g.Goals) != 2 {
		t.Fatalf("got %d goals, want 2", len(g.Goals))
	}
	if !g.Goals[0].Stats.Start.Before(g.Goals[1].Stats.Start) {
		t.Error("goals came back out of time order")
	}
}

func TestBuildOnEmptyHistory(t *testing.T) {
	opt := DefaultOptions(made)
	opt.Now = fixedNow
	g := Build(alone(agent.Project{Name: "empty", Source: "claude-code"}), nil, opt)

	if len(g.Goals) != 0 || g.Totals.Turns != 0 {
		t.Errorf("empty history should give an empty graph, got %+v", g.Totals)
	}
	if _, err := json.Marshal(g); err != nil {
		t.Errorf("an empty graph must still serialise: %v", err)
	}
}

// A session about one project regularly commits in another, a tool and its
// website worked on together being the usual case. Those commits are real but
// they are not this project's, and ten of one project's forty seven commit
// calls turned out to be a sibling repository's.
func TestCommitsMadeElsewhereAreNotThisProjects(t *testing.T) {
	for _, c := range []struct {
		name    string
		dir     string
		project string
		want    bool
	}{
		{"no cd is the project itself", "", "d:/boughs", true},
		{"the same place", "d:/boughs", "d:/boughs", true},
		{"a shell spelling of the same drive", "/d/boughs", "d:/boughs", true},
		{"windows separators", `d:\boughs`, "d:/boughs", true},
		{"a trailing separator", "d:/boughs/", "d:/boughs", true},
		{"a sibling repository", "/d/bough-site", "d:/boughs", false},
		{"a name that merely ends the same", "d:/my-boughs", "d:/boughs", false},
		{"somewhere else entirely", "d:/other", "d:/project", false},
	} {
		if got := here(c.dir, c.project, alone(agent.Project{Path: c.project})); got != c.want {
			t.Errorf("%s: here(%q, %q) = %v, want %v", c.name, c.dir, c.project, got, c.want)
		}
	}
}

// A commit made after moving into a subdirectory still belongs to the project.
//
// `cd internal && git commit` runs in this repository. The recorded directory
// is the bare "internal", which never equalled an absolute project path, so
// the commit was dropped from the diagram with nothing said. Only an absolute
// path can name somewhere else, because only an absolute path says where it
// starts from.
func TestCommitInASubdirectoryIsKept(t *testing.T) {
	const proj = "/home/me/proj"
	for _, c := range []struct {
		dir  string
		want bool
		why  string
	}{
		{"", true, "a command that does not move runs where the session is"},
		{proj, true, "the project itself"},
		{"internal", true, "cd into a subdirectory is still this repository"},
		{"./internal", true, "the same, written with a leading dot"},
		{"internal/agent/codex", true, "deeper down is still inside"},
		{"/home/me/other", false, "an absolute path somewhere else"},
		{"../other", false, "relative, but it climbs out of the project"},
		{"..", false, "the parent directory is not this project"},
	} {
		if got := here(c.dir, proj, alone(agent.Project{Path: proj})); got != c.want {
			t.Errorf("here(%q) = %v, want %v: %s", c.dir, got, c.want, c.why)
		}
	}
}

// The two spellings of a Windows drive are one place, and a relative path is
// still relative whichever way the project is written.
func TestCommitDirAcrossDriveSpellings(t *testing.T) {
	for _, c := range []struct {
		dir, project string
		want         bool
	}{
		{"d:/work/site", "d:/work/site", true},
		{"/d/work/site", "d:/work/site", true},
		{"d:/work/other", "d:/work/site", false},
		{"internal", "d:/work/site", true},
	} {
		if got := here(c.dir, c.project, alone(agent.Project{Path: c.project})); got != c.want {
			t.Errorf("here(%q, %q) = %v, want %v", c.dir, c.project, got, c.want)
		}
	}
}

// Matching a commit to the repository must not depend on the order the
// sessions happened to be walked in.
//
// Each repository commit goes to one agent commit, and it used to go to
// whichever reached it first. Two commits inside the same window meant the
// earlier-visited one took it, closer or not, and sessions are grouped by file
// rather than by time so that order is not even the order work happened in.
func TestCommitMatchingDoesNotDependOnOrder(t *testing.T) {
	when := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	have := []repo.Commit{
		{SHA: "aaa", When: when("2026-08-22T10:00:00Z")},
		{SHA: "bbb", When: when("2026-08-22T10:00:10Z")},
	}

	// A@10:00:05 is five seconds from either. B@10:00:00 is exactly on aaa.
	//
	// Settling in walk order gives aaa to A, because it is visited first and
	// aaa is as close as bbb, and B is left with bbb ten seconds away. Settling
	// the closest pair first gives aaa to B and bbb to A, which is the reading
	// that matches what happened.
	a := &agent.Commit{At: when("2026-08-22T10:00:05Z")}
	b := &agent.Commit{At: when("2026-08-22T10:00:00Z")}

	// Walked one way, then the other. The answer has to be the same.
	for _, order := range [][]*agent.Commit{{a, b}, {b, a}} {
		a.SHA, b.SHA = "", ""
		h := append([]repo.Commit(nil), have...)

		if missed := pair(order, h, matchWindow); len(missed) != 0 {
			t.Errorf("%d commits went unmatched, want 0", len(missed))
		}
		if b.SHA != "aaa" {
			t.Errorf("the commit sitting on aaa got %q, want aaa", b.SHA)
		}
		if a.SHA != "bbb" {
			t.Errorf("the commit five seconds from either got %q, want bbb", a.SHA)
		}
	}
}

// A repository commit is handed out once, and a commit with nothing near it
// comes back as unmatched rather than borrowing someone else's hash.
func TestPairHandsOutEachCommitOnce(t *testing.T) {
	when, err := time.Parse(time.RFC3339, "2026-08-22T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	have := []repo.Commit{{SHA: "aaa", When: when}}

	first := &agent.Commit{At: when.Add(time.Second)}
	second := &agent.Commit{At: when.Add(2 * time.Second)}
	far := &agent.Commit{At: when.Add(time.Hour)}
	zero := &agent.Commit{}

	missed := pair([]*agent.Commit{first, second, far, zero}, have, matchWindow)

	if first.SHA != "aaa" {
		t.Errorf("closest got %q, want aaa", first.SHA)
	}
	if second.SHA != "" {
		t.Errorf("second got %q, want nothing: aaa is spoken for", second.SHA)
	}
	if len(missed) != 3 {
		t.Errorf("%d unmatched, want 3", len(missed))
	}
}

// Build leaves the sessions it was given exactly as it found them.
//
// Everything inside writes into the turns: onlyHere drops commits made in
// another repository, and matching against git rewrites the hashes. Those
// edits used to land in the caller's own slices, so a second Build on one set
// of sessions saw the first one's leftovers and answered differently, and
// nothing else could reuse them afterwards.
func TestBuildDoesNotChangeTheSessionsItIsGiven(t *testing.T) {
	when := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	sessions := func() []agent.Session {
		return []agent.Session{{
			ID: "s1",
			Turns: []agent.Turn{{
				At: when, Text: "do it",
				Tools: map[string]int{"Bash": 1}, Files: map[string]int{},
				Edits: map[string]int{}, Lines: map[string]int{}, Models: map[string]int{},
				Committed: []agent.Commit{
					// One made somewhere else, which onlyHere drops.
					{SHA: "aaaa111", At: when, Dir: "/elsewhere"},
					{SHA: "bbbb222", At: when},
				},
				// Unnamed, so folding the sub-agent below writes its name here.
				Delegated: []agent.Delegation{{Kind: "Explore"}},
			}},
		}, {
			// A sub-agent, which fold adds into the turn above: its counts go
			// into that turn's maps.
			ID: "s2", ParentID: "s1",
			Turns: []agent.Turn{{
				At: when.Add(time.Second), TaskName: "/root/look",
				Tools: map[string]int{"Bash": 2}, Files: map[string]int{},
				Edits: map[string]int{}, Lines: map[string]int{}, Models: map[string]int{},
			}},
		}}
	}

	given := sessions()
	opt := DefaultOptions(made)
	opt.Now = func() time.Time { return when }

	first := Build(alone(agent.Project{Name: "p", Path: "/p"}), given, opt)

	if got := len(given[0].Turns[0].Committed); got != 2 {
		t.Errorf("the caller's commits went from 2 to %d", got)
	}
	if got := given[0].Turns[0].Tools["Bash"]; got != 1 {
		t.Errorf("the caller's tool count went from 1 to %d", got)
	}
	if got := given[0].Turns[0].Delegated[0].Name; got != "" {
		t.Errorf("the caller's hand-off was named %q", got)
	}

	// And the same input twice gives the same answer, which is only true if
	// the first run left nothing behind.
	second := Build(alone(agent.Project{Name: "p", Path: "/p"}), given, opt)
	if len(first.Goals) != len(second.Goals) {
		t.Errorf("two builds of one input: %d goals then %d", len(first.Goals), len(second.Goals))
	}
	if a, b := first.Totals.Commits, second.Totals.Commits; len(a) != len(b) {
		t.Errorf("two builds of one input: %d commits then %d", len(a), len(b))
	}
}

// A project read whole is every directory's sessions together, and the graph
// says which directories those were.
func TestBuildListsTheDirectoriesItRead(t *testing.T) {
	const tree = "/work/app/.claude/worktrees/calm-river"
	p := family.Project{Path: "/work/app", Agent: "claude-code", Members: []agent.Project{
		{Name: "app", Path: "/work/app", Source: "claude-code"},
		{Name: "calm-river", Path: tree, Source: "claude-code"},
	}}
	sessions := []agent.Session{
		{ID: "main", Dir: "/work/app", Turns: []agent.Turn{turn(1, 0, longRequest, "/work/app/export.go")}},
		{ID: "tree", Dir: tree, Turns: []agent.Turn{turn(2, 0, otherRequest, tree+"/search.go")}},
	}
	opt := DefaultOptions(made)
	opt.Now = fixedNow

	body, err := json.Marshal(Build(p, sessions, opt))
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Project struct {
			Name        string   `json:"name"`
			Path        string   `json:"path"`
			Directories []string `json:"directories"`
		} `json:"project"`
		Totals struct {
			Turns    int `json:"turns"`
			TopFiles []struct {
				Path string `json:"path"`
			} `json:"topFiles"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatal(err)
	}
	if g.Project.Name != "app" || g.Project.Path != "/work/app" {
		t.Errorf("project is %q at %q, want app at /work/app", g.Project.Name, g.Project.Path)
	}
	if want := []string{"/work/app", tree}; !slices.Equal(g.Project.Directories, want) {
		t.Errorf("directories = %q, want %q", g.Project.Directories, want)
	}
	if g.Totals.Turns != 2 {
		t.Errorf("%d prompts, want both directories' 2", g.Totals.Turns)
	}
	// The worktree's code is the project's work, not the agent's bookkeeping.
	if !slices.ContainsFunc(g.Totals.TopFiles, func(f struct {
		Path string `json:"path"`
	}) bool {
		return f.Path == tree+"/search.go"
	}) {
		t.Errorf("the worktree's source file is not among the top files: %+v", g.Totals.TopFiles)
	}
}

// A commit made in any of the project's directories is the project's, from
// whichever of them the session ran in. A worktree session that runs
// `cd <main checkout> && git commit` committed this project's work.
func TestCommitInAnotherMemberDirectoryIsKept(t *testing.T) {
	const tree = "/work/app/.claude/worktrees/calm-river"
	p := family.Project{Path: "/work/app", Members: []agent.Project{{Path: "/work/app"}, {Path: tree}}}
	for _, c := range []struct {
		dir, from string
		want      bool
		why       string
	}{
		{"/work/app", tree, true, "the main checkout, from a worktree"},
		{tree, "/work/app", true, "a worktree, from the main checkout"},
		{"../../..", tree, true, "climbing from the worktree to the checkout"},
		{"..", tree, false, "the worktrees directory is none of the project's"},
		{"/work/site", tree, false, "another repository"},
		{"/work/app/.claude/worktrees/other", tree, false, "a directory the project has no history in"},
	} {
		if got := here(c.dir, c.from, p); got != c.want {
			t.Errorf("here(%q from %q) = %v, want %v: %s", c.dir, c.from, got, c.want, c.why)
		}
	}
}

// Build keeps the commit, not only here.
func TestBuildKeepsACommitMadeInAMember(t *testing.T) {
	const tree = "/work/app/.claude/worktrees/calm-river"
	p := family.Project{Path: "/work/app", Members: []agent.Project{{Path: "/work/app"}, {Path: tree}}}
	at := turn(1, 0, longRequest)
	at.Committed = []agent.Commit{{Kind: "committed", Dir: "/work/app", At: at.At}, {Kind: "committed", Dir: "/work/site", At: at.At}}
	opt := DefaultOptions(made)
	opt.Now = fixedNow

	g := Build(p, []agent.Session{{ID: "tree", Dir: tree, Turns: []agent.Turn{at}}}, opt)
	if n := len(g.Totals.Commits); n != 1 {
		t.Errorf("%d commits, want the one made in the main checkout", n)
	}
}

// Sittings in two worktrees at once overlap, and the totals measure time in
// the order it passed rather than the order the sittings started in.
func TestTotalsMeasureOverlappingSittingsInTimeOrder(t *testing.T) {
	p := family.Project{Path: "/work/app", Members: []agent.Project{{Path: "/work/app"}, {Path: "/work/app/.claude/worktrees/w"}}}
	// One sitting from 9:00 to 9:40, another from 9:10 to 9:30.
	sessions := []agent.Session{
		{ID: "a", Turns: []agent.Turn{turn(1, 0, longRequest), turn(1, 20, "go on"), turn(1, 40, "and on")}},
		{ID: "b", Turns: []agent.Turn{turn(1, 10, otherRequest), turn(1, 30, "go on")}},
	}
	opt := DefaultOptions(made)
	opt.Now = fixedNow

	g := Build(p, sessions, opt)
	if got := g.Totals.ActiveMinutes; got != 40 {
		t.Errorf("active minutes = %d, want the 40 from the first prompt to the last", got)
	}
}
