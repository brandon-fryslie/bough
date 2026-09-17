package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/graph"
)

// second is a family other than the one bough opened on.
func second() graph.Graph {
	g := sample()
	g.Project.Name = "second"
	g.Project.Path = "/work/second"
	g.Goals[0].Label = "write the importer"
	return g
}

// site serves first as the page bough opened on, keyed by its path, and builds
// every family in families when it is asked for. The pages come back with the
// address so a test can wait on a build rather than guess how long it takes.
func site(t *testing.T, first graph.Graph, families map[string]Build) (string, *pages) {
	t.Helper()
	built, err := newPages(first.Project.Path, first, families, spelled)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = routes(built, first.Project.Path, srv.Listener.Addr().String())
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL, built
}

// family is the address of a family's page, with any more of the query after.
func family(base, key, more string) string {
	return base + "/family?key=" + url.QueryEscape(key) + more
}

// body fetches an address and fails the test unless it answers with status.
func body(t *testing.T, address string, status int) string {
	t.Helper()
	resp, text := get(t, address)
	if resp.StatusCode != status {
		t.Fatalf("%s: status %d, want %d\n%s", address, resp.StatusCode, status, text)
	}
	return text
}

// settled waits for the build of key's page to finish.
func settled(t *testing.T, built *pages, key string) {
	t.Helper()
	built.mu.Lock()
	m, ok := built.byKey[key]
	built.mu.Unlock()
	if !ok {
		t.Fatalf("nothing is building %s", key)
	}
	select {
	case <-m.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("the build of %s never finished", key)
	}
}

// opened asks for a family's page, which starts it building, waits for the
// build and asks again, handing back the page.
func opened(t *testing.T, base string, built *pages, key, more string) string {
	t.Helper()
	return settle(t, base, built, key, more, http.StatusOK)
}

// failure asks for a family whose build fails, waits for it and asks again,
// handing back the page that says why.
func failure(t *testing.T, base string, built *pages, key string) string {
	t.Helper()
	return settle(t, base, built, key, "", http.StatusInternalServerError)
}

// settle asks for a family, waits for its build and asks again, which has to
// answer with status. The first answer is the building page, or already the
// finished one, since a build this fast can be done before the first request
// is answered: which of the two it is belongs to the scheduler.
func settle(t *testing.T, base string, built *pages, key, more string, status int) string {
	t.Helper()
	resp, text := get(t, family(base, key, more))
	switch resp.StatusCode {
	case status:
		return text
	case http.StatusAccepted:
	default:
		t.Fatalf("%s: status %d, want 202 or %d\n%s", key, resp.StatusCode, status, text)
	}
	settled(t, built, key)
	return body(t, family(base, key, more), status)
}

// counted is a build of g that counts how often it runs.
func counted(g graph.Graph, calls *atomic.Int32) Build {
	return func() (graph.Graph, error) {
		calls.Add(1)
		return g, nil
	}
}

// A running server opens a family other than the one it started on, drawn the
// same way, and still serves the first.
func TestServesASecondFamily(t *testing.T) {
	base, built := site(t, sample(), map[string]Build{"/work/second": func() (graph.Graph, error) { return second(), nil }})

	page := opened(t, base, built, "/work/second", "")
	g := inlined(t, page)
	if g.Project.Name != "second" || g.Goals[0].Label != "write the importer" {
		t.Errorf("the family asked for drew %q with %q, want second's", g.Project.Name, g.Goals[0].Label)
	}
	if !strings.Contains(page, "<title>second · bough</title>") {
		t.Error("the page is not titled for the family asked for")
	}

	if g := inlined(t, body(t, base+"/", http.StatusOK)); g.Project.Name != "example" {
		t.Errorf("after opening another family, / draws %q, want example", g.Project.Name)
	}
}

