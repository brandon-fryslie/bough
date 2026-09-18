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
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
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
		asJSON      = fs.Bool("json", false, "write the graph as JSON instead of text")
		asText      = fs.Bool("text", false, "write to the terminal instead of opening a browser")
		list        = fs.Bool("list", false, "list the projects with history and stop")
		asPortfolio = fs.Bool("portfolio", false, "write every project's sittings as JSON and stop")
		verbose     = fs.Bool("v", false, "include every prompt in the text output, and every directory in --list")
		root        = fs.String("root", "", "read every agent's history from here instead of its usual location")
		out         = fs.String("o", "", "write to this file instead of standard output")
		showVer     = fs.Bool("version", false, "print the version and stop")
		noRepo      = fs.Bool("no-repo", false, "do not read the project's git history")
		agentFlag   = fs.String("agent", "all", "which agent history to read: "+strings.Join(registry.Flags(), ", "))
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

	b := builder{sources: sources, made: made, disk: disk, families: families, noRepo: *noRepo, stderr: stderr}

	if *list {
		return writeList(stdout, sources, whole, *verbose)
	}
	if *asPortfolio {
		// Summarised before -o is opened, not inside it. Reading a whole
		// machine takes the best part of a minute, and opening the file first
		// would truncate a good portfolio and then spend that minute failing
		// to replace it.
		doc := b.portfolio(whole, time.Now)
		return write(*out, stdout, func(w io.Writer) error { return writeJSON(w, doc) })
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

	g, err := b.build(target)
	if err != nil {
		return err
	}

	if *asJSON {
		return write(*out, stdout, func(w io.Writer) error { return writeJSON(w, g) })
	}
	if useBrowser(*asText, *out, stdout) {
		return browse(target.Key(), g, b.every(whole), stderr)
	}
	return write(*out, stdout, func(w io.Writer) error {
		return graph.WriteText(w, g, *verbose, registry.Display)
	})
}

// write runs body against where output goes: the file -o named, or the screen.
//
// [LAW:single-enforcer] One place answers -o, so every document bough writes
// answers it the same way. The close is reported rather than deferred away: a
// write that failed to flush leaves a file quietly missing its tail, and the
// exit code is the only place that can say so.
func write(path string, stdout io.Writer, body func(io.Writer) error) error {
	if path == "" {
		return body(stdout)
	}
	// The path is what the reader asked -o for, and writing there is the
	// whole point of the flag. Nothing here comes from a transcript.
	f, err := os.Create(path) //#nosec G304
	if err != nil {
		return err
	}
	return errors.Join(body(f), f.Close())
}

// writeJSON writes a document the way bough writes every one: indented, since
// a file somebody opens is read by a person at least as often as by a program.
func writeJSON(w io.Writer, document any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(document)
}

// builder is what building a project's graph needs from this edge.
type builder struct {
	sources  map[string]agent.Source
	made     []agent.MadeFor
	disk     *repo.Disk
	families *family.Resolver
	noRepo   bool
	stderr   io.Writer
}

// build is a project's graph, or why there is none.
//
// [LAW:single-enforcer] The one way from a project to its graph. The page bough
// opens on, --json, the text view and every family the running server is later
// asked for all come through here, so none of them can read or build a project
// differently from the others.
//
// Some transcripts may be unreadable while others are fine, so a partial read
// says so and carries on with what did load. Nothing loading at all is no
// graph, since an empty drawing would read as a project with no work in it.
//
// That last case is two facts, and the error says which. A history that would
// not read and a history that read and held nothing both leave nothing to
// draw, so a caller that refuses either way need not look; one that reports
// them to a reader has to tell them apart, and errNoHistory is how.
func (b builder) build(p family.Project) (graph.Graph, error) {
	sessions, err := read(b.sources, p)
	if len(sessions) == 0 {
		return graph.Graph{}, errors.Join(nothingRead(p, err), err)
	}
	if err != nil {
		fmt.Fprintf(b.stderr, "bough: some history of %s could not be read: %v\n", p.Name(), err)
	}
	return graph.Build(p, sessions, options(b.made, b.disk, b.families, p, sessions, b.noRepo)), nil
}

// errNoHistory marks a project whose history read cleanly and held nothing.
//
// It is a value rather than only a form of words because something downstream
// has to branch on it: the portfolio reports such a family as one with no
// work rather than one that failed, and reading that out of a message would
// tie the document's meaning to the wording of a sentence.
var errNoHistory = errors.New("no history to read")

