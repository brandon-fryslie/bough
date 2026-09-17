package synthetic

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/graph"
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

// The page shows a sitting's counts beside its tasks', so they have to agree,
// and a commit counted in the totals twice would be a commit that never was.
func TestSittingsAndTotalsAgreeWithTheirTasks(t *testing.T) {
	g := History(mustShape(t, 3, 9, 4, 0))
	totalTurns, shas := 0, map[string]bool{}
	for _, goal := range g.Goals {
		turns, commits := 0, 0
		for _, task := range goal.Tasks {
			turns += len(task.Turns)
			commits += len(task.Stats.Commits)
		}
		if goal.Stats.Turns != turns || len(goal.Stats.Commits) != commits {
			t.Errorf("%s counts %d prompts and %d commits; its tasks hold %d and %d",
				goal.ID, goal.Stats.Turns, len(goal.Stats.Commits), turns, commits)
		}
		totalTurns += turns
		for _, c := range goal.Stats.Commits {
			shas[c.SHA] = true
		}
	}
	if g.Totals.Turns != totalTurns {
		t.Errorf("totals count %d prompts, the sittings %d", g.Totals.Turns, totalTurns)
	}
	if len(g.Totals.Commits) != len(shas) {
		t.Errorf("totals hold %d commits but only %d distinct ones", len(g.Totals.Commits), len(shas))
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

// A prompt shown at a time outside its sitting contradicts the page it is
// shown on, which a busy shape used to do once a sitting passed 24 tasks.
//
// The second shape holds over 640 thousand prompts in one sitting, past which
// multiplying the sitting's length before dividing overflowed a Duration and
// sent the later prompts back before the sitting began.
func TestPromptsFallInsideTheirSittingInOrder(t *testing.T) {
	for _, s := range []Shape{mustShape(t, 2, 40, 15, 0), mustShape(t, 1, 1200, 1200, 0)} {
		promptsInOrder(t, History(s))
	}
}

func promptsInOrder(t *testing.T, g graph.Graph) {
	t.Helper()
	for _, goal := range g.Goals {
		var last time.Time
		for _, task := range goal.Tasks {
			for _, turn := range task.Turns {
				if turn.At.Before(goal.Stats.Start) || !turn.At.Before(goal.Stats.End) {
					t.Fatalf("%s has a prompt at %s, outside %s to %s",
						task.ID, turn.At, goal.Stats.Start, goal.Stats.End)
				}
				if !turn.At.After(last) {
					t.Fatalf("%s has a prompt at %s, not after the one before at %s", task.ID, turn.At, last)
				}
				last = turn.At
			}
		}
	}
}

// The pairs no link takes are never built, so a long history asking for a few
// links costs what those links cost. Building every pair of twenty thousand
// sittings first would take hundreds of millions of them.
func TestFewLinksOnALongHistoryStayCheap(t *testing.T) {
	g := History(mustShape(t, 20000, 1, 1, 50))
	if len(g.Links) != 50 {
		t.Errorf("%d links, want 50", len(g.Links))
	}
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

// Two agents on the same days: a sitting of each on every day, overlapping on
// every other one, the whole history in the order its sittings started and
// every sitting saying whose it is.
func TestAlongsideIsTwoAgentsOnTheSameDays(t *testing.T) {
	g := Alongside(mustShape(t, 4, 3, 2, 3))
	if want := []string{firstAgent, secondAgent}; len(g.Project.Agents) != 2 ||
		g.Project.Agents[0] != want[0] || g.Project.Agents[1] != want[1] {
		t.Errorf("agents = %q, want %q", g.Project.Agents, want)
	}
	if len(g.Goals) != 8 {
		t.Fatalf("%d sittings, want 8", len(g.Goals))
	}
	for i := 0; i < len(g.Goals); i += 2 {
		a, b := g.Goals[i], g.Goals[i+1]
		if a.Agent != firstAgent || b.Agent != secondAgent {
			t.Errorf("day %d holds sittings of %q and %q", i/2, a.Agent, b.Agent)
		}
		if !a.Stats.Start.Before(b.Stats.Start) {
			t.Errorf("day %d: the second agent's sitting starts first", i/2)
		}
		if overlap := b.Stats.Start.Before(a.Stats.End); overlap != (i/2%2 == 0) {
			t.Errorf("day %d: overlap is %v", i/2, overlap)
		}
	}
	for _, link := range g.Links {
		if number(link.From)%2 != 1 || number(link.To)%2 != 1 {
			t.Errorf("link %s to %s does not join the first agent's sittings", link.From, link.To)
		}
	}
	promptsInOrder(t, g)
}

// Every sitting and commit of the doubled history is its own.
func TestAlongsideNamesEverythingOnce(t *testing.T) {
	g := Alongside(mustShape(t, 3, 8, 2, 0))
	ids, shas := map[string]bool{}, map[string]bool{}
	for _, goal := range g.Goals {
		ids[goal.ID] = true
		for _, c := range goal.Stats.Commits {
			shas[c.SHA] = true
		}
	}
	if len(ids) != len(g.Goals) {
		t.Errorf("%d sittings share %d ids", len(g.Goals), len(ids))
	}
	if len(shas) != len(g.Totals.Commits) {
		t.Errorf("%d commits share %d hashes", len(g.Totals.Commits), len(shas))
	}
}

// Every size a measurement can ask for by name is a history NewShape would
// have made, with no two sizes the same.
func TestEveryNamedSizeIsAShapeThatCanExist(t *testing.T) {
	seen := map[Shape]string{}
	for _, name := range SizeNames() {
		got, err := Sized(name)
		if err != nil {
			t.Fatal(err)
		}
		want, err := NewShape(got.sittings, got.tasks, got.prompts, got.links)
		if err != nil || got != want {
			t.Errorf("%s is %+v, which NewShape makes as %+v, %v", name, got, want, err)
		}
		if other, ok := seen[got]; ok {
			t.Errorf("%s and %s are the same size", name, other)
		}
		seen[got] = name
	}
	if _, err := Sized("huge"); err == nil || !strings.Contains(err.Error(), "small, medium, large") {
		t.Errorf("an unknown size failed with %v, want the sizes there are", err)
	}
}
