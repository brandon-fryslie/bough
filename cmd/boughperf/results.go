package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/nickelsec/bough/internal/graph"

	"github.com/nickelsec/bough/internal/perf"
)

// results is everything one invocation measured, as it is kept in its file.
type results struct {
	Started time.Time `json:"started"`
	// Revision is the commit the page was built from, marked dirty when the
	// working tree had changes, since a number means nothing without the code
	// it measured.
	Revision string        `json:"revision"`
	Graph    measuredGraph `json:"graph"`
	Repeats  int           `json:"repeats"`
	Browsers []browser     `json:"browsers"`
}

// measuredGraph names the graph a run measured and fingerprints it, since two
// graphs can share a name: a file written again, or a synthetic size built by
// a changed generator.
type measuredGraph struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// fingerprint is g, named, with the hash of the JSON it is served as.
func fingerprint(name string, g graph.Graph) (measuredGraph, error) {
	body, err := json.Marshal(g)
	if err != nil {
		return measuredGraph{}, fmt.Errorf("fingerprinting the graph: %w", err)
	}
	sum := sha256.Sum256(body)
	return measuredGraph{Name: name, SHA256: hex.EncodeToString(sum[:])}, nil
}

// UnmarshalJSON reads results back from their file, each browser as the one
// kind its keys say it is.
func (r *results) UnmarshalJSON(raw []byte) error {
	var kept struct {
		Started  time.Time         `json:"started"`
		Revision string            `json:"revision"`
		Graph    measuredGraph     `json:"graph"`
		Repeats  int               `json:"repeats"`
		Browsers []json.RawMessage `json:"browsers"`
	}
	if err := json.Unmarshal(raw, &kept); err != nil {
		return err
	}
	if kept.Graph.SHA256 == "" {
		return errors.New("the results do not fingerprint the graph they measured, so nothing can be compared with them")
	}
	browsers := make([]browser, len(kept.Browsers))
	for i, b := range kept.Browsers {
		read, err := readBrowser(b)
		if err != nil {
			return fmt.Errorf("browser %d: %w", i, err)
		}
		browsers[i] = read
	}
	*r = results{Started: kept.Started, Revision: kept.Revision, Graph: kept.Graph, Repeats: kept.Repeats, Browsers: browsers}
	return nil
}

// readBrowser is a kept browser as skipped, unopened or played, whichever one
// of skipped, failed or scenarios it holds.
//
// [LAW:parse-dont-validate] a browser holding none, or more than one, is not
// one the file could have been written with.
func readBrowser(raw json.RawMessage) (browser, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, err
	}
	var read []func(json.RawMessage) (browser, error)
	for key, as := range map[string]func(json.RawMessage) (browser, error){
		"skipped":   readAs[skipped],
		"failed":    readAs[unopened],
		"scenarios": readAs[played],
	} {
		if _, ok := keys[key]; ok {
			read = append(read, as)
		}
	}
	if len(read) != 1 {
		return nil, fmt.Errorf("is not one of skipped, failed or played: %s", raw)
	}
	return read[0](raw)
}

// readAs reads raw as the kind of browser B.
func readAs[B browser](raw json.RawMessage) (browser, error) {
	var b B
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, err
	}
	return b, nil
}

// browser is how one browser went: skipped, unopened, or played.
//
// [LAW:types-are-the-program] each is its own type, so a browser that played
// cannot also carry why it did not.
type browser interface {
	// failed is whether anything asked of this browser did not happen.
	failed() bool
	// recorded is how many runs in this browser recorded something.
	recorded() int
	// name is the browser's.
	name() string
	// scenarios is every scenario played in the browser, in the order played.
	scenarios() []string
	// runsOf is what the browser recorded of scenario, or why it has nothing.
	runsOf(scenario string) (side, error)
	// report writes the browser's part of the table.
	report(w io.Writer)
}

// skipped is a browser whose driven input does not reach the page, so playing
// a scenario in it would measure a page at rest.
type skipped struct {
	Browser string `json:"browser"`
	Skipped string `json:"skipped"`
}

// unopened is a browser whose window would not open.
type unopened struct {
	Browser string `json:"browser"`
	Failed  string `json:"failed"`
}

// played is a browser every scenario was played in. CloseFailed is why its
// window would not close afterwards, which leaves the runs standing.
type played struct {
	Browser     string     `json:"browser"`
	Version     string     `json:"version"`
	Scenarios   []measured `json:"scenarios"`
	CloseFailed string     `json:"close_failed,omitempty"`
}

// measured is one scenario played repeatedly in one browser. It reads back
// from its runs alone: its kept summary is derived from them, and deriving it
// again cannot disagree with them.
type measured struct {
	Scenario string `json:"scenario"`
	Runs     []take `json:"runs"`
}

// take is one play of a scenario: what it recorded, or why it recorded
// nothing.
type take struct {
	recording perf.Recording
	err       error
}

func (s skipped) failed() bool  { return false }
func (u unopened) failed() bool { return true }

func (s skipped) recorded() int  { return 0 }
func (u unopened) recorded() int { return 0 }

func (s skipped) name() string  { return s.Browser }
func (u unopened) name() string { return u.Browser }
func (p played) name() string   { return p.Browser }

