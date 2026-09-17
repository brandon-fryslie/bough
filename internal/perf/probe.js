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
//                                    is the cue to begin the input.
//   __boughProbe.finish(done)        keeps recording until two frames have
//                                    started at or after the last input, then
//                                    stops and hands the recording to done.
//
// Why two: a frame's time is when it starts, before its layout and paint, so
// the first frame at or after an input is the one that handled it and only the
// start of the next shows how long that took. The input's own time can also
// fall after the start of the frame that dispatches it, so the count is taken
// against the input's time rather than against when finish was called.
// ParseRecording refuses a recording that stops sooner.
(function (root) {
  var INPUT = ["pointerdown", "pointermove", "pointerup", "wheel", "keydown"];

  root.__boughProbe = {
    start: function (quiet, done) {
      var recording = { frames: [], inputs: [] };
      var listen = { capture: true, passive: true };
      var finished = null;

      function note(e) { recording.inputs.push(e.timeStamp); }

      // drawn is whether two frames have started at or after the last input.
      function drawn() {
        var frames = recording.frames, inputs = recording.inputs;
        var last = inputs.length ? inputs[inputs.length - 1] : -Infinity;
        var after = 0;
        for (var i = frames.length - 1; i >= 0 && frames[i] >= last; i--) after++;
        return after >= 2;
      }

      function tick(t) {
        recording.frames.push(t);
        if (recording.frames.length === quiet + 1) done();
        if (finished && drawn()) {
          INPUT.forEach(function (name) { root.removeEventListener(name, note, listen); });
          finished(recording);
          return;
        }
        requestAnimationFrame(tick);
      }

      INPUT.forEach(function (name) { root.addEventListener(name, note, listen); });
      requestAnimationFrame(tick);

      root.__boughProbe.finish = function (done) { finished = done; };
    }
  };
})(typeof window !== "undefined" ? window : globalThis);
