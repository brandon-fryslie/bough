package scenario

import (
	"context"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/nickelsec/bough/internal/graph"
	"github.com/nickelsec/bough/internal/perf"
	"github.com/nickelsec/bough/internal/server"
	"github.com/nickelsec/bough/internal/synthetic"
	"github.com/nickelsec/bough/internal/webdriver"
)

// browsers names the real browsers to play scenarios in. Driving one opens a
// window and needs its driver set up, so it is asked for, never assumed:
//
//	go test ./internal/scenario -browsers=chrome,safari
var browsers = flag.String("browsers", "", "real browsers to drive, comma separated: chrome, safari")

// bough's page, drawn from a synthetic history, has somewhere for every
// scenario to land in every browser, and where the browser's driver input
// reaches the page, every scenario plays, takes, and records something to
// judge.
func TestScenariosPlayOnASyntheticHistory(t *testing.T) {
	if *browsers == "" {
		t.Skip("no -browsers to drive")
	}
	shape, err := synthetic.NewShape(24, 4, 5, 12)
	if err != nil {
		t.Fatal(err)
	}
	url := serve(t, synthetic.History(shape))

	for _, name := range strings.FieldsFunc(*browsers, func(r rune) bool { return r == ',' || r == ' ' }) {
		t.Run(name, func(t *testing.T) {
			b, err := webdriver.Named(name)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			s := open(ctx, t, b)

			t.Run("geometry", func(t *testing.T) {
				g, err := Open(ctx, s, url)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("stage %+v, bare canvas at %v, %d prompts to point at", g.Stage, g.Empty, len(g.Prompts))
			})
			for _, sc := range All {
				t.Run(sc.Name, func(t *testing.T) {
					if err := b.Input(); err != nil {
						t.Skip(err)
					}
					r, err := Run(ctx, s, url, sc)
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("%+v", perf.Summarize(r))
				})
			}
		})
	}
}

// serve puts g's page on a loopback port until the test ends.
func serve(t *testing.T, g graph.Graph) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	urls := make(chan string, 1)
	// stopped closes once Serve has returned, with its error in served, so
	// both the wait for an address and the cleanup can see it has.
	var served error
	stopped := make(chan struct{})
	go func() {
		served = server.Serve(ctx, "synthetic", g, nil, func(id string) string { return id }, func(url string) { urls <- url })
		close(stopped)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
		if served != nil {
			t.Errorf("serving the page: %v", served)
		}
	})
	select {
	case url := <-urls:
		return url
	case <-stopped:
		t.Fatal("the page stopped being served before it had an address")
		return ""
	}
}

// open starts b's driver and a session in it, both ended when the test is.
func open(ctx context.Context, t *testing.T, b webdriver.Browser) *webdriver.Session {
	t.Helper()
	d, err := webdriver.Start(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	s, err := d.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := s.Close(closing); err != nil {
			t.Error(err)
		}
	})
	return s
}
