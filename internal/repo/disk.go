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
// checkout both answer with the checkout itself. git lists the main tree first
// whichever worktree it is asked from, and lists a bare repository's own
// directory, which is as good a name as that repository has.
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
	out, err := run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(out, "\n")
	tree, _ := strings.CutPrefix(first, "worktree ")
	return tree
}
