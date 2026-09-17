package family

import "testing"

func TestProbeContainment(t *testing.T) {
	r := New(projectsOf(history), recordsOf(history), disk{t: t, dirs: exists, repos: repos})
	for _, p := range []string{"/Users/bmf/code/never-had-history", "/Users/bmf/Downloads/notes.txt"} {
		t.Logf("%s -> %+v", p, r.Resolve(p))
	}
}
