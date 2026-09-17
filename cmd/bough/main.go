// Command bough shows the shape of the work in a project's AI coding history.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/agent/registry"
	"github.com/nickelsec/bough/internal/banner"
	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/graph"
	"github.com/nickelsec/bough/internal/pick"
	"github.com/nickelsec/bough/internal/repo"
	"github.com/nickelsec/bough/internal/server"
)

// version is stamped by the release build. Anything else asks the toolchain.
var version = ""

// released reports what to print for --version.
//
// The release workflow passes the tag in, but that is not how most people get
// this. Both installs the readme documents go through the toolchain instead,
// and a plain build has nothing passed in at all, so every one of them used to
// answer "dev" including `go install ...@v0.3.4`. Go records the version it
// resolved, so ask for it rather than claiming not to know.
//
// A build from a working copy has no module version and reports "(devel)".
// That case keeps the revision, which is the part that identifies it, and says
// when the tree had uncommitted changes so a report naming it can be trusted.
func released() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}

	var revision, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 12 {
				s.Value = s.Value[:12]
			}
			revision = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = ", modified"
			}
		}
	}
	if revision == "" {
		return "dev"
	}
	return "dev (" + revision + dirty + ")"
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "bough:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("bough", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		asJSON    = fs.Bool("json", false, "write the graph as JSON instead of text")
		asText    = fs.Bool("text", false, "write to the terminal instead of opening a browser")
		list      = fs.Bool("list", false, "list the projects with history and stop")
		verbose   = fs.Bool("v", false, "include every prompt in the text output, and every directory in --list")
		root      = fs.String("root", "", "read every agent's history from here instead of its usual location")
		out       = fs.String("o", "", "write to this file instead of standard output")
		showVer   = fs.Bool("version", false, "print the version and stop")
		noRepo    = fs.Bool("no-repo", false, "do not read the project's git history")
		agentFlag = fs.String("agent", "all", "which agent history to read: "+strings.Join(registry.Flags(), ", "))
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, usage, strings.Join(registry.Flags(), ", "))
		fs.PrintDefaults()
	}
	// Flags are accepted before or after the project name. The standard parser
	// stops at the first argument that is not a flag, which would silently
	// ignore "bough my-project --json" and print the wrong thing.
	name, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return err
	}

	// Before anything reads the disk, so it answers on a machine with no
	// history on it at all.
	if *showVer {
		fmt.Fprintln(stdout, released())
		return nil
	}

	agents, err := registry.Select(*agentFlag)
	if err != nil {
		return err
	}

	// [LAW:dataflow-not-control-flow] Every agent read goes through the same
	// steps, so --root and the message for a machine with no history mean the
	// same thing whichever agents were asked for.
	sources := make(map[string]agent.Source, len(agents))
	var projects []agent.Project
	var searched []string
	for _, a := range agents {
		dir, err := historyRoot(a, *root)
		if err != nil {
			return err
		}
		src := a.Open(dir)
		sources[a.ID] = src
		searched = append(searched, a.Name+" in "+dir)

		found, err := src.Detect()
		if err != nil {
			return fmt.Errorf("reading %s history in %s: %w", a.Name, dir, err)
		}
		projects = append(projects, found...)
	}
	if len(projects) == 0 {
		return fmt.Errorf("no history found; looked for %s", strings.Join(searched, " and "))
	}

	// Directories are gathered into the projects they are part of before
	// anything is shown, so the listing, the chooser and a name all speak of
	// the same projects.
	made := everyAgentsRecord()
	disk := &repo.Disk{}
	families := resolver(projects, made, disk, *noRepo)
	whole := families.Projects()

	if *list {
		return writeList(stdout, sources, whole, *verbose)
	}

	target, err := choose(whole, families, name, os.Stdin, stderr)
	if errors.Is(err, pick.ErrCancelled) {
		// Backing out is a decision, not a failure. It reaches main as a value
		// so everything deferred on the way here still runs.
		return nil
	}
	if err != nil {
		return err
	}

	sessions, err := read(sources[target.Agent], target)
	if err != nil {
		// Some transcripts may be unreadable while others are fine, so say so
		// and carry on with what did load.
		fmt.Fprintf(stderr, "bough: some history could not be read: %v\n", err)
	}
	if len(sessions) == 0 {
		return fmt.Errorf("no readable history for %s", target.Name())
	}

	g := graph.Build(target, sessions, options(made, disk, target, *noRepo))

	w := stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}

	if *asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(g)
	}

	if useBrowser(*asText, *out, stdout) {
		return browse(g, stderr)
	}
	return graph.WriteText(w, g, *verbose)
}