// nothingRead says why a project produced no sessions at all: its history
// would not read, or it read and held none.
func nothingRead(p family.Project, err error) error {
	if err != nil {
		return fmt.Errorf("no readable history for %s", p.Name())
	}
	return fmt.Errorf("%s has %w", p.Name(), errNoHistory)
}

// every is a build for each project, by the key the server addresses it by.
//
// Each is safe to run beside another: the sources and the resolver are only
// read, and the disk guards what it remembers.
func (b builder) every(projects []family.Project) map[string]server.Build {
	builds := make(map[string]server.Build, len(projects))
	for _, p := range projects {
		builds[p.Key()] = func() (graph.Graph, error) { return b.build(p) }
	}
	return builds
}

// read is a project's history: each agent's members read by that agent's own
// source, and every agent's sessions together, since a family is its
// directories whichever agents worked in them.
func read(sources map[string]agent.Source, p family.Project) ([]agent.Session, error) {
	agents := p.Agents()
	found := make([][]agent.Session, len(agents))
	problems := make([]error, len(agents))
	for i, id := range agents {
		found[i], problems[i] = sources[id].Sessions(p.Of(id)...)
	}
	return slices.Concat(found...), errors.Join(problems...)
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
// and nothing is read, as before. So is where the work done outside the
// project landed.
func options(made []agent.MadeFor, disk *repo.Disk, families *family.Resolver, p family.Project, sessions []agent.Session, noRepo bool) graph.Options {
	opt := graph.DefaultOptions(made)
	opt.Tool = released()
	opt.Elsewhere = elsewhere(families, disk, p, graph.Visited(p, sessions), temporary(), noRepo)
	if !noRepo {
		opt.Repo = repo.ReadAll(disk.Checkouts(p.Path, append([]string{p.Path}, p.Directories()...)))
	}
	return opt
}

// elsewhere answers what the graph asks about the places a project's
// sittings worked: where each path leads once symlinks are followed, whether
// anything is there, and whose family that is. And, unless git is not to be
// read, the history of each other family a commit was made in, so its hashes
// are checked as the project's own are.
//
// [LAW:single-enforcer] The graph decides which commits count as work done
// elsewhere, and a repository is read only for those, since one read can take
// seconds.
func elsewhere(families *family.Resolver, disk *repo.Disk, p family.Project, v graph.Visits, temporary []string, noRepo bool) graph.Elsewhere {
	e := graph.Elsewhere{Places: map[string]graph.Place{}, Repos: map[string]repo.History{}, Temporary: temporary}
	for _, at := range slices.Concat(v.Files, v.Dirs) {
		e.Places[at] = where(families, at)
	}
	if noRepo {
		return e
	}

	// Each family's main tree, and every directory a commit was made in
	// there, which may be a worktree on a branch of its own.
	checkouts := map[string][]string{}
	for _, dir := range v.Dirs {
		pl, ok := e.CommittedIn(p, dir)
		if !ok {
			continue
		}
		key := pl.Family.Key()
		if _, ok := checkouts[key]; !ok {
			checkouts[key] = []string{pl.Family.Name}
		}
		checkouts[key] = append(checkouts[key], pl.Path)
	}
	for key, dirs := range checkouts {
		e.Repos[key] = repo.ReadAll(disk.Checkouts(dirs[0], dirs))
	}
	return e
}

// where is what the machine says about one path: where it leads, and whose
// family that is. A path the disk cannot follow, because nothing is there or
// it may not be read, is answered as it was asked.
func where(families *family.Resolver, at string) graph.Place {
	led, err := filepath.EvalSymlinks(at)
	if err != nil {
		return graph.Place{Path: at, Family: families.Resolve(at)}
	}
	return graph.Place{Path: led, Exists: true, Family: families.Resolve(led)}
}

// temporary are the directories this machine throws away: its own temporary
// directory and the conventional one, each also as its symlinks lead, since
// a path can arrive spelled either way. /tmp is where Claude Code makes its
// scratchpads, wherever the platform's own temporary directory is.
func temporary() []string {
	roots := []string{os.TempDir(), "/tmp"}
	for _, root := range roots {
		if led, err := filepath.EvalSymlinks(root); err == nil && led != root {
			roots = append(roots, led)
		}
	}
	return roots
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

// browse serves the graph, and any other family's when it is asked for, and
// waits for the reader to finish with it. key is the family g was built for.
func browse(key string, g graph.Graph, families map[string]server.Build, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// The address is printed before the browser is asked for, so a terminal
	// that cannot open one still tells the reader where to look.
	err := server.Serve(ctx, key, g, families, registry.Display, func(url string) {
		fmt.Fprintf(stderr, "bough is showing %s at %s\n", g.Project.Name, url)
		fmt.Fprintf(stderr, "press ctrl-c when you are done\n")
		openBrowser(url)
	})
	if err != nil {
		return err
	}
	return nil
}

// openBrowser asks the desktop to show a page.
//
// Failure is ignored on purpose. Plenty of places have no browser to open, and
// the reader has already been told the address.
//
// The url is built from the address the listener bound to, so it is always
// http://127.0.0.1 and a port the kernel chose. It carries nothing a user or
// a transcript supplied, and it is passed as an argument rather than through
// a shell, so there is nothing here for a subprocess to misread.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //#nosec G204
	case "darwin":
		cmd = exec.Command("open", url) //#nosec G204
	default:
		cmd = exec.Command("xdg-open", url) //#nosec G204
	}
	_ = cmd.Start()
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

	// A path and a name answer the same way: one project opens, and several
	// are named so the reader can say which. A place two agents worked in is
	// one project holding both, and --agent is how to read one of them.
	// An argument written as a path is only ever a path: `bough .` somewhere
	// with no history once opened a project whose name held a dot.
	matches := byName(projects, arg)
	if dir, ok := place(arg); ok {
		matches = byPath(projects, families.Resolve(dir))
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
			names[i] = fmt.Sprintf("%s in %s", m.Name(), m.Path)
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

// currentFirst puts first the project of the family the working directory
// belongs to, and reports how many projects that put first: one, or none when
// the directory's family has no history. The rest keep their order.
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
//
// Every agent that worked on it is named with how much of its history there
// is, spelled the way the listing and the page spell it. Naming only the
// unfamiliar one made the others look like the absence of an agent rather
// than a choice of one. The share is the size on disk rather than a count of
// prompts, since counting them means reading all of it.
func describe(p family.Project) string {
	ids := p.Agents()
	shares := make([]string, len(ids))
	for i, id := range ids {
		var bytes int64
		for _, m := range p.Of(id) {
			bytes += m.Bytes
		}
		shares[i] = strings.TrimSpace(registry.Display(id) + " " + size(bytes))
	}
	parts := []string{strings.Join(shares, " and ")}

	if n := len(p.Directories()); n > 1 {
		parts = append(parts, fmt.Sprintf("%d directories", n))
	}
	if last := p.LastWorked(); !last.IsZero() {
		parts = append(parts, ago(last))
	}
	return strings.Join(parts, ", ")
}

// size says roughly how much history a number of bytes is, or nothing for
// none.
func size(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%d MB", bytes>>20)
	case bytes > 0:
		return fmt.Sprintf("%d KB", bytes>>10)
	}
	return ""
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

// byName matches projects whose name holds the argument.
func byName(projects []family.Project, arg string) []family.Project {
	var matches []family.Project
	argLower := strings.ToLower(arg)
	for _, p := range projects {
		if strings.Contains(strings.ToLower(p.Name()), argLower) {
			matches = append(matches, p)
		}
	}
	return matches
}

// byPath matches the projects of the family a directory resolved to: the
// directory it is known by, one of its members, or anywhere else it reaches.
//
// [LAW:one-source-of-truth] The directory is resolved rather than compared
// with each member's path, so a path opens the same project the listing put
// it under, whichever spelling it was written in.
func byPath(projects []family.Project, f family.Family) []family.Project {
	var matches []family.Project
	for _, p := range projects {
		if p.Is(f) {
			matches = append(matches, p)
		}
	}
	return matches
}

// place is the directory an argument names, when it is written as one.
//
// [LAW:parse-dont-validate] The shape decides, not the disk. An absolute path,
// in either platform's spelling since a history written on Windows can be read
// anywhere, is a place whether or not it still exists: a deleted worktree's
// path still says whose it was. A relative one is a place when it is written as
// a path, `.` or with a separator, and is taken from where bough was run. A bare
// word is a name, even when a directory of that name sits where bough was run:
// taken as a path, a name resolved to the repository bough was run in and
// opened that instead of the project named.
func place(arg string) (string, bool) {
	slashed := strings.ReplaceAll(arg, `\`, "/")
	switch {
	case strings.HasPrefix(slashed, "/") || driveRooted.MatchString(slashed):
		return arg, true
	case slashed == "." || slashed == ".." || strings.Contains(slashed, "/"):
		abs, err := filepath.Abs(arg)
		return abs, err == nil
	}
	return "", false
}

// driveRooted matches a path that starts at a Windows drive.
var driveRooted = regexp.MustCompile(`^[A-Za-z]:(/|$)`)

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
// Every agent that worked on a project is named with its own count, on the
// one row. Naming only the unfamiliar one implies the others are somehow the
// default, which stopped being true when the second one arrived; and a
// project both agents worked on is one project, so one row says how much of
// it each did.
func writeList(w io.Writer, sources map[string]agent.Source, projects []family.Project, everyDir bool) error {
	type dir struct {
		path  string
		count count
	}
	type row struct {
		name  string
		path  string
		count count
		dirs  []dir
	}

	rows := make([]row, 0, len(projects))
	var nameW, pathW int
	for _, p := range projects {
		// The project is read whole, so a session whose records are spread
		// over several of its directories counts once.
		r := row{name: p.Name(), path: p.Path, count: counted(sources, p)}

		// Each directory on its own only when they are asked for, since that
		// reads every one of them a second time.
		shown := p.Directories()
		if !everyDir {
			shown = nil
		}
		for _, d := range shown {
			at := family.Project{Path: d, Members: slices.DeleteFunc(slices.Clone(p.Members), func(m agent.Project) bool {
				return agent.NormalisePath(m.Path) != agent.NormalisePath(d)
			})}
			r.dirs = append(r.dirs, dir{path: d, count: counted(sources, at)})
		}

		rows = append(rows, r)
		nameW = wider(nameW, r.name)
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
		fmt.Fprintf(w, "%-*s  %-*s  %s\n", nameW, r.name, pathW, r.path, r.count.say())

		// Under the project's own path, so the directories read as parts of it.
		for _, d := range r.dirs {
			fmt.Fprintf(w, "%-*s  %-*s  %s\n", nameW, "", pathW, d.path, d.count.say())
		}
	}
	return nil
}

// counted reads a project's history, agent by agent, into how much of it
// each agent holds.
func counted(sources map[string]agent.Source, p family.Project) count {
	c := count{dirs: len(p.Directories())}
	for _, id := range p.Agents() {
		members := p.Of(id)
		t := tallied(len(members))(sources[id].Sessions(members...))
		t.agent = registry.Display(id)
		c.agents = append(c.agents, t)
	}
	return c
}

// count is how much history a directory, or a whole project, holds: each
// agent's share, and across how many directories.
type count struct {
	agents []tally
	dirs   int
}

// say names each agent with its share, then how many directories they were
// taken from when there were several.
func (c count) say() string {
	shares := make([]string, len(c.agents))
	for i, t := range c.agents {
		shares[i] = t.say()
	}
	where := ""
	if c.dirs > 1 {
		where = fmt.Sprintf(" in %d directories", c.dirs)
	}
	return strings.Join(shares, " and ") + where
}

// tally is how much of one agent's history a directory, or a whole project,
// holds.
type tally struct {
	// agent is the agent's name, spelled for a person.
	agent string

	prompts int

	// dirs is how many of the agent's directories were read.
	dirs int

	// unread means some history could not be read, which is a different
	// thing from having no prompts. Both used to print as "0 prompts", so a
	// permission problem or a corrupt file read as an empty project and there
	// was nothing to say otherwise.
	unread bool
}

// tallied counts what a read of dirs directories found.
func tallied(dirs int) func([]agent.Session, error) tally {
	return func(sessions []agent.Session, err error) tally {
		t := tally{dirs: dirs, unread: err != nil}
		for _, s := range sessions {
			t.prompts += len(s.Turns)
		}
		return t
	}
}

// say gives the agent's count, or says there is none to give.
//
// A history that failed to read says so rather than reporting a number. The
// count came from a discarded error, so a project whose transcripts could not
// be opened printed "0 prompts" exactly as an empty one does, and nothing said
// which of the two it was. Only a single directory with nothing read is
// unreadable outright: across several, one failure says nothing of the rest.
func (t tally) say() string {
	switch {
	case !t.unread:
		return fmt.Sprintf("%s %d prompts", t.agent, t.prompts)
	case t.dirs == 1 && t.prompts == 0:
		return t.agent + " could not be read"
	}
	return fmt.Sprintf("%s %d prompts (some could not be read)", t.agent, t.prompts)
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
  bough --portfolio  write every project's sittings as JSON
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