// A page links to another family exactly when this server can open that
// family's page: whether it is the page bough opened on or one built on
// request, it is told which of the families its sittings worked in are
// served, and each visit in the words the terminal uses.
func TestAPageIsToldWhichFamiliesItCanOpen(t *testing.T) {
	third := second()
	third.Goals[0].ID = "g7"
	third.Goals[0].Elsewhere = []graph.Visit{
		{Family: "/work/example", Path: "/work/example", Commits: []graph.Commit{{SHA: "abc1234"}}, RepoRead: true},
		{Family: "/work/elsewhere", Path: "/work/elsewhere", Files: []graph.FileCount{{Path: "/work/elsewhere/a", Edits: 2}}},
	}
	base, built := site(t, sample(), map[string]Build{
		"/work/example": func() (graph.Graph, error) { return sample(), nil },
		"/work/second":  func() (graph.Graph, error) { return third, nil },
	})

	for _, c := range []struct {
		what    string
		page    string
		g       graph.Graph
		served  []string
		unknown []string
	}{
		{"the first page", body(t, base+"/", http.StatusOK), sample(), []string{"/work/second"}, []string{"/work/unheard"}},
		{"a family asked for", opened(t, base, built, "/work/second", ""), third, []string{"/work/example"}, []string{"/work/elsewhere"}},
	} {
		a := elsewhereIn(t, c.page)
		if len(a.Served) != len(c.served) {
			t.Errorf("%s serves %v, want only %v", c.what, a.Served, c.served)
		}
		for _, key := range c.served {
			if !a.Served[key] {
				t.Errorf("%s is not told it can open %s", c.what, key)
			}
		}
		for _, key := range c.unknown {
			if _, ok := a.Served[key]; ok {
				t.Errorf("%s is told it can open %s, which no family has", c.what, key)
			}
		}
		for _, goal := range c.g.Goals {
			for _, v := range goal.Elsewhere {
				if got, want := a.Said[goal.ID][v.Family], graph.DoneIn(v); got != want {
					t.Errorf("%s says %q for the work in %s, where the terminal says %q", c.what, got, v.Family, want)
				}
			}
		}
	}
}

// A family is built once. Every request after is served from memory, including
// a request by key for the family bough opened on, which was built before the
// server started.
func TestARepeatRequestIsServedFromMemory(t *testing.T) {
	var firstCalls, secondCalls atomic.Int32
	base, built := site(t, sample(), map[string]Build{
		"/work/example": counted(sample(), &firstCalls),
		"/work/second":  counted(second(), &secondCalls),
	})

	opened(t, base, built, "/work/second", "")
	for range 3 {
		if g := inlined(t, body(t, family(base, "/work/second", ""), http.StatusOK)); g.Project.Name != "second" {
			t.Errorf("a repeat request drew %q, want second", g.Project.Name)
		}
		if g := inlined(t, body(t, family(base, "/work/example", ""), http.StatusOK)); g.Project.Name != "example" {
			t.Errorf("the first family asked for by key drew %q, want example", g.Project.Name)
		}
	}

	if n := secondCalls.Load(); n != 1 {
		t.Errorf("the second family was built %d times, want once", n)
	}
	if n := firstCalls.Load(); n != 0 {
		t.Errorf("the family bough opened on was built again %d times on request", n)
	}
}

// A build takes seconds on a large history. Until it is done the request is
// answered at once with a page saying so, which asks again by itself.
func TestASlowBuildShowsItIsBuilding(t *testing.T) {
	gate := make(chan struct{})
	var calls atomic.Int32
	base, built := site(t, sample(), map[string]Build{"/work/second": func() (graph.Graph, error) {
		calls.Add(1)
		<-gate
		return second(), nil
	}})

	for range 2 {
		resp, waiting := get(t, family(base, "/work/second", ""))
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status %d while the build is held, want 202", resp.StatusCode)
		}
		if !strings.Contains(waiting, "Reading the history of /work/second") {
			t.Errorf("the page does not say the family is building:\n%s", waiting)
		}
		// It asks the address it came from, which carries the key.
		if got := resp.Header.Get("Refresh"); got != "1" {
			t.Errorf("the building page refreshes with %q, want every second", got)
		}
		if strings.Contains(waiting, "window.BOUGH") {
			t.Error("the building page carries a drawing")
		}
	}

	close(gate)
	settled(t, built, "/work/second")
	if g := inlined(t, body(t, family(base, "/work/second", ""), http.StatusOK)); g.Project.Name != "second" {
		t.Errorf("once built, the page draws %q, want second", g.Project.Name)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("asking while it built started %d builds, want one", n)
	}
}

