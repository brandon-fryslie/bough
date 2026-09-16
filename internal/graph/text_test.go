package graph

import (
	"strings"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/agent"
)

// A hand-off shows every fact the agent recorded, and a task another agent
// handed over is marked as one. When one field stood in for another, a task
// name was printed where the reader expects their own words.
func TestTextShowsEachRecordedFactForWhatItIs(t *testing.T) {
	prompt := spent(30, "make me a site", 100, 10)
	prompt.Delegated = []agent.Delegation{
		{Kind: "Explore", Description: "Research the fonts"},
		{Name: "pixel_art"},
	}
	orphan := agent.Session{ID: "C", ParentID: "missing", Turns: []agent.Turn{handed(31, "/root/sprites", 40, 4)}}

	opt := DefaultOptions()
	opt.SkipRepo = true
	opt.Now = func() time.Time { return minute(50) }
	g := Build(agent.Project{Name: "site"}, []agent.Session{{ID: "S", Turns: []agent.Turn{prompt}}, orphan}, opt)

	var out strings.Builder
	if err := WriteText(&out, g, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"handed off: Explore · Research the fonts\n",
		"handed off: pixel_art\n",
		"task from an agent: /root/sprites\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
