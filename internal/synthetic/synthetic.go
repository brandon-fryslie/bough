// Package synthetic builds project histories nobody lived, at whatever size a
// check needs.
//
// Real history cannot be committed, since it holds whatever somebody typed,
// yet the layout check and the performance runs both need graphs of a known
// shape that anyone can rebuild. Everything here is arithmetic on the shape,
// with no clock and no randomness, so one shape is always the same bytes.
package synthetic

import (
	"fmt"
	"strconv"
	"time"

	"github.com/nickelsec/bough/internal/graph"
)

// Shape is the size of a history. Outside this package it is only made by
// NewShape, so a Shape in hand always describes a history that can exist. The
// zero Shape is the empty history.
type Shape struct {
	sittings int
	tasks    int
	prompts  int
	links    int
}

// NewShape turns counts into a Shape, refusing any no history could have.
//
// sittings is how many days hold work, tasks how many tasks each of them holds,
// prompts the most prompts a single task holds, and links how many pairs of
// sittings came back to the same files.
func NewShape(sittings, tasks, prompts, links int) (Shape, error) {
	switch {
	case sittings < 0 || tasks < 0 || links < 0:
		return Shape{}, fmt.Errorf(
			"a history cannot hold a negative count: %d sittings, %d tasks, %d links",
			sittings, tasks, links)
	case prompts < 1:
		return Shape{}, fmt.Errorf(
			"a task is at least one prompt, so the most a task holds cannot be %d", prompts)
	case links > pairs(sittings):
		return Shape{}, fmt.Errorf(
			"%d sittings make %d pairs to link, which is fewer than %d links",
			sittings, pairs(sittings), links)
	}
	return Shape{sittings: sittings, tasks: tasks, prompts: prompts, links: links}, nil
}

func pairs(sittings int) int {
	return sittings * (sittings - 1) / 2
}

// sitting is how long every sitting lasts. Its prompts are spread across it.
const sitting = 4 * time.Hour

// History builds the graph a Shape describes.
//
// Sittings fall two days apart. Tasks within a sitting grow in weight, every
// third is hard so the crooked path is drawn, and every fourth ended in a
// commit. Task i holds i%prompts + 1 prompts, so a sitting's tasks range from
// one prompt up to the most the shape allows.
func History(s Shape) graph.Graph {
	start := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	g := graph.Graph{Schema: graph.SchemaVersion, Project: graph.Project{Name: "synthetic"}}

	for n := 0; n < s.sittings; n++ {
		day := start.AddDate(0, 0, n*2)
		goal := graph.Goal{
			ID:     "g" + strconv.Itoa(n+1),
			Label:  "a sitting",
			Period: day.Format("Mon 2 Jan"),
			Stats: graph.Stats{
				Start: day, End: day.Add(sitting),
				Turns: 10 * (n + 1), Edits: 12 * (n + 1),
			},
		}
		clock := prompter{day: day, total: s.promptsPerSitting()}
		for i := 0; i < s.tasks; i++ {
			goal.Tasks = append(goal.Tasks, task(goal.ID, i, s.prompts, &clock))
		}
		g.Goals = append(g.Goals, goal)
	}

	g.Links = links(s)
	return g
}

// promptsPerSitting is how many prompts every sitting holds in all.
func (s Shape) promptsPerSitting() int {
	total := 0
	for i := 0; i < s.tasks; i++ {
		total += i%s.prompts + 1
	}
	return total
}

// prompter hands out a sitting's prompt times in order, evenly spaced from its
// start to just before its end, so no prompt lands outside the sitting it
// belongs to however many the sitting holds.
type prompter struct {
	day   time.Time
	total int
	next  int
}

func (c *prompter) at() time.Time {
	t := c.day.Add(sitting * time.Duration(c.next) / time.Duration(c.total))
	c.next++
	return t
}

func task(goalID string, i, prompts int, clock *prompter) graph.Task {
	turns := make([]graph.Turn, i%prompts+1)
	for p := range turns {
		turns[p] = graph.Turn{At: clock.at(), Text: "a prompt"}
	}

	stats := graph.Stats{
		Turns: len(turns), Edits: i * 4,
		Struggle: map[bool]float64{true: 0.7, false: 0.2}[i%3 == 0],
	}
	if i%4 == 3 {
		stats.Commits = []graph.Commit{{SHA: fmt.Sprintf("%040x", i+1)}}
	}

	return graph.Task{
		ID:    goalID + ".t" + strconv.Itoa(i+1),
		Label: "a piece of work",
		Stats: stats,
		Turns: turns,
	}
}

// links chooses which pairs of sittings share files.
//
// Taking pairs in order would hang every link off the first sitting, while a
// real project comes back to a file after a day and after a month alike. So
// the pairs are numbered in order, the first sitting's first, and link k takes
// pair k*step modulo the number of pairs. A step sharing no factor with that
// number never takes one pair twice, and a step near five eighths of it puts
// each link far from the one before, mixing short spans with long ones.
//
// Nothing is built for the pairs no link takes, so the cost follows the links
// asked for rather than the square of the sittings.
func links(s Shape) []graph.Link {
	out := make([]graph.Link, s.links)
	total := pairs(s.sittings)
	step := scatter(total)
	for k := range out {
		from, to := unrank(k*step%total, s.sittings)
		out[k] = graph.Link{
			From:   "g" + strconv.Itoa(from+1),
			To:     "g" + strconv.Itoa(to+1),
			Files:  []string{"src/shared" + strconv.Itoa(k+1) + ".go"},
			Weight: k%5 + 1,
		}
	}
	return out
}

// scatter is the step links take through the pairs: the first whole number
// from five eighths of total upward that shares no factor with it.
func scatter(total int) int {
	step := max(total*5/8, 1)
	for gcd(step, total) != 1 {
		step++
	}
	return step
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// unrank turns a pair's number back into its two sittings, stepping past the
// pairs each earlier sitting starts.
func unrank(index, sittings int) (from, to int) {
	for from = 0; index >= sittings-1-from; from++ {
		index -= sittings - 1 - from
	}
	return from, from + 1 + index
}
