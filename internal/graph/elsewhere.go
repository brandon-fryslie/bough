package graph

import (
	"cmp"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/repo"
)

// A session regularly works in a sibling project: a tool and its website, a
// fix made in a dependency. On the history this was built against, 789 edits
// that were neither the agent's own nor temporary landed in another family,
// across 73 of 3,032 sittings, and 299 commits were made in one (2026-09-14).
// That work belongs to neither project's diagram as its own, but a sitting
// that did it should say so. This file records it.
//
// Where a path leads is a question for the machine: symlinks are followed on
// disk and families are resolved through git. So the build asks, through
// Visited, and the edge answers, through Elsewhere, and everything here is
// arithmetic over the answers.

// Visits are the places a project's sittings worked that only the machine can
// place: the question the edge answers with Elsewhere.
type Visits struct {
	// Files are the files the sittings edited, each absolute: a path recorded
	// relative to the directory its session ran in is joined to it.
	Files []string

	// Dirs are the directories commits were made in outside the project's
	// own directories.
	Dirs []string
}

// Place is what the machine says about one path Visited listed.
type Place struct {
	// Path is where the path leads once symlinks are followed, or the path as
	// asked when nothing is there to follow.
	Path string

	// Exists says something is there.
	Exists bool

	// Family is the family Path belongs to.
	Family family.Family
}

// Elsewhere is the edge's answer to Visited.
type Elsewhere struct {
	// Places answers for every path Visited listed, keyed as it listed them.
	Places map[string]Place

	// Repos is the history of each family a commit was made in, by
	// family.Family.Key. A family missing here was not read.
	Repos map[string]repo.History

	// Temporary are the directories whose contents are thrown away. Nothing
	// done in them, a scratchpad included, is another project's work.
	Temporary []string
}

// Visited lists what the build will ask of Elsewhere about these sessions.
//
// [LAW:one-source-of-truth] The sessions are prepared exactly as Build
// prepares them, so every path Build looks up is one listed here.
func Visited(p family.Project, sessions []agent.Session) Visits {
	files := map[string]bool{}
	dirs := map[string]bool{}
	for _, sess := range prepared(p, sessions) {
		for _, t := range sess.Turns {
			for f := range t.Edits {
				files[located(sess.Dir, f)] = true
			}
			for _, c := range t.Committed {
				dirs[c.Dir] = true
			}
		}
	}
	// A commit with no directory was made in the project's own.
	delete(dirs, "")
	return Visits{Files: slices.Sorted(maps.Keys(files)), Dirs: slices.Sorted(maps.Keys(dirs))}
}

// work is the place at, when what was done there is another family's work.
//
// Not work: a path nothing answered for, which includes a commit made in the
// project's own directories; a path in no family and no repository; one in
// the project's own family; a repository's .git directory; and anything under
// a temporary directory.
func (e Elsewhere) work(p family.Project, at string) (Place, bool) {
	pl, ok := e.Places[at]
	led := agent.NormalisePath(pl.Path)
	ok = ok && pl.Family.Evidence != family.None && !p.Is(pl.Family) &&
		!slices.Contains(strings.Split(led, "/"), ".git") &&
		!slices.ContainsFunc(e.Temporary, func(root string) bool { return under(led, agent.NormalisePath(root)) })
	return pl, ok
}

// CommittedIn is the place a commit was made in dir, when that was another
// family's work: the one rule for which commits are recorded, which the edge
// also asks before reading a repository for one. A directory that has gone
// cannot be placed in any repository, however its nearest surviving ancestor
// would answer.
func (e Elsewhere) CommittedIn(p family.Project, dir string) (Place, bool) {
	pl, ok := e.work(p, dir)
	return pl, ok && pl.Exists
}

