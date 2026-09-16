// Package transcript decides what an agent's recorded work means once that
// agent's own format has been decoded.
//
// Each agent package knows how its transcript is laid out: where a command
// sits, where its result sits, how a failure is marked. None of that is here.
// What is here is what follows once those have been read, and does not depend
// on which agent wrote them: which calls were commits, which result settles
// which call, what the commit was, how much a diff changed, and which day a
// timestamp belongs to.
//
// Claude and Codex each carried their own copy of this, and the copies drifted
// apart in ways nobody chose: one read a commit's directory from where the
// command ran and the other did not, and one forgot a commit whose result
// arrived after the next prompt. Where agents really do record different
// things, that difference arrives here as a field on Call or Result, not as a
// branch in a copy.
package transcript

import (
	"cmp"
	"time"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/agent/shell"
)

// Call is a shell command an agent issued, as that agent's format recorded it.
type Call struct {
	// Turn is the index of the turn the call was made in.
	Turn int

	// ID is what the call's result names to say which call it answers.
	ID string

	Command string

	// Workdir is the directory the agent recorded the command running in, or
	// "" when its format records none. Codex writes one; Claude Code's shell
	// tool does not.
	Workdir string
}

// Result is what came back for a call.
type Result struct {
	CallID string
	At     time.Time

	// Failed marks a result the agent recorded as an error.
	Failed bool

	// Reported is what the transcript itself says git did.
	Reported Reported
}

// Reported is a commit as the transcript itself described it, read from
// whatever the agent kept of git's output. Every field is empty where the
// transcript says nothing, which a quiet commit guarantees.
type Reported struct {
	SHA    string
	Branch string

	// Kind is "committed" or "amended" when the transcript says which. It wins
	// over the command's own reading, since it records what git did rather
	// than what it was asked to do.
	Kind string
}

// Commits holds commit calls until their results say whether they landed.
//
// Keyed by call id, and never cleared when a new prompt opens. Agents run calls
// in parallel and results come back interleaved, so whichever result arrives
// next says nothing about a commit; only its own does. A result can also arrive
// after the next prompt has opened, and the commit still belongs to the turn
// that issued it.
type Commits map[string]held

// held is a commit call waiting to hear whether it worked.
type held struct {
	turn int
	kind string
	dir  string
}

// Call holds a command until its result arrives, if the command commits. A call
// with no id is not held, because no result could ever name it.
func (c Commits) Call(call Call) {
	if call.ID == "" || !shell.IsCommit(call.Command) {
		return
	}
	kind := "committed"
	if shell.IsAmend(call.Command) {
		kind = "amended"
	}
	c[call.ID] = held{
		turn: call.Turn,
		kind: kind,
		// Where the command moved first beats where it was started, since the
		// move is the later of the two.
		dir: cmp.Or(shell.CommitDir(call.Command), agent.NormalisePath(call.Workdir)),
	}
}

// Settle credits a held commit to the turn that issued it, once its own result
// shows it landed. A result answering any other call settles nothing.
func (c Commits) Settle(turns []agent.Turn, r Result) {
	h, ok := c[r.CallID]
	if !ok {
		return
	}
	delete(c, r.CallID)
	// A refused commit is not a commit. They are common: nothing staged, or a
	// hook that said no.
	if r.Failed {
		return
	}
	turns[h.turn].Committed = append(turns[h.turn].Committed, agent.Commit{
		SHA:    r.Reported.SHA,
		Kind:   cmp.Or(r.Reported.Kind, h.kind),
		Branch: r.Reported.Branch,
		At:     r.At,
		Dir:    h.dir,
	})
}

// ChangedLines counts the lines of a diff that add or remove.
//
// Both directions count as change: rewriting a line is a removal and an
// addition, and moving a block around a file is work whether or not the totals
// come out even. Context lines and the markers a format puts round its hunks
// open with neither sign. Neither format read here writes "+++" or "---" file
// headers inside a hunk, so a line opening that way is a change whose own text
// begins with the sign.
func ChangedLines(lines []string) int {
	n := 0
	for _, l := range lines {
		if len(l) > 0 && (l[0] == '+' || l[0] == '-') {
			n++
		}
	}
	return n
}

// Time reads a record timestamp, returning the zero time if it is missing or
// malformed. A bad timestamp should never stop a session from loading.
func Time(stamp string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}
	}
	// Local time, not UTC. Agents write timestamps with a Z suffix, and
	// time.Parse hands those back in UTC, which is a different calendar day
	// from the one the person was sitting at for a good part of every evening.
	// A prompt typed at 01:57 in Asia/Calcutta is 20:27 the previous day in
	// UTC, and the diagram headed it with yesterday's date.
	//
	// Durations are unaffected either way, so segmenting never noticed. It is
	// the day a piece of work belongs to that was wrong, which is exactly what
	// the reader is looking at.
	//nolint:gosmopolitan // deliberate: the reader's own clock is the right
	// frame for which day a piece of work belongs to.
	return t.Local()
}
