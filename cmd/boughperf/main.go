// Command boughperf measures how smoothly bough's page draws while someone
// uses it, in real browsers driven with real input.
//
// It plays every scenario worth measuring, repeatedly, on the page of a
// synthetic history or of a graph bough wrote, prints how each drew, and keeps
// every recording in a file so two runs can be compared later.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nickelsec/bough/internal/agent/registry"
	"github.com/nickelsec/bough/internal/graph"
	"github.com/nickelsec/bough/internal/scenario"
	"github.com/nickelsec/bough/internal/server"
	"github.com/nickelsec/bough/internal/synthetic"
	"github.com/nickelsec/bough/internal/webdriver"
)

const usage = `usage: boughperf [flags]

Plays every scenario in every browser, the given number of times each, on the
page of a synthetic history or of a graph bough wrote with --json. Opens a
window in each browser. Prints how each scenario drew and keeps every
recording, with its summary, in a file. A browser whose driven input does not
reach the page is skipped, with why.

Exit status is 0 when every run recorded and at least one did, 1 when any run
or browser failed or nothing was measured, and 2 when the flags are wrong.

boughperf compare before.json after.json says what changed between two kept
runs, beyond the spread of their repeats.

`

// The exit statuses usage promises.
const (
	measuredAll = 0
	someFailed  = 1
	badFlags    = 2
)

// Deadlines, so a browser that hangs fails its run rather than the whole
// invocation.
const (
	opening = time.Minute
	playing = 2 * time.Minute
	closing = 10 * time.Second
)

func main() {
	os.Exit(command(os.Args[1:], os.Stdout, os.Stderr))
}

// command measures, or compares two kept runs when its first argument is
// compare.
func command(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "compare" {
		return compareRuns(args[1:], stdout, stderr)
	}
	return run(args, stdout, stderr)
}

func run(args []string, stdout, stderr io.Writer) int {
	started := time.Now().UTC()
	p, err := parse(args, started, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return measuredAll
	}
	if err != nil {
		fmt.Fprintln(stderr, "boughperf:", err)
		return badFlags
	}

	// [LAW:effects-at-boundaries] the flags said what to measure; what they
	// name is only looked for now, so a missing file or driver fails as the
	// run failing, not as the flags being wrong.
	g, err := p.history.read()
	if err != nil {
		fmt.Fprintln(stderr, "boughperf:", err)
		return someFailed
	}
	browsers, err := p.browsers(stderr)
	if err != nil {
		fmt.Fprintln(stderr, "boughperf:", err)
		return someFailed
	}
	revision, err := revision()
	if err != nil {
		fmt.Fprintln(stderr, "boughperf:", err)
		return someFailed
	}
	measuring, err := fingerprint(p.history.name, g)
	if err != nil {
		fmt.Fprintln(stderr, "boughperf:", err)
		return someFailed
	}

	// No interrupt is caught: the terminal interrupts the drivers too, so the
	// windows could not be closed in order anyway, and a run cut short is not
	// one to compare against.
	ctx := context.Background()
	r := results{Started: started, Revision: revision, Graph: measuring, Repeats: p.repeats}
	err = serve(ctx, g, func(url string) {
		for _, b := range browsers {
			r.Browsers = append(r.Browsers, measure(ctx, b, url, p, stderr))
		}
	})
	failed := err != nil
	if err != nil {
		fmt.Fprintln(stderr, "boughperf: serving the page:", err)
	}

	fmt.Fprintf(stdout, "%s at %s, %d runs each\n", p.history.name, revision, p.repeats)
	recorded := 0
	for _, b := range r.Browsers {
		b.report(stdout)
		failed = failed || b.failed()
		recorded += b.recorded()
	}
	if recorded == 0 {
		fmt.Fprintln(stderr, "boughperf: nothing was measured")
		failed = true
	}
	if err := keep(p.out, r); err != nil {
		fmt.Fprintln(stderr, "boughperf:", err)
		return someFailed
	}
	fmt.Fprintf(stdout, "kept in %s\n", p.out)

	if failed {
		return someFailed
	}
	return measuredAll
}