func (s skipped) scenarios() []string  { return nil }
func (u unopened) scenarios() []string { return nil }

func (p played) scenarios() []string {
	names := make([]string, len(p.Scenarios))
	for i, m := range p.Scenarios {
		names[i] = m.Scenario
	}
	return names
}

func (s skipped) runsOf(string) (side, error) {
	return side{}, fmt.Errorf("skipped, %s", s.Skipped)
}

func (u unopened) runsOf(string) (side, error) {
	return side{}, fmt.Errorf("failed to open, %s", u.Failed)
}

func (p played) runsOf(scenario string) (side, error) {
	for _, m := range p.Scenarios {
		if m.Scenario == scenario {
			if recorded := m.recorded(); len(recorded) > 0 {
				return side{version: p.Version, recordings: recorded}, nil
			}
			return side{}, fmt.Errorf("no run of %s recorded", scenario)
		}
	}
	return side{}, fmt.Errorf("%s not played", scenario)
}

func (p played) recorded() int {
	n := 0
	for _, m := range p.Scenarios {
		n += len(m.recorded())
	}
	return n
}

func (p played) failed() bool {
	if p.CloseFailed != "" {
		return true
	}
	for _, m := range p.Scenarios {
		for _, t := range m.Runs {
			if t.err != nil {
				return true
			}
		}
	}
	return false
}

// recorded is every recording the scenario's runs made, in the order they ran.
func (m measured) recorded() []perf.Recording {
	var out []perf.Recording
	for _, t := range m.Runs {
		if t.err == nil {
			out = append(out, t.recording)
		}
	}
	return out
}

// summary is the recorded runs judged together, and false when no run
// recorded anything to judge.
func (m measured) summary() (perf.Summary, bool) {
	recorded := m.recorded()
	if len(recorded) == 0 {
		return perf.Summary{}, false
	}
	return perf.Summarize(recorded[0], recorded[1:]...), true
}

// MarshalJSON keeps every run and, derived from them as it is printed, the
// summary, which is left out when no run recorded.
func (m measured) MarshalJSON() ([]byte, error) {
	kept := struct {
		Scenario string        `json:"scenario"`
		Runs     []take        `json:"runs"`
		Summary  *perf.Summary `json:"summary,omitempty"`
	}{Scenario: m.Scenario, Runs: m.Runs}
	if s, ok := m.summary(); ok {
		kept.Summary = &s
	}
	return json.Marshal(kept)
}

// UnmarshalJSON reads a run back as the recording or the failure it holds,
// refusing one that holds both or neither.
func (t *take) UnmarshalJSON(raw []byte) error {
	var kept struct {
		Recording *perf.Recording `json:"recording"`
		Failed    *string         `json:"failed"`
	}
	if err := json.Unmarshal(raw, &kept); err != nil {
		return err
	}
	switch {
	case kept.Recording != nil && kept.Failed == nil:
		*t = take{recording: *kept.Recording}
	case kept.Failed != nil && kept.Recording == nil:
		*t = take{err: errors.New(*kept.Failed)}
	default:
		return fmt.Errorf("a run is a recording or a failure, and this is not one: %s", raw)
	}
	return nil
}

// MarshalJSON keeps a run as {"recording": ...} or {"failed": "why"}.
func (t take) MarshalJSON() ([]byte, error) {
	if t.err != nil {
		return json.Marshal(map[string]string{"failed": t.err.Error()})
	}
	return json.Marshal(map[string]perf.Recording{"recording": t.recording})
}

func (s skipped) report(w io.Writer) {
	fmt.Fprintf(w, "%s: skipped, %s\n", s.Browser, s.Skipped)
}

func (u unopened) report(w io.Writer) {
	fmt.Fprintf(w, "%s: failed to open, %s\n", u.Browser, u.Failed)
}

// report writes a row a scenario: how many runs recorded, the input as it
// arrived in an average run, and the frames drawing it, judged together.
func (p played) report(w io.Writer) {
	fmt.Fprintf(w, "%s %s\n", p.Browser, p.Version)
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(table, "scenario\truns\tinputs/run\tspan/run\trefresh\tmedian\tp95\tworst\tmissed\t")
	for _, m := range p.Scenarios {
		recorded := len(m.recorded())
		s, ok := m.summary()
		if !ok {
			fmt.Fprintf(table, "%s\t%d/%d\t-\t-\t-\t-\t-\t-\t-\t\n", m.Scenario, recorded, len(m.Runs))
			continue
		}
		fmt.Fprintf(table, "%s\t%d/%d\t%d\t%s\t%s\t%s\t%s\t%s\t%d\t\n",
			m.Scenario, recorded, len(m.Runs),
			s.Inputs/recorded, ms(s.Span/time.Duration(recorded)),
			ms(s.Refresh), ms(s.Median), ms(s.P95), ms(s.Worst), s.Missed)
	}
	_ = table.Flush()
	if p.CloseFailed != "" {
		fmt.Fprintf(w, "%s: failed to close, %s\n", p.Browser, p.CloseFailed)
	}
}

// ms is d in milliseconds to a tenth, which is finer than any display draws.
func ms(d time.Duration) string {
	return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
}
