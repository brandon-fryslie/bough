// Package family answers one question: which project family does a directory
// belong to.
//
// A family is the set of directories that are the same project. Agents spread
// one project's work over many of them: linked worktrees, subdirectories a
// session happened to start in, scratchpads, and directories that have since
// been deleted. On the history this was built against, 274 project directories
// were 124 projects, and one repository spanned 39 directories (measured
// 2026-09-14). Everything that wants to show a project whole, or attribute an
// edit made somewhere else, asks here.
//
// Two things join directories. Sharing a git repository, meaning the same
// common directory, which covers worktrees and subdirectories and names the
// family after the repository's main working tree. And the agent's own record
// that a directory was made for a project, which is how a scratchpad finds its
// way home. A directory that no longer exists joins the repository of its
// nearest existing ancestor, since a deleted worktree is still that
// repository's work.
//
// Nothing else joins them. Two directories that merely contain one another
// stay apart: /Users/bmf and /Users/bmf/code both have history and contain
// everything, and joining them would fold every project into the home
// directory. Sharing a name is nothing at all.
//
// This package computes; it does not read. Git and the disk are asked through
// Disk, which the edge supplies, so every rule here is testable from a table.
package family

import (
	"iter"
	"path"
	"regexp"
	"strings"

	"github.com/nickelsec/bough/internal/agent"
)

// Disk is what the resolver asks of the machine.
type Disk interface {
	// Exists reports whether dir is a directory on disk.
	Exists(dir string) bool

	// MainTree is the main working tree of the repository dir sits in, or ""
	// when it sits in none. dir exists.
	MainTree(dir string) string
}

// Evidence is what put a directory in its family.
type Evidence int

const (
	// None means nothing is known about the directory. It is not inside any
	// project with history and, if the repository was consulted, not inside
	// a repository. The family is the directory itself.
	None Evidence = iota

	// Repository means the directory shares a git repository with the family, or
	// no longer exists and its nearest existing ancestor does.
	Repository

	// Recorded means the agent recorded that the directory was made for a project,
	// and exactly one known project is that one.
	Recorded

	// Project means the directory is, or lies inside, a project with history, and
	// nothing joins that project to anything. Its family is itself.
	Project
)

// Family is where a directory belongs.
type Family struct {
	// Name is the directory the family is known by: the repository's main
	// working tree, or the project's own path, as the source spelled it.
	Name string

	// Evidence says what joined the directory to the family.
	Evidence Evidence
}

// Key is the form two families are compared on. Two answers with the same key
// are the same family.
func (f Family) Key() string { return agent.NormalisePath(f.Name) }

// Resolver answers which family a directory belongs to.
type Resolver struct {
	// RepositoryConsulted says git was asked. Without it, families come only
	// from what the agent recorded, and a directory that stands alone may only
	// look that way.
	RepositoryConsulted bool

	disk Disk

	// known is every project with history, by the normalised form of its path.
	known map[string]*known
}

// known is one directory that has history, with whatever every source that
// saw it recorded about it.
type known struct {
	path   string
	serves []func(string) bool
}

// New resolves families with the repository consulted through disk.
func New(projects []agent.Project, disk Disk) *Resolver {
	return &Resolver{RepositoryConsulted: true, disk: disk, known: index(projects)}
}

// WithoutRepository resolves families from the agents' records alone, for
// --no-repo. A directory's own repository is never asked about, so a worktree
// the agent did not mark as one stays its own family.
func WithoutRepository(projects []agent.Project) *Resolver {
	return &Resolver{disk: nowhere{}, known: index(projects)}
}

// nowhere is a disk with nothing on it, which is what --no-repo means here:
// every walk up from a directory finds no repository.
type nowhere struct{}

func (nowhere) Exists(string) bool     { return false }
func (nowhere) MainTree(string) string { return "" }

