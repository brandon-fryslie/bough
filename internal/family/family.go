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
// directory. Sharing a name is nothing at all. The repository rule is the one
// that could override this, and does: a home directory that is itself a
// checkout is one project by these rules, with everything under it that is
// not a repository of its own.
//
// This package computes; it does not read. Git and the disk are asked through
// Disk, which the edge supplies, so every rule here is testable from a table.
package family

import (
	"cmp"
	"iter"
	"path"
	"regexp"
	"slices"
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
	// None means nothing claims the path: no agent recorded it as made for a
	// project, it is in no repository (when the repository was consulted),
	// and it is not itself a project with history. The family is the path
	// itself, and it belongs to nobody else's.
	None Evidence = iota

	// Repository means the directory shares a git repository with the family, or
	// no longer exists and its nearest existing ancestor does.
	Repository

	// Recorded means the agent recorded that the directory was made for a project,
	// and exactly one known project is that one.
	Recorded

	// Itself means the path is a project with history, and nothing joins that
	// project to anything. Its family is itself. A path that only lies inside
	// such a project is not this: containment alone joins nothing, or every
	// path under a home directory with history would belong to it.
	Itself
)

// Family is where a directory belongs.
type Family struct {
	// Name is the directory the family is known by: the repository's main
	// working tree as the Disk spells it, or the project's own path as the
	// source spelled it.
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

	// madeFor is every agent's reading of the directories it made.
	madeFor []agent.MadeFor

	// known is every project with history, by the normalised form of its path.
	known map[string]*known

	// projects are the projects with history as the sources reported them,
	// which Projects gathers into families.
	projects []agent.Project
}

// known is one directory that has history, with whatever every source that
// saw it recorded about it.
type known struct {
	// path is the first spelling seen, which names the family.
	path string

	// spellings is every spelling any source recorded. A record is written in
	// its agent's own form, which may keep what normalising folds away, so a
	// record is asked about each of them and not only the one that came first.
	spellings []string
}

// New resolves families among the projects with history, from each agent's
// record of the directories it made and the repository consulted through disk.
func New(projects []agent.Project, madeFor []agent.MadeFor, disk Disk) *Resolver {
	return &Resolver{RepositoryConsulted: true, disk: disk, madeFor: madeFor, known: index(projects), projects: projects}
}

