package portfolio

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/graph"
)

var generated = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

const request = "rework the export path so a document with an embedded font renders its first page"

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

// worked is a family with history, and the graph of it, built exactly as the
// command builds one.
func worked(name, path string) (family.Project, graph.Graph) {
	p := family.Project{Path: path, Members: []agent.Project{{
		Name:       name,
		Path:       path,
		Source:     "claude-code",
		LastWorked: time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC),
	}}}
	sessions := []agent.Session{{
		ID:     "s1",
		Source: "claude-code",
		Title:  "Working on the exporter",
		Turns: []agent.Turn{
			turn(1, 0, request, "export.go"),
			turn(1, 20, "keep going", "export.go"),
			turn(2, 0, "the search index needs to rebuild whenever a document is deleted", "search.go"),
			turn(2, 30, "now handle the empty case too", "search.go", "export.go"),
		},
	}}
	opt := graph.DefaultOptions(nil)
	opt.Now = func() time.Time { return generated }
	return p, graph.Build(p, sessions, opt)
}

// A sitting's id is the graph's own, or a reader that follows one back to the
// family it came from finds nothing under that name. The ids are assigned by
// position as the graph is built, so any second way of dividing the history
// into sittings would agree by luck until it stopped agreeing.
func TestSittingsCarryTheGraphsOwnIDs(t *testing.T) {
	p, g := worked("example", "/work/example")

	f := Known(p).Read(g)

	if len(g.Goals) == 0 {
		t.Fatal("the fixture produced no goals, so there is nothing to match")
	}
	if len(f.Sittings) != len(g.Goals) {
		t.Fatalf("summarised %d sittings for %d goals", len(f.Sittings), len(g.Goals))
	}
	for i, s := range f.Sittings {
		if s.ID != g.Goals[i].ID {
			t.Errorf("sitting %d is %q, the graph calls it %q", i, s.ID, g.Goals[i].ID)
		}
	}
}

// Every number a sitting reports is the graph's, so the timeline and the page
// opened from it cannot disagree about the same piece of work.
func TestSittingReportsWhatTheGraphSaysOfIt(t *testing.T) {
	p, g := worked("example", "/work/example")

	f := Known(p).Read(g)

	goal, got := g.Goals[0], f.Sittings[0]
	for _, c := range []struct {
		what      string
		got, want any
	}{
		{"agent", got.Agent, goal.Agent},
		{"label", got.Label, goal.Label},
		{"start", got.Start, goal.Stats.Start},
		{"end", got.End, goal.Stats.End},
		{"active minutes", got.ActiveMinutes, goal.Stats.ActiveMinutes},
		{"prompts", got.Prompts, goal.Stats.Turns},
		{"edits", got.Edits, goal.Stats.Edits},
		{"commits", got.Commits, len(goal.Stats.Commits)},
		{"struggle", got.Struggle, goal.Stats.Struggle},
	} {
		if c.got != c.want {
			t.Errorf("sitting reports %s as %v, the graph says %v", c.what, c.got, c.want)
		}
	}
}

// Everything a view needs to draw the shape of the portfolio is answerable
// before a single transcript is opened. That is what lets the next view fill
// in as it reads rather than showing nothing for the seventy seconds a whole
// machine takes to read.
func TestKnownAnswersBeforeAnyHistoryIsRead(t *testing.T) {
	worked := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	p := family.Project{Path: "/work/example", Members: []agent.Project{
		{Name: "example", Path: "/work/example", Source: "claude-code", LastWorked: worked.Add(-48 * time.Hour)},
		{Name: "wt", Path: "/work/example/.claude/worktrees/wt", Source: "codex", LastWorked: worked},
	}}

	f := Known(p)

	if f.Key != p.Key() || f.Name != "example" || f.Path != "/work/example" {
		t.Errorf("family is named %+v, the project says %q at %q", f, p.Name(), p.Path)
	}
	if want := []string{"claude-code", "codex"}; !slices.Equal(f.Agents, want) {
		t.Errorf("agents are %v, want %v", f.Agents, want)
	}
	if len(f.Directories) != 2 {
		t.Errorf("directories are %v, want both the checkout and the worktree", f.Directories)
	}
	// The whole family's latest, not the directory it is named after: a
	// checkout last touched two days ago through a worktree touched this
	// morning was worked on this morning.
	if !f.LastWorked.Equal(worked) {
		t.Errorf("last worked %v, want %v", f.LastWorked, worked)
	}
	if f.Unreadable != "" {
		t.Errorf("a family nobody has tried to read yet claims %q", f.Unreadable)
	}
}

