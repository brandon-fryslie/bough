package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/perf"
	"github.com/nickelsec/bough/internal/synthetic"
)

// recording is a recording as a browser at 120 Hz makes one, with count
// inputs drawn from frame 35 on and, when late, one frame of them held up.
func recording(t *testing.T, count int, late bool) perf.Recording {
	t.Helper()
	frames := make([]float64, 80)
	at := 1000.0
	for i := range frames {
		at += 8.333
		if late && i == 40 {
			at += 25
		}
		frames[i] = at
	}
	inputs := make([]int, count)
	for i := range inputs {
		inputs[i] = 35 + i
	}
	raw, err := json.Marshal(map[string]any{"frames": frames, "inputs": inputs})
	if err != nil {
		t.Fatal(err)
	}
	r, err := perf.ParseRecording(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Flags that name something that is not there, or ask for something that
// cannot be done, are refused before any browser opens, with status 2 and
// nothing kept.
func TestFlagsThatCannotBeCarriedOutAreRefused(t *testing.T) {
	for _, c := range []struct {
		args []string
		says string
	}{
		{[]string{"-repeats", "0"}, "at least once"},
		{[]string{"-size", "huge"}, "small, medium, large"},
		{[]string{"-browsers", "opera"}, "chrome and safari"},
		{[]string{"-browsers", "chrome", "-scenarios", "pinch"}, "drag-pan"},
		{[]string{"-size", "small", "-graph", "g.json"}, "give one"},
		{[]string{"-browsers", "chrome", "-graph", filepath.Join(t.TempDir(), "absent.json")}, "absent.json"},
		{[]string{"-browsers", "chrome", "small"}, "unexpected"},
	} {
		out := filepath.Join(t.TempDir(), "results.json")
		var stderr bytes.Buffer
		status := run(append(c.args, "-o", out), &bytes.Buffer{}, &stderr)
		if status != badFlags || !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%q: status %d, said %q; want %d, saying %q", c.args, status, stderr.String(), badFlags, c.says)
		}
		if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%q: kept results for an invocation that measured nothing", c.args)
		}
	}
}

// With no browser named and no browser's driver installed, there is nothing to
// play in, which is said, with how to set one up, rather than passing quietly.
func TestNoInstalledBrowserIsRefused(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var stderr bytes.Buffer
	status := run([]string{"-o", filepath.Join(t.TempDir(), "r.json")}, &bytes.Buffer{}, &stderr)
	if status != badFlags || !strings.Contains(stderr.String(), "chromedriver") {
		t.Errorf("status %d, said %q; want %d, saying how to install chromedriver", status, stderr.String(), badFlags)
	}
}

// A graph bough wrote with --json is measured as it was written, and named by
// its file.
func TestAGraphFileIsWhatIsMeasured(t *testing.T) {
	shape, err := synthetic.Sized("small")
	if err != nil {
		t.Fatal(err)
	}
	written := synthetic.Alongside(shape)
	file := filepath.Join(t.TempDir(), "graph.json")
	body, err := json.Marshal(written)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, body, 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := parse([]string{"-graph", file, "-browsers", "chrome"}, time.Now(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if p.graphName != file || len(p.graph.Goals) != len(written.Goals) || p.graph.Project.Agents[1] != "codex" {
		t.Errorf("measuring %q with %d goals by %v, want %s's %d goals by two agents", p.graphName, len(p.graph.Goals), p.graph.Project.Agents, file, len(written.Goals))
	}
}

// The kept file holds every run as it went, a recording or why there is none,
// and each scenario's summary of the runs that recorded, so a later
// comparison can judge the runs again or read the summary as it was printed.
func TestTheResultsKeepEveryRun(t *testing.T) {
	r := results{Repeats: 3, Browsers: []browser{
		played{Browser: "chrome", Version: "152", Scenarios: []measured{
			{Scenario: "drag-pan", Runs: []take{
				{recording: recording(t, 10, false)},
				{err: errors.New("drag-pan did not take")},
				{recording: recording(t, 10, true)},
			}},
			{Scenario: "hover-sweep", Runs: []take{{err: errors.New("no note")}}},
		}},
		skipped{Browser: "safari", Skipped: "safaridriver drops most of the input it is sent"},
	}}
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	var kept struct {
		Browsers []struct {
			Browser   string `json:"browser"`
			Version   string `json:"version"`
			Skipped   string `json:"skipped"`
			Scenarios []struct {
				Scenario string `json:"scenario"`
				Runs     []struct {
					Recording *perf.Recording `json:"recording"`
					Failed    string          `json:"failed"`
				} `json:"runs"`
				Summary *perf.Summary `json:"summary"`
			} `json:"scenarios"`
		} `json:"browsers"`
	}
	if err := json.Unmarshal(body, &kept); err != nil {
		t.Fatalf("the kept results did not read back: %v\n%s", err, body)
	}

	chrome, safari := kept.Browsers[0], kept.Browsers[1]
	if chrome.Version != "152" || safari.Skipped == "" {
		t.Errorf("kept chrome as version %q and safari as skipped for %q", chrome.Version, safari.Skipped)
	}
	drag := chrome.Scenarios[0]
	if len(drag.Runs) != 3 || drag.Runs[0].Recording == nil || drag.Runs[1].Failed != "drag-pan did not take" || drag.Runs[2].Recording == nil {
		t.Errorf("kept drag-pan's runs as %s", body)
	}
	want := perf.Summarize(recording(t, 10, false), recording(t, 10, true))
	if drag.Summary == nil || *drag.Summary != want {
		t.Errorf("kept drag-pan's summary as %+v, want the two recorded runs judged together, %+v", drag.Summary, want)
	}
	if hover := chrome.Scenarios[1]; hover.Summary != nil {
		t.Errorf("kept a summary, %+v, for a scenario no run of which recorded", hover.Summary)
	}
}

// The table says, a scenario a row, how many runs recorded and how they drew,
// and says why for every browser and scenario that did not.
func TestTheTableSaysWhatDidNotRun(t *testing.T) {
	var out bytes.Buffer
	for _, b := range []browser{
		played{Browser: "chrome", Version: "152", Scenarios: []measured{
			{Scenario: "drag-pan", Runs: []take{{recording: recording(t, 10, false)}, {err: errors.New("did not take")}}},
			{Scenario: "hover-sweep", Runs: []take{{err: errors.New("no note")}}},
		}, CloseFailed: "the window hung"},
		skipped{Browser: "safari", Skipped: "its input is dropped"},
		unopened{Browser: "chrome", Failed: "no chromedriver"},
	} {
		b.report(&out)
	}
	rows := map[string][]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			rows[fields[0]] = fields
		}
	}
	for first, want := range map[string]string{
		"chrome":      "chrome 152",
		"drag-pan":    "drag-pan 1/2 10 91.7ms 8.3ms 8.3ms 8.3ms 8.3ms 0",
		"hover-sweep": "hover-sweep 0/1 - - - - - - -",
	} {
		if got := strings.Join(rows[first], " "); got != want {
			t.Errorf("row %q, want %q:\n%s", got, want, out.String())
		}
	}
	for _, want := range []string{
		"chrome: failed to close, the window hung",
		"safari: skipped, its input is dropped",
		"chrome: failed to open, no chromedriver",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the table does not say %q:\n%s", want, out.String())
		}
	}
}

