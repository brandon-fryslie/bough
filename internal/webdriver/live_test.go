package webdriver

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/perf"
)

// browsers names the real browsers to drive. Driving one opens a window and
// needs its driver set up, so it is asked for, never assumed:
//
//	go test ./internal/webdriver -browsers=chrome,safari
var browsers = flag.String("browsers", "", "real browsers to drive, comma separated: chrome, safari")

// page fills the window and notes each input event that reaches it, with its
// timestamp and the page clock when it arrived.
const page = `<!doctype html>
<style>html, body { margin: 0; height: 100%; }</style>
<script>
window.seen = [];
for (const name of ["pointerdown", "pointermove", "pointerup", "wheel"]) {
  addEventListener(name, (e) => {
    seen.push({ type: e.type, trusted: e.isTrusted, stamp: e.timeStamp, now: performance.now(), deltaY: e.deltaY || 0 });
  }, { passive: true });
}
</script>`

// read hands back what the page saw, with the start time of the next frame
// and the page clock just after, both taken inside that frame.
const read = `const done = arguments[arguments.length - 1];
requestAnimationFrame((frame) => done({ seen: window.seen, frame: frame, now: performance.now() }));`

type seen struct {
	Seen []struct {
		Type    string  `json:"type"`
		Trusted bool    `json:"trusted"`
		Stamp   float64 `json:"stamp"`
		Now     float64 `json:"now"`
		DeltaY  float64 `json:"deltaY"`
	} `json:"seen"`
	Frame float64 `json:"frame"`
	Now   float64 `json:"now"`
}

// Real input reaches a real page as trusted events, stamped on the same clock
// the page's frames are, which is what lets a recording set one against the
// other.
func TestRealInputReachesTheBrowser(t *testing.T) {
	if *browsers == "" {
		t.Skip("no -browsers to drive")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, page)
	}))
	t.Cleanup(site.Close)

	known := map[string]Browser{"chrome": Chrome(), "safari": Safari()}
	names := strings.FieldsFunc(*browsers, func(r rune) bool { return r == ',' || r == ' ' })
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			b, ok := known[name]
			if !ok {
				t.Fatalf("no browser called %q", name)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			// One session a browser, because Safari allows only one at a time.
			s := open(ctx, t, b)
			t.Run("input", inputReachesThePage(ctx, s, site.URL))
			t.Run("probe", probeRecordsIt(ctx, s, site.URL))
		})
	}
}

// open starts b's driver and a session in it, both ended when the test is.
func open(ctx context.Context, t *testing.T, b Browser) *Session {
	t.Helper()
	d, err := Start(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	s, err := d.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// The test's own deadline may have passed by now; closing still has to
		// happen, but not wait on a hung browser forever, or the driver is never
		// stopped either.
		closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := s.Close(closing); err != nil {
			t.Error(err)
		}
	})
	return s
}

// A drag and a wheel turn arrive as trusted events, each stamped on the clock
// the page's frames start by.
func inputReachesThePage(ctx context.Context, s *Session, url string) func(*testing.T) {
	return func(t *testing.T) {
		if err := s.Navigate(ctx, url); err != nil {
			t.Fatal(err)
		}
		drag := Actions{Mouse: []MouseStep{Move{X: 100, Y: 100}, Press{}, Move{X: 200, Y: 150, Over: 200 * time.Millisecond}, Release{}}}
		if err := s.Perform(ctx, drag); err != nil {
			t.Fatal(err)
		}
		zoom := Actions{Wheel: []WheelStep{Scroll{X: 200, Y: 150, DeltaY: 120}}}
		if err := s.Perform(ctx, zoom); err != nil {
			t.Fatal(err)
		}

		raw, err := s.ExecuteAsync(ctx, read)
		if err != nil {
			t.Fatal(err)
		}
		var got seen
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("reading what the page saw: %v\n%s", err, raw)
		}

		types := map[string]int{}
		for _, e := range got.Seen {
			types[e.Type]++
			if !e.Trusted {
				t.Errorf("%s was not trusted, so it came from script, not the browser's input", e.Type)
			}
			// On one clock, an event is stamped at or before it is handled, and
			// not long before.
			if lag := e.Now - e.Stamp; lag < -1 || lag > 1000 {
				t.Errorf("%s stamped %.1f but handled at %.1f: not the page's clock", e.Type, e.Stamp, e.Now)
			}
			if e.Type == "wheel" && e.DeltaY <= 0 {
				t.Errorf("wheel turned by %v, want down", e.DeltaY)
			}
		}
		for _, want := range []string{"pointerdown", "pointermove", "pointerup", "wheel"} {
			if types[want] == 0 {
				t.Errorf("no %s reached the page; saw %v", want, types)
			}
		}
		if lag := got.Now - got.Frame; lag < -1 || lag > 1000 {
			t.Errorf("frame started %.1f but the clock read %.1f inside it: not the page's clock", got.Frame, got.Now)
		}
		t.Logf("saw %v; frame at %.1f, clock %.1f", types, got.Frame, got.Now)
	}
}

// The frame probe, fed real wheel input, hands back a recording that parses
// and judges some frames: the probe and this client meet here and nowhere
// else before a run.
func probeRecordsIt(ctx context.Context, s *Session, url string) func(*testing.T) {
	return func(t *testing.T) {
		if err := s.Navigate(ctx, url); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ExecuteAsync(ctx, perf.Probe+"\n__boughProbe.start(arguments[0], arguments[1]);", perf.Quiet); err != nil {
			t.Fatal(err)
		}

		turns := make([]WheelStep, 20)
		for i := range turns {
			turns[i] = Scroll{X: 200, Y: 150, DeltaY: 40, Over: 16 * time.Millisecond}
		}
		if err := s.Perform(ctx, Actions{Wheel: turns}); err != nil {
			t.Fatal(err)
		}

		raw, err := s.ExecuteAsync(ctx, "__boughProbe.finish(arguments[0]);")
		if err != nil {
			t.Fatal(err)
		}
		r, err := perf.ParseRecording(raw)
		if err != nil {
			t.Fatalf("the probe's recording did not parse: %v\n%s", err, raw)
		}
		summary := perf.Summarize(r)
		if summary.Frames == 0 {
			t.Errorf("no frames judged: %+v", summary)
		}
		t.Logf("%+v", summary)
	}
}
