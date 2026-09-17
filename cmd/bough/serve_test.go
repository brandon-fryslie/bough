package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/nickelsec/bough/internal/agent"
	"github.com/nickelsec/bough/internal/agent/registry"
	"github.com/nickelsec/bough/internal/family"
	"github.com/nickelsec/bough/internal/repo"
)

// opening is the builder run makes for a history root, and the projects it
// offers, for a test to ask the builder about directly.
func opening(t *testing.T, root string, noRepo bool, stderr *bytes.Buffer) (builder, []family.Project) {
	t.Helper()
	sources := map[string]agent.Source{}
	var projects []agent.Project
	for _, a := range registry.All() {
		src := a.Open(root)
		sources[a.ID] = src
		found, err := src.Detect()
		if err != nil {
			t.Fatal(err)
		}
		projects = append(projects, found...)
	}
	made := everyAgentsRecord()
	disk := &repo.Disk{}
	families := resolver(projects, made, disk, noRepo)
	return builder{sources: sources, made: made, disk: disk, families: families, noRepo: noRepo, stderr: stderr}, families.Projects()
}

// A family the server builds on request is the graph bough draws when it is
// opened on that family, since both come from one build.
func TestAFamilyBuiltOnRequestIsTheOneOpened(t *testing.T) {
	root := families(t)
	var errs bytes.Buffer
	b, projects := opening(t, root, true, &errs)
	builds := b.every(projects)
	if len(builds) != len(projects) {
		t.Fatalf("%d builds for %d projects", len(builds), len(projects))
	}

	for _, p := range projects {
		var out bytes.Buffer
		if err := run([]string{p.Path, "--json", "--no-repo", "--root", root}, &out, &errs); err != nil {
			t.Fatal(err)
		}
		build, ok := builds[p.Key()]
		if !ok {
			t.Fatalf("no build for %s", p.Key())
		}
		g, err := build()
		if err != nil {
			t.Fatal(err)
		}
		requested, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := parsed(t, requested), parsed(t, out.Bytes()); !reflect.DeepEqual(got, want) {
			t.Errorf("%s built on request as %+v, opened as %+v", p.Name(), got, want)
		}
	}
}

// Families are built side by side when several are asked for at once, each
// asking git about its directories through the one disk. Run under -race.
func TestFamiliesBuildSafelyTogether(t *testing.T) {
	app, site := siblings(t)
	// Written with forward slashes, since the transcript is JSON and a
	// Windows path's separators would be read as escapes.
	app, site = filepath.ToSlash(app), filepath.ToSlash(site)
	root := t.TempDir()
	sitting(t, root, app, 3, app+"/main.go")
	sitting(t, root, app+"/cmd", 2, site+"/index.html")
	sitting(t, root, site, 2, site+"/index.html")

	var errs bytes.Buffer
	b, projects := opening(t, root, false, &errs)
	builds := b.every(projects)
	// How many families the subdirectory makes is the resolver's business
	// and tested there. Here it only has to be more than one, so builds of
	// different families run together.
	if len(builds) < 2 {
		t.Fatalf("%d families, want app and site at least", len(builds))
	}

	var wg sync.WaitGroup
	for key, build := range builds {
		for range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if g, err := build(); err != nil || g.Totals.Turns == 0 {
					t.Errorf("%s: %d prompts, %v", key, g.Totals.Turns, err)
				}
			}()
		}
	}
	wg.Wait()
}

// A family none of whose history can be read has no graph, and says why: the
// words the command has always used, and what the read ran into.
func TestAnUnreadableFamilyHasNoGraph(t *testing.T) {
	root := families(t)
	var errs bytes.Buffer
	b, projects := opening(t, root, true, &errs)

	var draft family.Project
	for _, p := range projects {
		if p.Name() == "draft" {
			draft = p
		}
	}
	if len(draft.Members) != 1 {
		t.Fatalf("draft has %d members, want the one directory", len(draft.Members))
	}
	// Gone between listing and reading, as a history can be.
	if err := os.RemoveAll(draft.Members[0].Ref); err != nil {
		t.Fatal(err)
	}

	g, err := b.every(projects)[draft.Key()]()
	if err == nil {
		t.Fatalf("an unreadable family built a graph of %d prompts", g.Totals.Turns)
	}
	if !strings.Contains(err.Error(), "no readable history for draft") {
		t.Errorf("the error does not say there is no readable history: %v", err)
	}
	if strings.TrimPrefix(err.Error(), "no readable history for draft") == "" {
		t.Errorf("the error does not say what the read ran into: %v", err)
	}
}
