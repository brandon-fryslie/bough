package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nickelsec/bough/internal/perf"
)

const compareUsage = `usage: boughperf compare before.json after.json

Compares two kept runs of the same graph, scenario by scenario in each
browser, taken on displays that refresh at the same rate. Each metric is
shown as the middle of its runs with the least and most any run reached. A
change counts only when every run after is past every run before, which noise
alone does to one metric less than once in a hundred times (so five runs a
side, or more on one side for fewer on the other), and when it moves the
middle by more than half a frame for a time, since frame times jitter by less
than that. A comparison judges dozens of metrics, so a stray verdict can still
turn up: claim a change by the metrics it was meant to move.

Exit status is 0 when the runs were compared, 1 when a file cannot be read or
the runs measured different graphs, and 2 when the arguments are wrong.
`

// compareRuns is the compare command.
func compareRuns(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("boughperf compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, compareUsage) }
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return measuredAll
	} else if err != nil {
		return badFlags
	}
	if fs.NArg() != 2 {
		fmt.Fprintf(stderr, "boughperf compare: want the before and after files, got %q\n", fs.Args())
		return badFlags
	}

	before, err := readResults(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "boughperf compare:", err)
		return someFailed
	}
	after, err := readResults(fs.Arg(1))
	if err != nil {
		fmt.Fprintln(stderr, "boughperf compare:", err)
		return someFailed
	}
	lines, err := compare(before, after)
	if err != nil {
		fmt.Fprintln(stderr, "boughperf compare:", err)
		return someFailed
	}

	fmt.Fprintf(stdout, "%s, %s → %s\n", after.Graph.Name, before.Revision, after.Revision)
	for _, l := range lines {
		l.report(stdout)
	}
	return measuredAll
}