// A browser counts as failed when anything asked of it did not happen, and a
// skipped one does not, since why it was skipped is known and said.
func TestWhatCountsAsFailed(t *testing.T) {
	ok := take{recording: recording(t, 10, false)}
	for _, c := range []struct {
		name   string
		b      browser
		failed bool
	}{
		{"every run recorded", played{Scenarios: []measured{{Runs: []take{ok, ok}}}}, false},
		{"one run failed", played{Scenarios: []measured{{Runs: []take{ok}}, {Runs: []take{ok, {err: errors.New("x")}}}}}, true},
		{"the window would not close", played{Scenarios: []measured{{Runs: []take{ok}}}, CloseFailed: "hung"}, true},
		{"skipped", skipped{}, false},
		{"unopened", unopened{}, true},
	} {
		if got := c.b.failed(); got != c.failed {
			t.Errorf("%s: failed is %v", c.name, got)
		}
	}
}

// An invocation that plays in a browser whose input does not reach the page
// serves the page, keeps what happened, and fails, since nothing was measured.
func TestAnInvocationThatMeasuresNothingFails(t *testing.T) {
	out := filepath.Join(t.TempDir(), "perf", "results.json")
	var stdout, stderr bytes.Buffer
	status := run([]string{"-size", "small", "-browsers", "safari", "-repeats", "2", "-o", out}, &stdout, &stderr)

	if status != someFailed || !strings.Contains(stderr.String(), "nothing was measured") {
		t.Errorf("status %d, said %q; want %d, saying nothing was measured", status, stderr.String(), someFailed)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("kept nothing: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["graph"] != "synthetic small" || raw["revision"] == "" || raw["repeats"] != 2.0 {
		t.Errorf("kept %s", body)
	}
	if !strings.Contains(string(body), `"skipped"`) || !strings.Contains(stdout.String(), "safari: skipped") {
		t.Errorf("kept %s and printed %q, want safari skipped in both", body, stdout.String())
	}
}

// The revision measured is marked dirty when the working tree differs from its
// commit in any way go run would build, a file git does not track included.
func TestTheRevisionSaysWhenTheTreeIsDirty(t *testing.T) {
	t.Chdir(t.TempDir())
	git := func(args ...string) {
		t.Helper()
		all := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)
		if out, err := exec.Command("git", all...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile("main.go", []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "main.go")
	git("commit", "-q", "-m", "a commit")

	clean, err := revision()
	if err != nil || strings.HasSuffix(clean, "-dirty") {
		t.Errorf("a clean tree is at %q, %v", clean, err)
	}
	if err := os.WriteFile("new.go", []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dirty, err := revision(); err != nil || dirty != clean+"-dirty" {
		t.Errorf("a tree with an untracked file is at %q, %v; want %q", dirty, err, clean+"-dirty")
	}
}
