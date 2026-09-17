package perf

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The probe runs in a browser and its recording is read here, so the one
// place the two can disagree is the shape crossing between them. Node runs
// the real probe against a stand-in page, a frame clock and a little input,
// and whatever it hands back has to parse and summarise as that page drew.
func TestProbeRecordsWhatParses(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not available to run the probe")
	}

	dir := t.TempDir()
	probe := filepath.Join(dir, "probe.js")
	stand := filepath.Join(dir, "page.js")
	if err := os.WriteFile(probe, []byte(Probe), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stand, []byte(standInPage), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("node", stand, probe, strconv.Itoa(Quiet)).Output()
	if err != nil {
		t.Fatalf("running the probe: %v\n%s", err, out)
	}

	var run struct {
		Recording  json.RawMessage `json:"recording"`
		AskedAgain bool            `json:"askedAgain"`
		CutShort   bool            `json:"cutShort"`
		Listening  int             `json:"listening"`
		Pending    int             `json:"pending"`
	}
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatalf("reading the stand-in page's output: %v\n%s", err, out)
	}

	// A probe that kept listening or kept asking for frames would go on
	// costing the page it measured after the measurement ended.
	if run.Listening != 0 {
		t.Errorf("%d input listeners left on the page", run.Listening)
	}
	if run.Pending != 0 {
		t.Errorf("%d frame requests left after finishing", run.Pending)
	}
	if !run.AskedAgain {
		t.Error("asking a finished probe for its recording again did not hand it over")
	}
	if !run.CutShort {
		t.Error("a recording cut short by a new start was never handed to the finish waiting on it")
	}

	r, err := ParseRecording(run.Recording)
	if err != nil {
		t.Fatalf("the probe's recording did not parse: %v\n%s", err, run.Recording)
	}
	s := Summarize(r)
	if !near(s.Refresh, 16667*time.Microsecond) {
		t.Errorf("refresh %v, want the stand-in's 16.667ms", s.Refresh)
	}
	// The stand-in's last input is stamped after the frame that dispatches it,
	// and the frame that draws it runs 300ms long: the probe has to wait past
	// both, and those 300ms are 18 frames missed.
	if s.Missed != 18 || s.Frames != 13 {
		t.Errorf("%d frames with %d missed, want 13 with 18", s.Frames, s.Missed)
	}
}

// standInPage is the least of a browser the probe needs: a window to listen
// on, a frame clock that advances by hand, and input dispatched between
// frames. It prints the recording and what the probe left behind.
const standInPage = `
const fs = require("fs");
const listeners = {};
let pending = [];
globalThis.window = {
  addEventListener(name, fn) { (listeners[name] = listeners[name] || []).push(fn); },
  removeEventListener(name, fn) { listeners[name] = (listeners[name] || []).filter((f) => f !== fn); },
};
globalThis.requestAnimationFrame = (fn) => pending.push(fn);
eval(fs.readFileSync(process.argv[2], "utf8"));

let now = 1000;
function frame(extra = 0) {
  now += 16.667 + extra;
  const due = pending;
  pending = [];
  due.forEach((fn) => fn(now));
}
function input(name, after = 3) {
  (listeners[name] || []).forEach((fn) => fn({ timeStamp: now + after }));
}

// A recording abandoned partway, as a driver that gave up on a scenario would
// leave one, has to stop when the next starts.
window.__boughProbe.start(Number(process.argv[3]), () => {});
for (let i = 0; i < 2; i++) frame();
let cut = null;
window.__boughProbe.finish((r) => { cut = r; });

let ready = false;
window.__boughProbe.start(Number(process.argv[3]), () => { ready = true; });
for (let i = 0; !ready; i++) {
  if (i > 1000) throw new Error("the probe never said it was ready");
  frame();
}

for (let i = 0; i < 10; i++) {
  input(i % 2 ? "wheel" : "pointermove");
  frame();
}

// Text typed without a key, stamped later than the next frame starts, as Chrome
// can report a wheel it dispatches at the start of a frame, and followed by an
// event stamped earlier.
input("input", 20);
input("pointermove", 1);

let recording = null;
window.__boughProbe.finish((r) => { recording = r; });
for (let i = 0; !recording; i++) {
  if (i > 5) throw new Error("the probe never finished");
  // The second frame after is the first at or after the typing, so the one
  // after that is late by however long drawing it took.
  frame(i === 2 ? 300 : 0);
}

let again = null;
window.__boughProbe.finish((r) => { again = r; });

console.log(JSON.stringify({
  recording,
  askedAgain: again === recording,
  cutShort: cut !== null && cut.frames.length === 2,
  listening: Object.values(listeners).reduce((n, l) => n + l.length, 0),
  pending: pending.length,
}));
`
