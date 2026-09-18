package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/portfolio"
	"github.com/nickelsec/bough/internal/repo"
)

var portfolioNow = func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) }

// --portfolio writes one document holding every project, without asking which
// one to open. It is the whole machine or nothing: a chooser here would be
// asking which project to summarise them all from.
func TestPortfolioSummarisesEveryProject(t *testing.T) {
	root := twoProjects(t, "alpha", "beta")
	var out, errs bytes.Buffer

	if err := run([]string{"--portfolio", "--root", root}, &out, &errs); err != nil {
		t.Fatal(err)
	}

	var doc portfolio.Portfolio
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not a portfolio: %v\n%s", err, out.String())
	}
	if doc.Schema != portfolio.SchemaVersion {
		t.Errorf("document is schema %d, want %d", doc.Schema, portfolio.SchemaVersion)
	}
	if doc.Generated.IsZero() {
		t.Error("nothing says when this was generated, and history keeps growing")
	}
	names := map[string]int{}
	for _, f := range doc.Families {
		names[f.Name] = len(f.Sittings)
	}
	for _, want := range []string{"alpha", "beta"} {
		if _, ok := names[want]; !ok {
			t.Errorf("%q is missing from the portfolio: %v", want, names)
		}
		if names[want] == 0 {
			t.Errorf("%q summarised no sittings, though its history reads", want)
		}
	}
}

// The document is the summary, not a second copy of the history. A sitting's
// label is text somebody already wrote, so the opening of a prompt reaches it
// by design; the body of one never does, and that is the whole reason a
// portfolio can hold a hundred families where a hundred graphs could not.
func TestPortfolioIsSmallerThanTheGraphItSummarises(t *testing.T) {
	root := history(t, "example")
	var summary, whole, errs bytes.Buffer

	if err := run([]string{"--portfolio", "--root", root}, &summary, &errs); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"example", "--json", "--root", root}, &whole, &errs); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(summary.String(), "renders on the first page") {
		t.Errorf("a prompt was carried through in full:\n%s", summary.String())
	}
	if strings.Contains(summary.String(), `"text"`) {
		t.Errorf("the portfolio carries a prompt field:\n%s", summary.String())
	}
	if summary.Len() >= whole.Len() {
		t.Errorf("one project is %d bytes of portfolio and %d of graph; the summary has to be the smaller document",
			summary.Len(), whole.Len())
	}
}

// -o answers the same way here as it does for --json, and nothing goes to the
// screen when it is given.
func TestPortfolioWritesToAFile(t *testing.T) {
	root := history(t, "example")
	dest := filepath.Join(t.TempDir(), "portfolio.json")
	var out, errs bytes.Buffer

	if err := run([]string{"--portfolio", "--root", root, "-o", dest}, &out, &errs); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	var doc portfolio.Portfolio
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Errorf("file does not hold a portfolio: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("nothing should go to the screen when writing to a file, got: %s", out.String())
	}
}

// A project whose history will not open says so, rather than arriving looking
// like a project nobody has worked in. Both are no sittings, and the whole
// point of a portfolio is that a reader is looking at a hundred of these at
// once and cannot go and check.
func TestPortfolioSaysWhenAProjectCannotBeRead(t *testing.T) {
	b := builder{
		sources: map[string]agent.Source{
			"claude-code": readable{},
			"codex":       unreadable{},
		},
		disk:     &repo.Disk{},
		families: family.WithoutRepository(nil, nil),
		noRepo:   true,
		stderr:   io.Discard,
	}

	doc := b.portfolio([]family.Project{
		project("works", "/work/works", "claude-code"),
		project("broken", "/work/broken", "codex"),
	}, portfolioNow)

	works, broken := doc.Families[0], doc.Families[1]
	if len(works.Sittings) == 0 || works.Unreadable != "" {
		t.Errorf("a project that reads came back as %+v", works)
	}
	if broken.Unreadable == "" {
		t.Errorf("a project that could not be read came back as %+v", broken)
	}
	if !strings.Contains(broken.Unreadable, "disk is on fire") {
		t.Errorf("the reason is %q, which does not name the cause", broken.Unreadable)
	}
	// And it is still a family: a reader listing what is on the machine has to
	// see it, or the failure is invisible rather than reported.
	if broken.Name != "broken" || broken.Key != project("broken", "/work/broken", "codex").Key() {
		t.Errorf("the unreadable project lost its identity: %+v", broken)
	}
}

// Every project is summarised, however many there are and however few slots
// they are read through. Run under -race this is also the check that reading
// them at once is safe.
func TestPortfolioSummarisesMoreProjectsThanRunAtOnce(t *testing.T) {
	want := atOnce*3 + 1
	projects := make([]family.Project, want)
	for i := range projects {
		name := "p" + strconv.Itoa(i)
		projects[i] = project(name, "/work/"+name, "claude-code")
	}
	b := builder{
		sources:  map[string]agent.Source{"claude-code": readable{}},
		disk:     &repo.Disk{},
		families: family.WithoutRepository(nil, nil),
		noRepo:   true,
		stderr:   io.Discard,
	}

	doc := b.portfolio(projects, portfolioNow)

	if len(doc.Families) != want {
		t.Fatalf("summarised %d projects, want %d", len(doc.Families), want)
	}
	for i, f := range doc.Families {
		// In the order given, which is the order the resolver gathered them.
		// A worker pool that writes results as they finish loses that.
		if f.Name != projects[i].Name() {
			t.Errorf("family %d is %q, want %q", i, f.Name, projects[i].Name())
		}
		if len(f.Sittings) == 0 {
			t.Errorf("%q summarised no sittings", f.Name)
		}
	}
}

// project is a family worked in the one directory it is known by.
func project(name, path, agentID string) family.Project {
	return family.Project{Path: path, Members: []agent.Project{{Name: name, Path: path, Source: agentID}}}
}

// readable is a source with one sitting's worth of history in every project.
type readable struct{}

func (readable) Detect() ([]agent.Project, error) { return nil, nil }

func (readable) Sessions(projects ...agent.Project) ([]agent.Session, error) {
	sessions := make([]agent.Session, len(projects))
	for i, p := range projects {
		sessions[i] = agent.Session{ID: "s1", Source: p.Source, Turns: []agent.Turn{{
			At:    time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
			Text:  "rework the export path so an embedded font renders on the first page",
			Tools: map[string]int{},
			Files: map[string]int{p.Path + "/export.go": 1},
			Edits: map[string]int{p.Path + "/export.go": 1},
		}}}
	}
	return sessions, nil
}

// unreadable is a source whose history cannot be opened at all, which is a
// permission, a corrupt file, or a transcript with a line past the ceiling.
type unreadable struct{}

func (unreadable) Detect() ([]agent.Project, error) { return nil, nil }

func (unreadable) Sessions(...agent.Project) ([]agent.Session, error) {
	return nil, errors.New("disk is on fire")
}
