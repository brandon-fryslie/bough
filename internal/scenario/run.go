package scenario

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nickelsec/bough/internal/perf"
	"github.com/nickelsec/bough/internal/webdriver"
)

//go:embed page.js
var page string

// Open loads the page at url in s, waits until its diagram is drawn, and reads
// where things are on it.
func Open(ctx context.Context, s *webdriver.Session, url string) (Geometry, error) {
	if err := s.Navigate(ctx, url); err != nil {
		return Geometry{}, err
	}
	if _, err := ask(ctx, s, "drawn"); err != nil {
		return Geometry{}, err
	}
	raw, err := ask(ctx, s, "geometry")
	if err != nil {
		return Geometry{}, err
	}
	return readGeometry(raw)
}

// readGeometry keeps what the page said of itself as a Geometry, refusing a
// page with nowhere for a gesture to land.
func readGeometry(raw json.RawMessage) (Geometry, error) {
	var wire struct {
		Stage   Rect    `json:"stage"`
		Empty   *Point  `json:"empty"`
		Prompts []Point `json:"prompts"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Geometry{}, fmt.Errorf("reading the page's geometry: %w\n%s", err, raw)
	}
	switch {
	case wire.Stage.Width <= 0 || wire.Stage.Height <= 0:
		return Geometry{}, fmt.Errorf("the stage is %dx%d, too small to point at", wire.Stage.Width, wire.Stage.Height)
	case wire.Empty == nil:
		return Geometry{}, errors.New("no bare canvas in the middle of the stage to press on")
	case len(wire.Prompts) == 0:
		return Geometry{}, errors.New("no prompt on the stage that pointing reaches")
	}
	return Geometry{Stage: wire.Stage, Empty: *wire.Empty, Prompts: wire.Prompts}, nil
}

// Run plays sc on the page at url in s and hands back how the page drew it.
//
// The pointer is put at the gesture's start before recording, since getting
// there is not the gesture. A gesture that did not take as its scenario means
// is an error rather than a recording, because what it recorded is not that
// gesture.
func Run(ctx context.Context, s *webdriver.Session, url string, sc Scenario) (perf.Recording, error) {
	g, err := Open(ctx, s, url)
	if err != nil {
		return perf.Recording{}, err
	}
	gesture := sc.Place(g)
	at := gesture.start()
	if err := s.Perform(ctx, webdriver.Actions{Mouse: []webdriver.MouseStep{webdriver.Move{X: at.X, Y: at.Y}}}); err != nil {
		return perf.Recording{}, fmt.Errorf("%s: putting the pointer at its start: %w", sc.Name, err)
	}
	before, err := view(ctx, s)
	if err != nil {
		return perf.Recording{}, err
	}

	if _, err := s.ExecuteAsync(ctx, perf.Probe+"\n__boughProbe.start(arguments[0], arguments[1]);", perf.Quiet); err != nil {
		return perf.Recording{}, fmt.Errorf("%s: starting the probe: %w", sc.Name, err)
	}
	if err := s.Perform(ctx, gesture.actions()); err != nil {
		return perf.Recording{}, fmt.Errorf("%s: %w", sc.Name, err)
	}
	raw, err := s.ExecuteAsync(ctx, "__boughProbe.finish(arguments[0]);")
	if err != nil {
		return perf.Recording{}, fmt.Errorf("%s: finishing the probe: %w", sc.Name, err)
	}

	after, err := view(ctx, s)
	if err != nil {
		return perf.Recording{}, err
	}
	if !sc.Took(before, after) {
		return perf.Recording{}, fmt.Errorf("%s did not take as it means to, going from %+v to %+v, so its recording is not of that gesture", sc.Name, before, after)
	}
	r, err := perf.ParseRecording(raw)
	if err != nil {
		return perf.Recording{}, fmt.Errorf("%s: %w", sc.Name, err)
	}
	return r, nil
}

func view(ctx context.Context, s *webdriver.Session) (View, error) {
	raw, err := ask(ctx, s, "view")
	if err != nil {
		return View{}, err
	}
	var v View
	if err := json.Unmarshal(raw, &v); err != nil {
		return View{}, fmt.Errorf("reading the page's view: %w\n%s", err, raw)
	}
	return v, nil
}

// ask calls one of page.js's calls in the page and hands back its answer.
func ask(ctx context.Context, s *webdriver.Session, call string) (json.RawMessage, error) {
	raw, err := s.ExecuteAsync(ctx, page+"\n__boughPage."+call+"(arguments[arguments.length - 1]);")
	if err != nil {
		return nil, fmt.Errorf("asking the page for %s: %w", call, err)
	}
	return raw, nil
}
