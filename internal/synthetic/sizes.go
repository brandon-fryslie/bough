package synthetic

import (
	"fmt"
	"strings"
)

// sizes are the histories a measurement can ask for by name, smallest first,
// so a number taken on one machine names a graph anyone can rebuild on
// another.
var sizes = []struct {
	name  string
	shape Shape
}{
	// 240 prompts over 48 days: the prompts of the 249-prompt project the
	// stutter was first measured on, packed tighter.
	{"small", Shape{sittings: 24, tasks: 4, prompts: 5, links: 12}},
	// 1,404 prompts over 216 days.
	{"medium", Shape{sittings: 108, tasks: 6, prompts: 4, links: 60}},
	// 7,665 prompts over two years.
	{"large", Shape{sittings: 365, tasks: 8, prompts: 5, links: 200}},
}

// Sized is the Shape called name.
func Sized(name string) (Shape, error) {
	for _, s := range sizes {
		if s.name == name {
			return s.shape, nil
		}
	}
	return Shape{}, fmt.Errorf("no synthetic size called %q; there are %s", name, strings.Join(SizeNames(), ", "))
}

// SizeNames is the name of every size Sized knows, smallest first.
func SizeNames() []string {
	names := make([]string, len(sizes))
	for i, s := range sizes {
		names[i] = s.name
	}
	return names
}
