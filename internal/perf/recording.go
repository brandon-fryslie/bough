// Package perf measures how smoothly bough's page draws while someone uses it.
//
// A probe injected into the page records frame and input times; this package
// turns what it recorded into numbers that compare across runs and browsers.
// Driving a browser is someone else's job. Everything here is arithmetic.
package perf

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// Probe is the script that records a page. See probe.js for its two calls.
//
//go:embed probe.js
var Probe string

// Quiet is how many frame intervals the probe records before input begins.
// The browser's refresh interval is learned from them, so a recording with
// fewer is refused rather than judged against a guess.
const Quiet = 30

// Recording is what the probe saw during one scenario, checked. It is only
// made by ParseRecording, so every Recording has frames in order, at least
// Quiet intervals before the first input, and a frame after the last.
type Recording struct {
	frames []time.Duration
	inputs []time.Duration
}

// ParseRecording checks what the probe handed back and keeps it as a
// Recording. Anything a working probe could not have produced is an error.
func ParseRecording(raw []byte) (Recording, error) {
	// Pointers, because JSON has no NaN: the page writes a broken time as
	// null, which would otherwise arrive here as a plausible zero.
	var wire struct {
		Frames []*float64 `json:"frames"`
		Inputs []*float64 `json:"inputs"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Recording{}, fmt.Errorf("reading the probe's recording: %w", err)
	}

	frames, err := times(wire.Frames)
	if err != nil {
		return Recording{}, fmt.Errorf("frame times: %w", err)
	}
	inputs, err := times(wire.Inputs)
	if err != nil {
		return Recording{}, fmt.Errorf("input times: %w", err)
	}

	for i := 1; i < len(frames); i++ {
		if frames[i] <= frames[i-1] {
			return Recording{}, fmt.Errorf("frame %d at %v does not follow frame %d at %v", i, frames[i], i-1, frames[i-1])
		}
	}
	for i := 1; i < len(inputs); i++ {
		if inputs[i] < inputs[i-1] {
			return Recording{}, fmt.Errorf("input %d at %v arrived before input %d at %v", i, inputs[i], i-1, inputs[i-1])
		}
	}

	if len(inputs) == 0 {
		return Recording{}, errors.New("no input reached the page, so there is nothing to judge")
	}
	if quiet := countBefore(frames, inputs[0]) - 1; quiet < Quiet {
		return Recording{}, fmt.Errorf("%d quiet frame intervals before the first input, want at least %d", max(quiet, 0), Quiet)
	}
	if last := inputs[len(inputs)-1]; frames[len(frames)-1] <= last {
		return Recording{}, fmt.Errorf("no frame after the last input at %v, so its work was never seen drawn", last)
	}

	return Recording{frames: frames, inputs: inputs}, nil
}

// times turns the page's milliseconds into durations.
func times(ms []*float64) ([]time.Duration, error) {
	out := make([]time.Duration, len(ms))
	for i, m := range ms {
		if m == nil {
			return nil, fmt.Errorf("time %d is missing", i)
		}
		if *m < 0 {
			return nil, fmt.Errorf("time %d, %v, is not on the page's clock", i, *m)
		}
		out[i] = time.Duration(math.Round(*m * float64(time.Millisecond)))
	}
	return out, nil
}

// countBefore is how many frames started before t.
func countBefore(frames []time.Duration, t time.Duration) int {
	n := 0
	for n < len(frames) && frames[n] < t {
		n++
	}
	return n
}
