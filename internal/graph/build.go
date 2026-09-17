package graph

import (
	"fmt"
	"maps"
	"math"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/metrics"
	"github.com/nickelsec/bough/internal/repo"
	"github.com/nickelsec/bough/internal/rollup"
	"github.com/nickelsec/bough/internal/segment"
)

// Options carry the tuning from each stage, so a caller can change how the work
// is divided without this package knowing what the knobs mean.
type Options struct {
	Segment segment.Options
	Rollup  rollup.Options
	Links   rollup.LinkOptions

	// Tool is the version string recorded in the output.
	Tool string

	// Repo is the project's git history, when the caller read it.
	//
	// Passed in rather than read here. Building a graph is arithmetic over
	// sessions, and a function that shells out to git cannot be tested without
	// a filesystem and a git to shell out to. Reading the repository is the
	// caller's business; deciding what the commits mean is this package's.
	//
	// A zero History means it was not read, which is not the same as a
	// repository with no commits in it: a hash the transcript carried stays as
	// it is rather than being cleared as unreachable.
	Repo repo.History

	// Ambience tells the agent's own files from the user's work. It carries
	// what every agent reads out of the directories it makes, which only the
	// caller can supply, since this package may not see an agent.
	//
	// Left empty, every edit made in a Claude Code worktree reads as the
	// agent's own bookkeeping, which is why DefaultOptions asks for it.
	Ambience metrics.Ambience

	// Elsewhere is the edge's answer about where the sittings' work outside
	// the project landed, which Visited says how to ask for. Left empty,
	// nothing is recorded as done elsewhere.
	Elsewhere Elsewhere

	// MinVisit is the fewest edits a sitting's work in another family needs to
	// be recorded, when it committed nothing there.
	MinVisit int

	// Now supplies the timestamp, so tests can pin it.
	Now func() time.Time
}

// DefaultOptions are the fitted defaults for every stage, with every agent's
// reading of the directories it makes.
//
// [LAW:types-are-the-program] The readings are a parameter rather than a field
// to fill in afterwards: an empty Ambience is a valid value that quietly counts
// worktree code as the agent's bookkeeping, so a caller has to say what it is.
func DefaultOptions(made []agent.MadeFor) Options {
	return Options{
		Segment:  segment.DefaultOptions(),
		Rollup:   rollup.DefaultOptions(),
		Links:    rollup.DefaultLinkOptions(),
		Ambience: metrics.Ambience{Made: made},
		MinVisit: 2,
		Now:      time.Now,
	}
}

// Build turns a project's sessions into a graph.
//
// Goals from every session are gathered and ordered by when they happened, so
// a project worked on across several sessions reads as one run of work rather
// than as separate piles. Sessions are an artefact of how the agent stores
// things and mean little to the person who did the work. So are the
// directories they ran in: the sessions are those of every member of the
// project's family, and a worktree's sittings sit among the checkout's.
func Build(p family.Project, sessions []agent.Session, opt Options) Graph {
	if opt.Now == nil {
		opt.Now = time.Now
	}

	sessions = prepared(p, sessions)

	// Each commit is matched against the repository it was made in: the
	// project's own against the project's, and one made elsewhere against
	// that family's.
	made := committed(sessions)
	repoRead := fromRepo(opt.Repo, slices.DeleteFunc(slices.Clone(made), func(c *agent.Commit) bool { return c.Dir != "" }))
	opt.Elsewhere.confirm(p, made)

	// A sitting is divided out of one session, so the directory its session
	// ran in is the one its relative paths start from.
	type sitting struct {
		goal      rollup.Goal
		start     time.Time
		title     string
		elsewhere []Visit
	}
	sittings := make([]sitting, 0, len(sessions))
	for _, sess := range sessions {
		tasks := segment.Split(sess.Turns, opt.Segment)
		for _, g := range rollup.Group(tasks, opt.Rollup) {
			away := visits(p, g.Turns(), sess.Dir, opt)
			for j := range g.Tasks {
				g.Tasks[j].Turns = ours(g.Tasks[j].Turns)
			}
			sittings = append(sittings, sitting{goal: g, start: first(g.Turns()), title: sess.Title, elsewhere: away})
		}
	}
	// By when each started. Stable, so goals starting at the same moment stay
	// in the order their sessions were read.
	slices.SortStableFunc(sittings, func(a, b sitting) int { return a.start.Compare(b.start) })
	goals := make([]rollup.Goal, len(sittings))
	for i, s := range sittings {
		goals[i] = s.goal
	}

	g := Graph{
		Schema:    SchemaVersion,
		Generated: opt.Now().UTC(),
		Tool:      opt.Tool,
		Project: Project{
			Name:        p.Name(),
			Path:        p.Path,
			Directories: directories(p),
			Agent:       p.Agent,
			Sessions:    len(sessions),
			RepoRead:    repoRead,
		},
	}

	var everyTurn []agent.Turn
	for i, goal := range goals {
		turns := goal.Turns()
		everyTurn = append(everyTurn, turns...)

		out := Goal{
			ID:        fmt.Sprintf("g%d", i+1),
			Label:     rollup.Label(turns),
			Title:     sittings[i].title,
			Period:    rollup.Period(first(turns), last(turns)),
			Stats:     statsOf(turns, opt.Ambience),
			Elsewhere: sittings[i].elsewhere,
		}
		for j, t := range goal.Tasks {
			out.Tasks = append(out.Tasks, Task{
				ID:      fmt.Sprintf("g%d.t%d", i+1, j+1),
				Label:   rollup.Label(t.Turns),
				Reasons: reasonsOf(t),
				Stats:   statsOf(t.Turns, opt.Ambience),
				Turns:   turnsOf(t.Turns),
			})
		}
		g.Goals = append(g.Goals, out)
	}

	for _, l := range rollup.Links(goals, opt.Ambience, opt.Links) {
		g.Links = append(g.Links, Link{
			From:   fmt.Sprintf("g%d", l.From+1),
			To:     fmt.Sprintf("g%d", l.To+1),
			Files:  l.Files,
			Weight: l.Weight,
		})
	}

	// Goals are ordered by when they started, but two sittings in different
	// worktrees run at once, so one goal's turns can end after the next one's
	// begin. Measured in goal order those overlaps read as time running
	// backwards: low-talker, read whole, came to minus 927 active minutes.
	slices.SortStableFunc(everyTurn, func(a, b agent.Turn) int { return a.At.Compare(b.At) })
	g.Totals = statsOf(everyTurn, opt.Ambience)
	return g
}

