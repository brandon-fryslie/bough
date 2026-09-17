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
	for _, name := range strings.Split(*browsers, ",") {
		t.Run(name, func(t *testing.T) {
			b, ok := known[name]
			if !ok {
				t.Fatalf("no browser called %q", name)
			}
			drive(t, b, site.URL)
		})
	}
}

func drive(t *testing.T, b Browser, url string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	d, err := Start(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	s, err := d.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	}()

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
	t.Logf("%s saw %v; frame at %.1f, clock %.1f", b.name, types, got.Frame, got.Now)
}
