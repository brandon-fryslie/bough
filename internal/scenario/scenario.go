// Package scenario is the interactions worth measuring on bough's page, each
// written once as a gesture placed where the page drew things.
//
// A scenario is a value, so a Safari number and a Chrome number describe the
// same gesture, and adding one touches nothing that plays them.
package scenario

import (
	"slices"
	"time"

	"github.com/nickelsec/bough/internal/webdriver"
)

// Point is a place in the viewport, in CSS pixels from its top left.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Rect is a box in the viewport, in CSS pixels.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Geometry is where things are on the page as a browser drew it. It is read
// from the page rather than assumed, so a gesture lands where that browser, at
// that window size, put things. Read by Open, it always has bare canvas and at
// least one node.
type Geometry struct {
	Stage Rect
	// Empty is bare canvas near the stage's middle, where a press pans rather
	// than picking a node.
	Empty Point
	// Nodes is the middle of every node that pointing reaches, left to right.
	Nodes []Point
}

// View is what the page shows: where the diagram is, how large, and the note
// that is up, which is empty when there is none.
type View struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Scale float64 `json:"scale"`
	Note  string  `json:"note"`
}

// Scenario is one interaction: its name, the gesture it makes placed on a
// page, and whether the view the gesture left shows it took. A gesture that
// took nothing, a drag begun on a node say, would measure a page at rest.
type Scenario struct {
	Name  string
	Place func(Geometry) Gesture
	Took  func(before, after View) bool
}

// Gesture is input a person makes, placed in the viewport.
type Gesture interface {
	// start is where the pointer rests before the gesture begins.
	start() Point
	// actions is the gesture as WebDriver input, begun from start.
	actions() webdriver.Actions
}

// Pointer moves the pointer from From through each point in turn, one every
// Every, with the button held from first to last if Held.
type Pointer struct {
	From    Point
	Through []Point
	Every   time.Duration
	Held    bool
}

// Wheel turns the wheel Turns times with the pointer at At, each turn DeltaY
// pixels, one every Every. A positive DeltaY scrolls the page down.
type Wheel struct {
	At     Point
	DeltaY int
	Turns  int
	Every  time.Duration
}

func (p Pointer) start() Point { return p.From }

func (p Pointer) actions() webdriver.Actions {
	moves := make([]webdriver.MouseStep, len(p.Through))
	for i, to := range p.Through {
		moves[i] = webdriver.Move{X: to.X, Y: to.Y, Over: p.Every}
	}
	if !p.Held {
		return webdriver.Actions{Mouse: moves}
	}
	return webdriver.Actions{Mouse: slices.Concat([]webdriver.MouseStep{webdriver.Press{}}, moves, []webdriver.MouseStep{webdriver.Release{}})}
}

func (w Wheel) start() Point { return w.At }

func (w Wheel) actions() webdriver.Actions {
	turns := make([]webdriver.WheelStep, w.Turns)
	for i := range turns {
		turns[i] = webdriver.Scroll{X: w.At.X, Y: w.At.Y, DeltaY: w.DeltaY, Over: w.Every}
	}
	return webdriver.Actions{Wheel: turns}
}

// mouse is how often a mouse reports moving at 125 Hz, a common rate.
const mouse = 8 * time.Millisecond

// All is every scenario, in the order they run.
var All = []Scenario{
	{
		// A quarter of the stage to the left, as someone moving along a history.
		Name: "drag-pan",
		Place: func(g Geometry) Gesture {
			to := Point{X: g.Empty.X - g.Stage.Width/4, Y: g.Empty.Y}
			return Pointer{From: g.Empty, Through: line(g.Empty, to, 60), Every: mouse, Held: true}
		},
		Took: func(before, after View) bool { return after.X != before.X },
	},
	{
		Name:  "wheel-zoom-in",
		Place: scroll(-8),
		Took:  func(before, after View) bool { return after.Scale > before.Scale },
	},
	{
		Name:  "wheel-zoom-out",
		Place: scroll(8),
		Took:  func(before, after View) bool { return after.Scale < before.Scale },
	},
	{
		// Across the nodes left to right, each one's note shown in turn.
		Name: "hover-sweep",
		Place: func(g Geometry) Gesture {
			nodes := spread(g.Nodes, 40)
			return Pointer{From: nodes[0], Through: nodes[1:], Every: 2 * mouse}
		},
		Took: func(before, after View) bool { return after.Note != "" && after.Note != before.Note },
	},
}

// scroll places sixty small wheel turns of delta pixels on bare canvas, as a
// trackpad reports a steady scroll, so the zoom is the only thing drawn.
func scroll(delta int) func(Geometry) Gesture {
	return func(g Geometry) Gesture {
		return Wheel{At: g.Empty, DeltaY: delta, Turns: 60, Every: mouse}
	}
}

// line is the steps points from just past from to to, evenly spaced.
func line(from, to Point, steps int) []Point {
	out := make([]Point, steps)
	for i := range out {
		out[i] = Point{
			X: from.X + (to.X-from.X)*(i+1)/steps,
			Y: from.Y + (to.Y-from.Y)*(i+1)/steps,
		}
	}
	return out
}

// spread is at most n of points, evenly spaced through them and in order.
func spread(points []Point, n int) []Point {
	out := make([]Point, min(n, len(points)))
	for i := range out {
		out[i] = points[i*len(points)/len(out)]
	}
	return out
}