func statsOf(turns []agent.Turn, ambience metrics.Ambience) Stats {
	s := metrics.Summarise(turns, ambience)
	out := Stats{
		Start:         s.Start,
		End:           s.End,
		SpanMinutes:   int(s.Span.Minutes()),
		ActiveMinutes: int(s.Active.Minutes()),
		Turns:         s.Turns,
		Edits:         s.Edits,
		Files:         s.Files,
		Errors:        s.Errors,
		Churn:         s.Churn,
		LineChurn:     s.LineChurn,
		Models:        s.Models,
		ChurnFile:     s.ChurnFile,
		// Two places is plenty for a hint, and it keeps the same history from
		// producing byte-different output across platforms.
		Struggle: math.Round(s.Struggle()*100) / 100,
	}
	// Left out when the work was charged nothing, which is how a history read
	// before this was recorded arrives.
	if s.Tokens.Total() > 0 {
		out.Tokens = &Tokens{
			Input:      s.Tokens.Input,
			Output:     s.Tokens.Output,
			CacheRead:  s.Tokens.CacheRead,
			CacheWrite: s.Tokens.CacheWrite,
		}
	}
	for i, f := range s.TopFiles {
		if i >= topFileLimit {
			break
		}
		out.TopFiles = append(out.TopFiles, FileCount{Path: f.Path, Edits: f.Edits})
	}
	for _, c := range s.Commits {
		out.Commits = append(out.Commits, commitOf(c))
	}
	return out
}

// topFileLimit keeps the output readable. Beyond a handful the tail is noise,
// and the full list would dwarf everything else in the file.
const topFileLimit = 8

func turnsOf(turns []agent.Turn) []Turn {
	out := make([]Turn, 0, len(turns))
	for _, t := range turns {
		row := Turn{
			At:     t.At,
			Text:   t.Text,
			Task:   t.TaskName,
			Files:  len(t.Files),
			Errors: t.Errors,
		}
		for _, n := range t.Edits {
			row.Edits += n
		}
		for _, d := range t.Delegated {
			row.Delegated = append(row.Delegated, Delegation{Kind: d.Kind, Name: d.Name, Description: d.Description})
		}
		for _, c := range t.Committed {
			row.Committed = append(row.Committed, commitOf(c))
		}
		out = append(out, row)
	}
	return out
}

func reasonsOf(t segment.Task) []string {
	if len(t.Reasons) == 0 {
		return nil
	}
	out := make([]string, len(t.Reasons))
	for i, r := range t.Reasons {
		out[i] = string(r)
	}
	return out
}

func first(turns []agent.Turn) time.Time {
	if len(turns) == 0 {
		return time.Time{}
	}
	return turns[0].At
}

