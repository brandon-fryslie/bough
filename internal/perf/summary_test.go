package perf

import (
	"encoding/json"
	"testing"
	"time"
)

// page builds a recording the way a browser would produce one: frames at a
// steady interval, some of them late, and input drawn partway through.
type page struct {
	interval float64         // milliseconds between frames at rest
	frames   int             // how many frames were started
	late     map[int]float64 // frame index to extra milliseconds before it
	inputs   []int           // the frame that drew each
}

// at is when frame i started, counting every late frame before it.
func (p page) at(i int) float64 {
	t := 1000.0
	for j := 0; j <= i; j++ {
		t += p.interval + p.late[j]
	}
	return t
}

// with is the page with input drawn by each of the given frames.
func (p page) with(frames ...int) page {
	p.inputs = append(p.inputs, frames...)
	return p
}

func (p page) raw(t *testing.T) []byte {
	t.Helper()
	return encode(t, p.times(), p.inputs)
}

func (p page) times() []float64 {
	frames := make([]float64, p.frames)
	for i := range frames {
		frames[i] = p.at(i)
	}
	return frames
}

func encode(t *testing.T, frames, inputs any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"frames": frames, "inputs": inputs})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (p page) summary(t *testing.T) Summary {
	t.Helper()
	r, err := ParseRecording(p.raw(t))
	if err != nil {
		t.Fatal(err)
	}
	return Summarize(r)
}

func near(got, want time.Duration) bool {
	d := got - want
	return d < time.Millisecond/100 && d > -time.Millisecond/100
}

// A page with nothing to redraw keeps to its refresh interval while input
// arrives, and nothing is missed.
func TestAnIdlePageMissesNothing(t *testing.T) {
	s := page{interval: 16.667, frames: 60}.with(35, 45).summary(t)

	if !near(s.Refresh, 16667*time.Microsecond) {
		t.Errorf("refresh %v, want 16.667ms", s.Refresh)
	}
	if s.Missed != 0 {
		t.Errorf("missed %d frames on an idle page", s.Missed)
	}
	if !near(s.Median, s.Refresh) || !near(s.P95, s.Refresh) || !near(s.Worst, s.Refresh) {
		t.Errorf("median %v, p95 %v, worst %v; want each at the refresh interval", s.Median, s.P95, s.Worst)
	}
	// Inputs drawn by frames 35 and 45 are judged by the intervals beginning at
	// frames 34 through 45.
	if s.Frames != 12 {
		t.Errorf("%d frames judged, want 12", s.Frames)
	}
}

// A frame's cost shows only when the next one starts, so the last input is
// judged by the interval after the frame that handled it, not left out.
func TestTheLastInputIsJudgedByTheFrameThatDrewIt(t *testing.T) {
	s := page{interval: 16.667, frames: 60, late: map[int]float64{46: 300}}.with(35, 45).summary(t)

	if s.Missed != 18 {
		t.Errorf("missed %d, want the 18 lost to drawing the last input", s.Missed)
	}
}

// One frame held up for three refresh intervals is two frames the browser
// never started.
func TestAStalledFrameCountsWhatItMissed(t *testing.T) {
	s := page{interval: 16.667, frames: 60, late: map[int]float64{40: 33.334}}.with(35, 45).summary(t)

	if s.Missed != 2 {
		t.Errorf("missed %d, want 2", s.Missed)
	}
	if !near(s.Worst, 50001*time.Microsecond) {
		t.Errorf("worst %v, want 50.001ms", s.Worst)
	}
	if !near(s.Median, 16667*time.Microsecond) {
		t.Errorf("median %v, want 16.667ms; one stall should not move it", s.Median)
	}
}

// A 120 Hz display drawing at 60 Hz under load misses every other frame, which
// a fixed 16.7ms budget would call perfect.
func TestFramesAreJudgedAgainstTheBrowsersOwnRate(t *testing.T) {
	late := map[int]float64{}
	for i := 35; i <= 46; i++ {
		late[i] = 8.333
	}
	s := page{interval: 8.333, frames: 60, late: late}.with(35, 45).summary(t)

	if !near(s.Refresh, 8333*time.Microsecond) {
		t.Errorf("refresh %v, want 8.333ms", s.Refresh)
	}
	// Twelve intervals draw the input, each twice the resting interval.
	if s.Missed != 12 {
		t.Errorf("missed %d, want 12: one per interval drawn at half the rate", s.Missed)
	}
}

// Work after the input has been drawn is not the input's cost, even in the
// very next frame.
func TestStallsOutsideTheInputAreNotCounted(t *testing.T) {
	s := page{interval: 16.667, frames: 60, late: map[int]float64{47: 200}}.with(35, 45).summary(t)

	if s.Missed != 0 {
		t.Errorf("missed %d, counting a stall after the input ended", s.Missed)
	}
}

func TestParseRefusesWhatNoProbeProduces(t *testing.T) {
	quiet := page{interval: 16.667, frames: 60}
	steady := quiet.with(35)

	for _, c := range []struct {
		name string
		raw  string
	}{
		{"not JSON", `frames`},
		{"a missing frame time", `{"frames":[1,2,null],"inputs":[1]}`},
		{"a missing input", string(encode(t, quiet.times(), []any{nil}))},
		{"a negative time", `{"frames":[-1,2,3],"inputs":[1]}`},
		{"an input between frames", string(encode(t, quiet.times(), []float64{35.5}))},
		{"an input before any frame", string(encode(t, quiet.times(), []int{-1}))},
		{"frames out of order", `{"frames":[1,3,2],"inputs":[1]}`},
		{"inputs out of order", string(quiet.with(45, 35).raw(t))},
		{"a repeated frame", `{"frames":[1,2,2],"inputs":[1]}`},
		{"no input", string(quiet.raw(t))},
		{"too few quiet frames", string(quiet.with(10).raw(t))},
		{"the last input drawn by the last frame", string(quiet.with(59).raw(t))},
		{"the last input drawn by a frame never started", string(quiet.with(60).raw(t))},
	} {
		if _, err := ParseRecording([]byte(c.raw)); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}

	if _, err := ParseRecording(steady.raw(t)); err != nil {
		t.Errorf("a steady recording was refused: %v", err)
	}
}
