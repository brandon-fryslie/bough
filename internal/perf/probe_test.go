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
		Recording json.RawMessage `json:"recording"`
		Listening int             `json:"listening"`
		Pending   int             `json:"pending"`
	}
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatalf("reading the stand-in page's output: %v\n%s", err, out)
	}

	r, err := ParseRecording(run.Recording)
	if err != nil {
		t.Fatalf("the probe's recording did not parse: %v\n%s", err, run.Recording)
	}
	s := Summarize(r)
	if !near(s.Refresh, 16667*time.Microsecond) {
		t.Errorf("refresh %v, want the stand-in's 16.667ms", s.Refresh)
	}
	if s.Missed != 0 || s.Frames != 10 {
		t.Errorf("%d frames with %d missed, want 10 with none", s.Frames, s.Missed)
	}

	// A probe that kept listening or kept asking for frames would go on
	// costing the page it measured after the measurement ended.
	if run.Listening != 0 {
		t.Errorf("%d input listeners left on the page", run.Listening)
	}
	if run.Pending != 0 {
		t.Errorf("%d frame requests left after finishing", run.Pending)
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
function frame() {
  now += 16.667;
  const due = pending;
  pending = [];
  due.forEach((fn) => fn(now));
}
function input(name) {
  (listeners[name] || []).forEach((fn) => fn({ timeStamp: now + 3 }));
}

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

let recording = null;
window.__boughProbe.finish((r) => { recording = r; });
for (let i = 0; !recording; i++) {
  if (i > 5) throw new Error("the probe never finished");
  frame();
}

console.log(JSON.stringify({
  recording,
  listening: Object.values(listeners).reduce((n, l) => n + l.length, 0),
  pending: pending.length,
}));
`
