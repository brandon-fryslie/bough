// Package portfolio is every project family at once, summarised.
//
// A view of all the projects somebody works in cannot be a hundred graphs.
// Those carry every prompt in full, and on the history this was built against
// reading and building all 124 families took 71 s one at a time, eight at a
// time 13 s at around 430 MB of heap (measured 2026-09-14). So this is the
// smaller document such a view reads instead: a line for each family, and for
// each of its sittings the handful of numbers a timeline needs.
//
// No prompt text, and, as in graph, nothing about how any of it might be
// drawn. A sitting here is a reference rather than a copy: it carries the id
// the family's own graph gave it, so a reader that wants the work itself asks
// for that family and finds the sitting under the same name.
//
// A family arrives in two parts, because a view of everything has to draw
// before it has read everything. Known answers from what detection and the
// family resolver already know, with no history read at all. Read or Unread
// then says what came of reading it.
package portfolio

import (
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

	// Commits is how many were made, not which. The hashes and their subjects
	// are the family's graph to give.
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
		Key:         p.Key(),
		Name:        p.Name(),
		Path:        p.Path,
		Agents:      p.Agents(),
		Directories: p.Directories(),
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
func (f Family) Unread(err error) Family {
	f.Unreadable = err.Error()
	return f
}
