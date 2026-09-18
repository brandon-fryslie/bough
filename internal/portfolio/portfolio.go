// Package portfolio is every project family at once, summarised.
//
// A view of all the projects somebody works in cannot be a hundred graphs.
// Those carry every prompt in full, and on the history this was built against
// reading and building all 124 families took 71 s one at a time, eight at a
// time 13 s at around 430 MB of heap (measured 2026-09-14). So this is the
// smaller document such a view reads instead: a line for each family, and for
// each of its sittings the handful of numbers a timeline needs.
//
// No prompt bodies, and, as in graph, nothing about how any of it might be
// drawn. A sitting's label is the exception worth naming: it is text somebody
// already wrote, so the opening of a prompt reaches this document by design.
// It is carried exactly as the family's graph spells it rather than shortened
// again here, since one sitting labelled two ways in two documents is worse
// than a long label.
//
// A sitting is otherwise a reference rather than a copy: it carries the id the
// family's own graph gave it, so a reader that wants the work itself asks for
// that family and finds the sitting under the same name.
//
// A family arrives in two parts, because a view of everything has to draw
// before it has read everything. Known answers from what detection and the
// family resolver already know, with no history read at all. Read or Unread
// then says what came of reading it.
package portfolio

import (
	"fmt"
	"strings"
	"time"

	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/graph"
)

// SchemaVersion is bumped when the shape below changes in a way that would
// break a reader. Consumers should check it and refuse politely rather than
// misread newer output.
//
// It is this document's own and moves independently of graph.SchemaVersion.
// The two describe different things, and tying them together would mean every
// change to a prompt's fields claimed the portfolio had changed too.
const SchemaVersion = 1

// Portfolio is every family with history, summarised.
type Portfolio struct {
	Schema int `json:"schema"`

	// Generated is when this was produced, which matters because history keeps
	// growing and two portfolios of the same machine will differ.
	Generated time.Time `json:"generated"`

	// Tool is the version of bough that produced this.
	Tool string `json:"tool,omitempty"`

	// Families are in the order they were gathered, which is by name and then
	// by path, so two runs over an unchanged machine agree.
	Families []Family `json:"families"`
}

// Family is one project family: what is known of it, and its sittings.
type Family struct {
	// Key identifies the family among all of them, the same form
	// family.Project.Key compares on, so a reader holding this can ask for
	// that family by name.
	Key string `json:"key"`

	Name string `json:"name"`

	// Path is the directory the family is known by.
	Path string `json:"path"`

	// Agents are the IDs of the agents with history in the family, each once
	// and in the order IDs sort.
	Agents []string `json:"agents"`

	// Directories are every directory with history that belongs to the family.
	// A family worked in one place has one.
	Directories []string `json:"directories"`

	// LastWorked is when any of the family's history was last added to. It is
	// zero when nothing recorded a time.
	LastWorked time.Time `json:"lastWorked"`

	// Unreadable is why none of the family's history could be read, when none
	// of it could. Empty when it was read, in which case Sittings is what was
	// found, which may be nothing.
	//
	// Without this the two are the same document. A family whose transcripts
	// will not open and a family that has no work in it both arrive with no
	// sittings, and a view that cannot tell them apart draws an empty lane
	// over work that is really there.
	Unreadable string `json:"unreadable,omitempty"`

	// Sittings are the family's, in the order its graph holds them, which is
	// the order the work happened.
	//
	// Every one that could be read. Where some of a family's history failed
	// and the rest loaded, the sittings are what loaded, and this document
	// does not yet say so: the terminal reports it and nothing carries it
	// here. Until it does, a reader cannot take the absence of a sitting as
	// proof the work never happened.
	Sittings []Sitting `json:"sittings"`
}

