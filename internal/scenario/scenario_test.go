package scenario

import (
	"strings"
	"testing"

	"github.com/nickelsec/bough/internal/webdriver"
)

// A page as a browser might have drawn it: a stage below a header, bare
// canvas off its middle, and prompts scattered across it.
var drawn = Geometry{
	Stage:   Rect{X: 0, Y: 60, Width: 1200, Height: 740},
	Empty:   Point{X: 640, Y: 420},
	Prompts: []Point{{X: 100, Y: 300}, {X: 350, Y: 500}, {X: 700, Y: 200}, {X: 1100, Y: 780}},
}

// A driver refuses input aimed outside the viewport, and input off the stage
// would land on something other than the diagram, so every point a scenario
// touches is on the stage it was placed on.
func TestEveryGestureStaysOnTheStage(t *testing.T) {
	for _, sc := range All {
		g := sc.Place(drawn)
		touched := []Point{g.start()}
		a := g.actions()
		for _, step := range a.Mouse {
			if m, ok := step.(webdriver.Move); ok {
				touched = append(touched, Point{X: m.X, Y: m.Y})
			}
		}
		for _, step := range a.Wheel {
			if s, ok := step.(webdriver.Scroll); ok {
				touched = append(touched, Point{X: s.X, Y: s.Y})
			}
		}
		if len(a.Mouse)+len(a.Wheel) == 0 {
			t.Errorf("%s makes no input", sc.Name)
		}
		s := drawn.Stage
		for _, p := range touched {
			if p.X < s.X || p.X >= s.X+s.Width || p.Y < s.Y || p.Y >= s.Y+s.Height {
				t.Errorf("%s touches %v, off the stage %+v", sc.Name, p, s)
			}
		}
	}
}

// A view left as it was never counts as a gesture taking, or a scenario that
// did nothing would pass as measured.
func TestAViewLeftAsItWasTookNothing(t *testing.T) {
	names := map[string]bool{}
	for _, sc := range All {
		if names[sc.Name] {
			t.Errorf("two scenarios are called %s", sc.Name)
		}
		names[sc.Name] = true
		for _, v := range []View{{Scale: 1}, {X: 40, Y: -12, Scale: 2.5, Note: "a note @ 10,20"}} {
			if sc.Took(v, v) {
				t.Errorf("%s took with the view unchanged at %+v", sc.Name, v)
			}
		}
	}
}

// Bare canvas and a node to point at are what every gesture is placed from,
// so a page without them is refused before anything is played on it.
func TestAPageWithNowhereToPointIsRefused(t *testing.T) {
	for name, raw := range map[string]string{
		"not JSON":       `stage`,
		"no stage":       `{"stage":{"x":0,"y":0,"width":0,"height":0},"empty":{"x":1,"y":1},"prompts":[{"x":2,"y":2}]}`,
		"no bare canvas": `{"stage":{"x":0,"y":0,"width":800,"height":600},"empty":null,"prompts":[{"x":2,"y":2}]}`,
		"no prompt":      `{"stage":{"x":0,"y":0,"width":800,"height":600},"empty":{"x":1,"y":1},"prompts":[]}`,
	} {
		if _, err := readGeometry([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := readGeometry([]byte(`{"stage":{"x":0,"y":0,"width":800,"height":600},"empty":{"x":1,"y":1},"prompts":[{"x":2,"y":2}]}`)); err != nil {
		t.Errorf("a page with somewhere to point was refused: %v", err)
	}
}

// Every scenario is found by its name, and a name no scenario has is refused
// with the names there are.
func TestAScenarioIsFoundByName(t *testing.T) {
	for _, sc := range All {
		got, err := Named(sc.Name)
		if err != nil || got.Name != sc.Name {
			t.Errorf("%s found %q, %v", sc.Name, got.Name, err)
		}
	}
	if _, err := Named("pinch"); err == nil || !strings.Contains(err.Error(), "hover-sweep") {
		t.Errorf("an unknown scenario failed with %v, want the scenarios there are", err)
	}
}
