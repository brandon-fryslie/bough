package internal_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The core is only reusable across agents if the parts above the boundary never
// learn which agent they came from. Reaching into one for a field that happens
// to be handy costs nothing until a second agent is added, and by then the
// reaching is everywhere.
//
// Everything from a normalised turn upward has to stay clean. internal/agent
// itself is the boundary: it holds Turn and the handful of things both sides
// need, and the core is allowed to see it. What the core may not see is any
// agent's own package, where one transcript format's quirks live.
//
// This check failed to do its job once already. A commit moved a path helper
// into internal/agent/shell, internal/graph followed it across the boundary,
// and `go test ./...` reported the package as cached ok for two more commits
// while the test underneath was failing.
func TestCoreDoesNotDependOnAnyAgent(t *testing.T) {
	independent := []string{
		"github.com/nickelsec/bough/internal/segment",
		"github.com/nickelsec/bough/internal/rollup",
		"github.com/nickelsec/bough/internal/metrics",
		"github.com/nickelsec/bough/internal/graph",
		"github.com/nickelsec/bough/internal/family",
		"github.com/nickelsec/bough/internal/portfolio",
	}

	for _, pkg := range independent {
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		// Not a skip. This is the only check on the boundary, and a toolchain
		// that cannot answer turns it green without running it, which is worse
		// than not having it: the suite says the boundary held when nothing
		// looked at it.
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}

		deps := strings.Fields(string(out))
		// An empty answer is not a clean answer. go list exiting zero with
		// nothing to say would otherwise pass every package here.
		if len(deps) == 0 {
			t.Fatalf("go list -deps %s returned nothing, so nothing was checked", pkg)
		}

		// And the package has to actually be among them, or a typo in the list
		// above would quietly check something else.
		if !contains(deps, pkg) {
			t.Fatalf("go list -deps %s does not include %s; is the path right?", pkg, pkg)
		}

		for _, dep := range deps {
			if strings.Contains(dep, "/internal/agent/") {
				t.Errorf("%s depends on %s; it must only see normalised turns", pkg, dep)
			}
		}
	}
}

func contains(all []string, want string) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}
