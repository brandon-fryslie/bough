package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/graph"
)

// spelled names the agents the way the command does, from a table of the
// test's own.
func spelled(id string) string {
	return map[string]string{"claude-code": "Claude Code", "codex": "Codex"}[id]
}

func sample() graph.Graph {
	start := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	return graph.Graph{
		Schema:  graph.SchemaVersion,
		Project: graph.Project{Name: "example", Path: "/work/example", Agents: []string{"claude-code"}},
		Goals: []graph.Goal{{
			ID:     "g1",
			Agent:  "claude-code",
			Label:  "rework the export path",
			Period: "Sat 1 Aug",
			Stats:  graph.Stats{Start: start, End: start.Add(time.Hour), Turns: 2},
			Tasks: []graph.Task{{
				ID:    "g1.t1",
				Label: "rework the export path",
				Stats: graph.Stats{Turns: 2},
				Turns: []graph.Turn{
					{At: start, Text: "rework the export path"},
					{At: start.Add(time.Minute), Text: "keep going"},
				},
			}},
			// Work in a family bough can open, and in a repository it has no
			// history for.
			Elsewhere: []graph.Visit{
				{Family: "/work/second", Path: "/work/second", Files: []graph.FileCount{{Path: "/work/second/importer.go", Edits: 3}}, RepoRead: true},
				{Family: "/work/unheard", Path: "/work/unheard", Commits: []graph.Commit{{SHA: "abc1234"}}},
			},
		}},
		Totals: graph.Stats{Turns: 2},
	}
}

// start runs a server and hands back its address, so a test can talk to it.
func start(t *testing.T, g graph.Graph) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	urls := make(chan string, 1)
	go Serve(ctx, g.Project.Path, g, nil, spelled, func(u string) { urls <- u })

	select {
	case url := <-urls:
		return url
	case <-time.After(5 * time.Second):
		t.Fatal("the server never reported an address")
		return ""
	}
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

// A person's coding history is theirs. Binding anything but loopback would put
// it on the network, so this is the test that matters most in this package.
func TestServesOnLoopbackOnly(t *testing.T) {
	url := start(t, sample())

	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("listening on %s, which is not loopback", url)
	}
}

// Serving is also how tests and measurement runs load the page, and each of
// those drives its own client. A server that opened the desktop browser put a
// stray window in front of the person running them, once per server started,
// so launching anything is left to the command that wants it.
//
// Checked on the package's own imports rather than by watching for a window,
// since proving a window never appeared means waiting for one that might yet.
func TestServingLaunchesNothing(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{join .Imports " "}}`, ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	imports := strings.Fields(string(out))
	if len(imports) == 0 {
		t.Fatal("go list returned no imports, so nothing was checked")
	}
	for _, imp := range imports {
		if imp == "os/exec" {
			t.Error("the server imports os/exec; opening a browser belongs to the command")
		}
	}
}

// Building a family's page is handed to the server by the command, so the
// server never learns how a family is found or read. Importing either would
// let a second way of building a graph grow in here beside the command's.
//
// Direct imports only: the graph this package draws is itself built on
// families, and seeing that type is not the same as reading one.
func TestServingReadsNoHistory(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{join .Imports " "}}`, ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	imports := strings.Fields(string(out))
	if len(imports) == 0 {
		t.Fatal("go list returned no imports, so nothing was checked")
	}
	for _, imp := range imports {
		if imp == "github.com/nickelsec/bough/internal/family" || strings.HasPrefix(imp, "github.com/nickelsec/bough/internal/agent") {
			t.Errorf("the server imports %s; building a family belongs to the command", imp)
		}
	}
}

func TestServesThePage(t *testing.T) {
	url := start(t, sample())
	resp, body := get(t, url+"/")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("content type %q, want html", got)
	}
	for _, want := range []string{"<style>", "window.BOUGH", "id=\"tree\"", "example"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
}