func last(turns []agent.Turn) time.Time {
	if len(turns) == 0 {
		return time.Time{}
	}
	return turns[len(turns)-1].At
}

// matchWindow is how far a commit in the repository may sit from the tool call
// that made it and still be the same commit.
//
// The two happen within a second or two of each other, so this is generous.
// It is not tight enough to worry about: on the history this was fitted to, 47
// of 49 commits landed within five seconds and only one had another commit
// close enough to be mistaken for it.
const matchWindow = 90 * time.Second

// fromRepo fills in what the transcript could not say.
//
// A commit made with git's quiet flag reaches the transcript with no hash,
// because Claude Code recovers the hash by reading what git printed. The
// repository has it, sitting where the transcript already says the project is,
// so the two are matched by time.
//
// The repository is also the better authority when the two disagree. A hash in
// a transcript was true when it was written; rebasing or amending afterwards
// leaves it pointing at something the repository can no longer reach, and a
// hash nobody can look up is worse than none.
//
// Nothing here is required. A project that has moved, was never a repository,
// or is on a machine without git leaves the transcript's own account standing.
// fromRepo fills in what the transcript could not say, and reports whether the
// repository was actually consulted.
func fromRepo(h repo.History, made []*agent.Commit) bool {
	// Nothing was consulted, so nothing can be confirmed or contradicted. The
	// hashes the transcript carried stay as they are: unverified, but the best
	// that is known. Clearing them here would empty every hash on a machine
	// with no git installed.
	if !h.Read {
		return false
	}
	for _, c := range pair(made, h.Commits, matchWindow) {
		// The repository was read and has no commit here, so any hash the
		// transcript carried is one the repository can no longer reach:
		// rebased, amended, or dropped. Showing it would offer the reader
		// something to check that does not check out, which is worse than
		// showing nothing.
		c.SHA = ""
		c.Branch = ""
	}
	return true
}

// directories are the member directories the project's sessions were read
// from.
func directories(p family.Project) []string {
	dirs := make([]string, len(p.Members))
	for i, m := range p.Members {
		dirs[i] = m.Path
	}
	return dirs
}

// prepared is the sessions as every stage below reads them: copied, each
// commit placed, and delegated work folded in.
func prepared(p family.Project, sessions []agent.Session) []agent.Session {
	// Everything below writes into the turns it is given: placing a commit
	// rewrites its directory, and matching against the repository rewrites the
	// hashes. Those edits used to land in the caller's own slices, so calling
	// Build twice on one set of sessions gave two different answers and
	// nothing else could reuse them afterwards.
	sessions = clone(sessions)

	// Before delegated work is folded into the turn that asked for it: a
	// sub-agent's commit moved relative to the directory its own session ran
	// in, which may be another of the project's directories.
	anchor(p, sessions)

	// Delegated work belongs inside the turn that asked for it, so it is put
	// back before anything is measured or divided. Doing it here rather than in
	// each agent keeps the sub-agent's own session intact up to this point,
	// which is what makes its prompts and tokens countable at all.
	return fold(sessions)
}

// committed is every commit the agent made, gathered before any of them is
// matched.
//
// Matching one at a time as they were walked let the order sessions happened
// to be in decide the answer. A repository commit is claimed by the first
// agent commit to reach it, so when two fell inside the same window the one
// visited first took it, whether or not it was the closer. Sessions are
// grouped by file rather than by time, so that order is not even the order the
// work happened in.
func committed(sessions []agent.Session) []*agent.Commit {
	var made []*agent.Commit
	for _, sess := range sessions {
		for i := range sess.Turns {
			for j := range sess.Turns[i].Committed {
				made = append(made, &sess.Turns[i].Committed[j])
			}
		}
	}
	return made
}

// confirm matches each commit made in another family against that family's
// repository, all of one family's together, as fromRepo does the project's.
func (e Elsewhere) confirm(p family.Project, made []*agent.Commit) {
	byFamily := map[string][]*agent.Commit{}
	for _, c := range made {
		if pl, ok := e.committedIn(p, c.Dir); ok {
			byFamily[pl.Family.Key()] = append(byFamily[pl.Family.Key()], c)
		}
	}
	// A family missing from Repos was not read, which fromRepo takes as it
	// takes the project's own unread repository: the hashes stand unchecked.
	for key, commits := range byFamily {
		fromRepo(e.Repos[key], commits)
	}
}

