package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// Everything git would read from the environment about where a repository is
// goes; what it needs to run stays.
func TestWithoutGitEnv(t *testing.T) {
	got := withoutGitEnv([]string{
		"HOME=/Users/bmf", "GIT_DIR=/elsewhere/.git", "GIT_WORK_TREE=/elsewhere",
		"GIT_CEILING_DIRECTORIES=/Users", "GIT_OBJECT_DIRECTORY=/q",
		"GIT_EXEC_PATH=/opt/git/libexec", "GIT_TRACE=1", "GIT_CONFIG_GLOBAL=/x/gitconfig",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=safe.directory", "GIT_CONFIG_VALUE_0=*",
	})
	want := []string{"HOME=/Users/bmf", "GIT_EXEC_PATH=/opt/git/libexec", "GIT_TRACE=1", "GIT_CONFIG_GLOBAL=/x/gitconfig",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=safe.directory", "GIT_CONFIG_VALUE_0=*"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("kept %v, want %v", got, want)
	}
}

func writeFile(dir, name, body string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
}

// A history says whether the repository was actually consulted.
//
// An empty repository and a machine with no git installed both come back with
// no commits, and they mean different things. A hash the transcript carried is
// a claim: when the repository was read and cannot find it, the hash is stale
// and showing it offers the reader something to check that does not check out.
// When the repository was never read, the same hash is simply unconfirmed, and
// clearing it would empty every hash on a machine without git.
func TestHistorySaysWhetherItWasRead(t *testing.T) {
	if h := Read(""); h.Read {
		t.Error("an empty path reported that it read a repository")
	}
	if h := Read(filepath.Join(t.TempDir(), "nothing-here")); h.Read {
		t.Error("a missing directory reported that it read a repository")
	}
	// A real repository, which is the case that has to come back true.
	if h := Read("."); !h.Read {
		t.Skip("no git available, so there is nothing to compare against")
	}
}

// A subdirectory and a linked worktree both name the checkout they belong to,
// and a directory outside any repository names nothing.
func TestDiskNamesTheMainTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	main := filepath.Join(base, "main")
	linked := filepath.Join(base, "linked")
	if err := os.MkdirAll(filepath.Join(main, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(main, "init", "-q")
	run(main, "commit", "-q", "--allow-empty", "-m", "First commit")
	run(main, "worktree", "add", "-q", linked, "-b", "linked")
	// A submodule keeps its git directory under the superproject's.
	sub := filepath.Join(base, "sub")
	run(base, "init", "-q", "sub")
	run(sub, "commit", "-q", "--allow-empty", "-m", "Submodule")
	run(main, "-c", "protocol.file.allow=always", "submodule", "add", "-q", "../sub", "vendor/sub")

	// A bare repository has no main tree, and names its family after itself
	// however many checkouts it has and in whatever order they sort.
	bare := filepath.Join(base, "bare.git")
	bareTree := filepath.Join(base, "zz-checkout")
	run(base, "clone", "-q", "--bare", main, bare)
	run(bare, "worktree", "add", "-q", bareTree, "-b", "zz")
	laterTree := filepath.Join(base, "aa-checkout")
	run(bare, "worktree", "add", "-q", laterTree, "-b", "aa")

	// A submodule's own linked worktree, which git lists after the submodule's
	// git directory.
	subLinked := filepath.Join(base, "sub-linked")
	run(filepath.Join(main, "vendor", "sub"), "worktree", "add", "-q", subLinked, "-b", "sub-linked")

	// The checkout reached through a symlink, the way /tmp is /private/tmp.
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(main, alias); err != nil {
		t.Fatal(err)
	}

	// One repository has one spelling from wherever it is asked about, the one
	// with symlinks resolved. The temporary directory on macOS sits behind a
	// symlink, so this is tested on every run there.
	resolved := func(p string) string {
		t.Helper()
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	var d Disk
	for _, c := range []struct{ dir, want string }{
		{main, resolved(main)},
		{filepath.Join(main, "sub", "deep"), resolved(main)},
		{filepath.Join(alias, "sub", "deep"), resolved(main)},
		{linked, resolved(main)},
		{bareTree, resolved(bare)},
		{laterTree, resolved(bare)},
		{filepath.Join(main, "vendor", "sub"), resolved(filepath.Join(main, "vendor", "sub"))},
		{subLinked, resolved(filepath.Join(main, "vendor", "sub"))},
	} {
		if got := d.MainTree(c.dir); got != c.want {
			t.Errorf("MainTree(%q) = %q, want %q", c.dir, got, c.want)
		}
	}
	if got := d.MainTree(base); got != "" {
		t.Errorf("MainTree of a directory outside any repository = %q, want none", got)
	}
	if d.Exists(filepath.Join(base, "nothing-here")) {
		t.Error("a missing directory exists")
	}
	if !d.Exists(linked) {
		t.Error("the linked worktree does not exist")
	}
}
