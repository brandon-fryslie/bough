// Package agent defines the boundary between bough and the coding agents whose
// history it reads.
//
// Agents store their history in very different ways. Claude Code writes one
// JSONL file per session. Cursor keeps chat in a SQLite database shared by a
// whole workspace. Others will differ again. Nothing above this package is
// allowed to know which of those it is looking at, so a Source hands back
// normalised Turn values and keeps its own storage quirks to itself.
//
// The rule that keeps this honest: internal/segment and internal/rollup must
// never import an agent implementation. They see Turn and nothing else.
package agent

import (
	"path"
	"regexp"
	"strings"
	"time"
)

// Agent is what bough knows about a coding agent before reading any of its
// history: what it is called, where it keeps that history, and how to read it.
//
// Every view of an agent's identity reads it from here, the flag, the terminal
// and the page alike. Spelling a name out anywhere else is how the page once
// kept a table of its own that nothing checked against this one.
type Agent struct {
	// ID is how the history names its agent, for example "claude-code". It is
	// what Project.Source and the graph carry.
	ID string

	// Flag is how --agent spells it, for example "claude".
	Flag string

	// Name is what a person reads, for example "Claude Code". "claude-code" is
	// the name of a source; "Claude Code" is the name of a tool.
	Name string

	// History is where the agent keeps its history, relative to the home
	// directory.
	History string

	// Open reads history kept under root.
	Open func(root string) Source

	// MadeFor reads which project a directory was made for, from the path the
	// agent gave it. Every agent has one, and one that makes no directories on
	// a project's behalf answers nil for every path.
	MadeFor MadeFor
}

// MadeFor is what an agent wrote into the path of a directory it made on a
// project's behalf, a scratchpad or a worktree, read back. The path may be
// anywhere inside such a directory. The answer is false for a path in no such
// directory, which is the ordinary case.
type MadeFor func(path string) (made Made, ok bool)

// Made is one directory an agent made on a project's behalf, as read from a
// path inside it.
type Made struct {
	// For asks whether a candidate is the project the directory was made for.
	//
	// It is a question rather than a path because the agent writes the
	// project's name in its own form, and Claude Code's form cannot be read
	// back (docs/format.md): a dash in the original is indistinguishable from
	// a separator. So the agent, which knows the form, is asked whether a
	// candidate matches, and the core compares candidates it knows about
	// without learning the form itself. More than one may match, and what
	// that means is the core's decision.
	For func(candidate string) bool

	// Within is the rest of the path below that directory, with slashes for
	// separators: "/src/main.go" for a file in it, empty for the directory
	// itself. It is a path like any other, so a directory made inside this
	// one is found by asking about it in turn. Where the directory sits says whose work it holds; this says
	// what the work was. A worktree lives under a project's .claude directory,
	// and a file in it is the project's code however that location reads.
	Within string
}

// Source reads one coding agent's history from one place.
type Source interface {
	// Detect reports the projects this agent has history for on this machine.
	// A source that is not installed returns no projects and no error.
	Detect() ([]Project, error)

	// Sessions reads every session belonging to the projects, each session
	// once however many of them its records are spread over. One project read
	// whole is several directories, and an agent that resumes a session from
	// another directory replays its earlier records there. A session that
	// cannot be read is reported through the error return without stopping
	// the ones that can, since partial history is still worth showing.
	Sessions(projects ...Project) ([]Session, error)
}

// Project is a codebase an agent has worked on.
type Project struct {
	// Name is what the user would call this project, usually the directory name.
	Name string

	// Path is the working directory, recovered from the session records rather
	// than from any directory name the agent may have mangled.
	Path string

	// Source is the ID of the agent this came from.
	Source string

	// Ref locates the project inside the agent's own storage. Its meaning is
	// private to the Source that produced it.
	Ref string

	// LastWorked is when the history was last added to. It comes from the
	// files rather than from their contents, so listing projects stays cheap
	// even on a large history.
	LastWorked time.Time

	// Bytes is roughly how much history there is, again from the files rather
	// than their contents. It says which projects are substantial, not how
	// many prompts they hold.
	Bytes int64
}

// Session is one continuous stretch of work as the agent recorded it.
//
// Do not mistake this for a unit of work. On Claude Code a single session can
// run for nine days and cover a dozen unrelated things, which is the whole
// reason bough has to segment from the inside.
type Session struct {
	ID    string
	Title string // the agent's own label for the session, when it has one
	Turns []Turn

	// Source is the ID of the agent that recorded the session, as
	// Project.Source is for a project. A family two agents worked in is read
	// as one, and this is what keeps each sitting its own agent's.
	Source string

	// Dir is the directory the session ran in, the project it was read for.
	// A commit whose command moved somewhere relative moved from here, and a
	// project read whole from several directories has sessions from each.
	Dir string

	// ParentID names the session that delegated this work, empty when a person
	// started it.
	//
	// An agent that spawns another gets a session of its own, because the
	// sub-agent has its own prompts and its own token spend and folding those
	// into the parent would hide work that did happen. But it is not a separate
	// stretch of work: it runs inside the turn that asked for it, and drawing it
	// alongside the parent says the person started two things when they started
	// one.
	ParentID string
}

// Delegation is a unit of work the agent handed to a sub-agent.
//
// Each field holds one fact whichever agent recorded it, and a fact the agent
// did not record is left empty rather than filled from another field. Claude
// names the sort of sub-agent and writes a brief but never names the task;
// Codex names the task, sometimes the sort, and encrypts the brief. When one
// field used to carry whichever name was to hand, every reader had to know
// which agent wrote it to know what the word meant.
type Delegation struct {
	// Kind is the sort of sub-agent, for example "Explore" or "Plan". Empty
	// when the agent does not name a type.
	Kind string

	// Name is what this particular piece of work was called, for example
	// "pixel_art".
	Name string

	// Description is what the sub-agent was asked to do, in the words used at
	// the time. Empty when the brief is unreadable: Codex encrypts it.
	Description string
}

