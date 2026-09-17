package graph

import (
	"slices"
	"strings"
	"testing"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/family"
)

// named spells an agent the way the command does, from a table of the test's
// own.
func named(id string) string {
	return map[string]string{"claude-code": "Claude Code", "codex": "Codex"}[id]
}

// together is one directory both agents worked in at once: a Claude Code
// session from 09:00 to 10:00 and a Codex session from 09:30 to 10:30, both
// editing export.go twice. The two sessions share an ID, which each agent is
// free to give.
func together() (family.Project, []agent.Session) {
	p := family.Project{Path: "/work/app", Members: []agent.Project{
		{Name: "app", Path: "/work/app", Source: "claude-code"},
		{Name: "app", Path: "/work/app", Source: "codex"},
	}}
	return p, []agent.Session{
		{ID: "s1", Source: "codex", Dir: "/work/app", Turns: []agent.Turn{
			turn(1, 30, otherRequest, "export.go"),
			turn(1, 90, "and cover it with a test", "export.go"),
		}},
		{ID: "s1", Source: "claude-code", Dir: "/work/app", Turns: []agent.Turn{
			turn(1, 0, longRequest, "export.go"),
			turn(1, 60, "keep going", "export.go"),
		}},
	}
}

// A family both agents worked in is one graph holding both agents' sittings,
// in the order they started, each saying whose it is.
func TestBuildHoldsBothAgentsSittings(t *testing.T) {
	p, sessions := together()
	opt := DefaultOptions(made)
	opt.Now = fixedNow
	g := Build(p, sessions, opt)

	if want := []string{"claude-code", "codex"}; !slices.Equal(g.Project.Agents, want) {
		t.Errorf("project agents = %q, want %q", g.Project.Agents, want)
	}
	if want := []string{"/work/app"}; !slices.Equal(g.Project.Directories, want) {
		t.Errorf("directories = %q, want the one both worked in: %q", g.Project.Directories, want)
	}
	var whose []string
	for _, goal := range g.Goals {
		whose = append(whose, goal.Agent)
	}
	if want := []string{"claude-code", "codex"}; !slices.Equal(whose, want) {
		t.Fatalf("sittings are %q, want Claude Code's then Codex's", whose)
	}
	if g.Totals.Turns != 4 || g.Project.Sessions != 2 {
		t.Errorf("%d prompts over %d sessions, want both agents' 4 over 2", g.Totals.Turns, g.Project.Sessions)
	}
}

// Two sessions that overlap in time stay two sittings. The overlap is what
// the page shows; it is never a reason to make one sitting of the two.
func TestOverlappingSessionsOfTwoAgentsStayTwoSittings(t *testing.T) {
	p, sessions := together()
	opt := DefaultOptions(made)
	opt.Now = fixedNow
	g := Build(p, sessions, opt)

	if len(g.Goals) != 2 {
		t.Fatalf("%d sittings, want 2", len(g.Goals))
	}
	a, b := g.Goals[0].Stats, g.Goals[1].Stats
	if !b.Start.Before(a.End) {
		t.Fatalf("the fixture does not overlap: %s to %s, then %s to %s", a.Start, a.End, b.Start, b.End)
	}
	if a.Turns != 2 || b.Turns != 2 {
		t.Errorf("the sittings hold %d and %d prompts, want each agent's own 2", a.Turns, b.Turns)
	}
}

// Sittings that returned to the same files are linked whichever agents they
// were.
func TestLinksJoinSittingsAcrossAgents(t *testing.T) {
	p, sessions := together()
	opt := DefaultOptions(made)
	opt.Now = fixedNow
	g := Build(p, sessions, opt)

	if len(g.Links) != 1 || g.Links[0].From != "g1" || g.Links[0].To != "g2" {
		t.Errorf("links = %+v, want Claude Code's sitting joined to Codex's", g.Links)
	}
}

// A session is folded only into its own agent's. A Codex sub-agent whose
// parent ID happens to be a Claude Code session's is still Codex's work, and
// with its parent not read it stands as a sitting of its own.
func TestDelegatedWorkIsNeverFoldedIntoAnotherAgent(t *testing.T) {
	p, sessions := together()
	sessions = append(sessions, agent.Session{
		ID: "child", Source: "codex", ParentID: "s1", Dir: "/work/app",
		Turns: []agent.Turn{handed(0, "/root/probe", 10, 1)},
	})
	sessions[0].ID = "codex-parent"
	opt := DefaultOptions(made)
	opt.Now = fixedNow
	g := Build(p, sessions, opt)

	if len(g.Goals) != 3 {
		t.Fatalf("%d sittings, want the orphaned Codex sub-agent kept as its own third", len(g.Goals))
	}
	for _, goal := range g.Goals {
		if goal.Agent == "claude-code" && goal.Stats.Turns != 2 {
			t.Errorf("Claude Code's sitting holds %d prompts; a Codex sub-agent was folded into it", goal.Stats.Turns)
		}
	}
}

// A family one agent worked in names that agent, and every sitting is its.
func TestAOneAgentFamilyNamesItsAgent(t *testing.T) {
	p, sessions := sample()
	opt := DefaultOptions(made)
	opt.Now = fixedNow
	g := Build(alone(p), sessions, opt)

	if want := []string{"claude-code"}; !slices.Equal(g.Project.Agents, want) {
		t.Errorf("project agents = %q, want %q", g.Project.Agents, want)
	}
	for _, goal := range g.Goals {
		if goal.Agent != "claude-code" {
			t.Errorf("%s is %q's, want claude-code's", goal.ID, goal.Agent)
		}
	}
}

// The terminal marks each sitting with its agent, in the order the sittings
// started across both.
func TestTextInterleavesTwoAgentsInTimeOrder(t *testing.T) {
	p, sessions := together()
	opt := DefaultOptions(made)
	opt.Now = fixedNow
	var out strings.Builder
	if err := WriteText(&out, Build(p, sessions, opt), false, named); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "from Claude Code and Codex\n") {
		t.Errorf("the header does not name both agents:\n%s", text)
	}
	claude := strings.Index(text, "[Claude Code] ")
	codex := strings.Index(text, "[Codex] ")
	if claude < 0 || codex < 0 || claude > codex {
		t.Errorf("want Claude Code's sitting marked, then Codex's:\n%s", text)
	}
}

// A family one agent worked in reads as it always has, apart from naming its
// agent once: no sitting is marked with the agent every one of them shares.
func TestTextOfOneAgentNamesItOnce(t *testing.T) {
	p, sessions := sample()
	opt := DefaultOptions(made)
	opt.Now = fixedNow
	var out strings.Builder
	if err := WriteText(&out, Build(alone(p), sessions, opt), false, named); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if n := strings.Count(text, "Claude Code"); n != 1 {
		t.Errorf("the agent is named %d times, want once:\n%s", n, text)
	}
	if !strings.Contains(text, "from Claude Code\n") {
		t.Errorf("the header does not name the agent:\n%s", text)
	}
}
