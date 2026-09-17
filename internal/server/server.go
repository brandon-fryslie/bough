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

// Serve puts a graph's page on a loopback port and waits until the caller
// stops it.
//
// The address goes to announce once the port is bound. What happens next is
// the caller's business: the command prints it and opens a browser, while a
// test or a measurement run points its own client at it. Opening the desktop
// browser from in here made every one of those open a stray window.
//
// agentName is the agent that wrote the history, spelled for a person. It is
// passed in rather than looked up so that the page takes its names from the
// same place the terminal does, without this package learning about agents.
func Serve(ctx context.Context, g graph.Graph, agentName string, announce func(url string)) error {
	// Port zero asks the operating system for a free one, which avoids both
	// guessing and colliding with whatever else is running.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listening on loopback: %w", err)
	}
	defer func() { _ = listener.Close() }()

	page, err := render(g, agentName)
	if err != nil {
		return err
	}

	url := "http://" + listener.Addr().String()
	if announce != nil {
		announce(url)
	}

	srv := &http.Server{
		Handler:           routes(page),
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

// routes wires up the page and the files it needs.
func routes(page []byte) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The page is built once and never changes while the process runs, so
		// there is nothing to revalidate.
		w.Header().Set("Cache-Control", "no-store")
		// A failed write means the browser went away mid-response, which is
		// normal and leaves nothing to do.
		_, _ = w.Write(page)
	})

	mux.Handle("/img/", http.FileServer(http.FS(assets)))
	return mux
}

// render builds the page with the graph already inside it.
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
func render(g graph.Graph, agentName string) ([]byte, error) {
	parts := map[string]string{}
	for _, name := range []string{"index.html", "fonts.css", "bough.css", "layout.js", "bough.js"} {
		body, err := readAsset(name)
		if err != nil {
			return nil, err
		}
		parts[name] = body
	}

	data, err := json.Marshal(g)
	if err != nil {
		return nil, fmt.Errorf("encoding the graph: %w", err)
	}

	replace := strings.NewReplacer(
		"{{.Title}}", escapeHTML(g.Project.Name),
		"{{.Agent}}", escapeHTML(agentName),
		"{{.Fonts}}", parts["fonts.css"],
		"{{.CSS}}", parts["bough.css"],
		"{{.Layout}}", parts["layout.js"],
		"{{.JS}}", parts["bough.js"],
		"{{.Graph}}", escapeScript(string(data)),
	)
	return []byte(replace.Replace(parts["index.html"])), nil
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