// everyAgentsRecord is every agent's reading of the directories it makes.
// Every agent, not only the ones asked for: a session of one agent can work in
// a directory another made.
func everyAgentsRecord() []agent.MadeFor {
	all := registry.All()
	made := make([]agent.MadeFor, len(all))
	for i, a := range all {
		made[i] = a.MadeFor
	}
	return made
}

// resolver decides which directories are one project. Under --no-repo git is
// never asked, so only what the agents recorded joins directories.
func resolver(projects []agent.Project, made []agent.MadeFor, disk *repo.Disk, noRepo bool) *family.Resolver {
	if noRepo {
		return family.WithoutRepository(projects, made)
	}
	return family.New(projects, made, disk)
}

// read gathers the sessions of every directory in a project. A directory that
// cannot be read is reported without losing the others.
func read(src agent.Source, p family.Project) ([]agent.Session, error) {
	sessions := make([]agent.Session, 0, len(p.Members))
	var problems []error
	for _, m := range p.Members {
		found, err := src.Sessions(m)
		sessions = append(sessions, found...)
		if err != nil {
			problems = append(problems, err)
		}
	}
	return sessions, errors.Join(problems...)
}

// options are what building a project's graph needs from this edge.
//
// [LAW:single-enforcer] The one place a graph's options are made, so every
// build carries every agent's reading of the directories it makes.
//
// Reading git happens here, at the edge, rather than inside the graph.
// Building a graph is arithmetic over sessions; shelling out is not, and a
// package that does both cannot be tested without a filesystem. Every
// checkout of the project's repository among its directories is read, since
// each worktree has a branch of its own. A project in no repository has none,
// and nothing is read, as before.
func options(made []agent.MadeFor, disk *repo.Disk, p family.Project, noRepo bool) graph.Options {
	opt := graph.DefaultOptions(made)
	opt.Tool = released()
	if !noRepo {
		dirs := make([]string, 0, len(p.Members)+1)
		dirs = append(dirs, p.Path)
		for _, m := range p.Members {
			dirs = append(dirs, m.Path)
		}
		opt.Repo = repo.ReadAll(disk.Checkouts(p.Path, dirs))
	}
	return opt
}

// historyRoot is where to read an agent's history: the --root given, or the
// agent's own place under the home directory when there was none.
func historyRoot(a agent.Agent, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding %s history: %w", a.Name, err)
	}
	return filepath.Join(home, a.History), nil
}

// useBrowser decides between the page and the terminal.
//
// The page is the default because it is what the tool exists to show, but only
// when there is somebody watching. Anything redirected or piped gets text, so
// that reading bough into a file or through less behaves as it always has
// rather than opening a window and hanging on a port.
func useBrowser(textWanted bool, outFile string, stdout io.Writer) bool {
	if textWanted || outFile != "" {
		return false
	}
	f, ok := stdout.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// browse serves the graph and waits for the reader to finish with it.
func browse(g graph.Graph, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err := server.Serve(ctx, g, registry.Display(g.Project.Agent), func(url string) {
		fmt.Fprintf(stderr, "bough is showing %s at %s\n", g.Project.Name, url)
		fmt.Fprintf(stderr, "press ctrl-c when you are done\n")
	})
	if err != nil {
		return err
	}
	return nil
}

// splitArgs separates the project name from the flags, so either order works.
func splitArgs(args []string) (name string, flags []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// A flag that takes a value and was written with a space needs its
			// value kept alongside it.
			if valueFlags[strings.TrimLeft(a, "-")] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		if name == "" {
			name = a
		}
	}
	return name, flags
}

// valueFlags are the flags that take a separate value.
var valueFlags = map[string]bool{"root": true, "o": true, "agent": true}