// Sitting is one stretch of work, small enough that every family's can be held
// at once.
type Sitting struct {
	// ID is the id the family's own graph gave this sitting. It is that
	// graph's, not this document's, so a reader can carry it back.
	ID string `json:"id"`

	// Agent is the ID of the agent whose session this came from.
	Agent string `json:"agent"`

	// Label is text somebody already wrote, never anything invented. Empty
	// when there was nothing honest to use.
	Label string `json:"label,omitempty"`

	Start time.Time `json:"start"`
	End   time.Time `json:"end"`

	// ActiveMinutes leaves out the breaks and is closer to time actually
	// worked. The span is not carried: Start and End already say it.
	ActiveMinutes int `json:"activeMinutes"`

	// Prompts is how many turns the sitting took.
	Prompts int `json:"prompts"`

	Edits int `json:"edits"`

	// Commits is how many the sitting made in this family, not which: the
	// hashes and their subjects are the family's graph to give.
	//
	// In this family, because that is what the graph counts here. A sitting
	// that committed in another family has those commits recorded against
	// that family in the graph's own elsewhere, and neither project owns the
	// other, so neither row claims the other's work.
	Commits int `json:"commits"`

	// Struggle rates how hard the work looked, from 0 to 1, and carries the
	// same warning it does in the graph: it is a heuristic nobody has checked
	// against their own memory of the work.
	Struggle float64 `json:"struggle"`
}

// New is the document holding these families, in the order given.
func New(tool string, generated time.Time, families []Family) Portfolio {
	return Portfolio{
		Schema:    SchemaVersion,
		Generated: generated.UTC(),
		Tool:      tool,
		// Copied, and never nil: the document is a value a caller can hold on
		// to while it keeps building, and a reader gets an empty list rather
		// than a null on a machine whose every family failed to appear.
		Families: append([]Family{}, families...),
	}
}

// Known is a family's entry before any of its history has been read: what
// detection and the family resolver have already answered, and no sittings.
//
// This is what makes a view that fills in as it reads possible. Every field
// here is available the moment the families are resolved, so the shape of the
// whole portfolio can be drawn before the first transcript is opened.
//
// [LAW:one-source-of-truth] All of it comes from the family, none of it from
// the graph that Read brings later, so the entry drawn before the history
// arrives is the same entry that is there afterwards.
func Known(p family.Project) Family {
	return Family{
		Key:  p.Key(),
		Name: p.Name(),
		Path: p.Path,
		// Copied empty rather than left nil, as the lists above and below
		// are: a family carrying no agents is one with an empty list of
		// them, and a reader that has to tell null from [] is reading two
		// spellings of the same nothing.
		Agents:      append([]string{}, p.Agents()...),
		Directories: append([]string{}, p.Directories()...),
		LastWorked:  p.LastWorked(),
		Sittings:    []Sitting{},
	}
}

// Read is what f becomes once its history is read: one sitting for each of the
// graph's goals, in the graph's order and under the graph's own ids.
//
// [LAW:one-source-of-truth] The sittings are projected from the graph rather
// than worked out a second way from the sessions. Divide the history twice and
// the two answers drift, and the id a reader carries back stops naming the
// sitting it was given.
func (f Family) Read(g graph.Graph) Family {
	f.Sittings = make([]Sitting, len(g.Goals))
	for i, goal := range g.Goals {
		f.Sittings[i] = Sitting{
			ID:            goal.ID,
			Agent:         goal.Agent,
			Label:         goal.Label,
			Start:         goal.Stats.Start,
			End:           goal.Stats.End,
			ActiveMinutes: goal.Stats.ActiveMinutes,
			Prompts:       goal.Stats.Turns,
			Edits:         goal.Stats.Edits,
			Commits:       len(goal.Stats.Commits),
			Struggle:      goal.Stats.Struggle,
		}
	}
	return f
}

// Unread is what f becomes when none of its history could be read. err is the
// reason, and it is the only account of it a reader of this document gets, so
// it has to say something a person can act on.
//
// The opening of it. A failed read names every transcript it could not open,
// and a family with hundreds of corrupt ones would put hundreds of lines into
// the document whose whole premise is being the small one. What is left out is
// counted rather than dropped quietly, and running bough at that project
// prints the whole of it.
func (f Family) Unread(err error) Family {
	f.Unreadable = opening(err.Error(), reasonLines)
	return f
}

// reasonLines is how much of a failure the document carries: enough to name
// the family and the first causes.
const reasonLines = 5

// opening is s cut to at most n lines, saying how many it left.
func opening(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n... and %d more", len(lines)-n)
}
