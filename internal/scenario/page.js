// What a scenario needs to know of bough's page, injected by whatever drives
// the browser and never shipped in the page: where things are drawn, so input
// lands on them, and the view, so a gesture can be seen to have taken.
//
// Three calls, each handing its answer to done:
//
//   __boughPage.drawn(done)      waits until the diagram is drawn and has
//                                finished growing in
//   __boughPage.geometry(done)   where the stage, bare canvas and prompts are
//   __boughPage.view(done)       where the diagram is, how large, and the note
//                                that is up
(function (root) {
  var doc = root.document;
  // A day's group holds its square, its date, and its tasks with their
  // prompts, and the page notes whatever in it the pointer enters. PROMPT is
  // the innermost of those, which a pointer arriving on always enters.
  var DAY = ".day";
  var PROMPT = ".prompt-node";

  function canvas() { return doc.getElementById("canvas"); }

  // Bare canvas is stage the pointer can rest on or press without entering
  // anything the page notes, or a control drawn over the stage.
  function bare(stage, x, y) {
    var hit = doc.elementFromPoint(x, y);
    return hit !== null && stage.contains(hit) && hit.closest(DAY) === null;
  }

  // MARGIN is how far bare canvas has to stay bare around a point. Nodes keep
  // a smallest size on screen as the view zooms out, so one just beside a
  // still pointer can grow under it.
  var MARGIN = 24;

  root.__boughPage = {
    drawn: function (done) {
      // The diagram's transform is set the first time the view is applied, and
      // the page grows the diagram in for a second after. Until both are done
      // the page is still animating, and a recording would measure that.
      var shown = canvas() && canvas().getAttribute("transform");
      if (shown && !doc.body.classList.contains("growing")) return done();
      requestAnimationFrame(function () { root.__boughPage.drawn(done); });
    },

    geometry: function (done) {
      var stage = doc.getElementById("stage");
      var box = stage.getBoundingClientRect();
      var middle = { x: box.left + box.width / 2, y: box.top + box.height / 2 };

      // The bare canvas nearest the middle, looked for in the middle half of
      // the stage so a drag from it has room to travel in any direction.
      var candidates = [];
      for (var y = box.top + box.height / 4; y <= box.top + box.height * 3 / 4; y += 8) {
        for (var x = box.left + box.width / 4; x <= box.left + box.width * 3 / 4; x += 8) {
          candidates.push({ x: Math.round(x), y: Math.round(y) });
        }
      }
      candidates.sort(function (a, b) {
        return Math.hypot(a.x - middle.x, a.y - middle.y) - Math.hypot(b.x - middle.x, b.y - middle.y);
      });
      var empty = candidates.find(function (p) {
        return [[0, 0], [-MARGIN, 0], [MARGIN, 0], [0, -MARGIN], [0, MARGIN]].every(function (d) {
          return bare(stage, p.x + d[0], p.y + d[1]);
        });
      }) || null;

      // A prompt counts when pointing at its middle reaches it, not something
      // drawn over it.
      var prompts = [];
      stage.querySelectorAll(PROMPT).forEach(function (prompt) {
        var r = prompt.getBoundingClientRect();
        var p = { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) };
        if (doc.elementFromPoint(p.x, p.y) === prompt) prompts.push(p);
      });
      prompts.sort(function (a, b) { return a.x - b.x || a.y - b.y; });

      done({
        stage: { x: Math.round(box.left), y: Math.round(box.top), width: Math.round(box.width), height: Math.round(box.height) },
        empty: empty,
        prompts: prompts
      });
    },

    view: function (done) {
      var t = /translate\(([^,]+),([^)]+)\) scale\(([^)]+)\)/.exec(canvas().getAttribute("transform"));
      var pop = doc.getElementById("pop");
      var r = pop.getBoundingClientRect();
      done({
        x: Number(t[1]),
        y: Number(t[2]),
        scale: Number(t[3]),
        // A note is told apart from another by what it says and where it
        // stands, since two prompts can say the same thing.
        note: pop.hidden ? "" : pop.textContent + " @ " + Math.round(r.left) + "," + Math.round(r.top)
      });
    }
  };
})(window);