// plan is what an invocation was asked to measure. The graph and the browsers
// are described rather than found, since finding them reads the machine.
type plan struct {
	history   history
	browsers  func(stderr io.Writer) ([]webdriver.Browser, error)
	scenarios []scenario.Scenario
	repeats   int
	out       string
}

// history is the graph to measure: its name, as the results give it, and how
// to read it.
type history struct {
	name string
	read func() (graph.Graph, error)
}

// parse reads the flags into a plan, refusing any that name nothing or ask
// for what cannot be done. It reads nothing but the flags.
func parse(args []string, started time.Time, stderr io.Writer) (plan, error) {
	fs := flag.NewFlagSet("boughperf", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	var (
		size      = fs.String("size", "medium", "the synthetic history to measure: "+strings.Join(synthetic.SizeNames(), ", "))
		file      = fs.String("graph", "", "measure the graph bough wrote to this file with --json instead of a synthetic history")
		browsers  = fs.String("browsers", "", "browsers to play in, comma separated: chrome, safari (default every browser whose driver is installed)")
		scenarios = fs.String("scenarios", "", "scenarios to play, comma separated (default every one)")
		repeats   = fs.Int("repeats", 5, "how many times to play each scenario in each browser")
		out       = fs.String("o", filepath.Join("perf-results", started.Format("20060102T150405Z")+".json"), "the file to keep the results in")
	)
	if err := fs.Parse(args); err != nil {
		return plan{}, err
	}
	if fs.NArg() > 0 {
		return plan{}, fmt.Errorf("unexpected arguments %q", fs.Args())
	}
	if *repeats < 1 {
		return plan{}, fmt.Errorf("-repeats is %d, and a scenario is played at least once", *repeats)
	}
	sizeSet := false
	fs.Visit(func(f *flag.Flag) { sizeSet = sizeSet || f.Name == "size" })

	h, err := pickHistory(*size, sizeSet, *file)
	if err != nil {
		return plan{}, err
	}
	bs, err := pickBrowsers(*browsers)
	if err != nil {
		return plan{}, err
	}
	scs, err := pickScenarios(*scenarios)
	if err != nil {
		return plan{}, err
	}
	return plan{history: h, browsers: bs, scenarios: scs, repeats: *repeats, out: *out}, nil
}

// pickHistory is the graph file -graph names, or the synthetic size.
func pickHistory(size string, sizeSet bool, file string) (history, error) {
	if file == "" {
		shape, err := synthetic.Sized(size)
		return history{
			name: "synthetic " + size,
			read: func() (graph.Graph, error) { return synthetic.History(shape), nil },
		}, err
	}
	if sizeSet {
		return history{}, errors.New("-size and -graph each name the graph to measure; give one")
	}
	return history{name: file, read: func() (graph.Graph, error) {
		f, err := os.Open(file) //#nosec G304 -- the graph the person running this named to measure
		if err != nil {
			return graph.Graph{}, err
		}
		defer f.Close()
		g, err := graph.Read(f)
		if err != nil {
			return graph.Graph{}, fmt.Errorf("%s: %w", file, err)
		}
		return g, nil
	}}, nil
}

// pickBrowsers is the browsers named, or, when none are, every browser whose
// driver is installed once the machine is asked. A browser left out is said to
// be, with why, so the table's silence about it is never mistaken for a pass.
func pickBrowsers(names string) (func(io.Writer) ([]webdriver.Browser, error), error) {
	if names == "" {
		return installed, nil
	}
	listed, err := split(names)
	if err != nil {
		return nil, err
	}
	var out []webdriver.Browser
	for _, name := range listed {
		b, err := webdriver.Named(name)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return func(io.Writer) ([]webdriver.Browser, error) { return out, nil }, nil
}

// installed is every browser whose driver is installed, and an error when
// there are none.
func installed(stderr io.Writer) ([]webdriver.Browser, error) {
	var out []webdriver.Browser
	var missing []error
	for _, b := range webdriver.Browsers() {
		if err := b.Installed(); err != nil {
			fmt.Fprintf(stderr, "boughperf: not playing in %s: %v\n", b.Name(), err)
			missing = append(missing, err)
			continue
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no browser's driver is installed: %w", errors.Join(missing...))
	}
	return out, nil
}

// pickScenarios is the scenarios named, in the order they run, or every one
// when none are.
func pickScenarios(names string) ([]scenario.Scenario, error) {
	if names == "" {
		return scenario.All, nil
	}
	listed, err := split(names)
	if err != nil {
		return nil, err
	}
	var out []scenario.Scenario
	for _, name := range listed {
		sc, err := scenario.Named(name)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

// split is the names in a comma separated list, each named once. A list that
// names nothing is refused rather than read as asking for nothing.
func split(list string) ([]string, error) {
	names := strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' })
	if len(names) == 0 {
		return nil, fmt.Errorf("%q names nothing", list)
	}
	return names, once(names)
}

// once refuses a name given twice. A browser or scenario played twice under
// one name is kept twice, and a comparison could only ever find the first.
//
// [LAW:single-enforcer] flags and kept files are both held to it here.
func once(names []string) error {
	for i, name := range names {
		if slices.Contains(names[:i], name) {
			return fmt.Errorf("%s is named twice", name)
		}
	}
	return nil
}

// revision is the commit the working tree is at, and dirty when anything in
// it differs from that commit, a new untracked file included, since go run
// builds the page from whatever is there.
func revision() (string, error) {
	commit, err := exec.Command("git", "describe", "--always", "--abbrev=12").Output()
	if err != nil {
		return "", fmt.Errorf("naming the commit being measured with git describe: %w", err)
	}
	changes, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return "", fmt.Errorf("finding what differs from the commit being measured with git status: %w", err)
	}
	measured := strings.TrimSpace(string(commit))
	if len(changes) > 0 {
		measured += "-dirty"
	}
	return measured, nil
}

// serve puts g's page on a loopback port, hands its address to use, and stops
// serving once use returns. It returns only once use has, so nothing use
// writes is still being written when its caller reads it.
func serve(ctx context.Context, g graph.Graph, use func(url string)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var using sync.WaitGroup
	err := server.Serve(ctx, "measured", g, nil, registry.Display, func(url string) {
		// Serve holds the page up until ctx ends, so the page is used beside
		// it and ends it when done.
		using.Go(func() {
			defer cancel()
			use(url)
		})
	})
	cancel()
	using.Wait()
	return err
}

// measure plays every scenario of p in b, reporting each run on stderr as it
// ends.
func measure(ctx context.Context, b webdriver.Browser, url string, p plan, stderr io.Writer) browser {
	if err := b.Input(); err != nil {
		return skipped{Browser: b.Name(), Skipped: err.Error()}
	}
	opened, cancel := context.WithTimeout(ctx, opening)
	w, err := webdriver.Open(opened, b)
	cancel()
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", b.Name(), err)
		return unopened{Browser: b.Name(), Failed: err.Error()}
	}

	result := played{Browser: b.Name(), Version: w.Version()}
	for _, sc := range p.scenarios {
		m := measured{Scenario: sc.Name}
		for i := range p.repeats {
			playable, cancel := context.WithTimeout(ctx, playing)
			r, err := scenario.Run(playable, w.Session, url, sc)
			cancel()
			m.Runs = append(m.Runs, take{recording: r, err: err})
			fmt.Fprintf(stderr, "%s %s %d/%d: %s\n", b.Name(), sc.Name, i+1, p.repeats, outcome(err))
		}
		result.Scenarios = append(result.Scenarios, m)
	}

	closable, cancel := context.WithTimeout(context.WithoutCancel(ctx), closing)
	defer cancel()
	if err := w.Close(closable); err != nil {
		// The runs stand; the window it may have left open is a failure too.
		result.CloseFailed = err.Error()
	}
	return result
}

func outcome(err error) string {
	if err != nil {
		return "failed, " + err.Error()
	}
	return "recorded"
}

// keep writes r to path, making its directory if need be.
func keep(path string, r results) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("writing the results: %w", err)
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}
