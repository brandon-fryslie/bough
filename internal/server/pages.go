package server

import (
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/nickelsec/bough/internal/graph"
)

// pages are the families' pages this server has built, and the ones it is
// building.
//
// [LAW:no-shared-mutable-globals] The one owner of what every request shares.
// A family's page is built once, by the first request for it, and kept for as
// long as the process runs. Requests that arrive while it builds share that
// build, so a history is never read twice at once.
type pages struct {
	families map[string]Build
	named    func(agentID string) string

	mu    sync.Mutex
	byKey map[string]*making
}

// making is one family's page, finished once done is closed. page and err are
// written before done closes and read only after, so they need no lock.
type making struct {
	done chan struct{}
	page page
	err  error
}

// newPages holds the page for g, the family first names, from the start.
func newPages(first string, g graph.Graph, families map[string]Build, named func(agentID string) string) (*pages, error) {
	opened, err := render(g, named)
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	close(done)
	return &pages{
		families: families,
		named:    named,
		byKey:    map[string]*making{first: {done: done, page: opened}},
	}, nil
}

// state is where a family's page stands when it is asked for.
type state int

const (
	// unknown means no family has the key.
	unknown state = iota
	// building means the page is not ready yet.
	building
	// failed means the build finished without a page, and err says why.
	failed
	// ready means the page is there.
	ready
)

// found is a family's page as it stands.
type found struct {
	state state
	page  page
	err   error
}

// look is the page for key as it stands, starting its build if nothing has.
// It never waits on a build: a page that is not ready says so at once.
func (p *pages) look(key string) found {
	p.mu.Lock()
	defer p.mu.Unlock()

	m, ok := p.byKey[key]
	if !ok {
		build, known := p.families[key]
		if !known {
			return found{state: unknown}
		}
		m = &making{done: make(chan struct{})}
		p.byKey[key] = m
		go m.make(build, p.named)
	}

	select {
	case <-m.done:
	default:
		return found{state: building}
	}
	if m.err != nil {
		// [LAW:no-silent-failure] A failure is told to the request that finds
		// it, and then forgotten, so the next request reads the history again
		// rather than being shown an old failure for as long as bough runs.
		delete(p.byKey, key)
		return found{state: failed, err: m.err}
	}
	return found{state: ready, page: m.page}
}

// make builds the page and then says it is finished.
func (m *making) make(build Build, named func(agentID string) string) {
	defer close(m.done)
	g, err := build()
	if err != nil {
		m.err = err
		return
	}
	m.page, m.err = render(g, named)
}

// span is the date range a page opens on: each end a day as the page's date
// inputs spell it, or empty where the range is open.
type span struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// parseSpan is the range a request asks for, from its from and to.
//
// [LAW:parse-dont-validate] The one place a requested range is checked. A span
// holds only days this wrote out itself, so what goes into the page is never
// the request's own text.
func parseSpan(q url.Values) (span, error) {
	from, err := day(q, "from")
	if err != nil {
		return span{}, err
	}
	to, err := day(q, "to")
	if err != nil {
		return span{}, err
	}
	// Both are written YYYY-MM-DD with a four digit year, so comparing them as
	// text compares them as dates.
	if from != "" && to != "" && from > to {
		return span{}, fmt.Errorf("the range runs backwards: from %s is after to %s", from, to)
	}
	return span{From: from, To: to}, nil
}

// day is one end of the range, or empty when the request leaves it open.
func day(q url.Values, name string) (string, error) {
	v := q.Get(name)
	if v == "" {
		return "", nil
	}
	t, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return "", fmt.Errorf("%s=%q is not a date written as YYYY-MM-DD", name, v)
	}
	return t.Format(time.DateOnly), nil
}

// script is the range as the page reads it, a JSON object. Its values are days
// this package wrote, so there is nothing in them to escape.
func (s span) script() string {
	return fmt.Sprintf(`{"from":%q,"to":%q}`, s.From, s.To)
}
