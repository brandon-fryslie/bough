// Package server puts the graph in front of a browser.
//
// It listens on the loopback address only. A person's coding history is theirs,
// and a viewer for it has no business being reachable from anywhere else on the
// network, so the address is written out rather than left to a wildcard bind.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/nickelsec/bough/internal/graph"
)

// Build makes one family's graph. On a large history it takes seconds, and it
// fails when the history cannot be read.
type Build func() (graph.Graph, error)

// Serve puts a graph's page on a loopback port, along with the page of any
// other family it is asked for, and waits until the caller stops it.
//
// first is the key of the family g was built for, which is the page at /.
// families builds any family's graph by its key, so /family?key=<key> can open
// a project without restarting bough. Building is passed in rather than done
// here for the same reason named is: this package never learns about agents
// or how a family's history is read.
//
// The address goes to announce once the port is bound. What happens next is
// the caller's business: the command prints it and opens a browser, while a
// test or a measurement run points its own client at it. Opening the desktop
// browser from in here made every one of those open a stray window.
//
// named spells an agent's ID for a person. It is passed in rather than looked
// up so that the page takes its names from the same place the terminal does,
// without this package learning about agents.
func Serve(ctx context.Context, first string, g graph.Graph, families map[string]Build, named func(agentID string) string, announce func(url string)) error {
	// Port zero asks the operating system for a free one, which avoids both
	// guessing and colliding with whatever else is running.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listening on loopback: %w", err)
	}
	defer func() { _ = listener.Close() }()

	built, err := newPages(first, g, families, named)
	if err != nil {
		return err
	}

	url := "http://" + listener.Addr().String()
	if announce != nil {
		announce(url)
	}

	srv := &http.Server{
		Handler:           routes(built, first, listener.Addr().String()),
		ReadHeaderTimeout: 5 * time.Second,
	}

	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// routes wires up the pages and the files they need. / is the family bough
// opened on, and /family is any family by its key. The key is a query
// parameter rather than part of the path because keys are paths themselves,
// and the mux redirects a path holding "//".
//
// Only requests addressed to host, the address the listener bound, are
// answered. Binding loopback keeps other machines out, but a web page open in
// the same browser can point a name it controls at 127.0.0.1 and read
// whatever answers there as its own. Its requests carry its own name as the
// host, so they are refused before any history is read.
func routes(built *pages, first, host string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		respond(w, r, built, first)
	})
	mux.HandleFunc("/family", func(w http.ResponseWriter, r *http.Request) {
		respond(w, r, built, r.URL.Query().Get("key"))
	})

	mux.Handle("/img/", http.FileServer(http.FS(assets)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host {
			notice(w, http.StatusMisdirectedRequest, fmt.Sprintf("bough answers only at http://%s.", host))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// respond answers a request for one family's page: the page, opened on the
// dates asked for; a page saying it is still being built; or a page saying why
// there is none.
func respond(w http.ResponseWriter, r *http.Request, built *pages, key string) {
	// Every answer here is about this moment: a page still building will be
	// ready soon, and a failed one is tried again, so none may be kept.
	w.Header().Set("Cache-Control", "no-store")

	// The dates are checked before anything is looked up, so a request that
	// cannot be answered never starts a build.
	opened, err := parseSpan(r.URL.Query())
	if err != nil {
		notice(w, http.StatusBadRequest, err.Error())
		return
	}

	found := built.look(key)
	switch found.state {
	case unknown:
		notice(w, http.StatusNotFound, fmt.Sprintf("No project has the identifier %q.", key))
	case building:
		// The browser asks again every second until the real page is there.
		// The refresh names no address, so it asks the one it came from.
		w.Header().Set("Refresh", "1")
		notice(w, http.StatusAccepted, fmt.Sprintf("Reading the history of %s. Its page opens here when it is ready.", key))
	case failed:
		notice(w, http.StatusInternalServerError, found.err.Error())
	case ready:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// A failed write means the browser went away mid-response, which is
		// normal and leaves nothing to do. What the request asked for reaches
		// the page only as days parseSpan wrote out itself.
		_, _ = w.Write(found.page.with(opened)) //#nosec G705 -- the range is parsed, not echoed
	}
}

// notice is a plain text page saying one thing, for every answer that is not
// a drawing.
//
// Plain text rather than markup, because the message can hold what the request
// said: an identifier, a date. As text it cannot be anything but text, so
// there is nothing to escape and no escaping to forget. nosniff stops a
// browser second-guessing that.
func notice(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message+"\n") //#nosec G705 -- served as text/plain with nosniff, so it is never markup
}

// page is a family's page, whole but for the date range it opens on, which
// each request brings its own of.
//
// It is split where the range goes in the template, before anything is put
// into it, rather than searched for in the finished page. A prompt can say
// anything, including the placeholder, and a search would find that too.
type page struct {
	before, after string
}

// with is the page opened on a date range.
func (p page) with(s span) []byte {
	return []byte(p.before + s.script() + p.after)
}

// render builds a family's page with the graph already inside it.
//
// Inlining rather than fetching means the page is whole the moment it loads,
// with no second request to wait on, and a saved copy still works after the
// process has gone.
//
// The substitution is done by hand rather than with html/template. There are
// four values, all of them produced here rather than supplied by anyone, and
// the template package drags in reflection and the crypto tree behind its
// contextual escaping. That cost eight megabytes of binary for four
// replacements that need one escaping rule between them.
func render(g graph.Graph, named func(agentID string) string) (page, error) {
	parts := map[string]string{}
	for _, name := range []string{"index.html", "fonts.css", "bough.css", "layout.js", "bough.js"} {
		body, err := readAsset(name)
		if err != nil {
			return page{}, err
		}
		parts[name] = body
	}

	data, err := json.Marshal(g)
	if err != nil {
		return page{}, fmt.Errorf("encoding the graph: %w", err)
	}

	before, after, ok := strings.Cut(parts["index.html"], "{{.Range}}")
	if !ok {
		return page{}, errors.New("index.html has no place for the date range")
	}

	replace := strings.NewReplacer(
		"{{.Title}}", escapeHTML(g.Project.Name),
		"{{.Agents}}", agents(g.Project.Agents, named),
		"{{.Fonts}}", parts["fonts.css"],
		"{{.CSS}}", parts["bough.css"],
		"{{.Layout}}", parts["layout.js"],
		"{{.JS}}", parts["bough.js"],
		"{{.Graph}}", escapeScript(string(data)),
	)
	return page{before: replace.Replace(before), after: replace.Replace(after)}, nil
}

// agents is the key naming every agent whose history the page draws, one
// entry each in the order the graph lists them, which is the order the page
// gives each its mark in.
func agents(ids []string, named func(agentID string) string) string {
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&b, `<span class="mark-agent" data-agent="%s">%s</span>`, escapeHTML(id), escapeHTML(named(id)))
	}
	return b.String()
}

// escapeHTML makes text safe to drop into the page body. Project names come
// from a directory on this machine, but a name holding a bracket should show
// as that bracket rather than starting a tag.
func escapeHTML(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	).Replace(s)
}

// escapeScript makes JSON safe to sit inside a script element.
//
// JSON is already valid JavaScript, and Go's encoder turns angle brackets into
// escapes on the way out, which handles this on its own today. The rule is
// kept because that behaviour is a setting rather than a guarantee, and people
// do paste markup into these conversations, so a prompt holding the text that
// ends a script element would otherwise cut the page in half.
func escapeScript(s string) string {
	return strings.ReplaceAll(s, "</", `<\/`)
}

func readAsset(name string) (string, error) {
	f, err := assets.Open(name)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	return string(b), err
}
