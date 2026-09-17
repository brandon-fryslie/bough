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
	"slices"
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
// made by ParseRecording, so every Recording has frames in order of their
// start times, and inputs, each the index of the frame that drew it, in the
// order they arrived: the first drawn after at least Quiet intervals, and the
// last drawn early enough that the next frame's start shows what drawing it
// cost.
type Recording struct {
	frames []time.Duration
	inputs []int
}

// wire is a recording as the probe hands it over: frame start times in
// milliseconds, and the index of the frame that drew each input. Pointers,
// because JSON has no NaN: the page writes a broken time as null, which would
// otherwise arrive as a plausible zero.
type wire struct {
	Frames []*float64 `json:"frames"`
	Inputs []*int     `json:"inputs"`
}

// ParseRecording checks what the probe handed back and keeps it as a
// Recording. Anything a working probe could not have produced is an error.
func ParseRecording(raw []byte) (Recording, error) {
	var wire wire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Recording{}, fmt.Errorf("reading the probe's recording: %w", err)
	}

	ms, err := present(wire.Frames)
	if err != nil {
		return Recording{}, fmt.Errorf("frame times: %w", err)
	}
	inputs, err := present(wire.Inputs)
	if err != nil {
		return Recording{}, fmt.Errorf("inputs: %w", err)
	}

	frames := make([]time.Duration, len(ms))
	for i, m := range ms {
		frames[i] = time.Duration(math.Round(m * float64(time.Millisecond)))
	}
	for i := 1; i < len(frames); i++ {
		if frames[i] <= frames[i-1] {
			return Recording{}, fmt.Errorf("frame %d at %v does not follow frame %d at %v", i, frames[i], i-1, frames[i-1])
		}
	}
	// [LAW:parse-dont-validate] the probe notes each input as the count of
	// frames started so far, which only grows.
	if !slices.IsSorted(inputs) {
		return Recording{}, errors.New("inputs are drawn by frames out of the order they arrived in, which the probe cannot note")
	}

	if len(inputs) == 0 {
		return Recording{}, errors.New("no input reached the page, so there is nothing to judge")
	}
	if quiet := inputs[0] - 1; quiet < Quiet {
		return Recording{}, fmt.Errorf("%d quiet frame intervals before the first input, want at least %d", max(quiet, 0), Quiet)
	}
	// [LAW:one-source-of-truth] the probe's finish stops on this same count;
	// its contract test holds the two together.
	if last := inputs[len(inputs)-1]; last+2 > len(frames) {
		return Recording{}, fmt.Errorf("the last input is drawn by frame %d of %d, so no frame after it shows what drawing it cost", last, len(frames))
	}

	return Recording{frames: frames, inputs: inputs}, nil
}

// MarshalJSON writes r in the shape the probe handed it over, so a recording
// kept in a file reads back through ParseRecording as the same recording.
func (r Recording) MarshalJSON() ([]byte, error) {
	w := wire{Frames: make([]*float64, len(r.frames)), Inputs: make([]*int, len(r.inputs))}
	for i, f := range r.frames {
		ms := float64(f) / float64(time.Millisecond)
		w.Frames[i] = &ms
	}
	for i := range r.inputs {
		w.Inputs[i] = &r.inputs[i]
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads a kept recording back, checked as ParseRecording checks
// the probe's.
//
// [LAW:single-enforcer] ParseRecording is the one way a Recording is made.
func (r *Recording) UnmarshalJSON(raw []byte) error {
	parsed, err := ParseRecording(raw)
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}

// present is the values the page wrote, refusing any it left out and any
// below zero, which neither a clock reading nor a count can be.
func present[T int | float64](values []*T) ([]T, error) {
	out := make([]T, len(values))
	for i, v := range values {
		if v == nil {
			return nil, fmt.Errorf("value %d is missing", i)
		}
		if *v < 0 {
			return nil, fmt.Errorf("value %d, %v, is below zero", i, *v)
		}
		out[i] = *v
	}
	return out, nil
}
