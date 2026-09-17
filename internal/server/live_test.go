package server

import (
	"context"
	"encoding/json"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/graph"
	"github.com/nickelsec/bough/internal/synthetic"
	"github.com/nickelsec/bough/internal/webdriver"
)

// browsers names the real browsers to load the page in. Driving one opens a
// window and needs its driver set up, so it is asked for, never assumed:
//
//	go test ./internal/server -run Live -browsers=chrome,safari
var browsers = flag.String("browsers", "", "real browsers to drive, comma separated: chrome, safari")

// turns dispatches wheel turns on the middle of the stage, all of them within
// one frame or one a frame, and hands back how often the view was written and
// the stage measured while they were drawn, and the view they left.
const turns = `
var perFrame = arguments[0], done = arguments[arguments.length - 1];
var stage = document.getElementById("stage");
var canvas = document.getElementById("canvas");
var box = stage.getBoundingClientRect();

var writes = 0, measured = 0;
new MutationObserver(function (records) { writes += records.length; })
  .observe(canvas, { attributes: true, attributeFilter: ["transform"] });
var measure = stage.getBoundingClientRect;
stage.getBoundingClientRect = function () { measured++; return measure.apply(this, arguments); };

function turn() {
  stage.dispatchEvent(new WheelEvent("wheel", {
    deltaY: -8, clientX: box.left + box.width / 2, clientY: box.top + box.height / 2,
    bubbles: true, cancelable: true
  }));
}
// Two frames after the last turn, so the frame drawing it has run and the
// observer has been told.
function settle() {
  requestAnimationFrame(function () {
    requestAnimationFrame(function () {
      stage.getBoundingClientRect = measure;
      done({ writes: writes, measured: measured, view: canvas.getAttribute("transform") });
    });
  });
}
var left = 10;
requestAnimationFrame(function frame() {
  if (perFrame) {
    turn();
    if (--left > 0) return requestAnimationFrame(frame);
  } else {
    while (left-- > 0) turn();
  }
  settle();
});
`

// drawnIn waits until the page has drawn its diagram and finished growing it
// in, and the view it opened at is applied.
const drawnIn = `
var done = arguments[arguments.length - 1];
(function wait() {
  var canvas = document.getElementById("canvas");
  if (canvas && canvas.getAttribute("transform") && !document.body.classList.contains("growing")) return done(true);
  requestAnimationFrame(wait);
})();
`

type turned struct {
	Writes   int    `json:"writes"`
	Measured int    `json:"measured"`
	View     string `json:"view"`
}

// However fast wheel turns arrive, the page draws the view once a frame and
// never measures the stage to place them, and where they leave the view does
// not depend on how many frames they arrived over.
func TestLiveWheelTurnsDrawOnceAFrame(t *testing.T) {
	if *browsers == "" {
		t.Skip("no -browsers to drive")
	}
	shape, err := synthetic.Sized("small")
	if err != nil {
		t.Fatal(err)
	}
	url := serveLive(t, synthetic.History(shape))

	for _, name := range strings.FieldsFunc(*browsers, func(r rune) bool { return r == ',' || r == ' ' }) {
		t.Run(name, func(t *testing.T) {
			b, err := webdriver.Named(name)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			w, err := webdriver.Open(ctx, b)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				if err := w.Close(closing); err != nil {
					t.Error(err)
				}
			})

			play := func(perFrame bool) turned {
				t.Helper()
				if err := w.Navigate(ctx, url); err != nil {
					t.Fatal(err)
				}
				if _, err := w.ExecuteAsync(ctx, drawnIn); err != nil {
					t.Fatal(err)
				}
				raw, err := w.ExecuteAsync(ctx, turns, perFrame)
				if err != nil {
					t.Fatal(err)
				}
				var got turned
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("%v: %s", err, raw)
				}
				return got
			}

			together, apart := play(false), play(true)
			if together.Writes != 1 {
				t.Errorf("ten turns in one frame wrote the view %d times, want once", together.Writes)
			}
			if together.Measured != 0 || apart.Measured != 0 {
				t.Errorf("wheel turns measured the stage %d times in one frame and %d over ten, want none", together.Measured, apart.Measured)
			}
			if together.View != apart.View {
				t.Errorf("ten turns left the view at %s in one frame and at %s over ten", together.View, apart.View)
			}
		})
	}
}

// serveLive puts g's page on a loopback port until the test ends.
func serveLive(t *testing.T, g graph.Graph) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	urls := make(chan string, 1)
	var served error
	stopped := make(chan struct{})
	go func() {
		served = Serve(ctx, "synthetic", g, nil, func(id string) string { return id }, func(url string) { urls <- url })
		close(stopped)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
		if served != nil {
			t.Errorf("serving the page: %v", served)
		}
	})
	select {
	case url := <-urls:
		return url
	case <-stopped:
		t.Fatal("the page stopped being served before it had an address")
		return ""
	}
}