// readResults is the results kept at path.
func readResults(path string) (results, error) {
	body, err := os.ReadFile(path) //#nosec G304 -- a results file the person running this named to compare
	if err != nil {
		return results{}, err
	}
	var r results
	if err := json.Unmarshal(body, &r); err != nil {
		return results{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// metric is one number a run is judged by. Every one is better lower.
type metric struct {
	name string
	of   func(perf.Summary) float64
	show func(float64) string
	// jitter is how far the metric moves without anything having changed, for
	// a browser whose frames come every refresh.
	jitter func(refresh time.Duration) float64
}

// metrics is what a comparison judges, in the order it shows them.
var metrics = []metric{
	frameTime("median", func(s perf.Summary) time.Duration { return s.Median }),
	frameTime("p95", func(s perf.Summary) time.Duration { return s.P95 }),
	frameTime("worst", func(s perf.Summary) time.Duration { return s.Worst }),
	{
		name: "missed",
		of:   func(s perf.Summary) float64 { return float64(s.Missed) },
		show: func(v float64) string { return fmt.Sprintf("%.0f", v) },
		// Missed frames are already counted in whole frames.
		jitter: func(time.Duration) float64 { return 0 },
	},
}

// frameTime is a metric of frame intervals, in milliseconds. A browser starts
// frames on the display's refresh, so an interval that changed is a whole
// refresh longer or shorter, and anything under half of one is jitter.
func frameTime(name string, d func(perf.Summary) time.Duration) metric {
	return metric{
		name:   name,
		of:     func(s perf.Summary) float64 { return float64(d(s)) / float64(time.Millisecond) },
		show:   showMillis,
		jitter: func(refresh time.Duration) float64 { return float64(refresh) / float64(time.Millisecond) / 2 },
	}
}

func showMillis(v float64) string {
	return fmt.Sprintf("%.1fms", v)
}

// spread is one metric across a scenario's runs, each run judged alone: how
// many runs, and the least, middle and most of them.
type spread struct {
	runs                int
	least, middle, most float64
}

func spreadOf(m metric, recordings []perf.Recording) spread {
	values := make([]float64, len(recordings))
	for i, r := range recordings {
		values[i] = m.of(perf.Summarize(r))
	}
	slices.Sort(values)
	return spread{runs: len(values), least: values[0], middle: values[(len(values)-1)/2], most: values[len(values)-1]}
}

func (s spread) show(m metric) string {
	return fmt.Sprintf("%s (%s–%s)", m.show(s.middle), m.show(s.least), m.show(s.most))
}

// verdict is what a comparison makes of one metric's change.
type verdict string

const (
	better       verdict = "better"
	worse        verdict = "worse"
	withinSpread verdict = "within the spread"
	tooFewRuns   verdict = "too few runs to judge"
)

// significance is how rarely noise alone may pass for a change in one metric.
// It is not divided among the metrics a comparison judges: they move together
// within a scenario, and the half frame a time must also move filters most of
// what noise lines up, so a stricter bound would ask for far more runs than
// the false verdicts it saves.
const significance = 0.01

// sameDisplay is how far apart two refresh intervals may be and still be one
// display's. A refresh is a median of frame times browsers report to a tenth
// of a millisecond, so one display's can measure 2.4% apart at 240 Hz. Rates
// closer than this, like 175 Hz and 180 Hz, pass for one display, and only a
// long stall's frame time moves by more than half a frame between them.
const sameDisplay = 0.05

// judge calls a change better or worse only when every run after is past
// every run before, the separation is one noise makes less often than
// significance, and the middle moved further than the metric jitters.
func judge(before, after spread, jitter float64) verdict {
	moved := after.middle - before.middle
	switch {
	case chance(before.runs, after.runs) >= significance:
		return tooFewRuns
	case after.most < before.least && -moved > jitter:
		return better
	case after.least > before.most && moved > jitter:
		return worse
	default:
		return withinSpread
	}
}

// chance is how often n runs and m runs of the same noise come out with every
// run of one past every run of the other: two orders out of the (n+m)!/(n!m!)
// ways of ranking them together, which is 2·n!·m!/(n+m)!.
func chance(n, m int) float64 {
	p := 2.0
	for i := 1; i <= m; i++ {
		p *= float64(i) / float64(n+i)
	}
	return p
}

// line is one browser and scenario of a comparison, as it is reported.
type line interface {
	report(w io.Writer)
}

// compared is a scenario both runs recorded in a browser on displays drawing
// frames every refresh, metric by metric.
type compared struct {
	browser, scenario string
	before, after     side
	refresh           time.Duration
}

// uncompared is a scenario, or a whole browser, at least one run has nothing
// of, and why for each run.
type uncompared struct {
	browser, scenario string
	before, after     error
}

// compare pairs every scenario either run played, browser by browser in the
// order they were kept. Runs of different graphs are refused: nothing in them
// measures the same page.
func compare(before, after results) ([]line, error) {
	if before.Graph.SHA256 != after.Graph.SHA256 {
		return nil, fmt.Errorf("the runs measured different graphs, %s (%.12s) and %s (%.12s)",
			before.Graph.Name, before.Graph.SHA256, after.Graph.Name, after.Graph.SHA256)
	}
	ps := pairs(before, after)
	if len(ps) == 0 {
		return nil, errors.New("neither run measured any browser, so there is nothing to compare")
	}
	lines := make([]line, len(ps))
	for i, p := range ps {
		lines[i] = lineOf(p, before, after)
	}
	return lines, nil
}

// lineOf is p compared, or why it cannot be. Runs on displays refreshing at
// different rates are not compared: every frame time moves with the display,
// and a faster one would pass for a faster page.
func lineOf(p pair, before, after results) line {
	b, berr := sideOf(before, p.browser, p.scenario)
	a, aerr := sideOf(after, p.browser, p.scenario)
	if berr != nil || aerr != nil {
		return uncompared{browser: p.browser, scenario: p.scenario, before: berr, after: aerr}
	}
	br, ar := b.refresh(), a.refresh()
	if float64(max(br, ar)-min(br, ar)) > float64(min(br, ar))*sameDisplay {
		return uncompared{browser: p.browser, scenario: p.scenario,
			before: fmt.Errorf("drawn every %s at rest", ms(br)), after: fmt.Errorf("drawn every %s at rest", ms(ar))}
	}
	return compared{browser: p.browser, scenario: p.scenario, before: b, after: a, refresh: br}
}

type pair struct {
	browser, scenario string
}

// pairs is every scenario played in either run, by browser. A browser neither
// run played a scenario in is one pair with no scenario, so it is still said
// why.
func pairs(runs ...results) []pair {
	var browsers []string
	scenarios := map[string][]string{}
	for _, r := range runs {
		for _, b := range r.Browsers {
			name := b.name()
			if !slices.Contains(browsers, name) {
				browsers = append(browsers, name)
			}
			for _, sc := range b.scenarios() {
				if !slices.Contains(scenarios[name], sc) {
					scenarios[name] = append(scenarios[name], sc)
				}
			}
		}
	}
	// Every browser is at least one pair.
	out := make([]pair, 0, len(browsers))
	for _, name := range browsers {
		played := scenarios[name]
		if len(played) == 0 {
			played = []string{""}
		}
		for _, sc := range played {
			out = append(out, pair{browser: name, scenario: sc})
		}
	}
	return out
}

// side is one run's recordings of a scenario in a browser, at least one.
type side struct {
	version    string
	recordings []perf.Recording
}

// refresh is the browser's frame interval at rest across the side's runs.
func (s side) refresh() time.Duration {
	return perf.Summarize(s.recordings[0], s.recordings[1:]...).Refresh
}

// sideOf is what r recorded of scenario in browser, or why it has nothing.
func sideOf(r results, browser, scenario string) (side, error) {
	for _, b := range r.Browsers {
		if b.name() == browser {
			return b.runsOf(scenario)
		}
	}
	return side{}, fmt.Errorf("not run in %s", browser)
}

func (c compared) report(w io.Writer) {
	fmt.Fprintf(w, "%s %s: %d runs in %s → %d runs in %s\n", c.browser, c.scenario,
		len(c.before.recordings), c.before.version, len(c.after.recordings), c.after.version)
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, m := range metrics {
		b, a := spreadOf(m, c.before.recordings), spreadOf(m, c.after.recordings)
		fmt.Fprintf(table, "  %s\t%s\t→ %s\t%s\n", m.name, b.show(m), a.show(m), judge(b, a, m.jitter(c.refresh)))
	}
	_ = table.Flush()
}

func (u uncompared) report(w io.Writer) {
	fmt.Fprintf(w, "%s: not compared; before, %s; after, %s\n",
		strings.TrimSpace(u.browser+" "+u.scenario), recordedOr(u.before), recordedOr(u.after))
}

func recordedOr(err error) string {
	if err != nil {
		return err.Error()
	}
	return "recorded"
}