func index(projects []agent.Project) map[string]*known {
	byKey := map[string]*known{}
	for _, p := range projects {
		key := agent.NormalisePath(p.Path)
		k, ok := byKey[key]
		if !ok {
			k = &known{path: p.Path}
			byKey[key] = k
		}
		if p.Serves != nil {
			k.serves = append(k.serves, p.Serves)
		}
	}
	return byKey
}

// Resolve answers for one absolute path, which may be a file or a directory
// and need not exist. A relative path is nowhere in particular, so nothing is
// known about it: resolving it against the directory a session ran in is the
// caller's to do, since only the caller knows that directory.
func (r *Resolver) Resolve(p string) Family {
	p = slashed(p)
	if !absolute.MatchString(p) {
		return Family{Name: p, Evidence: None}
	}
	return r.resolve(p, map[string]bool{})
}

// absolute matches a path that starts at a root, unix or Windows.
var absolute = regexp.MustCompile(`^(?:/|[A-Za-z]:/)`)

// resolve carries the projects already followed through their records, so a
// pair of directories each recorded as made for the other cannot chase one
// another forever.
func (r *Resolver) resolve(p string, followed map[string]bool) Family {
	// [LAW:dataflow-not-control-flow] Three questions, always in this order,
	// each answered by the nearest directory that can answer it.
	//
	// The agent's record comes first. A scratchpad is where a session
	// experiments, and nine on the history this was built against had a
	// throwaway repository inside them; the record says whose work that was,
	// where the repository would only say it was its own.
	for dir := range ancestors(p) {
		k, ok := r.known[agent.NormalisePath(dir)]
		if !ok {
			continue
		}
		if home, ok := r.recorded(k, followed); ok {
			return Family{Name: home.Name, Evidence: Recorded}
		}
	}

	// The repository is asked at the nearest directory that exists, and only
	// there: the answer for a deleted directory is its ancestor's, and the
	// answer for an existing one is its own. An ancestor that exists and is in
	// no repository ends the walk with nothing, which is how containment alone
	// never joins.
	for dir := range ancestors(p) {
		if !r.disk.Exists(dir) {
			continue
		}
		if tree := r.disk.MainTree(dir); tree != "" {
			return Family{Name: tree, Evidence: Repository}
		}
		break
	}

	for dir := range ancestors(p) {
		if k, ok := r.known[agent.NormalisePath(dir)]; ok {
			return Family{Name: k.path, Evidence: Project}
		}
	}

	return Family{Name: p, Evidence: None}
}

// recorded follows what the agent recorded about k: the one known project k
// was made for, resolved in turn. A record that names no known project, or
// more than one, since the form it is written in is lossy, is no record.
func (r *Resolver) recorded(k *known, followed map[string]bool) (Family, bool) {
	key := agent.NormalisePath(k.path)
	if followed[key] {
		return Family{}, false
	}
	followed[key] = true

	var matched []*known
	for candidate, other := range r.known {
		if candidate == key {
			continue
		}
		for _, serves := range k.serves {
			if serves(other.path) {
				matched = append(matched, other)
				break
			}
		}
	}
	if len(matched) != 1 {
		return Family{}, false
	}
	return r.resolve(matched[0].path, followed), true
}

// slashed puts a path into the form the walk runs on: one separator, case
// kept. The case has to stay, since the disk is asked about these and a
// case-sensitive filesystem will not find a lower-cased spelling of a
// directory that exists.
func slashed(p string) string {
	p = path.Clean(strings.ReplaceAll(p, `\`, "/"))
	// path.Dir takes "C:/x" down to "C:", which on Windows is not the root of
	// the drive but wherever the process happens to be on it.
	if drive.MatchString(p) {
		p += "/"
	}
	return p
}

// drive matches a bare Windows drive designator.
var drive = regexp.MustCompile(`^[A-Za-z]:$`)

// ancestors yields p and then each directory above it, ending at the root.
func ancestors(p string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for {
			if !yield(p) {
				return
			}
			parent := slashed(path.Dir(p))
			if parent == p || parent == "." {
				return
			}
			p = parent
		}
	}
}
