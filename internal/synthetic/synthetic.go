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
	"sort"
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
				Start: day, End: day.Add(4 * time.Hour),
				Turns: 10 * (n + 1), Edits: 12 * (n + 1),
			},
		}
		for i := 0; i < s.tasks; i++ {
			goal.Tasks = append(goal.Tasks, task(goal.ID, day, i, s.prompts))
		}
		g.Goals = append(g.Goals, goal)
	}

	g.Links = links(s)
	return g
}

func task(goalID string, day time.Time, i, prompts int) graph.Task {
	turns := make([]graph.Turn, i%prompts+1)
	for p := range turns {
		turns[p] = graph.Turn{At: day.Add(time.Duration(i*10+p) * time.Minute), Text: "a prompt"}
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
// the pairs are taken in a fixed scramble: each pair's position is multiplied
// by an odd constant modulo 2^64, which reorders the positions without two
// ever landing on the same key.
func links(s Shape) []graph.Link {
	type pair struct {
		from, to int
		key      uint64
	}
	all := make([]pair, 0, pairs(s.sittings))
	var position uint64
	for from := 0; from < s.sittings; from++ {
		for to := from + 1; to < s.sittings; to++ {
			all = append(all, pair{from: from, to: to, key: position * 0x9E3779B97F4A7C15})
			position++
		}
	}
	sort.Slice(all, func(a, b int) bool { return all[a].key < all[b].key })

	out := make([]graph.Link, s.links)
	for k, p := range all[:s.links] {
		out[k] = graph.Link{
			From:   "g" + strconv.Itoa(p.from+1),
			To:     "g" + strconv.Itoa(p.to+1),
			Files:  []string{"src/shared" + strconv.Itoa(k+1) + ".go"},
			Weight: k%5 + 1,
		}
	}
	return out
}
