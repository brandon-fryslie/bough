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

// page fills the window and notes each input event that reaches it.
const page = `<!doctype html>
<style>html, body { margin: 0; height: 100%; }</style>
<script>
window.seen = [];
for (const name of ["pointerdown", "pointermove", "pointerup", "wheel"]) {
  addEventListener(name, (e) => {
    seen.push({ type: e.type, trusted: e.isTrusted, deltaY: e.deltaY || 0 });
  }, { passive: true });
}
</script>`

// read hands back what the page saw, once a frame has started since the input.
const read = `const done = arguments[arguments.length - 1];
requestAnimationFrame(() => done(window.seen));`

type seen []struct {
	Type    string  `json:"type"`
	Trusted bool    `json:"trusted"`
	DeltaY  float64 `json:"deltaY"`
}

// frames hands back the start times of the next twenty frames.
const frames = `const done = arguments[arguments.length - 1];
const started = [];
requestAnimationFrame(function tick(t) {
  started.push(t);
  if (started.length < 20) requestAnimationFrame(tick);
  else done(started);
});`

// A real browser loads a page, runs scripts in it, and draws frames at a live
// rate. Where its driver's input can be trusted, that input reaches the page
// and the probe records it.
func TestRealBrowsers(t *testing.T) {
	if *browsers == "" {
		t.Skip("no -browsers to drive")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, page)
	}))
	t.Cleanup(site.Close)

	names := strings.FieldsFunc(*browsers, func(r rune) bool { return r == ',' || r == ' ' })
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			b, err := Named(name)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			// One session a browser, because Safari allows only one at a time.
			s := open(ctx, t, b)

			t.Run("frames", framesKeepComing(ctx, s, site.URL))
			t.Run("input", withInput(b, inputReachesThePage(ctx, s, site.URL)))
			t.Run("probe", withInput(b, probeRecordsIt(ctx, s, site.URL)))
		})
	}
}

// withInput is check, run only where input through b's driver reaches the page.
func withInput(b Browser, check func(*testing.T)) func(*testing.T) {
	return func(t *testing.T) {
		if err := b.Input(); err != nil {
			t.Skip(err)
		}
		check(t)
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

// see loads the page, performs a, and hands back what the page saw.
func see(ctx context.Context, t *testing.T, s *Session, url string, a Actions) seen {
	t.Helper()
	if err := s.Navigate(ctx, url); err != nil {
		t.Fatal(err)
	}
	if err := s.Perform(ctx, a); err != nil {
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
	return got
}

// The driven window draws as a window in front does. One the browser counts
// as hidden or in the background draws few frames or none, and the probe
// would judge that as the page's own stutter.
func framesKeepComing(ctx context.Context, s *Session, url string) func(*testing.T) {
	return func(t *testing.T) {
		if err := s.Navigate(ctx, url); err != nil {
			t.Fatal(err)
		}
		raw, err := s.ExecuteAsync(ctx, frames)
		if err != nil {
			t.Fatal(err)
		}
		var started []float64
		if err := json.Unmarshal(raw, &started); err != nil {
			t.Fatalf("reading the frame times: %v\n%s", err, raw)
		}
		// A window in front draws no slower than the 30 Hz Safari keeps to in Low
		// Power Mode, and a throttled one far slower than this, when at all.
		if each := (started[len(started)-1] - started[0]) / float64(len(started)-1); each > 100 {
			t.Errorf("a frame every %.1fms, as a window the browser has throttled draws", each)
		}
		t.Logf("frames started %v", started)
	}
}

// A drag and a wheel turn arrive as trusted events.
func inputReachesThePage(ctx context.Context, s *Session, url string) func(*testing.T) {
	return func(t *testing.T) { //nolint:thelper // a subtest body, handed to t.Run through withInput
		got := see(ctx, t, s, url, Actions{
			Mouse: []MouseStep{Move{X: 100, Y: 100}, Press{}, Move{X: 200, Y: 150, Over: 200 * time.Millisecond}, Release{}, Pause(0)},
			Wheel: []WheelStep{Pause(0), Pause(0), Pause(0), Pause(0), Scroll{X: 200, Y: 150, DeltaY: 120}},
		})

		types := map[string]int{}
		for _, e := range got {
			types[e.Type]++
			if !e.Trusted {
				t.Errorf("%s was not trusted, so it came from script, not the browser's input", e.Type)
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
		t.Logf("saw %v", types)
	}
}

// The frame probe, fed real wheel input, hands back a recording that parses
// and judges some frames: the probe and this client meet here and nowhere else
// before a run.
func probeRecordsIt(ctx context.Context, s *Session, url string) func(*testing.T) {
	return func(t *testing.T) { //nolint:thelper // a subtest body, handed to t.Run through withInput
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
