// Works out where every node and line goes.
//
// Nothing here draws anything. It takes the graph and returns nodes and edges,
// which keeps it testable: two nodes on top of each other or a runaway canvas
// fails an assertion instead of being noticed by eye later.
//
// The arrangement is the one you would draw on paper. A spine runs left to
// right through time, with a square for every day worked. Each day carries its
// tasks above and below it, and each task carries a circle for every prompt.
// Straight lines join a node to its parent and to nothing else.
//
// Two agents' work shares the one spine. Each agent then has a side of it, and
// sittings of the two that ran at the same time start in the same column, one
// above the spine and one below, so the overlap is where the eye already is.
//
// Every number comes from the data or from a constant below, and nothing is
// random, so the same history always draws the same way and a screenshot of it
// is stable.
(function (root) {
  "use strict";

  // Two forms of the same diagram. The overview packs it in so the whole thing
  // fits a window; the reading form gives each node the room its label needs.
  var FORM = {
    overview: { dayGap: 150, taskGap: 132, stem: 92, promptGap: 24, promptRun: 5, rows: 2, spread: 168 },
    reading: { dayGap: 240, taskGap: 178, stem: 130, promptGap: 32, promptRun: 5, rows: 2, spread: 250 }
  };

  // Node sizes. A day is a square, a task is a smaller square, a prompt is a
  // circle. The day square grows a little with the work in it, but stays
  // inside a range so the row of them still reads as a row.
  var DAY = { min: 26, max: 46 };
  var TASK = { min: 14, max: 26 };
  var PROMPT_R = 6;

  var MARGIN = 120;

  // How far a sitting's square sits off the spine when its agent has a side of
  // the spine to itself, as a share of the square: past half of it, so the two
  // squares of sittings drawn together never touch, with room to spare for a
  // mark drawn a little larger than the square.
  var LEAN = 0.65;
  var SPINE_Y = 0; // filled in once the tallest column is known

  // A task at or above this counts as hard. The score behind it is a guess, so
  // it only ever changes the colour and never appears as a figure.
  var HARD = 0.5;

  // A day square is 36 units. Legible means about this many pixels for one,
  // which keeps a prompt circle near eleven and comfortably clickable.
  var READABLE = 32 / 36;

  // The other end of the same question. A ceiling on how big a node may be
  // drawn, not on the scale: an absolute cap means something different on a
  // small project, where the whole diagram came to forty pixels across in the
  // middle of a window fourteen hundred wide, geometrically centred and still
  // reading as lost.
  var COMFORTABLE = 72 / 36;

  // wholeScale is the largest scale that still shows every day at once.
  //
  // seen is the box the drawing occupies, r the room available, spine the
  // model's spine or null. It lives here rather than in the page because it is
  // arithmetic over the layout, and the check that guards it can only load
  // what this file exports: the copy that used to sit in the checker asserted
  // against its own restatement of these lines.
  function wholeScale(seen, r, spine) {
    var s = Math.min(COMFORTABLE, r.h / seen.height, r.w / seen.width);

    // The spine runs past the nodes at both ends, further at the arrow. It is
    // not centred on, or the work sits off to one side, but it still has to
    // fit, or the arrow is clipped by the edge of the window.
    if (spine) {
      var mid = seen.x + seen.width / 2;
      var reach = Math.max(mid - (spine.x1 - 13), spine.x2 + 13 - mid) * 2;
      if (reach > 0) s = Math.min(s, r.w / reach);
    }
    return s > 0 ? s : 1;
  }

  // homeScale is where the view opens: legible, but never larger than showing
  // the whole thing, since blowing up a two day history helps nobody.
  function homeScale(seen, r, spine) {
    var all = wholeScale(seen, r, spine);
    if (all >= READABLE) return all;

    // Legible, but still bounded by the height. A ribbon may be scrolled
    // sideways; one taller than the window has nowhere to go.
    var legible = Math.min(READABLE, Math.max(all, r.h / seen.height));

    // Showing the whole diagram is worth having, but never at the cost of the
    // nodes being smaller than they need to be. Where the whole thing fits at
    // the legible scale it is already being shown; where it does not, opening
    // whole means shrinking below legible, and that is the thing being fixed.
    //
    // An earlier version took the whole view whenever it came within a fixed
    // fraction of legible. That fraction ignored how much bigger the nodes
    // would actually be, and it produced a diagram that shrank as the window
    // grew: a twelve day history opened at thirty two pixels on a 1440 screen
    // and thirty on a 1920 one, because the wider screen brought the whole
    // view inside the threshold. A larger window must never give smaller
    // nodes.
    return Math.max(all, legible);
  }

  function clamp(v, lo, hi) {
    return v < lo ? lo : v > hi ? hi : v;
  }

  // size maps a count onto a node size. The square root matters: with linear
  // scaling one busy day flattens every other node to the minimum.
  function size(value, most, range) {
    if (!most) return range.min;
    return Math.round(
      range.min + (range.max - range.min) * Math.sqrt(clamp(value / most, 0, 1))
    );
  }

  function days(a, b) {
    return (new Date(b) - new Date(a)) / 86400000;
  }

  // sides are the sides of the spine a sitting's tasks hang on, above first.
  //
  // One agent's history uses both, as it always has. With two, each agent has
  // one, so sittings of the two that happened together can share a column
  // without their work landing on top of each other. Only two agents are read,
  // so two sides are enough to give each its own.
  function sides(agents, agent) {
    if (agents.length < 2) return [-1, 1];
    return [agents.indexOf(agent) % 2 === 0 ? -1 : 1];
  }

  // overlaps lists, for each sitting, the sittings of another agent that were
  // under way at the same time as it, earliest first. Goals arrive in the
  // order they started, so the search along from each stops at the first that
  // started after it ended.
  //
  // Only another agent's. One agent working in two worktrees at once is how
  // that agent's history has always been drawn, and this answers a different
  // question: what the other agent was doing meanwhile.
  function overlaps(goals) {
    var found = goals.map(function () { return []; });
    for (var i = 0; i < goals.length; i++) {
      var end = new Date(goals[i].stats.end);
      for (var j = i + 1; j < goals.length && new Date(goals[j].stats.start) <= end; j++) {
        if (goals[j].agent === goals[i].agent) continue;
        found[i].push(j);
        found[j].push(i);
      }
    }
    found.forEach(function (list) { list.sort(function (a, b) { return a - b; }); });
    return found;
  }

  // columns numbers the column each sitting is drawn in. A new column starts
  // wherever no two overlapping sittings would sit either side of the break,
  // so every pair that overlapped shares one. With one agent nothing overlaps
  // in this sense, and every sitting has a column of its own, as it always did.
  function columns(goals, pairs) {
    // The earliest sitting at or after each one that reaches back before it.
    var reach = new Array(goals.length);
    var earliest = Infinity;
    for (var j = goals.length - 1; j >= 0; j--) {
      earliest = Math.min(earliest, j, pairs[j].length ? pairs[j][0] : j);
      reach[j] = earliest;
    }
    var out = new Array(goals.length);
    var n = -1;
    for (var i = 0; i < goals.length; i++) {
      if (reach[i] >= i) n++;
      out[i] = n;
    }
    return out;
  }

  // build returns everything needed to draw one diagram.
  function build(graph, mode) {
    var form = FORM[mode] || FORM.overview;
    var goals = graph.goals || [];
    if (!goals.length) {
      return { width: 0, height: 0, spineY: 0, spine: null, days: [], links: [] };
    }

    var mostDayEdits = 0;
    var mostTaskEdits = 0;
    goals.forEach(function (goal) {
      mostDayEdits = Math.max(mostDayEdits, goal.stats.edits || 0);
      goal.tasks.forEach(function (task) {
        mostTaskEdits = Math.max(mostTaskEdits, task.stats.edits || 0);
      });
    });

    var agents = (graph.project && graph.project.agents) || [];
    var pairs = overlaps(goals);
    var column = columns(goals, pairs);

    // A pure time axis collides, because two sittings on the same day land on
    // top of each other. Time decides the pause, content decides the room,
    // and the column always gets at least what it needs.
    function pause(from, to) {
      var apart = Math.max(0, days(from, to));
      return form.dayGap * (0.55 + 0.45 * Math.sqrt(clamp(apart / 3, 0, 1)));
    }

    // Lay each day out around a spine at y = 0 first, then shift the whole
    // thing down once we know how far the tallest column reached upward.
    var laid = [];
    var frontier = MARGIN;
    var start = MARGIN;
    var lanes = {};
    var previousEnd = null;
    var above = 0;
    var below = 0;

    goals.forEach(function (goal, n) {
      var hang = sides(agents, goal.agent);
      var lane = hang.join(",");

      // A new column starts clear of everything drawn so far. Within one, each
      // side of the spine runs on from its own last sitting, and a sitting
      // that overlapped one already drawn starts no earlier than that one did,
      // which puts the two in line.
      if (column[n] !== column[n - 1]) {
        if (previousEnd) frontier += pause(previousEnd, goal.stats.start);
        start = frontier;
        lanes = {};
      }
      var x = lanes[lane] ? lanes[lane].end + pause(lanes[lane].last, goal.stats.start) : start;
      var earlier = pairs[n].filter(function (k) { return k < n; });
      if (earlier.length) x = Math.max(x, laid[earlier[0]].x);

      var square = size(goal.stats.edits || 0, mostDayEdits, DAY);
      // Off the spine towards its tasks when they hang on one side only, and
      // on it when they hang on both.
      var lean = hang.reduce(function (a, b) { return a + b; }, 0) / hang.length;

      var day = {
        id: goal.id,
        goal: goal,
        kind: "day",
        x: Math.round(x),
        y: SPINE_Y + lean * Math.round(square * LEAN + 2),
        size: square,
        lean: lean,
        lane: lane,
        column: column[n],
        overlaps: pairs[n].map(function (k) { return goals[k].id; }),
        tasks: []
      };

      // Tasks take the sides in turn, so a busy day grows in both directions
      // rather than into a tall stack on one side.
      var ranks = {};
      var reach = 0;

      goal.tasks.forEach(function (task, i) {
        var side = hang[i % hang.length];
        var upward = side < 0;
        var rank = ranks[side] || 0;
        ranks[side] = rank + 1;
        var box = size(task.stats.edits || 0, mostTaskEdits, TASK);

        // A busy day spreads sideways once it has stacked a couple of rows.
        // Without this one ten-task day sets the height of the whole canvas
        // and every quiet day around it is drawn tiny to fit.
        var row = rank % form.rows;
        var column = Math.floor(rank / form.rows);

        var node = {
          id: task.id,
          task: task,
          kind: "task",
          hard: (task.stats.struggle || 0) >= HARD,
          // Whether the work landed. Unlike hard, this is not a guess.
          shipped: (task.stats.commits || []).length > 0,
          side: side,
          x: day.x + column * form.spread,
          y: SPINE_Y + side * (form.stem + row * form.taskGap),
          size: box,
          prompts: []
        };

        // Prompts run outward from their task in a short column, wrapping into
        // a second and third file rather than growing without limit.
        var turns = task.turns || [];
        for (var p = 0; p < turns.length; p++) {
          var file = Math.floor(p / form.promptRun);
          var place = p % form.promptRun;
          var run = Math.min(form.promptRun, turns.length - file * form.promptRun);
          node.prompts.push({
            id: task.id + ".p" + (p + 1),
            turn: turns[p],
            index: p,
            kind: "prompt",
            // Centre each file of prompts on the task, so a task with two
            // prompts is not lopsided against one with five.
            x: node.x + (place - (run - 1) / 2) * form.promptGap,
            y: node.y + side * (form.promptGap + file * form.promptGap),
            r: PROMPT_R
          });
        }

        var depth = Math.abs(node.y - SPINE_Y) +
          Math.ceil(turns.length / form.promptRun) * form.promptGap + PROMPT_R + 30;
        if (upward) above = Math.max(above, depth);
        else below = Math.max(below, depth);

        // A spread day, or a wide file of prompts, sticks out past the column
        // the day started in, and the next day has to clear it.
        var half = ((Math.min(form.promptRun, turns.length) - 1) / 2) * form.promptGap;
        reach = Math.max(reach, column * form.spread + half + PROMPT_R);

        day.tasks.push(node);
      });

      day.reach = reach;
      laid.push(day);
      lanes[lane] = { end: x + reach, last: goal.stats.end };
      frontier = Math.max(frontier, x + reach);
      previousEnd = goal.stats.end;
    });

    // Now that the extremes are known, drop everything so the topmost node
    // sits just under the margin.
    var shift = above + MARGIN;
    laid.forEach(function (day) {
      day.y += shift;
      day.tasks.forEach(function (task) {
        task.y += shift;
        task.prompts.forEach(function (p) { p.y += shift; });
      });
    });

    // The drawing ends where its furthest day's work does, which with two
    // agents need not be the day drawn last.
    var right = Math.max.apply(null, laid.map(function (day) { return day.x + day.reach; }));
    return {
      width: right + MARGIN,
      height: shift + below + MARGIN,
      spineY: shift,
      spine: { x1: MARGIN / 2, x2: right + MARGIN / 2, y: shift },
      days: laid,
      links: links(graph, laid, shift, above),
      agents: agents,
      hard: HARD
    };
  }

  // links join days that came back to the same files. They bow away from the
  // spine so they never read as part of the structure.
  function links(graph, laid, shift, above) {
    var at = {};
    laid.forEach(function (day) { at[day.id] = day; });

    // Well clear of the highest node, so a link never crosses the work.
    var lift = shift - above - MARGIN * 0.35;

    return (graph.links || []).map(function (link) {
      var from = at[link.from];
      var to = at[link.to];
      if (!from || !to) return null;
      return {
        from: link.from,
        to: link.to,
        weight: link.weight,
        files: link.files,
        path: "M" + from.x + "," + from.y +
          " C" + from.x + "," + lift + " " + to.x + "," + lift + " " + to.x + "," + to.y
      };
    }).filter(Boolean);
  }

  root.BoughLayout = {
    build: build,
    HARD: HARD,
    READABLE: READABLE,
    COMFORTABLE: COMFORTABLE,
    wholeScale: wholeScale,
    homeScale: homeScale
  };
})(typeof module !== "undefined" && module.exports ? module.exports : window);
