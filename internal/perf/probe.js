// The frame probe, injected into a page under measurement and never shipped in
// one.
//
// It notes when the browser starts each frame and when input reaches the page,
// both on the page's own clock, and judges nothing. Every browser has
// requestAnimationFrame and input event timestamps, so a recording means the
// same thing in Chrome as in Safari. Go parses what comes back and does the
// arithmetic.
//
// Two calls, made by whatever drives the browser:
//
//   __boughProbe.start(quiet, done)  records frames, and calls done once
//                                    quiet frame intervals have passed, which
//                                    is the cue to begin the input. A
//                                    recording still running is dropped.
//   __boughProbe.finish(done)        keeps recording until two frames have
//                                    started at or after the latest input,
//                                    then stops and hands the recording to
//                                    done.
//
// Why two: a frame's time is when it starts, before its layout and paint, so
// the first frame at or after an input is the one that handled it and only the
// start of the next shows how long that took. The input's own time can also
// fall after the start of the frame that dispatches it, so the count is taken
// against the input's time rather than against when finish was called.
// ParseRecording refuses a recording that stops sooner.
(function (root) {
  // input is here for text that arrives without a key, as a driver can type it.
  var INPUT = ["pointerdown", "pointermove", "pointerup", "wheel", "keydown", "input"];
  var listen = { capture: true, passive: true };

  // stop ends the recording in progress, so a probe started twice on one page
  // never leaves an earlier loop and its listeners costing the page.
  var stop = function () {};

  root.__boughProbe = {
    start: function (quiet, done) {
      stop();

      var recording = { frames: [], inputs: [] };
      var running = true;
      var finished = null;

      function note(e) { recording.inputs.push(e.timeStamp); }

      // drawn is whether two frames have started at or after the latest input.
      // Inputs are kept as they arrive, which need not be the order of their
      // times.
      function drawn() {
        var frames = recording.frames;
        var latest = recording.inputs.reduce(function (a, b) { return Math.max(a, b); }, -Infinity);
        var after = 0;
        for (var i = frames.length - 1; i >= 0 && frames[i] >= latest; i--) after++;
        return after >= 2;
      }

      function tick(t) {
        if (!running) return;
        recording.frames.push(t);
        if (recording.frames.length === quiet + 1) done();
        if (finished && drawn()) {
          stop();
          finished(recording);
          return;
        }
        requestAnimationFrame(tick);
      }

      stop = function () {
        running = false;
        INPUT.forEach(function (name) { root.removeEventListener(name, note, listen); });
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
