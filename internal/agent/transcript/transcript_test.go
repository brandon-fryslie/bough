package transcript

import (
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/agent"
)

func turns(n int) []agent.Turn { return make([]agent.Turn, n) }

// A commit is settled by its own call's result, not by whatever finishes next.
// Agents issue calls in parallel and results come back interleaved.
func TestCommitIsSettledByItsOwnResult(t *testing.T) {
	ts := turns(1)
	c := Commits{}
	c.Call(Call{Turn: 0, ID: "c1", Command: "git commit -m x"})
	c.Call(Call{Turn: 0, ID: "c2", Command: "go test ./..."})

	c.Settle(ts, Result{CallID: "c2", Failed: true})
	c.Settle(ts, Result{CallID: "c1", Reported: Reported{SHA: "abc1234"}})

	if got := ts[0].Committed; len(got) != 1 || got[0].SHA != "abc1234" {
		t.Fatalf("got %+v, want the one commit its own result settled", got)
	}
}

func TestRefusedCommitIsNotACommit(t *testing.T) {
	ts := turns(1)
	c := Commits{}
	c.Call(Call{Turn: 0, ID: "c1", Command: "git commit -m x"})
	c.Settle(ts, Result{CallID: "c1", Failed: true})
	if n := len(ts[0].Committed); n != 0 {
		t.Errorf("got %d commits, want 0", n)
	}
}

// A result that lands after the next prompt still belongs to the turn that
// issued the commit. Codex used to forget every held commit when a prompt
// opened, and lost these.
func TestLateResultIsCreditedToTheIssuingTurn(t *testing.T) {
	ts := turns(2)
	c := Commits{}
	c.Call(Call{Turn: 0, ID: "c1", Command: "git commit -m x"})
	c.Settle(ts, Result{CallID: "c1"})
	if len(ts[0].Committed) != 1 || len(ts[1].Committed) != 0 {
		t.Errorf("commits = %d then %d, want 1 then 0", len(ts[0].Committed), len(ts[1].Committed))
	}
}

// A result that answers nothing held, or a call no result could ever name,
// settles nothing.
func TestOnlyNamedCommitCallsAreHeld(t *testing.T) {
	ts := turns(1)
	c := Commits{}
	c.Call(Call{Turn: 0, ID: "", Command: "git commit -m x"})
	c.Call(Call{Turn: 0, ID: "c1", Command: "git status"})
	c.Settle(ts, Result{CallID: ""})
	c.Settle(ts, Result{CallID: "c1"})
	if n := len(ts[0].Committed); n != 0 {
		t.Errorf("got %d commits, want 0", n)
	}
}

// The kind comes from the transcript when it says, and from the command when it
// does not. Codex never says; Claude Code says when git was not silenced.
func TestKindPrefersWhatTheTranscriptReported(t *testing.T) {
	cases := []struct {
		command, reported, want string
	}{
		{"git commit -m x", "", "committed"},
		{"git commit --amend --no-edit", "", "amended"},
		{"git commit -m x", "amended", "amended"},
	}
	for _, tc := range cases {
		ts := turns(1)
		c := Commits{}
		c.Call(Call{Turn: 0, ID: "c1", Command: tc.command})
		c.Settle(ts, Result{CallID: "c1", Reported: Reported{Kind: tc.reported}})
		if got := ts[0].Committed[0].Kind; got != tc.want {
			t.Errorf("%q reported %q: kind = %q, want %q", tc.command, tc.reported, got, tc.want)
		}
	}
}

// A commit's directory is where the command moved first, then where the agent
// recorded it running, then the project's own.
func TestCommitDirectory(t *testing.T) {
	cases := []struct {
		command, workdir, want string
	}{
		{"cd /Elsewhere && git commit -m x", "/w", "/elsewhere"},
		{"git commit -m x", `D:\W`, "d:/w"},
		{"git commit -m x", "", ""},
	}
	for _, tc := range cases {
		ts := turns(1)
		c := Commits{}
		c.Call(Call{Turn: 0, ID: "c1", Command: tc.command, Workdir: tc.workdir})
		c.Settle(ts, Result{CallID: "c1"})
		if got := ts[0].Committed[0].Dir; got != tc.want {
			t.Errorf("%q in %q: dir = %q, want %q", tc.command, tc.workdir, got, tc.want)
		}
	}
}

func TestSettledCommitCarriesTheResult(t *testing.T) {
	ts := turns(1)
	at := time.Date(2026, 9, 9, 20, 27, 47, 0, time.UTC)
	c := Commits{}
	c.Call(Call{Turn: 0, ID: "c1", Command: "git commit -m x"})
	c.Settle(ts, Result{CallID: "c1", At: at, Reported: Reported{SHA: "d0a65cc", Branch: "main"}})
	got := ts[0].Committed[0]
	if got.SHA != "d0a65cc" || got.Branch != "main" || !got.At.Equal(at) {
		t.Errorf("got %+v, want sha d0a65cc on main at %s", got, at)
	}
}

func TestChangedLinesCountsBothDirections(t *testing.T) {
	lines := []string{" kept", "-gone", "+added", "@@ ctx", "*** End of File", "", "+++i;"}
	// "+++i;" adds the line "++i;". Neither format writes file headers inside
	// a hunk, so it is a change like any other.
	if got := ChangedLines(lines); got != 3 {
		t.Errorf("got %d changed lines, want 3", got)
	}
}

// Times belong to the day the person was sitting at, not to UTC.
//
// Agents write timestamps with a Z suffix and time.Parse hands those back in
// UTC. East of Greenwich that is a different calendar day for a good part of
// every evening: a prompt typed at 01:57 in Asia/Calcutta is 20:27 the day
// before in UTC, and the diagram headed it with yesterday's date.
//
// Durations are the same either way, so segmenting never noticed and every
// test passed. It is the day a piece of work belongs to that was wrong, which
// is the thing the reader is actually looking at.
func TestTimesComeBackInLocalTime(t *testing.T) {
	got := Time("2026-09-09T20:27:47.202Z")

	//nolint:gosmopolitan // the local zone is the thing under test.
	if got.Location() != time.Local {
		t.Errorf("location = %s, want the local zone", got.Location())
	}

	// The instant itself must not move. This is a change of how the time is
	// presented, not of when it happened.
	want := time.Date(2026, 9, 9, 20, 27, 47, 202000000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("time = %s, want the same instant as %s", got, want)
	}
}

// A missing or malformed timestamp still comes back as the zero time rather
// than as the zero time shifted into some zone, which would no longer be zero.
func TestBadTimestampsStayZero(t *testing.T) {
	for _, in := range []string{"", "not a time", "2026-13-45T99:99:99Z"} {
		if got := Time(in); !got.IsZero() {
			t.Errorf("Time(%q) = %s, want the zero time", in, got)
		}
	}
}
