// The frame probe, injected into a page under measurement and never shipped in
// one.
//
// It notes when the browser starts each frame, and for each input the frame
// that draws it, and judges nothing. Every browser has requestAnimationFrame,
// so a recording means the same thing in Chrome as in Safari. Go parses what
// comes back and does the arithmetic.
//
// Two calls, made by whatever drives the browser:
//
//   __boughProbe.start(quiet, done)  records frames, and calls done once
//                                    quiet frame intervals have passed, which
//                                    is the cue to begin the input. A
//                                    recording still running is stopped and
//                                    handed to any finish waiting on it.
//   __boughProbe.finish(done)        keeps recording until the frame after
//                                    the one that draws the latest input has
//                                    started, then stops and hands the
//                                    recording to done.
//
// Why the frame after: a frame's time is when it starts, before its layout and
// paint, so only the start of the next shows how long drawing took. The wait
// is counted from the latest input rather than from when finish was called,
// since input can still be arriving then. ParseRecording refuses a recording
// that stops sooner.
(function (root) {
  // input is here for text that arrives without a key, as a driver can type it.
  var INPUT = ["pointerdown", "pointermove", "pointerup", "wheel", "keydown", "input"];
  var listen = { capture: true, passive: true };

  // stop ends the recording in progress, so a probe started twice on one page
  // never leaves an earlier loop and its listeners costing the page, nor a
  // driver waiting on it.
  var stop = function () {};

  root.__boughProbe = {
    start: function (quiet, done) {
      stop();

      var recording = { frames: [], inputs: [] };
      var running = true;
      var finished = null;

      // An input is noted as how many frames had started when it arrived,
      // which is the index of the frame that draws it. Input a browser hands a
      // frame, as Chrome does wheel turns, arrives before that frame's tick;
      // input between frames arrives before the next one's. No time is read:
      // the time a frame starts says nothing of which of those happened, and
      // Safari stamps input it is driven with on some other clock.
      function note() { recording.inputs.push(recording.frames.length); }

      // drawn is whether the frame after the one that draws the latest input
      // has started. With no input at all any two frames do, so a driver whose
      // input never arrived still gets its recording back, and hears why from
      // the parse.
      function drawn() {
        var latest = recording.inputs.reduce(function (a, b) { return Math.max(a, b); }, 0);
        return recording.frames.length >= latest + 2;
      }

      function tick(t) {
        if (!running) return;
        recording.frames.push(t);
        if (recording.frames.length === quiet + 1) done();
        if (finished && drawn()) return stop();
        requestAnimationFrame(tick);
      }

      // [LAW:dataflow-not-control-flow] a recording ends one way, whether it
      // drew its input or a new start cut it short: it stops, and whoever is
      // waiting on finish gets it. A cut-short one fails to parse, loudly.
      stop = function () {
        if (!running) return;
        running = false;
        INPUT.forEach(function (name) { root.removeEventListener(name, note, listen); });
        if (finished) finished(recording);
      };

      INPUT.forEach(function (name) { root.addEventListener(name, note, listen); });
      requestAnimationFrame(tick);

      // A recording already stopped answers at once, so a driver asking twice
      // gets the recording rather than a wait that never ends.
      root.__boughProbe.finish = function (done) {
        if (running) finished = done;
        else done(recording);
      };
    }
  };
})(typeof window !== "undefined" ? window : globalThis);