// here reports whether a commit made in a session that ran in from was made
// in one of this project's own directories.
//
// A command that does not move is running where the session is, which is here.
//
// So is one that moves somewhere relative. `cd internal && git commit` runs in
// a subdirectory of the session, which is still this repository, and comparing
// the bare "internal" against an absolute project path never matched: a real
// commit in this project was dropped from the diagram without a word. Only an
// absolute path can name somewhere else, because only an absolute path says
// where it starts from.
//
// A path that climbs out with .. is the exception. It is relative but it can
// leave the session's directory, so it is resolved against that directory and
// compared.
func here(in, from string, p family.Project) bool {
	if in == "" {
		return true
	}
	if !rooted(in) {
		if !strings.Contains(in, "..") {
			return true
		}
		return p.Holds(path.Join(agent.NormalisePath(from), in))
	}
	return p.Holds(in)
}

// rooted reports whether a path says for itself where it starts.
//
// Both spellings of a Windows drive count, since a transcript carries "d:/x"
// and "/d/x" for the same place, and both are absolute.
func rooted(p string) bool {
	p = strings.ReplaceAll(p, `\`, "/")
	return strings.HasPrefix(p, "/") || driveLetter.MatchString(p)
}

// driveLetter matches a path that opens with a Windows drive, as "d:/work".
var driveLetter = regexp.MustCompile(`^[a-zA-Z]:/`)

// pair matches the commits an agent made to the ones in the repository,
// closest pair first, and returns the ones nothing matched.
//
// Matching them one at a time let the order sessions were walked in decide the
// answer: a repository commit went to the first agent commit that reached it,
// so when two fell inside the same window the one visited first took it,
// closer or not. Sessions are grouped by file rather than by time, so that
// order is not even the order the work happened in.
//
// Deciding all of them together removes the question. Every pair inside the
// window is measured, the closest is settled first, and each side drops out
// once it is spoken for. Ties fall to the earlier commit so the same input
// always gives the same answer.
func pair(made []*agent.Commit, have []repo.Commit, window time.Duration) []*agent.Commit {
	type link struct {
		made, have int
		gap        time.Duration
	}

	var links []link
	for m, c := range made {
		if c == nil || c.At.IsZero() {
			continue
		}
		for h := range have {
			if have[h].When.IsZero() {
				continue
			}
			gap := have[h].When.Sub(c.At)
			if gap < 0 {
				gap = -gap
			}
			if gap > window {
				continue
			}
			links = append(links, link{made: m, have: h, gap: gap})
		}
	}

	sort.SliceStable(links, func(i, j int) bool {
		if links[i].gap != links[j].gap {
			return links[i].gap < links[j].gap
		}
		if links[i].have != links[j].have {
			return links[i].have < links[j].have
		}
		return links[i].made < links[j].made
	})

	tookMade := make([]bool, len(made))
	tookHave := make([]bool, len(have))
	for _, l := range links {
		if tookMade[l.made] || tookHave[l.have] {
			continue
		}
		tookMade[l.made] = true
		tookHave[l.have] = true

		found := have[l.have]
		c := made[l.made]
		c.SHA = found.SHA
		c.Subject = found.Subject
		c.Added = found.Added
		c.Removed = found.Removed
	}

	var missed []*agent.Commit
	for i, c := range made {
		if c != nil && !tookMade[i] {
			missed = append(missed, c)
		}
	}
	return missed
}

// commitOf copies a commit across the boundary into the shape the output uses.
//
// Written out twice before, once for a task's list and once for a prompt's, so
// a field added to one arrived in the graph from one place and not the other.
func commitOf(c agent.Commit) Commit {
	return Commit{
		SHA:     c.SHA,
		Kind:    c.Kind,
		Branch:  c.Branch,
		Subject: c.Subject,
		Added:   c.Added,
		Removed: c.Removed,
	}
}

// clone copies the sessions deeply enough that nothing below can be seen by
// the caller.
//
// Every slice and map a turn owns is copied, not only the ones known to be
// written today. This used to copy just the commit list, on the reasoning that
// the counting maps were only ever read, and that stopped being true the moment
// a sub-agent was folded in: its counts were added to the caller's own maps and
// its task name written into the caller's hand-offs, so a second build counted
// the sub-agent twice. Deciding what is safe to share means knowing every
// stage below, which is the knowledge this function exists to make unnecessary.
func clone(sessions []agent.Session) []agent.Session {
	out := make([]agent.Session, len(sessions))
	for i, s := range sessions {
		s.Turns = slices.Clone(s.Turns)
		for j := range s.Turns {
			t := &s.Turns[j]
			t.Tools = maps.Clone(t.Tools)
			t.Files = maps.Clone(t.Files)
			t.Edits = maps.Clone(t.Edits)
			t.Lines = maps.Clone(t.Lines)
			t.Models = maps.Clone(t.Models)
			t.Delegated = slices.Clone(t.Delegated)
			t.Committed = slices.Clone(t.Committed)
		}
		out[i] = s
	}
	return out
}