// WithoutRepository resolves families from the agents' records alone, for
// --no-repo. A directory's own repository is never asked about, so a worktree
// the agent did not mark as one stays its own family.
func WithoutRepository(projects []agent.Project, madeFor []agent.MadeFor) *Resolver {
	return &Resolver{disk: nowhere{}, madeFor: madeFor, known: index(projects), projects: projects}
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
		if !slices.Contains(k.spellings, p.Path) {
			k.spellings = append(k.spellings, p.Path)
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

// absolute matches a path that starts at a root: unix, a Windows drive, or a
// Windows network share, which the first alternative covers.
var absolute = regexp.MustCompile(`^(?:/|[A-Za-z]:/)`)

// resolve carries the paths already followed through their records, so a
// pair of directories each recorded as made for the other cannot chase one
// another forever.
func (r *Resolver) resolve(p string, followed map[string]bool) Family {
	// Three questions, always in this order.
	//
	// The agent's record comes first. A scratchpad is where a session
	// experiments, and nine on the history this was built against had a
	// throwaway repository inside them; the record says whose work that was,
	// where the repository would only say it was its own.
	if home, ok := r.recorded(p, followed); ok {
		return Family{Name: home.Name, Evidence: Recorded}
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

	if k, ok := r.known[agent.NormalisePath(p)]; ok {
		return Family{Name: k.path, Evidence: Itself}
	}
	return Family{Name: p, Evidence: None}
}

// recorded follows what the agents recorded in p: the one known project the
// directory p lies in was made for, resolved in turn. A record that names no
// known project, or more than one, since the form it is written in is lossy,
// is no record.
func (r *Resolver) recorded(p string, followed map[string]bool) (Family, bool) {
	key := agent.NormalisePath(p)
	if followed[key] {
		return Family{}, false
	}
	followed[key] = true

	matched := map[string]*known{}
	for _, madeFor := range r.madeFor {
		made, ok := madeFor(p)
		if !ok {
			continue
		}
		for candidate, other := range r.known {
			if slices.ContainsFunc(other.spellings, made.For) {
				matched[candidate] = other
			}
		}
	}
	if len(matched) != 1 {
		return Family{}, false
	}
	for _, other := range matched {
		// The path as the source spelled it, which may not be the form the
		// walk climbs on.
		return r.resolve(slashed(other.path), followed), true
	}
	return Family{}, false
}

// slashed puts a path into the form the walk runs on: one separator, case
// kept. The case has to stay, since the disk is asked about these and a
// case-sensitive filesystem will not find a lower-cased spelling of a
// directory that exists.
func slashed(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	// A Windows network share, \\server\share, starts with two separators,
	// and cleaning folds them into one that names the current drive instead.
	if strings.HasPrefix(p, "//") {
		return "/" + path.Clean(p[1:])
	}
	p = path.Clean(p)
	// path.Dir takes "C:/x" down to "C:", which on Windows is not the root of
	// the drive but wherever the process happens to be on it.
	if drive.MatchString(p) {
		p += "/"
	}
	return p
}

// drive matches a bare Windows drive designator.
var drive = regexp.MustCompile(`^[A-Za-z]:$`)

// share matches the root of a Windows network share, above which there is
// nothing to walk to.
var share = regexp.MustCompile(`^//[^/]+/[^/]+$`)

// ancestors yields p and then each directory above it, ending at the root.
func ancestors(p string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for {
			if !yield(p) || share.MatchString(p) {
				return
			}
			parent := slashed(path.Dir(p))
			if strings.HasPrefix(p, "//") {
				parent = slashed("/" + path.Dir(p[1:]))
			}
			if parent == p || parent == "." {
				return
			}
			p = parent
		}
	}
}

// Project is one agent's history of one family: what a person means by a
// project. The worktrees, subdirectories and scratchpads that agent worked in
// are its members, rather than projects of their own named after whatever
// the agent called a worktree.
//
// A family two agents worked on is two projects, one per agent. Whether those
// should be one is a separate question from whether two directories are one.
type Project struct {
	// Path is the directory the family is known by, as Family.Name spells it.
	// It need not have history of its own: a repository whose every sitting
	// ran in a worktree is still named after its main tree.
	Path string

	// Agent is the ID of the agent whose history this is.
	Agent string

	// Members are the directories this agent has history in that belong to
	// the family, ordered by path. There is always at least one.
	Members []agent.Project
}

// Name is what a person calls the project: the last element of its path.
func (p Project) Name() string { return path.Base(slashed(p.Path)) }

// Key identifies the project among all of them: its family and its agent.
// Two projects with the same key are the same project, however their members
// were spelled.
func (p Project) Key() string { return agent.NormalisePath(p.Path) + " " + p.Agent }

// Is reports whether the project is one of family f's: one agent's history
// of it.
func (p Project) Is(f Family) bool { return agent.NormalisePath(p.Path) == f.Key() }

// Holds reports whether a directory is this project's own: the directory the
// family is known by, or one of its members.
//
// Nothing is resolved here, so nothing reads the disk: a directory that is
// neither is not this project's, even when git would say it shares the
// repository. That keeps the question answerable where only sessions are.
func (p Project) Holds(dir string) bool {
	want := agent.NormalisePath(dir)
	return want == agent.NormalisePath(p.Path) || slices.ContainsFunc(p.Members, func(m agent.Project) bool {
		return agent.NormalisePath(m.Path) == want
	})
}

// Projects gathers the projects the resolver was made from into one per
// family and agent, in the order the agents first appear and then by name
// and path.
func (r *Resolver) Projects() []Project {
	var out []Project
	at := map[string]int{}
	agents := map[string]int{}
	for _, p := range r.projects {
		if _, ok := agents[p.Source]; !ok {
			agents[p.Source] = len(agents)
		}
		// [LAW:one-source-of-truth] A member's family is what Resolve says,
		// the same answer anything else asking about that directory gets.
		group := Project{Path: r.Resolve(p.Path).Name, Agent: p.Source}
		i, ok := at[group.Key()]
		if !ok {
			i = len(out)
			at[group.Key()] = i
			out = append(out, group)
		}
		out[i].Members = append(out[i].Members, p)
	}
	for i := range out {
		slices.SortStableFunc(out[i].Members, func(a, b agent.Project) int {
			return strings.Compare(a.Path, b.Path)
		})
	}
	slices.SortStableFunc(out, func(a, b Project) int {
		if a.Agent != b.Agent {
			return agents[a.Agent] - agents[b.Agent]
		}
		// Then by path, so two projects of one name keep an order between runs.
		return cmp.Or(strings.Compare(a.Name(), b.Name()), strings.Compare(a.Path, b.Path))
	})
	return out
}
