package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nickelsec/bough/internal/perf"
	"github.com/nickelsec/bough/internal/synthetic"
)

// stalled is a run at 120 Hz whose input was drawn with one frame held up by
// stall milliseconds, so its worst frame and missed frames grow with the
// stall while its median stays at rest.
func stalled(t *testing.T, stall float64) take {
	t.Helper()
	return drawnEvery(t, 8.333, stall)
}

// drawnEvery is stalled on a display that starts a frame every refresh
// milliseconds.
func drawnEvery(t *testing.T, refresh, stall float64) take {
	t.Helper()
	frames := make([]float64, 80)
	at := 1000.0
	for i := range frames {
		at += refresh
		if i == 40 {
			at += stall
		}
		frames[i] = at
	}
	raw, err := json.Marshal(map[string]any{"frames": frames, "inputs": []int{35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := perf.ParseRecording(raw)
	if err != nil {
		t.Fatal(err)
	}
	return take{recording: r}
}

// runs is a run stalled by each of stalls.
func runs(t *testing.T, stalls ...float64) []take {
	t.Helper()
	out := make([]take, len(stalls))
	for i, stall := range stalls {
		out[i] = stalled(t, stall)
	}
	return out
}

// kept is a run of one graph with chrome having played scenarios.
func kept(revision string, scenarios ...measured) results {
	return results{
		Revision: revision,
		Graph:    measuredGraph{Name: "synthetic small", SHA256: "abc"},
		Browsers: []browser{played{Browser: "chrome", Version: "152", Scenarios: scenarios}},
	}
}

// verdicts is what a comparison made of each metric of the one scenario it
// compared.
func verdicts(t *testing.T, before, after []take) map[string]verdict {
	t.Helper()
	lines, err := compare(kept("a", measured{Scenario: "drag-pan", Runs: before}), kept("b", measured{Scenario: "drag-pan", Runs: after}))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := lines[0].(compared)
	if len(lines) != 1 || !ok {
		t.Fatalf("compared %+v, want drag-pan alone", lines)
	}
	out := map[string]verdict{}
	for _, m := range metrics {
		out[m.name] = judge(spreadOf(m, c.before.recordings), spreadOf(m, c.after.recordings), m.jitter(c.refresh))
	}
	return out
}

// A change is only called better or worse when every run after it is past
// every run before, with runs enough that noise would rarely line up so, and
// a move larger than frame times jitter; anything else is not a result.
func TestAChangeCountsOnlyBeyondTheNoise(t *testing.T) {
	for _, c := range []struct {
		name          string
		before, after []take
		want          map[string]verdict
	}{
		{
			"stalls that went away",
			runs(t, 100, 105, 110, 115, 120),
			runs(t, 0, 1, 2, 3, 4),
			map[string]verdict{"median": withinSpread, "worst": better, "missed": better},
		},
		{
			"stalls that arrived",
			runs(t, 0, 1, 2, 3, 4),
			runs(t, 100, 105, 110, 115, 120),
			map[string]verdict{"median": withinSpread, "worst": worse, "missed": worse},
		},
		{
			"a change inside the spread",
			runs(t, 0, 25, 50, 75, 100),
			runs(t, 40, 45, 50, 55, 60),
			map[string]verdict{"worst": withinSpread, "missed": withinSpread},
		},
		{
			"a spread that only widened",
			runs(t, 40, 45, 50, 55, 60),
			runs(t, 0, 25, 50, 75, 100),
			map[string]verdict{"worst": withinSpread, "missed": withinSpread},
		},
		{
			// Every run after is past every run before, by less than half a
			// frame: what identical code measured twice looks like.
			"a move smaller than a frame",
			runs(t, 0, 0.1, 0.2, 0.3, 0.4),
			runs(t, 2, 2.1, 2.2, 2.3, 2.4),
			map[string]verdict{"p95": withinSpread, "worst": withinSpread, "missed": withinSpread},
		},
		{
			"a move smaller than a frame, the other way",
			runs(t, 2, 2.1, 2.2, 2.3, 2.4),
			runs(t, 0, 0.1, 0.2, 0.3, 0.4),
			map[string]verdict{"p95": withinSpread, "worst": withinSpread, "missed": withinSpread},
		},
		{
			"four runs a side",
			runs(t, 100, 100, 100, 100),
			runs(t, 0, 0, 0, 0),
			map[string]verdict{"median": tooFewRuns, "worst": tooFewRuns, "missed": tooFewRuns},
		},
		{
			// Fewer runs on one side are made up for by more on the other.
			"four runs before, six after",
			runs(t, 100, 105, 110, 115),
			runs(t, 0, 1, 2, 3, 4, 5),
			map[string]verdict{"worst": better, "missed": better},
		},
		{
			"failed runs are not runs",
			append(runs(t, 100, 105, 110, 115), take{err: errors.New("did not take")}),
			runs(t, 0, 1, 2, 3, 4),
			map[string]verdict{"worst": tooFewRuns},
		},
	} {
		got := verdicts(t, c.before, c.after)
		for metric, want := range c.want {
			if got[metric] != want {
				t.Errorf("%s: %s is %q, want %q", c.name, metric, got[metric], want)
			}
		}
	}
}

// Every run of one side past every run of the other happens by noise alone
// in two of the ways the runs can be ranked together.
func TestTheChanceOfNoiseLiningUp(t *testing.T) {
	for _, c := range []struct {
		n, m int
		want float64
	}{
		{1, 1, 1},
		{2, 2, 1.0 / 3},
		{3, 3, 1.0 / 10},
		{5, 5, 1.0 / 126},
		{4, 6, 1.0 / 105},
	} {
		if got := chance(c.n, c.m); got < c.want*0.999999 || got > c.want*1.000001 {
			t.Errorf("chance(%d, %d) = %v, want %v", c.n, c.m, got, c.want)
		}
	}
}

// Runs of different graphs measure different pages, so they are not compared
// at all, even under the same name.
func TestRunsOfDifferentGraphsAreRefused(t *testing.T) {
	before := kept("a", measured{Scenario: "drag-pan", Runs: []take{stalled(t, 0), stalled(t, 0)}})
	after := before
	after.Graph.SHA256 = "abd"
	if _, err := compare(before, after); err == nil || !strings.Contains(err.Error(), "different graphs") {
		t.Errorf("compared with %v, want a refusal", err)
	}
}

// A graph's fingerprint is the same every time it is built, and different for
// a different graph, whatever the two are named.
func TestAGraphsFingerprintIsItsContent(t *testing.T) {
	sum := func(size string) string {
		t.Helper()
		shape, err := synthetic.Sized(size)
		if err != nil {
			t.Fatal(err)
		}
		g, err := fingerprint("the same name", synthetic.History(shape))
		if err != nil {
			t.Fatal(err)
		}
		return g.SHA256
	}
	if first, again := sum("small"), sum("small"); first != again {
		t.Error("one graph fingerprinted two ways")
	}
	if sum("small") == sum("medium") {
		t.Error("two graphs fingerprinted the same")
	}
}

// Whatever cannot be compared is still reported, with why on each side, so a
// scenario missing from a comparison is never mistaken for one unchanged.
func TestWhatCannotBeComparedSaysWhy(t *testing.T) {
	ok := []take{stalled(t, 0), stalled(t, 0)}
	before := kept("a",
		measured{Scenario: "drag-pan", Runs: ok},
		measured{Scenario: "hover-sweep", Runs: ok},
		measured{Scenario: "wheel-zoom-in", Runs: ok},
	)
	before.Browsers = append(before.Browsers, skipped{Browser: "safari", Skipped: "its input is dropped"})
	after := kept("b",
		measured{Scenario: "drag-pan", Runs: ok},
		measured{Scenario: "hover-sweep", Runs: []take{{err: errors.New("no note")}}},
	)
	after.Browsers = append(after.Browsers, unopened{Browser: "safari", Failed: "no remote automation"})

	lines, err := compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for _, l := range lines {
		l.report(&out)
	}
	for _, want := range []string{
		"chrome drag-pan: 2 runs in 152 → 2 runs in 152",
		"chrome hover-sweep: not compared; before, recorded; after, no run of hover-sweep recorded",
		"chrome wheel-zoom-in: not compared; before, recorded; after, wheel-zoom-in not played",
		"safari: not compared; before, skipped, its input is dropped; after, failed to open, no remote automation",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the comparison does not say %q:\n%s", want, out.String())
		}
	}
}

// A scenario is compared only between runs drawn on displays refreshing at
// one rate, since every frame time moves with the display: a faster display
// is not a faster page.
func TestRunsOnAnotherDisplayAreNotCompared(t *testing.T) {
	for _, c := range []struct {
		name          string
		before, after float64
		compared      bool
	}{
		{"one display", 8.333, 8.333, true},
		{"one display, measured a little apart", 8.333, 8.6, true},
		{"60 Hz, then 120 Hz", 16.667, 8.333, false},
		{"144 Hz, then 120 Hz", 6.944, 8.333, false},
	} {
		lines, err := compare(
			kept("a", measured{Scenario: "drag-pan", Runs: []take{drawnEvery(t, c.before, 0)}}),
			kept("b", measured{Scenario: "drag-pan", Runs: []take{drawnEvery(t, c.after, 0)}}),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, got := lines[0].(compared); got != c.compared {
			var out bytes.Buffer
			lines[0].report(&out)
			t.Errorf("%s: compared is %v, want %v: %s", c.name, got, c.compared, out.String())
		}
	}

	lines, err := compare(
		kept("a", measured{Scenario: "drag-pan", Runs: []take{drawnEvery(t, 16.667, 0)}}),
		kept("b", measured{Scenario: "drag-pan", Runs: []take{drawnEvery(t, 8.333, 0)}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	lines[0].report(&out)
	if want := "chrome drag-pan: not compared; before, drawn every 16.7ms at rest; after, drawn every 8.3ms at rest"; !strings.Contains(out.String(), want) {
		t.Errorf("said %q, want %q", out.String(), want)
	}
}

// A kept file reads back as the results that were kept, every kind of browser
// and run as it was, and a file no run could have written is refused.
func TestKeptResultsReadBack(t *testing.T) {
	want := kept("a", measured{Scenario: "drag-pan", Runs: []take{stalled(t, 10), {err: errors.New("did not take")}}})
	want.Browsers = append(want.Browsers,
		skipped{Browser: "safari", Skipped: "its input is dropped"},
		unopened{Browser: "firefox", Failed: "no driver"},
	)
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got results
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("did not read back: %v\n%s", err, body)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back %+v, kept %+v", got, want)
	}

	recording, err := json.Marshal(stalled(t, 0).recording)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"a browser both skipped and played": `{"graph":{"name":"g","sha256":"abc"},"browsers":[{"browser":"x","skipped":"y","scenarios":[]}]}`,
		"a browser neither":                 `{"graph":{"name":"g","sha256":"abc"},"browsers":[{"browser":"x"}]}`,
		"a run both recorded and failed":    `{"graph":{"name":"g","sha256":"abc"},"browsers":[{"browser":"x","version":"1","scenarios":[{"scenario":"s","runs":[{"failed":"z","recording":` + string(recording) + `}]}]}]}`,
		"a run neither":                     `{"graph":{"name":"g","sha256":"abc"},"browsers":[{"browser":"x","version":"1","scenarios":[{"scenario":"s","runs":[{}]}]}]}`,
		"no graph fingerprint":              `{"graph":{"name":"g"},"browsers":[]}`,
		"a browser kept twice":              `{"graph":{"name":"g","sha256":"abc"},"browsers":[{"browser":"x","skipped":"y"},{"browser":"x","failed":"z"}]}`,
		"a scenario kept twice":             `{"graph":{"name":"g","sha256":"abc"},"browsers":[{"browser":"x","version":"1","scenarios":[{"scenario":"s","runs":[]},{"scenario":"s","runs":[]}]}]}`,
		"a browser that played nothing":     `{"graph":{"name":"g","sha256":"abc"},"browsers":[{"browser":"x","version":"1","scenarios":[]}]}`,
	} {
		if err := json.Unmarshal([]byte(raw), &results{}); err == nil {
			t.Errorf("%s: read", name)
		}
	}

	// Results kept before graphs were fingerprinted are refused for that, not
	// for the bare string they named the graph with.
	if err := json.Unmarshal([]byte(`{"graph":"synthetic small","browsers":[]}`), &results{}); err == nil || !strings.Contains(err.Error(), "do not fingerprint") {
		t.Errorf("read results kept before fingerprints with %v, want them refused for lacking one", err)
	}
}

// compare is its own command: two kept files in, what changed out, and the
// statuses its usage promises.
func TestTheCompareCommand(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, r results) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := keep(path, r); err != nil {
			t.Fatal(err)
		}
		return path
	}
	before := write("before.json", kept("v1", measured{Scenario: "drag-pan", Runs: runs(t, 100, 105, 110, 115, 120)}))
	after := write("after.json", kept("v2", measured{Scenario: "drag-pan", Runs: runs(t, 0, 1, 2, 3, 4)}))
	other := kept("v3", measured{Scenario: "drag-pan", Runs: runs(t, 0, 1, 2, 3, 4)})
	other.Graph.SHA256 = "another"
	elsewhere := write("other.json", other)

	var stdout, stderr bytes.Buffer
	if status := command([]string{"compare", before, after}, &stdout, &stderr); status != measuredAll {
		t.Fatalf("status %d, said %q", status, stderr.String())
	}
	if !strings.Contains(stdout.String(), "synthetic small, v1 → v2") || !strings.Contains(stdout.String(), "better") {
		t.Errorf("printed %q, want the revisions and the worst frame better", stdout.String())
	}

	for _, c := range []struct {
		args   []string
		status int
	}{
		{[]string{"compare", before}, badFlags},
		{[]string{"compare", before, after, after}, badFlags},
		{[]string{"compare", before, filepath.Join(dir, "absent.json")}, someFailed},
		{[]string{"compare", before, elsewhere}, someFailed},
	} {
		if status := command(c.args, &bytes.Buffer{}, &bytes.Buffer{}); status != c.status {
			t.Errorf("%q: status %d, want %d", c.args[1:], status, c.status)
		}
	}
}
