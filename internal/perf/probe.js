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
//   __boughProbe.finish(done)        waits for one more frame, so the work the
//                                    last input caused has been drawn, stops,
//                                    and hands the recording to done.
(function (root) {
  var INPUT = ["pointerdown", "pointermove", "pointerup", "wheel", "keydown"];

  root.__boughProbe = {
    start: function (quiet, done) {
      var recording = { frames: [], inputs: [] };
      var next = tick;
      var listen = { capture: true, passive: true };

      function note(e) { recording.inputs.push(e.timeStamp); }

      function tick(t) {
        recording.frames.push(t);
        if (recording.frames.length === quiet + 1) done();
        requestAnimationFrame(function (t) { next(t); });
      }

      INPUT.forEach(function (name) { root.addEventListener(name, note, listen); });
      requestAnimationFrame(function (t) { next(t); });

      root.__boughProbe.finish = function (done) {
        next = function (t) {
          recording.frames.push(t);
          INPUT.forEach(function (name) { root.removeEventListener(name, note, listen); });
          next = function () {};
          done(recording);
        };
      };
    }
  };
})(typeof window !== "undefined" ? window : globalThis);