// The page has to work with no network at all, and keep working when saved to
// disk, so everything it needs is already inside it. That holds for every page
// the server hands out: a family's drawing however it was asked for, and the
// plain pages that say a drawing is not there.
func TestPageFetchesNothingExternal(t *testing.T) {
	gate := make(chan struct{})
	families := map[string]Build{
		"/work/second": func() (graph.Graph, error) { return second(), nil },
		"/work/slow":   func() (graph.Graph, error) { <-gate; return second(), nil },
		"/work/broken": func() (graph.Graph, error) { return graph.Graph{}, errors.New("no readable history for broken") },
	}
	base, built := site(t, sample(), families)
	t.Cleanup(func() { close(gate) })

	pages := map[string]string{
		"the first page":          body(t, base+"/", http.StatusOK),
		"a family asked for":      opened(t, base, built, "/work/second", ""),
		"a page still building":   body(t, family(base, "/work/slow", ""), http.StatusAccepted),
		"an unknown family":       body(t, family(base, "/work/none", ""), http.StatusNotFound),
		"a malformed date":        body(t, base+"/?from=soon", http.StatusBadRequest),
		"an unreadable history":   failure(t, base, built, "/work/broken"),
		"a page opened on a date": body(t, base+"/?from=2026-08-01", http.StatusOK),
	}
	for what, page := range pages {
		// The server's own address is allowed to appear, and so is the SVG
		// namespace, which is an identifier rather than somewhere to fetch
		// from.
		cleaned := strings.ReplaceAll(page, base, "")
		cleaned = strings.ReplaceAll(cleaned, "http://www.w3.org/2000/svg", "")

		for _, bad := range []string{"http://", "https://", "//cdn", "googleapis", "fonts.g"} {
			if strings.Contains(cleaned, bad) {
				t.Errorf("%s reaches out to %q", what, bad)
			}
		}
	}
}

// Inlining the graph means the page is whole the moment it loads, with nothing
// to wait on and nothing to break if it is saved and opened later.
func TestGraphIsInlinedAndParses(t *testing.T) {
	g := sample()
	url := start(t, g)
	_, body := get(t, url+"/")

	parsed := inlined(t, body)
	if parsed.Project.Name != g.Project.Name {
		t.Errorf("inlined project is %q, want %q", parsed.Project.Name, g.Project.Name)
	}
	if len(parsed.Goals) != len(g.Goals) {
		t.Errorf("inlined %d goals, want %d", len(parsed.Goals), len(g.Goals))
	}
}

// inlined is the graph a page carries, failing the test when there is none.
func inlined(t *testing.T, body string) graph.Graph {
	t.Helper()
	const marker = "window.BOUGH = "
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatal("the graph was not inlined")
	}
	rest := body[i+len(marker):]
	end := strings.Index(rest, ";</script>")
	if end < 0 {
		t.Fatal("could not find the end of the inlined graph")
	}

	var parsed graph.Graph
	if err := json.Unmarshal([]byte(rest[:end]), &parsed); err != nil {
		t.Fatalf("the inlined graph is not valid JSON: %v", err)
	}
	return parsed
}

// elsewhereIn is what a page was told about work done in other families,
// failing the test when it was told nothing.
func elsewhereIn(t *testing.T, body string) away {
	t.Helper()
	const marker = "window.BOUGH_ELSEWHERE = "
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatal("the work done elsewhere was not inlined")
	}
	rest := body[i+len(marker):]
	end := strings.Index(rest, ";</script>")
	if end < 0 {
		t.Fatal("could not find the end of the inlined work done elsewhere")
	}

	var parsed away
	if err := json.Unmarshal([]byte(rest[:end]), &parsed); err != nil {
		t.Fatalf("the inlined work done elsewhere is not valid JSON: %v", err)
	}
	return parsed
}

func TestServesTheArtwork(t *testing.T) {
	url := start(t, sample())

	for _, name := range []string{"logo.png", "icon-32.png", "icon-180.png"} {
		resp, body := get(t, url+"/img/"+name)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d, want 200", name, resp.StatusCode)
			continue
		}
		if len(body) == 0 {
			t.Errorf("%s came back empty", name)
		}
		if got := resp.Header.Get("Content-Type"); got != "image/png" {
			t.Errorf("%s: content type %q", name, got)
		}
	}
}