// Requests that arrive together for a family nobody has built share one
// build, so its history is not read twice at once.
func TestConcurrentRequestsShareOneBuild(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{}, 8)
	var calls atomic.Int32
	base, built := site(t, sample(), map[string]Build{"/work/second": func() (graph.Graph, error) {
		calls.Add(1)
		started <- struct{}{}
		<-gate
		return second(), nil
	}})

	var wg sync.WaitGroup
	statuses := make([]int, 8)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(family(base, "/work/second", ""))
			if err != nil {
				t.Error(err)
				return
			}
			_ = resp.Body.Close()
			statuses[i] = resp.StatusCode
		}()
	}
	wg.Wait()
	// The build runs on its own, so it is waited for rather than assumed to
	// have begun by the time the last answer arrived.
	<-started

	for i, status := range statuses {
		if status != http.StatusAccepted {
			t.Errorf("request %d: status %d, want 202 while the build is held", i, status)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("%d requests started %d builds, want one", len(statuses), n)
	}

	close(gate)
	settled(t, built, "/work/second")
	body(t, family(base, "/work/second", ""), http.StatusOK)
	if n := calls.Load(); n != 1 {
		t.Errorf("serving the built page started more builds: %d", n)
	}
}

// A request can carry a date range, which reaches the page inlined beside the
// graph for the page to apply. Either end may be left open, and a request with
// neither opens the page unfiltered.
func TestADateRangeIsInlinedIntoThePage(t *testing.T) {
	base, built := site(t, sample(), map[string]Build{"/work/second": func() (graph.Graph, error) { return second(), nil }})
	opened(t, base, built, "/work/second", "")

	for _, c := range []struct {
		query, want string
	}{
		{"from=2026-08-01&to=2026-08-02", `{"from":"2026-08-01","to":"2026-08-02"}`},
		{"from=2026-08-01", `{"from":"2026-08-01","to":""}`},
		{"to=2026-08-02", `{"from":"","to":"2026-08-02"}`},
		{"from=2026-08-01&to=2026-08-01", `{"from":"2026-08-01","to":"2026-08-01"}`},
		{"", `{"from":"","to":""}`},
	} {
		for what, page := range map[string]string{
			"the first page":     body(t, base+"/?"+c.query, http.StatusOK),
			"a family asked for": body(t, family(base, "/work/second", "&"+c.query), http.StatusOK),
		} {
			if want := "<script>window.BOUGH_RANGE = " + c.want + ";</script>"; !strings.Contains(page, want) {
				t.Errorf("%s with %q does not carry %s", what, c.query, want)
			}
		}
	}
}

// The page applies an inlined range the way a person applies one they typed:
// into the date fields, then the Apply button. A range set on the filters
// alone would filter the drawing while the panel showed no dates, and the
// Clear button would have nothing to clear.
//
// Checked in the script's source, since the rail needs a browser to run.
func TestThePageAppliesTheRangeThroughTheDateFields(t *testing.T) {
	b, err := assets.ReadFile("bough.js")
	if err != nil {
		t.Fatal(err)
	}
	src := withoutComments(string(b))

	applied := []string{
		"var range = window.BOUGH_RANGE;",
		"from.value = range.from;",
		"to.value = range.to;",
		`document.getElementById("f-when-go").click();`,
	}
	at := 0
	for _, line := range applied {
		i := strings.Index(src[at:], line)
		if i < 0 {
			t.Fatalf("the script does not apply the inlined range with %q after what comes before it", line)
		}
		at += i + len(line)
	}
	// And it is the same Apply that reads those fields into the filters.
	if !strings.Contains(src, "filters.from = from.value || null;") || !strings.Contains(src, "filters.to = to.value || null;") {
		t.Error("the Apply button no longer reads the date fields into the filters")
	}
}

