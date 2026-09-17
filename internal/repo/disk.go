package repo

import (
	"os"
	"path/filepath"
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
// checkout both answer with the checkout itself. It is spelled the way git
// spells it, with every symlink resolved, whatever spelling dir was asked in:
// a repository has that one spelling from wherever it is reached, where the
// asker's spelling would name a checkout /tmp/x from inside it and
// /private/tmp/x from its linked worktree outside, and split one family in
// two.
func (d *Disk) MainTree(dir string) string {
	d.mu.Lock()
	tree, ok := d.trees[dir]
	d.mu.Unlock()
	if ok {
		return tree
	}

	// Git runs unlocked, so callers sharing a Disk wait on one another only
	// for the map. Two asking about one directory at once both run git and
	// get the same answer.
	tree = mainTree(dir)

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.trees == nil {
		d.trees = map[string]string{}
	}
	d.trees[dir] = tree
	return tree
}

// mainTree is the main working tree as git spells it.
//
// git lists a repository's main entry first, from anywhere in the repository:
// a subdirectory, a linked worktree, inside the git directory itself. That
// entry is a checkout in the ordinary case, and in three layouts it is the git
// directory instead: a bare repository, a submodule, whose git directory sits
// under .git/modules, and a repository made with --separate-git-dir. Asking
// the entry for its top level answers all of them the same way. A checkout
// answers with itself, a submodule's git directory with the checkout it is
// configured for, and the other two have no checkout to name, so the entry
// itself is the name: it is the repository, and it stays put however many
// worktrees are added.
func mainTree(dir string) string {
	out, err := run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(out, "\n")
	entry, ok := strings.CutPrefix(first, "worktree ")
	if !ok {
		return ""
	}
	// git writes forward slashes on every platform, and a project's path is
	// spelled with the host's own separator, so the answer is too.
	top, err := run(entry, "rev-parse", "--show-toplevel")
	if err != nil {
		return filepath.Clean(entry)
	}
	return filepath.Clean(strings.TrimRight(top, "\n"))
}
