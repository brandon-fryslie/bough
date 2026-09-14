package agent

import "testing"

// The two spellings of a Windows drive are one place.
//
// A unix style shell writes "/d/work/site" where the transcript elsewhere says
// "d:/work/site", and one project's commits arrived as both inside a single
// session. There were two normalisers in the tree doing this differently, and
// the one the agents called did not fold the drive at all, so the same
// directory compared as two and real work was thrown away as belonging
// somewhere else.
func TestNormalisePathFoldsBothDriveSpellings(t *testing.T) {
	want := "d:/work/site"
	for _, in := range []string{
		"d:/work/site",
		"/d/work/site",
		`D:\work\site`,
		"D:/Work/Site/",
		"d:/work/./site",
	} {
		if got := NormalisePath(in); got != want {
			t.Errorf("NormalisePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// A relative path stays relative. Only an absolute one says where it starts.
func TestNormalisePathLeavesRelativePathsAlone(t *testing.T) {
	for in, want := range map[string]string{
		"internal":     "internal",
		"./internal":   "internal",
		"internal/foo": "internal/foo",
		"":             "",
	} {
		if got := NormalisePath(in); got != want {
			t.Errorf("NormalisePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The same file appears with different drive letter casing and separators
// across a session. Grouping by file only works if those collapse together.
func TestNormalisePathCollapsesCasingAndSeparators(t *testing.T) {
	same := []string{
		`D:\proj\src\main.go`,
		`d:\proj\src\main.go`,
		`d:/proj/src/main.go`,
		`d:/proj//src/main.go`,
	}
	want := NormalisePath(same[0])
	for _, p := range same[1:] {
		if got := NormalisePath(p); got != want {
			t.Errorf("NormalisePath(%q) = %q, want %q", p, got, want)
		}
	}
	if NormalisePath("") != "" {
		t.Error("empty path should stay empty")
	}
}

// The earlier version of this used path/filepath, which splits on whatever
// separator the host machine happens to use. That passed on Windows and failed
// everywhere else, because a transcript written on Windows is still full of
// backslashes when it is read on Linux. Pinning the exact result catches that,
// where comparing two paths to each other did not.
func TestNormalisePathIsTheSameOnEveryPlatform(t *testing.T) {
	cases := map[string]string{
		`D:\proj\src\main.go`:  "d:/proj/src/main.go",
		`d:/proj//src/main.go`: "d:/proj/src/main.go",
		`/home/x/proj/main.go`: "/home/x/proj/main.go",
		`C:\Users\x\notes.md`:  "c:/users/x/notes.md",
	}
	for in, want := range cases {
		if got := NormalisePath(in); got != want {
			t.Errorf("NormalisePath(%q) = %q, want %q", in, got, want)
		}
	}
}
