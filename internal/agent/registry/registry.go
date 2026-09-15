// Package registry lists the agents bough can read.
//
// It is the one place that names them all. Adding an agent is a line in All
// and the package that reads it; the flag, the usage text, the message for a
// machine with no history and the names on the page all follow from here.
package registry

import (
	"fmt"
	"strings"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/agent/claude"
	"github.com/nickelsec/bough/internal/agent/codex"
)

// everyAgent is how --agent spells reading all of them.
const everyAgent = "all"

// All is every agent bough can read, in the order they are asked.
func All() []agent.Agent {
	return []agent.Agent{claude.Agent(), codex.Agent()}
}

// Flags is every spelling --agent accepts, the way usage text lists them.
func Flags() []string {
	all := All()
	flags := make([]string, 0, len(all)+1)
	for _, a := range all {
		flags = append(flags, a.Flag)
	}
	return append(flags, everyAgent)
}

// Select reads an --agent value into the agents it names. An agent's ID is
// accepted as well as its flag, since the listing shows the one and people
// type the other.
func Select(flag string) ([]agent.Agent, error) {
	want := strings.ToLower(flag)
	if want == everyAgent {
		return All(), nil
	}
	for _, a := range All() {
		if want == a.Flag || want == a.ID {
			return []agent.Agent{a}, nil
		}
	}
	return nil, fmt.Errorf("unknown agent %q; supported: %s", flag, strings.Join(Flags(), ", "))
}

// Display names an agent for a person to read.
//
// An ID no agent claims comes back as it was given, which says more than an
// empty column would.
func Display(id string) string {
	for _, a := range All() {
		if a.ID == id {
			return a.Name
		}
	}
	return id
}
