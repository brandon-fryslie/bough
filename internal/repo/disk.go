package repo

import (
	"os"
	"path/filepath"
	"slices"
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
// A checkout's own top level is that tree unless the checkout is a linked
// worktree, which git shows by keeping its git directory apart from the common
// one. Then the tree is the first checkout git lists. That listing names a
// bare repository first, marked bare, and names a submodule by its git
// directory under .git/modules, which is nobody's project: the submodule's
// checkout is the top level its git directory is configured with.
//
// One layout has no answer. A repository whose git directory was moved out
// with --separate-git-dir lists that directory as its main tree, and nothing
// in it records where the checkout is, so a linked worktree of it names no
// tree and stands alone.
func mainTree(dir string) string {
	out, err := run(dir, "rev-parse", "--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		return ""
	}
	// Only the top level is always absolute; the two git directories are
	// written relative to dir when they sit inside it.
	top, gitDir, common := lines[0], resolved(dir, lines[1]), resolved(dir, lines[2])
	if gitDir == common {
		return top
	}
	out, err = run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	for _, stanza := range strings.Split(out, "\n\n") {
		lines := strings.Split(stanza, "\n")
		if slices.Contains(lines, "bare") {
			continue
		}
		tree, _ := strings.CutPrefix(lines[0], "worktree ")
		if resolved(dir, tree) != common {
			return tree
		}
		out, err := run(common, "rev-parse", "--show-toplevel")
		if err != nil {
			return ""
		}
		return strings.TrimRight(out, "\n")
	}
	return ""
}

// resolved is p, taken relative to dir, with every symlink resolved, so two
// spellings git uses for one directory compare equal.
func resolved(dir, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
