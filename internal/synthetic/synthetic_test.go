package synthetic

import (
	"bytes"
	"encoding/json"
	"testing"
)

func mustShape(t *testing.T, sittings, tasks, prompts, links int) Shape {
	t.Helper()
	s, err := NewShape(sittings, tasks, prompts, links)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A measurement compared across two runs, or a layout failure reported on one
// machine and chased on another, only means something if both built the same
// graph.
func TestOneShapeIsAlwaysTheSameBytes(t *testing.T) {
	s := mustShape(t, 30, 5, 4, 40)
	first, err := json.Marshal(History(s))
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(History(s))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("the same shape built two different graphs")
	}
}

func TestHistoryHasTheShapeAskedFor(t *testing.T) {
	g := History(mustShape(t, 5, 4, 3, 6))

	if len(g.Goals) != 5 {
		t.Fatalf("%d sittings, want 5", len(g.Goals))
	}
	ids := map[string]int{}
	for n, goal := range g.Goals {
		ids[goal.ID] = n
		if len(goal.Tasks) != 4 {
			t.Errorf("%s holds %d tasks, want 4", goal.ID, len(goal.Tasks))
		}
		for i, task := range goal.Tasks {
			if want := []int{1, 2, 3, 1}[i]; len(task.Turns) != want {
				t.Errorf("%s holds %d prompts, want %d", task.ID, len(task.Turns), want)
			}
		}
	}

	if len(g.Links) != 6 {
		t.Fatalf("%d links, want 6", len(g.Links))
	}
	seen := map[[2]string]bool{}
	for _, link := range g.Links {
		from, okFrom := ids[link.From]
		to, okTo := ids[link.To]
		if !okFrom || !okTo {
			t.Errorf("link %s to %s names a sitting that does not exist", link.From, link.To)
			continue
		}
		if from >= to {
			t.Errorf("link %s to %s runs backwards in time", link.From, link.To)
		}
		key := [2]string{link.From, link.To}
		if seen[key] {
			t.Errorf("link %s to %s appears twice", link.From, link.To)
		}
		seen[key] = true
	}
}

// Every pair a shape allows can be linked, which is where a scramble that
// collided would lose one.
func TestEveryPairCanBeLinked(t *testing.T) {
	g := History(mustShape(t, 9, 1, 1, 36))
	seen := map[[2]string]bool{}
	for _, link := range g.Links {
		seen[[2]string{link.From, link.To}] = true
	}
	if len(seen) != 36 {
		t.Errorf("%d distinct pairs linked, want all 36", len(seen))
	}
}

// Links drawn in pair order would all start on the first sitting and cost the
// browser less than the long, crossing arcs of real history.
func TestLinksSpreadAcrossTheHistory(t *testing.T) {
	g := History(mustShape(t, 40, 1, 1, 30))
	starts := map[string]bool{}
	spans := map[int]bool{}
	for _, link := range g.Links {
		starts[link.From] = true
		spans[number(link.To)-number(link.From)] = true
	}
	if len(starts) < 10 {
		t.Errorf("links start on only %d sittings", len(starts))
	}
	if len(spans) < 10 {
		t.Errorf("links cover only %d different spans", len(spans))
	}
}

func number(id string) int {
	n := 0
	for _, c := range id[1:] {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestTheZeroShapeIsTheEmptyHistory(t *testing.T) {
	g := History(Shape{})
	if len(g.Goals) != 0 || len(g.Links) != 0 {
		t.Errorf("the zero shape built %d sittings and %d links", len(g.Goals), len(g.Links))
	}
}

func TestNewShapeRefusesHistoriesThatCannotExist(t *testing.T) {
	for _, c := range []struct {
		name                            string
		sittings, tasks, prompts, links int
	}{
		{"negative sittings", -1, 1, 1, 0},
		{"negative tasks", 1, -1, 1, 0},
		{"negative links", 2, 1, 1, -1},
		{"a task with no prompts", 1, 1, 0, 0},
		{"more links than pairs", 3, 1, 1, 4},
		{"a link with one sitting", 1, 1, 1, 1},
	} {
		if _, err := NewShape(c.sittings, c.tasks, c.prompts, c.links); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}
