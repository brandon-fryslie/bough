package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nickelsec/bough/internal/graph"
	"github.com/nickelsec/bough/internal/synthetic"
)

// The layout is arithmetic over the graph, so it is checked like arithmetic
// rather than by looking at it. A tree that tangles on somebody else's history
// is the main risk in the drawing, and it is catchable here.
//
// Node runs the check because the layout has to be the same code the browser
// uses. Reimplementing it in Go would test a copy rather than the thing.
func TestLayoutHoldsUp(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not available to run the layout")
	}

	dir := t.TempDir()
	graphs := writeGraphs(t, dir)
	script := filepath.Join(dir, "check.js")
	if err := os.WriteFile(script, []byte(layoutCheck), 0o644); err != nil {
		t.Fatal(err)
	}

	args := append([]string{script, "layout.js"}, graphs...)
	out, err := exec.Command("node", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("layout checks failed:\n%s", out)
	}
	t.Logf("\n%s", out)
}

// writeGraphs produces the shapes worth checking: a busy project, a quiet one,
// one with a single sitting, one with no links at all, and two agents working
// the same days.
func writeGraphs(t *testing.T, dir string) []string {
	t.Helper()
	var paths []string

	cases := map[string]graph.Graph{
		"busy":   shaped(t, 10, 11),
		"quiet":  shaped(t, 2, 1),
		"single": shaped(t, 1, 4),
		// Around three times wider than tall. Long enough not to fit at a
		// legible scale on a 1440 screen, short enough to fit whole on a 1920
		// one, which is the shape that used to shrink as the window grew. The
		// busy fixture is eight times wider and never comes close.
		"middling": shaped(t, 12, 3),
		// One sitting, one task. Small enough in both directions that the
		// ceiling on node size is what limits the opening view, rather than
		// the width or the height of the window. Nothing else here reaches
		// it: the next smallest is bounded by its height at 1.94 against a
		// ceiling of 2, so a change to that ceiling went unnoticed.
		"tiny": shaped(t, 1, 1),
		// Two agents on the same days: on every other day a sitting of each
		// ran at the same time, and on the rest one followed the other.
		"together": alongside(t, 6, 3),
		// And busy enough that each side of the spine spreads sideways.
		"together-busy": alongside(t, 4, 9),
	}
	for name, g := range cases {
		body, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name+".json")
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

// shaped is a synthetic history whose tasks hold one more prompt each, from
// one up to as many as the sitting has tasks.
func shaped(t *testing.T, sittings, tasks int) graph.Graph {
	t.Helper()
	s, err := synthetic.NewShape(sittings, tasks, tasks, 0)
	if err != nil {
		t.Fatal(err)
	}
	return synthetic.History(s)
}

// alongside is shaped's history with a second agent's sitting beside each one.
func alongside(t *testing.T, sittings, tasks int) graph.Graph {
	t.Helper()
	s, err := synthetic.NewShape(sittings, tasks, tasks, 0)
	if err != nil {
		t.Fatal(err)
	}
	return synthetic.Alongside(s)
}