// A family whose history will not open and a family that has none are
// different facts. Both arrive with no sittings, so without the reason they
// are the same document, and a view drawing one draws an empty lane over work
// that is really there.
func TestUnreadableIsNotTheSameAsEmpty(t *testing.T) {
	p, _ := worked("example", "/work/example")

	empty := Known(p).Read(graph.Graph{})
	broken := Known(p).Unread(errors.New("no readable history for example: line too long"))

	if len(empty.Sittings) != 0 || empty.Unreadable != "" {
		t.Errorf("a family read with no work in it says %+v", empty)
	}
	if len(broken.Sittings) != 0 {
		t.Errorf("a family that could not be read reports sittings: %+v", broken.Sittings)
	}
	if !strings.Contains(broken.Unreadable, "line too long") {
		t.Errorf("the reason is %q, which does not say what went wrong", broken.Unreadable)
	}
}

// Neither case may serialise as null. A reader of this document walks the
// sittings of every family, and a null is a second empty it has to know about.
func TestEveryFamilyCarriesAListOfSittings(t *testing.T) {
	p, _ := worked("example", "/work/example")

	for _, f := range []Family{
		Known(p),
		Known(p).Read(graph.Graph{}),
		Known(p).Unread(errors.New("could not be read")),
	} {
		body, err := json.Marshal(New("test", generated, []Family{f}))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte(`"sittings":null`)) {
			t.Errorf("sittings serialised as null: %s", body)
		}
	}
	body, err := json.Marshal(New("test", generated, nil))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(`"families":null`)) {
		t.Errorf("families serialised as null: %s", body)
	}
}

// Several families are one document, in the order they were given, which is
// the order the resolver gathered them.
func TestPortfolioHoldsEveryFamilyInTheOrderGiven(t *testing.T) {
	first, firstGraph := worked("alpha", "/work/alpha")
	second, _ := worked("beta", "/work/beta")
	third, _ := worked("gamma", "/work/gamma")

	doc := New("test", generated, []Family{
		Known(first).Read(firstGraph),
		Known(second).Read(graph.Graph{}),
		Known(third).Unread(errors.New("no readable history for gamma")),
	})

	if doc.Schema != SchemaVersion {
		t.Errorf("document is schema %d, want %d", doc.Schema, SchemaVersion)
	}
	if !doc.Generated.Equal(generated) {
		t.Errorf("generated %v, want %v", doc.Generated, generated)
	}
	names := make([]string, len(doc.Families))
	for i, f := range doc.Families {
		names[i] = f.Name
	}
	if want := []string{"alpha", "beta", "gamma"}; !slices.Equal(names, want) {
		t.Errorf("families are %v, want %v", names, want)
	}
	if len(doc.Families[0].Sittings) == 0 {
		t.Error("the family with history summarised no sittings")
	}
}

// The document is the summary, not a second copy of the history: it holds no
// prompt text, and like the graph nothing about how any of it is drawn.
//
// The whole reason it exists is that a hundred graphs carry every prompt in
// full. A field that let the text back in would undo that without failing.
func TestPortfolioCarriesNoPromptTextOrPresentation(t *testing.T) {
	p, g := worked("example", "/work/example")

	body, err := json.Marshal(New("test", generated, []Family{Known(p).Read(g)}))
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(body, []byte(request)) {
		t.Errorf("a prompt was carried through in full:\n%s", body)
	}
	for _, banned := range []string{
		`"x"`, `"y"`, `"color"`, `"colour"`, `"width"`, `"height"`,
		`"radius"`, `"size"`, `"collapsed"`, `"expanded"`, `"zoom"`, `"font"`,
	} {
		if bytes.Contains(body, []byte(banned)) {
			t.Errorf("portfolio contains %s, which is a rendering concern", banned)
		}
	}
}
