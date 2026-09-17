package webdriver

import (
	"context"
	"time"
)

// Actions is input to perform: one mouse and one wheel, each a list of steps.
// The browser runs the lists side by side, one step from each per tick, and a
// tick lasts as long as its longest step.
//
// [LAW:types-are-the-program] one mouse and one wheel is all a person has, so
// that is all this can say; each step type belongs to the one source that can
// perform it.
type Actions struct {
	Mouse []MouseStep
	Wheel []WheelStep
}

// MouseStep is something the mouse does.
type MouseStep interface {
	mouseStep() map[string]any
}

// WheelStep is something the wheel does.
type WheelStep interface {
	wheelStep() map[string]any
}

// Move takes the mouse to X, Y in CSS pixels from the viewport's top left,
// through the points between, over the given time.
type Move struct {
	X, Y int
	Over time.Duration
}

// Press holds the left button down.
type Press struct{}

// Release lets the left button up.
type Release struct{}

// Scroll turns the wheel by DeltaX, DeltaY pixels with the mouse at X, Y,
// spread over the given time.
type Scroll struct {
	X, Y           int
	DeltaX, DeltaY int
	Over           time.Duration
}

// Pause does nothing for a while, on either source.
type Pause time.Duration

func (m Move) mouseStep() map[string]any {
	return map[string]any{"type": "pointerMove", "origin": "viewport", "x": m.X, "y": m.Y, "duration": m.Over.Milliseconds()}
}

func (Press) mouseStep() map[string]any {
	return map[string]any{"type": "pointerDown", "button": 0}
}

func (Release) mouseStep() map[string]any {
	return map[string]any{"type": "pointerUp", "button": 0}
}

func (s Scroll) wheelStep() map[string]any {
	return map[string]any{"type": "scroll", "origin": "viewport", "x": s.X, "y": s.Y, "deltaX": s.DeltaX, "deltaY": s.DeltaY, "duration": s.Over.Milliseconds()}
}

func (p Pause) mouseStep() map[string]any { return p.step() }
func (p Pause) wheelStep() map[string]any { return p.step() }

func (p Pause) step() map[string]any {
	return map[string]any{"type": "pause", "duration": time.Duration(p).Milliseconds()}
}

// wire is the actions in the protocol's shape: a list of input sources, each
// with its own steps. A source with no steps is left out, so a driver that
// cannot turn a wheel can still move a mouse.
func (a Actions) wire() map[string]any {
	sources := []map[string]any{}
	if len(a.Mouse) > 0 {
		sources = append(sources, map[string]any{
			"type": "pointer", "id": "mouse",
			"parameters": map[string]any{"pointerType": "mouse"},
			"actions":    steps(a.Mouse, MouseStep.mouseStep),
		})
	}
	if len(a.Wheel) > 0 {
		sources = append(sources, map[string]any{
			"type": "wheel", "id": "wheel",
			"actions": steps(a.Wheel, WheelStep.wheelStep),
		})
	}
	return map[string]any{"actions": sources}
}

func steps[S any](in []S, wire func(S) map[string]any) []map[string]any {
	out := make([]map[string]any, len(in))
	for i, s := range in {
		out[i] = wire(s)
	}
	return out
}

// Perform sends a to the browser and returns once every step has run.
func (s *Session) Perform(ctx context.Context, a Actions) error {
	_, err := s.post(ctx, "/actions", a.wire())
	return err
}
