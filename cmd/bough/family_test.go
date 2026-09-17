package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nickelsec/bough/internal/agent/registry"
	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/repo"
)

// sitting writes a Claude Code transcript of one session that ran in cwd:
// prompts prompts, each an hour apart, the first of them editing a file.
func sitting(t *testing.T, root, cwd string, prompts int, edit string) {
	t.Helper()
	id := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(cwd, "-")
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < prompts; i++ {
		lines = append(lines,
			fmt.Sprintf(`{"uuid":"u%d","type":"user","sessionId":"%s","promptId":"p%d","cwd":"%s","timestamp":"2026-08-01T%02d:00:00.000Z",`+
				`"message":{"role":"user","content":[{"type":"text","text":"a request long enough to count as one, number %d"}]}}`,
				i, id, i, cwd, 9+i, i))
		if i == 0 {
			lines = append(lines, `{"uuid":"a0","type":"assistant","timestamp":"2026-08-01T09:05:00.000Z","message":{"role":"assistant","content":[`+
				`{"type":"tool_use","name":"Edit","input":{"file_path":"`+edit+`"}}]}}`)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A repository worked in from its checkout, a worktree the agent made and a
// scratchpad made for it; a home directory that contains it; and a project
// in no repository at all. None of these paths exist, so only the agent's
// record joins anything, with or without git.
const (
	app       = "/work/app"
	tree      = "/work/app/.claude/worktrees/calm-river"
	pad       = "/private/tmp/claude-501/-work-app/1d56911b-b2f0-46e1-96a3-e1622bc1875c/scratchpad"
	home      = "/work"
	loneDraft = "/writing/draft"
)

func families(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	sitting(t, root, app, 3, app+"/main.go")
	sitting(t, root, tree, 2, tree+"/export.go")
	sitting(t, root, pad, 1, pad+"/probe.go")
	sitting(t, root, home, 4, home+"/notes.txt")
	sitting(t, root, loneDraft, 5, loneDraft+"/chapter.md")
	return root
}

// The listing has one row per project, counting every directory's prompts,
// and the directories themselves when asked for.
func TestListShowsOneRowPerFamily(t *testing.T) {
	for _, flags := range [][]string{{"--no-repo"}, {}} {
		root := families(t)
		var out, errs bytes.Buffer
		args := append([]string{"--list", "-v", "--agent", "claude", "--root", root}, flags...)
		if err := run(args, &out, &errs); err != nil {
			t.Fatal(err)
		}

		var rows []string
		for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
			if !strings.HasPrefix(line, " ") {
				rows = append(rows, filepath.ToSlash(strings.Join(strings.Fields(line), " ")))
			}
		}
		// A path is listed the way the host spells it.
		listing := filepath.ToSlash(out.String())
		want := []string{
			"app [Claude Code] /work/app 6 prompts in 3 directories",
			"draft [Claude Code] /writing/draft 5 prompts",
			// Containing the repository does not make it the home directory's.
			"work [Claude Code] /work 4 prompts",
		}
		if strings.Join(rows, "\n") != strings.Join(want, "\n") {
			t.Errorf("with %q the rows are\n%s\nwant\n%s", flags, strings.Join(rows, "\n"), strings.Join(want, "\n"))
		}
		for _, member := range []string{tree + "  2 prompts", pad + "  1 prompts"} {
			if !strings.Contains(listing, member) {
				t.Errorf("with %q the listing does not show %q:\n%s", flags, member, listing)
			}
		}
	}
}

// A member's path opens its whole family, and so does the family's name.
func TestAMemberPathOpensItsFamily(t *testing.T) {
	root := families(t)
	for _, arg := range []string{tree, pad, app, "app"} {
		var out, errs bytes.Buffer
		if err := run([]string{arg, "--json", "--no-repo", "--agent", "claude", "--root", root}, &out, &errs); err != nil {
			t.Fatal(err)
		}
		g := parsed(t, out.Bytes())
		if filepath.ToSlash(g.Project.Path) != app || len(g.Project.Directories) != 3 || g.Totals.Turns != 6 {
			t.Errorf("%s opened %s over %q with %d prompts, want app over 3 directories with 6",
				arg, g.Project.Path, g.Project.Directories, g.Totals.Turns)
		}
		// The worktree's code is the project's work, not the agent's bookkeeping.
		if !g.touched(tree + "/export.go") {
			t.Errorf("%s: the worktree's source file is not among the top files: %+v", arg, g.Totals.TopFiles)
		}
	}
}

// A project in no repository, with nothing recorded about it, opens as the
// one directory it always was.
func TestAProjectInNoRepositoryOpensAsBefore(t *testing.T) {
	root := families(t)
	var out, errs bytes.Buffer
	if err := run([]string{loneDraft, "--json", "--agent", "claude", "--root", root}, &out, &errs); err != nil {
		t.Fatal(err)
	}
	g := parsed(t, out.Bytes())
	// A path is spelled the way the host spells it.
	if g.Project.Name != "draft" || filepath.ToSlash(g.Project.Path) != loneDraft ||
		filepath.ToSlash(strings.Join(g.Project.Directories, "|")) != loneDraft || g.Totals.Turns != 5 {
		t.Errorf("got %+v with %d prompts", g.Project, g.Totals.Turns)
	}
}

// Every build of a family carries every agent's reading of the directories it
// makes, whichever agents were asked for. Left out, a worktree's code counts as
// the agent's own bookkeeping.
func TestAFamilyBuildCarriesEveryAgentsRecord(t *testing.T) {
	opt := options(everyAgentsRecord(), &repo.Disk{}, family.Project{Path: app}, true)
	if got, want := len(opt.Ambience.Made), len(registry.All()); got != want {
		t.Errorf("the build carries %d agents' records, want %d", got, want)
	}
	if opt.Ambience.Ambient(tree + "/export.go") {
		t.Error("a file in a Claude Code worktree reads as the agent's own")
	}
}

type graphJSON struct {
	Project struct {
		Name        string   `json:"name"`
		Path        string   `json:"path"`
		Directories []string `json:"directories"`
	} `json:"project"`
	Totals struct {
		Turns    int `json:"turns"`
		TopFiles []struct {
			Path string `json:"path"`
		} `json:"topFiles"`
	} `json:"totals"`
}

func parsed(t *testing.T, b []byte) graphJSON {
	t.Helper()
	var g graphJSON
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b)
	}
	return g
}

