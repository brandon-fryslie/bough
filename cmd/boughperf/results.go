package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/nickelsec/bough/internal/perf"
)

// results is everything one invocation measured, as it is kept in its file.
type results struct {
	Started time.Time `json:"started"`
	// Revision is the commit the page was built from, marked dirty when the
	// working tree had changes, since a number means nothing without the code
	// it measured.
	Revision string    `json:"revision"`
	Graph    string    `json:"graph"`
	Repeats  int       `json:"repeats"`
	Browsers []browser `json:"browsers"`
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

// measured is one scenario played repeatedly in one browser.
type measured struct {
	Scenario string
	Runs     []take
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
