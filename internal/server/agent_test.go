package server

import (
	"strings"
	"testing"

	"github.com/nickelsec/bough/internal/graph"
)

// The page says which agent wrote the history, whichever agent that was.
//
// It used to name Codex and say nothing for Claude Code, which read as though
// Claude were the absence of an agent rather than a choice of one. That was
// true while there was only one agent to read. It stopped being true at the
// second.
func TestThePageNamesTheAgent(t *testing.T) {
	for id, name := range map[string]string{"claude-code": "Claude Code", "codex": "Codex"} {
		g := graph.Graph{
			Schema:  graph.SchemaVersion,
			Project: graph.Project{Name: "a project", Path: "/p", Agents: []string{id}},
		}
		b, err := render(g, spelled)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b.with(span{})), `data-agent="`+id+`">`+name+`</span>`) {
			t.Errorf("%s: the page does not name the agent", id)
		}
	}
}

// A project both agents worked on names both, once each and in the order the
// graph lists them, since that order is what gives each agent its mark.
func TestThePageNamesEveryAgentInOrder(t *testing.T) {
	g := graph.Graph{Schema: graph.SchemaVersion, Project: graph.Project{Agents: []string{"claude-code", "codex"}}}
	b, err := render(g, spelled)
	if err != nil {
		t.Fatal(err)
	}
	want := `id="agents"><span class="mark-agent" data-agent="claude-code">Claude Code</span>` +
		`<span class="mark-agent" data-agent="codex">Codex</span></span>`
	if !strings.Contains(string(b.with(span{})), want) {
		t.Errorf("the page does not name both agents in order")
	}
}

// And it spells the agent the way it was told, rather than from a table of its
// own. The page once kept one, and an agent missing from it would have shown on
// the page as something other than what the terminal called it.
func TestThePageTakesAgentNamesFromGo(t *testing.T) {
	g := graph.Graph{Schema: graph.SchemaVersion, Project: graph.Project{Agents: []string{"p<i>"}}}
	b, err := render(g, func(string) string { return "Pi <agent>" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b.with(span{})), `data-agent="p&lt;i&gt;">Pi &lt;agent&gt;</span>`) {
		t.Errorf("the page did not show the name it was given, escaped")
	}
}
