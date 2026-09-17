package graph

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A graph written as --json writes it reads back as the same graph, and one
// in another schema, or not a graph at all, is refused.
func TestAWrittenGraphReadsBack(t *testing.T) {
	want := Graph{
		Schema:    SchemaVersion,
		Generated: time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC),
		Project:   Project{Name: "app", Path: "/src/app", Agents: []string{"claude-code"}},
		Goals:     []Goal{{ID: "g1", Label: "a sitting", Agent: "claude-code"}},
	}
	written, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(strings.NewReader(string(written)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back %+v, wrote %+v", got, want)
	}

	older := want
	older.Schema--
	olderWritten, err := json.Marshal(older)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"an older schema": string(olderWritten),
		"no schema":       `{"project":{"name":"app"}}`,
		"not JSON":        `goals`,
	} {
		if _, err := Read(strings.NewReader(raw)); err == nil {
			t.Errorf("%s: read", name)
		}
	}
}
