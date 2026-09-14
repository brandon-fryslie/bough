package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A project that is not a repository, or has moved, is the ordinary case rather
// than a failure. It must come back empty and quiet.
func TestReadToleratesWhatIsNotThere(t *testing.T) {
	for _, dir := range []string{"", t.TempDir(), "/no/such/path/anywhere"} {
		if got := Read(dir); len(got.Commits) != 0 {
			t.Errorf("Read(%q) returned %d commits, want none", dir, len(got.Commits))
		}
	}
}

func TestParseLogReadsCommitsAndLineCounts(t *testing.T) {
	// The shape git produces for the format Read asks for.
	out := "\x1eabc1234\x1f2026-08-22T10:00:00+05:30\x1fAdd the parser\n" +
		"12\t3\tmain.go\n" +
		"4\t0\tREADME.md\n" +
		"\x1edef5678\x1f2026-08-22T11:30:00+05:30\x1fFix a typo\n" +
		"1\t1\tdoc.md\n"

	got := parseLog(out)
	if len(got) != 2 {
		t.Fatalf("got %d commits, want 2", len(got))
	}
	if got[0].SHA != "abc1234" || got[0].Subject != "Add the parser" {
		t.Errorf("first commit = %+v", got[0])
	}
	if got[0].Added != 16 || got[0].Removed != 3 || got[0].Files != 2 {
		t.Errorf("line counts = +%d -%d over %d files, want +16 -3 over 2",
			got[0].Added, got[0].Removed, got[0].Files)
	}
	if got[0].When.IsZero() {
		t.Error("timestamp was not parsed")
	}
}

// A binary file shows "-" for both counts, which must not be read as a number.
func TestParseLogIgnoresBinaryCounts(t *testing.T) {
	out := "\x1eabc1234\x1f2026-08-22T10:00:00+05:30\x1fAdd a picture\n" +
		"-\t-\timage.png\n" +
		"5\t2\tmain.go\n"

	got := parseLog(out)
	if len(got) != 1 {
		t.Fatalf("got %d commits, want 1", len(got))
	}
	if got[0].Added != 5 || got[0].Removed != 2 {
		t.Errorf("got +%d -%d, want +5 -2", got[0].Added, got[0].Removed)
	}
	if got[0].Files != 2 {
		t.Errorf("got %d files, want 2", got[0].Files)
	}
}

// The real thing, against a repository made for the test. Skipped where git is
// not installed, since that is a machine bough still has to work on.
func TestReadAgainstARealRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-q")
	if err := writeFile(dir, "a.txt", "one\ntwo\n"); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	// Quiet on purpose: this is the case that started all of it.
	run("commit", "-q", "-m", "First commit")

	got := Read(dir)
	if len(got.Commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(got.Commits))
	}
	if got.Commits[0].Subject != "First commit" {
		t.Errorf("subject = %q", got.Commits[0].Subject)
	}
	if got.Commits[0].Added != 2 {
		t.Errorf("added = %d, want 2", got.Commits[0].Added)
	}
	if got.Commits[0].SHA == "" {
		t.Error("no hash, which is the whole point of reading the repository")
	}
}

func writeFile(dir, name, body string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
}