// choose decides which project to read.
//
// Naming a project reads that one. Otherwise the list is always offered, even
// when the current directory has history of its own, so that opening bough
// always shows what is there rather than jumping straight into one project.
// The project you are standing in is marked and put first, so the common case
// is still a single keypress.
func choose(projects []family.Project, families *family.Resolver, arg string, in io.Reader, out io.Writer) (family.Project, error) {
	if arg == "" {
		return offer(projects, families.Resolve(workingDir()), in, out)
	}

	if p, ok := byPath(projects, families, arg); ok {
		return p, nil
	}

	var matches []family.Project
	for _, p := range projects {
		name := strings.ToLower(p.Name())
		tagged := fmt.Sprintf("%s [%s]", name, strings.ToLower(p.Agent))
		argLower := strings.ToLower(arg)
		if strings.Contains(name, argLower) || strings.Contains(tagged, argLower) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return family.Project{}, fmt.Errorf("no project matching %q; try --list", arg)
	default:
		// With the path, since two projects can share a name and the path is
		// then the only way to ask for one of them.
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = fmt.Sprintf("%s [%s] in %s", m.Name(), m.Agent, m.Path)
		}
		return family.Project{}, fmt.Errorf("%q matches several projects: %s", arg, strings.Join(names, ", "))
	}
}

// offer asks which project to read, with the one you are standing in first.
func offer(projects []family.Project, cwd family.Family, in io.Reader, out io.Writer) (family.Project, error) {
	// The mark only appears when there is a question to ask. Naming a project
	// means you know what you want, and a banner would be in the way.
	banner.Write(out, "what did you actually build?")

	projects, here := currentFirst(projects, cwd)

	items := make([]pick.Item, len(projects))
	for i, p := range projects {
		label := p.Name()
		if i < here {
			label += "  (here)"
		}
		items[i] = pick.Item{Label: label, Detail: describe(p)}
	}

	i, err := pick.From(in, out, "Which project?", items)
	if err != nil {
		// Cancelling comes back as a value rather than as an exit. Calling
		// os.Exit here skipped every deferred close on the way out and made
		// this path impossible to drive from a test.
		return family.Project{}, err
	}
	return projects[i], nil
}

// currentFirst puts first the projects of the family the working directory
// belongs to, one per agent that worked there, and reports how many there are.
// The rest keep their order.
//
// The family is passed in rather than resolved here. The directory was an
// ambient fact reached for three levels below run, which is the same reason
// the writers are passed: a caller cannot ask what this does from anywhere
// else.
func currentFirst(projects []family.Project, cwd family.Family) ([]family.Project, int) {
	var here, rest []family.Project
	for _, p := range projects {
		if p.Is(cwd) {
			here = append(here, p)
		} else {
			rest = append(rest, p)
		}
	}
	return append(here, rest...), len(here)
}

// describe is the dimmer text beside a project name, enough to tell which one
// is wanted without reading any of the history.
func describe(p family.Project) string {
	var bytes int64
	var last time.Time
	for _, m := range p.Members {
		bytes += m.Bytes
		if m.LastWorked.After(last) {
			last = m.LastWorked
		}
	}

	// Named for every agent, and spelled the way the listing and the page spell
	// it. Naming only the unfamiliar one made the others look like the absence
	// of an agent rather than a choice of one.
	var parts []string
	switch {
	case bytes >= 1<<20:
		parts = append(parts, fmt.Sprintf("%d MB", bytes>>20))
	case bytes > 0:
		parts = append(parts, fmt.Sprintf("%d KB", bytes>>10))
	}
	if n := len(p.Members); n > 1 {
		parts = append(parts, fmt.Sprintf("%d directories", n))
	}
	if !last.IsZero() {
		parts = append(parts, ago(last))
	}
	return strings.TrimSpace("[" + registry.Display(p.Agent) + "] " + strings.Join(parts, ", "))
}

// ago says how long ago something happened, the way a person would.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return "just now"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d weeks ago", int(d.Hours()/24/7))
	default:
		return t.Format("Jan 2006")
	}
}

// byPath matches a project by a directory: the one it is known by, one of its
// members, or anywhere else its family reaches.
//
// [LAW:one-source-of-truth] The directory is resolved rather than compared
// with each member's path, so a path opens the same project the listing put
// it under, whichever spelling it was written in: the same normaliser the rest
// of the tool compares paths with, which understands a transcript written on
// Windows and read anywhere else.
func byPath(projects []family.Project, families *family.Resolver, path string) (family.Project, bool) {
	want := families.Resolve(path)
	for _, p := range projects {
		if p.Is(want) {
			return p, true
		}
	}
	return family.Project{}, false
}