// under reports whether p is dir or lies inside it. Both are normalised.
func under(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// visits is the work one sitting did in other families, from the turns it
// holds and the directory its session ran in.
//
// A visit of fewer than opt.MinVisit edits is incidental and left out, unless
// the sitting committed there: a commit is never incidental.
func visits(p family.Project, turns []agent.Turn, dir string, opt Options) []Visit {
	type found struct {
		visit Visit
		edits int
		files map[string]int
	}
	byFamily := map[string]*found{}
	in := func(f family.Family) *found {
		w, ok := byFamily[f.Key()]
		if !ok {
			w = &found{visit: Visit{Family: f.Key(), Path: f.Name, RepoRead: opt.Elsewhere.Repos[f.Key()].Read}, files: map[string]int{}}
			byFamily[f.Key()] = w
		}
		return w
	}

	for _, t := range turns {
		// In order, so the first path into a family names it the same way on
		// every run.
		for _, f := range slices.Sorted(maps.Keys(t.Edits)) {
			at := located(dir, f)
			// [LAW:single-enforcer] The agent's own files are judged here, by
			// the path as recorded, before a symlink can lead one into a
			// project: a note under ~/.claude is still the agent's when it
			// points into a dotfiles checkout.
			if opt.Ambience.Ambient(at) {
				continue
			}
			if pl, ok := opt.Elsewhere.work(p, at); ok {
				w := in(pl.Family)
				// Spelled as the machine led there, not in the lower-cased
				// form paths are compared in.
				w.files[strings.ReplaceAll(pl.Path, `\`, "/")] += t.Edits[f]
				w.edits += t.Edits[f]
			}
		}
		for _, c := range t.Committed {
			if pl, ok := opt.Elsewhere.CommittedIn(p, c.Dir); ok {
				w := in(pl.Family)
				w.visit.Commits = append(w.visit.Commits, commitOf(c))
			}
		}
	}

	visits := make([]Visit, 0, len(byFamily))
	for _, w := range byFamily {
		if w.edits < opt.MinVisit && len(w.visit.Commits) == 0 {
			continue
		}
		for f, n := range w.files {
			w.visit.Files = append(w.visit.Files, FileCount{Path: f, Edits: n})
		}
		slices.SortFunc(w.visit.Files, func(a, b FileCount) int {
			return cmp.Or(cmp.Compare(b.Edits, a.Edits), strings.Compare(a.Path, b.Path))
		})
		visits = append(visits, w.visit)
	}
	slices.SortFunc(visits, func(a, b Visit) int {
		return cmp.Or(cmp.Compare(edits(b), edits(a)), strings.Compare(a.Family, b.Family))
	})
	return visits
}

// edits is how many edits a visit made.
func edits(v Visit) int {
	n := 0
	for _, f := range v.Files {
		n += f.Edits
	}
	return n
}

// located is where a recorded path is: itself when it says where it starts,
// and otherwise joined to the directory the session ran in.
func located(dir, p string) string {
	if rooted(p) {
		return p
	}
	return path.Join(strings.ReplaceAll(dir, `\`, "/"), p)
}

// anchor places every commit once, while each is still in the session that
// ran it: one made in the project's own directories has its directory
// cleared, and one made anywhere else carries the whole directory it was made
// in. Delegated work is folded into another session afterwards, and a
// directory relative to the sub-agent's session means nothing there.
func anchor(p family.Project, sessions []agent.Session) {
	for _, sess := range sessions {
		for i := range sess.Turns {
			for j := range sess.Turns[i].Committed {
				c := &sess.Turns[i].Committed[j]
				c.Dir = placed(c.Dir, sess.Dir, p)
			}
		}
	}
}

// placed is the directory a commit recorded as made in `in` was made in, from
// a session that ran in from: empty when it is one of the project's own.
func placed(in, from string, p family.Project) string {
	if here(in, from, p) {
		return ""
	}
	return located(from, in)
}

// ours is the turns with only the project's own commits in them. The rest are
// recorded as visits, and counting them here too would put work on the
// diagram that was done somewhere else.
func ours(turns []agent.Turn) []agent.Turn {
	out := slices.Clone(turns)
	for i := range out {
		out[i].Committed = slices.DeleteFunc(slices.Clone(out[i].Committed), func(c agent.Commit) bool { return c.Dir != "" })
	}
	return out
}