// A date that cannot be read, or a range that runs backwards, is refused with
// a page that says what was wrong, before any family is built.
func TestAMalformedDateIsABadRequest(t *testing.T) {
	var calls atomic.Int32
	base, _ := site(t, sample(), map[string]Build{"/work/second": counted(second(), &calls)})

	for _, c := range []struct {
		query, says string
	}{
		{"from=yesterday", `from="yesterday" is not a date written as YYYY-MM-DD`},
		{"to=2026-13-01", `to="2026-13-01" is not a date`},
		{"from=2026-8-1", `from="2026-8-1" is not a date`},
		{"to=2026-02-30", `to="2026-02-30" is not a date`},
		{"from=2026-08-02&to=2026-08-01", "the range runs backwards: from 2026-08-02 is after to 2026-08-01"},
		{"from=%3C/script%3E", `from="</script>" is not a date`},
	} {
		for _, address := range []string{base + "/?" + c.query, family(base, "/work/second", "&"+c.query)} {
			page := body(t, address, http.StatusBadRequest)
			if !strings.Contains(page, c.says) {
				t.Errorf("%s says\n%s\nwant it to say %s", address, page, c.says)
			}
			if strings.Contains(page, "window.BOUGH") {
				t.Errorf("%s drew a page for a range it refused", address)
			}
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("a refused request started %d builds", n)
	}
}

// An identifier no family has is a plain page saying so.
func TestAnUnknownFamilyIsNotFound(t *testing.T) {
	base, _ := site(t, sample(), map[string]Build{"/work/second": func() (graph.Graph, error) { return second(), nil }})

	for _, key := range []string{"/work/none", "", "/work/second/..", "<script>alert(1)</script>"} {
		resp, page := get(t, family(base, key, ""))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%q: status %d, want 404", key, resp.StatusCode)
		}
		// Plain text, so an identifier holding markup is shown as written.
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%q: served as %q, want plain text the browser does not sniff", key, got)
		}
		if !strings.Contains(page, `No project has the identifier "`+key+`".`) {
			t.Errorf("%q: the page does not say no project has it:\n%s", key, page)
		}
		if strings.Contains(page, "window.BOUGH") {
			t.Errorf("%q: an unknown family drew a page", key)
		}
	}
}

// A family whose history cannot be read says that, with the reason, and draws
// nothing. An empty drawing would read as a project with no work in it.
func TestUnreadableHistorySaysSo(t *testing.T) {
	base, built := site(t, sample(), map[string]Build{"/work/broken": func() (graph.Graph, error) {
		return graph.Graph{}, errors.New("no readable history for broken\nreading s1.jsonl: token too long")
	}})

	page := failure(t, base, built, "/work/broken")
	for _, says := range []string{"no readable history for broken", "reading s1.jsonl: token too long"} {
		if !strings.Contains(page, says) {
			t.Errorf("the page does not say %q:\n%s", says, page)
		}
	}
	for _, drawing := range []string{"window.BOUGH", `id="tree"`, "<svg"} {
		if strings.Contains(page, drawing) {
			t.Errorf("the page for an unreadable history carries %q, which is a drawing", drawing)
		}
	}
}

// A failed build is not kept. The next request reads the history again, so
// a history fixed while bough runs opens without a restart.
func TestAFailedBuildIsTriedAgain(t *testing.T) {
	var calls atomic.Int32
	base, built := site(t, sample(), map[string]Build{"/work/second": func() (graph.Graph, error) {
		if calls.Add(1) == 1 {
			return graph.Graph{}, errors.New("no readable history for second")
		}
		return second(), nil
	}})

	failure(t, base, built, "/work/second")
	if g := inlined(t, opened(t, base, built, "/work/second", "")); g.Project.Name != "second" {
		t.Errorf("the second try drew %q, want second", g.Project.Name)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("built %d times, want twice: once failing and once again", n)
	}
}

// Only requests addressed to the server's own address are answered. A page
// elsewhere that points a name of its own at 127.0.0.1 sends that name as the
// host, and is refused before any family is built for it.
func TestARequestForAnotherHostIsRefused(t *testing.T) {
	var calls atomic.Int32
	base, _ := site(t, sample(), map[string]Build{"/work/second": counted(second(), &calls)})

	for _, address := range []string{base + "/", family(base, "/work/second", ""), base + "/img/logo.png"} {
		req, err := http.NewRequest(http.MethodGet, address, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "rebound.example:" + strings.Split(base, ":")[2]
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMisdirectedRequest {
			t.Errorf("%s asked for as %s: status %d, want 421", address, req.Host, resp.StatusCode)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("a refused request started %d builds", n)
	}
}
