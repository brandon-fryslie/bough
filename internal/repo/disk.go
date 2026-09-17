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
// one. Then the tree is the first entry git lists, which is the main tree in
// the ordinary case and in two layouts is not a checkout at all.
//
// A bare repository lists itself, marked bare. It has no main tree, and its
// checkouts are listed by name, not by age, so naming the family after one
// would rename it whenever a worktree was added ahead of it alphabetically.
// The repository itself is the one name that stays put.
//
// A submodule lists its git directory under .git/modules, which is nobody's
// project; the checkout is the top level that directory is configured with.
// A repository made with --separate-git-dir lists its git directory the same
// way and records no checkout at all, so a linked worktree of it names no tree
// and stands alone.
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
	main, _, _ := strings.Cut(out, "\n\n")
	entry := strings.Split(main, "\n")
	tree, _ := strings.CutPrefix(entry[0], "worktree ")
	switch {
	case slices.Contains(entry, "bare"):
		return common
	case resolved(dir, tree) == common:
		out, err := run(common, "rev-parse", "--show-toplevel")
		if err != nil {
			return ""
		}
		return strings.TrimRight(out, "\n")
	default:
		return tree
	}
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
