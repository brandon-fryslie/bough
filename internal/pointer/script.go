// Package pointer plays input through a real pointing device: lowtalker's
// virtual mouse, whose reports reach the system the way a hand's would. A
// browser cannot tell them from a mouse, which it can with input driven
// through WebDriver: Chrome hands driven input to frames one event at a time,
// and Safari drops most of it.
package pointer

import (
	"bytes"
	"encoding/json"
	"math"
	"time"
)

// ScreenPoint is a place on the screen, in points from the top left of the
// main display.
type ScreenPoint struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Script is what the device plays: the cursor put at Start, and then, once the
// clock starts, each report at its time, in order.
type Script struct {
	Start   ScreenPoint
	Reports []Report
}

// Report is one report the device sends, At after the clock starts.
type Report struct {
	At   time.Duration
	Does Action
}

// Action is what one report does.
//
// [LAW:types-are-the-program] a scenario presses the left button and lets
// every button go, so those are the only button reports there are.
type Action interface {
	fields() map[string]any
}

// Press holds the left button down.
type Press struct{}

// Release lets every button up.
type Release struct{}

// Move moves the pointer by DX, DY counts, each -127 to 127. Counts are what a
// mouse reports, not points: the system scales them by how fast they arrive,
// as it does a hand's.
type Move struct {
	DX, DY int
}

// Turn turns the wheel by Ticks, -127 to 127. A positive turn scrolls content
// up, as a wheel rolled away from you does.
type Turn struct {
	Ticks int
}

func (Press) fields() map[string]any   { return map[string]any{"down": "left"} }
func (Release) fields() map[string]any { return map[string]any{"up": true} }
func (m Move) fields() map[string]any {
	return map[string]any{"move": map[string]int{"dx": m.DX, "dy": m.DY}}
}
func (t Turn) fields() map[string]any {
	return map[string]any{"wheel": map[string]int{"v": t.Ticks, "h": 0}}
}

// lines is the script as lowtalker reads it: JSON Lines, the start first, and
// then a line a report with its time in milliseconds.
func (s Script) lines() ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	if err := enc.Encode(map[string]ScreenPoint{"to": s.Start}); err != nil {
		return nil, err
	}
	for _, r := range s.Reports {
		line := r.Does.fields()
		// Microseconds are as fine as the device reports the times it sent at.
		line["t_ms"] = math.Round(float64(r.At.Microseconds())) / 1000
		if err := enc.Encode(line); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}
