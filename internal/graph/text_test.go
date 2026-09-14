package graph

import (
	"bytes"
	"strings"
	"testing"
)

// A reading that did not consult the repository says so.
//
// A commit hash means two things. Read the repository and a hash is one it
// confirmed, with anything stale cleared. Do not read it and every hash is
// whatever the transcript claimed, unchecked. "Not a git repository", "git is
// not installed" and --no-repo all arrive at the same place, and without a
// word about it they look exactly like a clean confirmation.
func TestTextSaysWhenTheRepositoryWasNotRead(t *testing.T) {
	g := Graph{
		Project: Project{Name: "x", RepoRead: false},
		Totals:  Stats{Commits: []Commit{{SHA: "abc1234"}}},
	}
	var b bytes.Buffer
	if err := WriteText(&b, g, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "the repository was not read") {
		t.Errorf("nothing said the repository went unread:\n%s", b.String())
	}

	g.Project.RepoRead = true
	b.Reset()
	if err := WriteText(&b, g, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "the repository was not read") {
		t.Errorf("a confirmed reading claimed it was not read:\n%s", b.String())
	}
}
