package repo

import (
	"os"
	"strings"
	"sync"
)

// Disk answers the family resolver's questions about this machine: whether a
// directory exists, and which repository it sits in. It is the edge the
// resolver keeps its reads behind, so nothing that decides families runs git.
//
// Answers are remembered, since attributing a history's edits asks about the
// same few hundred directories thousands of times and each fresh answer is a
// git process. The zero value is ready to use, and safe to share between
// goroutines.
type Disk struct {
	mu    sync.Mutex
	trees map[string]string
}

// Exists reports whether dir is a directory on disk.
func (d *Disk) Exists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// MainTree is the main working tree of the repository dir sits in, or ""
// when it sits in none, git is not installed, or git could not say.
//
// The main tree names a family, so a linked worktree and a subdirectory of the
// checkout both answer with the checkout itself. A checkout's own top level is
// that tree unless the checkout is a linked worktree, which git shows by
// keeping its git directory apart from the common one; then the tree is the
// first git lists. Asking the listing alone names a submodule after its git
// directory under .git/modules, which exists and is nobody's project.
func (d *Disk) MainTree(dir string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if tree, ok := d.trees[dir]; ok {
		return tree
	}
	if d.trees == nil {
		d.trees = map[string]string{}
	}
	tree := mainTree(dir)
	d.trees[dir] = tree
	return tree
}

func mainTree(dir string) string {
	out, err := run(dir, "rev-parse", "--path-format=absolute",
		"--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		return ""
	}
	top, gitDir, common := lines[0], lines[1], lines[2]
	if gitDir == common {
		return top
	}
	out, err = run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(out, "\n")
	tree, _ := strings.CutPrefix(first, "worktree ")
	return tree
}