// Commit is a commit the agent made while working on a turn.
//
// This is the only thing in the graph that is not inferred. Everything else,
// where a task starts and ends and how hard it looked, comes from heuristics.
// A commit either exists in the repository or it does not, which makes it the
// one claim a reader can check.
//
// It is also incomplete by nature: a commit the person typed themselves never
// appears in any agent's history, so this says what the agent committed and
// nothing about the rest.
type Commit struct {
	// SHA is the abbreviated hash the agent recorded, when it recorded one.
	// Claude Code reads the hash back out of what git printed, so a commit made
	// quietly has none until the repository is consulted.
	SHA string

	// Kind is what happened, for example "committed" or "amended".
	Kind string

	// Branch is where it landed, when the agent recorded one.
	Branch string

	// At is when the commit call returned, which is within a second or two of
	// the commit itself. It is what lets a commit with no hash be matched
	// against the repository.
	At time.Time

	// Dir is where the commit was made, when the command moved somewhere first.
	// Empty means the directory the session ran in, and a relative path is
	// relative to it.
	//
	// A session about one project often commits in another, a tool and its
	// website being worked on together for instance. Those commits are real but
	// they belong to that other repository, and this is what lets them be told
	// apart.
	Dir string

	// Subject, Added and Removed come from the repository rather than the
	// transcript, and are empty when it could not be read. The transcript knows
	// a commit happened; only the repository knows how big it was.
	Subject string
	Added   int
	Removed int
}

// Tokens is what a stretch of work cost, in the four counts the transcript
// keeps.
//
// The interesting one is CacheRead, and it is interesting because of its size.
// The model has no memory between messages, so every reply re-reads the whole
// conversation so far along with the files and the instructions. Caching makes
// each re-read cheap and it is paid every turn, which on the corpus this was
// built against came to around 596 times the output and most of the bill.
type Tokens struct {
	// Input is text sent fresh, uncached. It is close to nothing in practice.
	Input int

	// Output is what the model wrote. The part everyone pictures, and about a
	// tenth of the cost.
	Output int

	// CacheRead is re-reading what was already sent. Paid every turn.
	CacheRead int

	// CacheWrite is storing context so it can be re-read cheaply. Paid once.
	CacheWrite int
}

// Add sums another set of counts into this one.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
}

// Total is every token the work was charged for.
func (t *Tokens) Total() int {
	return t.Input + t.Output + t.CacheRead + t.CacheWrite
}

// Turn is one human prompt and everything the agent did in response.
//
// This is the unit every later stage works from. Anything agent-specific has
// already been resolved by the time a Turn exists.
type Turn struct {
	At time.Time

	// Text is what the human typed, empty when nobody typed anything.
	Text string

	// TaskName is what the work was called when another agent handed it over
	// rather than a person asking for it. A sub-agent's turn has no prompt, and
	// writing the task's name into Text made a field documented as the
	// person's own words hold something no person wrote.
	TaskName string

	// Tools counts calls by tool name.
	Tools map[string]int

	// Files counts every file the agent touched, keyed by normalised path.
	Files map[string]int

	// Edits counts only files the agent changed, a subset of Files.
	Edits map[string]int

	// Lines counts how much changed in each of those files. Edits counts
	// calls, which says a file was touched; this says whether that was a typo
	// or a rewrite. On the corpus this was fitted to, 246 edits changed two
	// lines or fewer and 75 changed over a hundred.
	Lines map[string]int

	// Errors is how many tool calls came back as failures.
	Errors int

	// Tokens is what answering this turn was charged for.
	Tokens Tokens

	// Models counts output tokens by model name. A project usually has one,
	// but a model changed partway through is worth being able to say.
	Models map[string]int

	// Delegated is the sub-agent work this turn started. These are the one place
	// the record holds real branching, and each carries a description written at
	// the time, which makes it a better label than anything inferred later.
	Delegated []Delegation

	// Committed is what the agent committed during this turn, in order.
	Committed []Commit

	// SegmentHint marks a boundary the agent itself recorded, such as a context
	// compaction. Free evidence, worth more than anything we infer.
	SegmentHint bool
}

// NormalisePath puts a file path into a comparable form.
//
// The same file turns up written several ways across a session, because the
// drive letter changes case between records and separators differ by platform.
// Grouping by path only works once those are settled.
//
// This deliberately does not use path/filepath. A transcript written on
// Windows can be read on any machine, so backslashes have to be understood
// everywhere rather than only where the host happens to use them.
//
// A Windows drive is folded to the "d:/" spelling whether it arrived that way
// or as the "/d/" a unix style shell writes. One project's commits arrived as
// both in the same session, and they are one directory: comparing them without
// this said the work happened somewhere else and threw it away. There were two
// normalisers here doing this differently, and the one that did not understand
// "/d/" was the one the agents called.
func NormalisePath(p string) string {
	if p == "" {
		return ""
	}
	p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	if m := shellDrive.FindStringSubmatch(p); m != nil {
		p = m[1] + ":/" + m[2]
	}
	return path.Clean(p)
}

// shellDrive matches the "/d/some/path" a unix style shell uses for a Windows
// drive, so it can be written the way the transcript records it.
var shellDrive = regexp.MustCompile(`^/([a-z])/(.*)$`)
