package main

import (
	"io"
	"sync"
	"time"

	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/portfolio"
)

// atOnce is how many families are read and summarised at the same time.
//
// It is a memory ceiling as much as a speed one, because each one holds a
// whole graph until its summary is taken. On the machine this was fitted
// against, all 124 families came to 71 s read one at a time and 13 s read
// eight at a time, at around 430 MB of heap (measured 2026-09-14). More at
// once buys little beyond that and costs another graph's worth of memory
// each, which is why this is a number here rather than a flag: a reader
// choosing it would be choosing how much memory bough may use, without
// anything to choose it by.
const atOnce = 8

// portfolio summarises every family, several at a time.
//
// [LAW:single-enforcer] Each family goes through build, the same path the page
// and --json take, so a sitting here carries the id that family's own graph
// gives it rather than one arrived at a second way.
func (b builder) portfolio(projects []family.Project, now func() time.Time) portfolio.Portfolio {
	// One writer between all of them. A partial read reports itself, and two
	// reports written at the same moment arrive spliced into each other.
	b.stderr = &serialised{to: b.stderr}

	families := make([]portfolio.Family, len(projects))
	running := make(chan struct{}, atOnce)
	var wg sync.WaitGroup
	for i, p := range projects {
		// The slot is taken before the goroutine starts rather than inside it,
		// so a machine with a hundred families has eight reads in flight and
		// not a hundred goroutines queueing to be one.
		running <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-running }()
			families[i] = b.summarise(p)
		}()
	}
	wg.Wait()
	return portfolio.New(released(), now(), families)
}

// summarise is one family's entry: what the resolver already knew about it,
// and then what came of reading its history.
func (b builder) summarise(p family.Project) portfolio.Family {
	known := portfolio.Known(p)
	g, err := b.build(p)
	if err != nil {
		return known.Unread(err)
	}
	return known.Read(g)
}

// serialised is one writer several goroutines share.
type serialised struct {
	mu sync.Mutex
	to io.Writer
}

func (s *serialised) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.to.Write(p)
}