func (g graphJSON) touched(path string) bool {
	for _, f := range g.Totals.TopFiles {
		if f.Path == path {
			return true
		}
	}
	return false
}

// A relative path is taken from where bough was run.
func TestARelativePathOpensTheFamilyItIsIn(t *testing.T) {
	base := t.TempDir()
	// Resolved, since the temporary directory can sit behind a symlink.
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	here := filepath.Join(base, "site.com")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	sitting(t, root, filepath.ToSlash(here), 2, filepath.ToSlash(here)+"/index.html")
	// A project whose name contains a dot, which "." would match by name.
	sitting(t, root, "/work/other.git", 1, "/work/other.git/main.go")
	t.Chdir(here)

	var out, errs bytes.Buffer
	if err := run([]string{".", "--json", "--no-repo", "--agent", "claude", "--root", root}, &out, &errs); err != nil {
		t.Fatal(err)
	}
	if g := parsed(t, out.Bytes()); g.Project.Name != "site.com" {
		t.Errorf("bough . opened %q, want the project it was run in", g.Project.Name)
	}
}

// A name is a name, wherever bough is run. Taken as a path from inside a
// repository with history, a name that is no directory there resolved to that
// repository and opened it instead of the project named.
func TestANameOpensTheProjectNamedFromInsideAnother(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	here, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q", here)
	// Without whatever repository a hook exported, or git would init that one.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	root := t.TempDir()
	sitting(t, root, filepath.ToSlash(here), 2, filepath.ToSlash(here)+"/main.go")
	sitting(t, root, "/work/beta", 1, "/work/beta/main.go")
	t.Chdir(here)

	var out, errs bytes.Buffer
	if err := run([]string{"beta", "--json", "--agent", "claude", "--root", root}, &out, &errs); err != nil {
		t.Fatal(err)
	}
	if g := parsed(t, out.Bytes()); g.Project.Name != "beta" {
		t.Errorf("bough beta opened %q from inside another repository", g.Project.Name)
	}
}