// writeList prints one line per project, in columns wide enough for what is
// actually in them, and under each the directories it was read from when
// every directory is asked for.
//
// The widths used to be fixed at 24 and 40, which held while every project was
// a short name in a short path. A Codex project is named after a directory that
// can run well past both, and one long row then pushed its own path and count
// out of line with every other row. Measuring first costs a pass over a list
// that is already in memory.
//
// The agent gets a column of its own rather than being stuck onto the name. It
// is the same width on every row so the eye can run down it, and it is filled
// in for every agent: naming only the unfamiliar one implies the others are
// somehow the default, which stopped being true when the second one arrived.
func writeList(w io.Writer, sources map[string]agent.Source, projects []family.Project, everyDir bool) error {
	type dir struct {
		path  string
		count tally
	}
	type row struct {
		name  string
		agent string
		path  string
		count tally
		dirs  []dir
	}

	rows := make([]row, 0, len(projects))
	var nameW, agentW, pathW int
	for _, p := range projects {
		r := row{name: p.Name(), agent: "[" + registry.Display(p.Agent) + "]", path: p.Path}
		for _, m := range p.Members {
			sessions, err := sources[m.Source].Sessions(m)
			d := dir{path: m.Path, count: tally{dirs: 1}}
			if err != nil {
				d.count.unread = 1
			}
			for _, s := range sessions {
				d.count.prompts += len(s.Turns)
			}
			r.count = r.count.add(d.count)
			r.dirs = append(r.dirs, d)
		}
		rows = append(rows, r)
		nameW = wider(nameW, r.name)
		agentW = wider(agentW, r.agent)
		pathW = wider(pathW, r.path)
	}

	// The path column is padded to what the paths need, up to a limit. Paths
	// vary by far more than names do, and padding to the longest let one deep
	// path push the counts on every other row most of a screen to the right to
	// line up with nothing. Capping it means the common case still lines up and
	// an unusually long path overflows its own row rather than everyone else's.
	if pathW > pathLimit {
		pathW = pathLimit
	}

	for _, r := range rows {
		fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n",
			nameW, r.name, agentW, r.agent, pathW, r.path, r.count.say())

		// Under the project's own path, so the directories read as parts of it.
		shown := r.dirs
		if !everyDir {
			shown = nil
		}
		for _, d := range shown {
			fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n", nameW, "", agentW, "", pathW, d.path, d.count.say())
		}
	}
	return nil
}

// tally is how much history a directory, or a whole project, holds.
type tally struct {
	prompts int

	// dirs is how many directories were counted.
	dirs int

	// unread is how many of them had history that could not be read, which
	// is a different thing from having no prompts. Both used to print as
	// "0 prompts", so a permission problem or a corrupt file read as an empty
	// project and there was nothing to say otherwise.
	unread int
}

func (t tally) add(o tally) tally {
	return tally{prompts: t.prompts + o.prompts, dirs: t.dirs + o.dirs, unread: t.unread + o.unread}
}

// say gives the count, across however many directories it was taken from, or
// says there is none to give.
//
// A history that failed to read says so rather than reporting a number. The
// count came from a discarded error, so a project whose transcripts could not
// be opened printed "0 prompts" exactly as an empty one does, and nothing said
// which of the two it was. Across several directories it says how many
// failed, since one failure does not make the others unreadable.
func (t tally) say() string {
	where := ""
	if t.dirs > 1 {
		where = fmt.Sprintf(" in %d directories", t.dirs)
	}
	switch {
	case t.unread == 0:
		return fmt.Sprintf("%d prompts%s", t.prompts, where)
	case t.dirs == 1 && t.prompts == 0:
		return "could not be read"
	case t.dirs == 1:
		return fmt.Sprintf("%d prompts, some could not be read", t.prompts)
	}
	return fmt.Sprintf("%d prompts%s, %d of them with history that could not be read", t.prompts, where, t.unread)
}

const usage = `bough shows the shape of the work in a project's AI coding history.

  bough              choose a project and open it in a browser
  bough my-project   open a project by name, or by any directory in it
  bough --text       write to the terminal instead
  bough --list       show which projects have history
  bough --list -v    and every directory each project was read from
  bough --json       write the graph as JSON
  bough --version    print the version
  bough --no-repo    leave the project's git history unread
  bough --agent=NAME  read one agent's history: %s

Anything piped or redirected is written as text, so bough > notes.txt and
bough | less behave as you would expect.

Options:
`

// pathLimit caps the path column. Chosen so an ordinary project path lines up
// and a deep one overflows its own row instead of everyone else's.
const pathLimit = 44

// wider reports the greater of a width and the width of a string, counting
// runes rather than bytes so a name outside ASCII still lines up.
func wider(at int, s string) int {
	if n := utf8.RuneCountInString(s); n > at {
		return n
	}
	return at
}

// workingDir is where bough was run, or "" if the question cannot be answered.
func workingDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}
