package graph

import (
	"encoding/json"
	"fmt"
	"io"
)

// Read reads a graph bough wrote with --json, refusing one written in another
// schema rather than misreading it.
func Read(r io.Reader) (Graph, error) {
	var g Graph
	if err := json.NewDecoder(r).Decode(&g); err != nil {
		return Graph{}, fmt.Errorf("reading a graph: %w", err)
	}
	if g.Schema != SchemaVersion {
		return Graph{}, fmt.Errorf("the graph is written in schema %d, and this bough reads schema %d; write it again with this bough's --json", g.Schema, SchemaVersion)
	}
	return g, nil
}