func TestUnknownPathIsNotFound(t *testing.T) {
	url := start(t, sample())
	resp, _ := get(t, url+"/nothing-here")

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
}

// A project with nothing in it still has to produce a page rather than an
// error, since somebody will point this at a directory they barely used.
func TestEmptyGraphStillRenders(t *testing.T) {
	url := start(t, graph.Graph{
		Schema:  graph.SchemaVersion,
		Project: graph.Project{Name: "empty", Agents: []string{"claude-code"}},
	})
	resp, body := get(t, url+"/")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "empty") {
		t.Error("the page does not name the project")
	}
}

// Stopping should not leave the port held.
func TestShutdownIsClean(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	urls := make(chan string, 1)
	done := make(chan error, 1)

	go func() { done <- Serve(ctx, "/work/example", sample(), nil, spelled, func(u string) { urls <- u }) }()
	<-urls
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("stopping gave an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("the server did not stop")
	}
}

// People paste markup into these conversations, so a prompt can contain the
// text that ends a script element. Left alone it would cut the page in half.
func TestPromptsCannotBreakOutOfTheScript(t *testing.T) {
	g := sample()
	g.Goals[0].Tasks[0].Turns[0].Text = `look at </script><script>alert(1)</script> this`
	// Along with the placeholder the date range goes in, which a prompt can
	// hold as easily as any other text.
	g.Goals[0].Tasks[0].Turns[1].Text = `{{.Range}}`
	// A family is known by a directory, whose name can hold the same text. It
	// reaches the page as a key the server can open and in the words for the
	// work done there.
	hostile := `/work/</script><script>alert(2)</script>`
	g.Goals[0].Elsewhere = []graph.Visit{{Family: hostile, Path: `/work/x</script><script>alert(3)</script>`, Commits: []graph.Commit{{SHA: "abc1234"}}, RepoRead: true}}

	base, built := site(t, g, map[string]Build{
		"/work/other": func() (graph.Graph, error) { return g, nil },
		hostile:       func() (graph.Graph, error) { return g, nil },
	})
	for what, page := range map[string]string{
		"the first page":     body(t, base+"/?from=2026-08-01", http.StatusOK),
		"a family asked for": opened(t, base, built, "/work/other", "&from=2026-08-01"),
	} {
		// The dangerous sequence must not survive into the page as written.
		if strings.Contains(page, "</script><script>alert") {
			t.Errorf("%s: a prompt closed the script element and opened another", what)
		}
		// It still has to be there, since the prompt is what the reader came
		// to see. Go's encoder escapes the angle brackets on its way into
		// JSON, which already neutralises this; the replacer is a second line
		// in case that behaviour is ever turned off.
		if !strings.Contains(page, `u003c/script`) && !strings.Contains(page, `<\/script`) {
			t.Errorf("%s: the prompt was lost rather than escaped", what)
		}
		// The prompt holding the placeholder is still that prompt, and the
		// range went where the page reads it and nowhere else.
		if got := inlined(t, page).Goals[0].Tasks[0].Turns[1].Text; got != `{{.Range}}` {
			t.Errorf("%s: a prompt reading {{.Range}} came out as %q", what, got)
		}
		if n := strings.Count(page, `"from":"2026-08-01"`); n != 1 {
			t.Errorf("%s: the range appears %d times, want once", what, n)
		}
		// And the family's key and its words survive as written.
		if a := elsewhereIn(t, page); !a.Served[hostile] || a.Said["g1"][hostile] != graph.DoneIn(g.Goals[0].Elsewhere[0]) {
			t.Errorf("%s: the family holding markup came out as %+v", what, a)
		}
	}
}

// A project name showing up in the title should be text, not markup.
func TestProjectNameIsEscaped(t *testing.T) {
	g := sample()
	g.Project.Name = `a<b"c&d`

	url := start(t, g)
	_, body := get(t, url+"/")

	if strings.Contains(body, `<title>a<b`) {
		t.Error("the project name went into the title unescaped")
	}
	if !strings.Contains(body, "a&lt;b&quot;c&amp;d") {
		t.Error("the project name was not escaped as expected")
	}
}
