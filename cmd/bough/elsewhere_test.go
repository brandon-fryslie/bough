package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/graph"
	"github.com/nickelsec/bough/internal/repo"
)

// git runs git in dir, without whatever repository a hook exported.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
}

// siblings is a directory holding two repositories, app and site, spelled the
// way the disk resolves them.
func siblings(t *testing.T) (app, site string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app, site = filepath.Join(base, "app"), filepath.Join(base, "site")
	for _, dir := range []string{app, site} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "init", "-q")
	}
	return app, site
}

// answered builds app's one sitting, with the edge's answers about where its
// work landed. Nothing is temporary here: the fixtures live in the temporary
// directory, and the graph's rule about that is tested on its own.
func answered(t *testing.T, families *family.Resolver, app string, turn agent.Turn, noRepo bool) graph.Goal {
	t.Helper()
	p := family.Project{Path: app, Members: []agent.Project{{Path: app, Source: "claude-code"}}}
	sessions := []agent.Session{{ID: "s", Dir: app, Turns: []agent.Turn{turn}}}
	opt := graph.DefaultOptions(everyAgentsRecord())
	opt.Elsewhere = elsewhere(families, &repo.Disk{}, p, graph.Visited(p, sessions), nil, noRepo)
	g := graph.Build(p, sessions, opt)
	if len(g.Goals) != 1 {
		t.Fatalf("%d goals, want one", len(g.Goals))
	}
	return g.Goals[0]
}

func edited(files ...string) agent.Turn {
	t := agent.Turn{At: time.Now(), Text: "work", Files: map[string]int{}, Edits: map[string]int{}}
	for _, f := range files {
		t.Files[filepath.ToSlash(f)]++
		t.Edits[filepath.ToSlash(f)]++
	}
	return t
}

// An edit made through a symlink is the work of the family the link leads
// into, recorded where the file really is.
func TestAnEditThroughASymlinkIsTheFamilyItLeadsInto(t *testing.T) {
	app, site := siblings(t)
	if err := os.WriteFile(filepath.Join(site, "index.html"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(app), "link")
	if err := os.Symlink(site, link); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	file := filepath.Join(link, "index.html")
	families := family.New([]agent.Project{{Path: app, Source: "claude-code"}}, everyAgentsRecord(), &repo.Disk{})

	goal := answered(t, families, app, edited(file, file), false)
	want := filepath.ToSlash(filepath.Join(site, "index.html"))
	if len(goal.Elsewhere) != 1 || goal.Elsewhere[0].Family != agent.NormalisePath(site) ||
		len(goal.Elsewhere[0].Files) != 1 || goal.Elsewhere[0].Files[0].Path != want {
		t.Errorf("elsewhere = %+v, want two edits to %s in %s", goal.Elsewhere, want, site)
	}
}

// A commit made in another repository is checked against that repository,
// which is read for it.
func TestACommitInAnotherRepositoryIsCheckedThere(t *testing.T) {
	app, site := siblings(t)
	git(t, site, "commit", "-q", "--allow-empty", "-m", "fix the header")
	families := family.New([]agent.Project{{Path: app, Source: "claude-code"}}, everyAgentsRecord(), &repo.Disk{})

	work := edited()
	work.Committed = []agent.Commit{{Kind: "committed", At: time.Now(), Dir: filepath.ToSlash(site)}}
	goal := answered(t, families, app, work, false)
	if len(goal.Elsewhere) != 1 || !goal.Elsewhere[0].RepoRead ||
		len(goal.Elsewhere[0].Commits) != 1 || goal.Elsewhere[0].Commits[0].Subject != "fix the header" {
		t.Errorf("elsewhere = %+v, want the commit matched in %s", goal.Elsewhere, site)
	}
}

// A directory that has gone is answered as gone, though its nearest ancestor
// still names a repository.
func TestADeletedDirectoryIsAnsweredAsGone(t *testing.T) {
	app, site := siblings(t)
	gone := filepath.Join(site, "gone")
	families := family.New([]agent.Project{{Path: app, Source: "claude-code"}}, everyAgentsRecord(), &repo.Disk{})

	if pl := where(families, gone); pl.Exists || pl.Family.Evidence != family.Repository {
		t.Errorf("where(%s) = %+v, want a place that is gone, in the sibling repository", gone, pl)
	}
	work := edited()
	work.Committed = []agent.Commit{{Kind: "committed", At: time.Now(), Dir: filepath.ToSlash(gone)}}
	if goal := answered(t, families, app, work, false); len(goal.Elsewhere) != 0 {
		t.Errorf("elsewhere = %+v, want nothing for a commit in a deleted directory", goal.Elsewhere)
	}
	// Nor is the sibling's repository read for a commit that is not recorded.
	p := family.Project{Path: app, Members: []agent.Project{{Path: app}}}
	v := graph.Visits{Dirs: []string{filepath.ToSlash(gone)}}
	if e := elsewhere(families, &repo.Disk{}, p, v, nil, false); len(e.Repos) != 0 {
		t.Errorf("read %d repositories for a commit in a deleted directory", len(e.Repos))
	}
}

// Under --no-repo nothing asks git, so a sibling repository is nobody's
// family and nothing is recorded in it.
func TestNoRepoRecordsNoSiblingRepository(t *testing.T) {
	app, site := siblings(t)
	if err := os.WriteFile(filepath.Join(site, "index.html"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(site, "index.html")
	families := family.WithoutRepository([]agent.Project{{Path: app, Source: "claude-code"}}, everyAgentsRecord())

	work := edited(file, file)
	work.Committed = []agent.Commit{{Kind: "committed", At: time.Now(), Dir: filepath.ToSlash(site)}}
	if goal := answered(t, families, app, work, true); len(goal.Elsewhere) != 0 {
		t.Errorf("elsewhere = %+v, want nothing without the repository", goal.Elsewhere)
	}
}

// The machine's temporary directory is temporary however it is spelled.
func TestTemporaryIncludesTheMachinesOwn(t *testing.T) {
	roots := temporary()
	if !slices.Contains(roots, os.TempDir()) {
		t.Errorf("temporary() = %q, want %s among them", roots, os.TempDir())
	}
	if led, err := filepath.EvalSymlinks(os.TempDir()); err == nil && !slices.Contains(roots, led) {
		t.Errorf("temporary() = %q, want %s among them", roots, led)
	}
}
