package registry

import (
	"strings"
	"testing"
)

// Each agent is told apart by its ID, its flag and its name. Two sharing any one
// of them would make --agent, the listing or the page ambiguous.
func TestAgentsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range All() {
		for _, v := range []string{a.ID, a.Flag, a.Name, a.History} {
			if v == "" {
				t.Errorf("%+v leaves something unnamed", a)
			}
		}
		for _, v := range []string{"id:" + a.ID, "flag:" + a.Flag, "name:" + a.Name} {
			if seen[v] {
				t.Errorf("two agents share %s", v)
			}
			seen[v] = true
		}
	}
}

func TestSelectAcceptsEachFlagAndID(t *testing.T) {
	for _, a := range All() {
		for _, spelled := range []string{a.Flag, a.ID, strings.ToUpper(a.Flag)} {
			got, err := Select(spelled)
			if err != nil {
				t.Fatalf("%q: %v", spelled, err)
			}
			if len(got) != 1 || got[0].ID != a.ID {
				t.Errorf("%q selected %v, want %s", spelled, got, a.ID)
			}
		}
	}

	got, err := Select("all")
	if err != nil || len(got) != len(All()) {
		t.Errorf("all selected %d agents, want %d (err %v)", len(got), len(All()), err)
	}
}

// A mistyped agent says what would have worked.
func TestSelectNamesTheChoicesWhenItRefuses(t *testing.T) {
	_, err := Select("cursor")
	if err == nil {
		t.Fatal("an unknown agent was accepted")
	}
	for _, flag := range Flags() {
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("error does not offer %q: %v", flag, err)
		}
	}
}

func TestDisplay(t *testing.T) {
	for _, a := range All() {
		if got := Display(a.ID); got != a.Name {
			t.Errorf("Display(%q) = %q, want %q", a.ID, got, a.Name)
		}
	}
	if got := Display("unheard-of"); got != "unheard-of" {
		t.Errorf("an unknown agent came back as %q", got)
	}
}
