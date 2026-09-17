package server

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The scripts are shipped as they are written, so a typo in one is a page that
// does not load at all rather than a feature that misbehaves. Nothing else in
// the suite reads them as JavaScript: the Go tests serve the bytes and check
// the headers, which a broken script passes cleanly.
//
// This is not a parser and does not try to be. It catches the mistake that
// actually happened, which is a string left open by a newline written into it
// raw, along with the unbalanced brackets that follow from the same slip.
func TestScriptsAreWellFormed(t *testing.T) {
	for _, name := range []string{"bough.js", "layout.js"} {
		b, err := assets.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := wellFormed(string(b)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Both scripts are one function scope each, and a function declared twice in
// one scope is not an error in JavaScript: the later declaration silently
// replaces the earlier everywhere, including in code above both. That happened
// once, a show for the view shadowed by the show for the note, and the page
// loaded with its diagram never placed.
//
// Declarations at the top of each script's scope are indented two spaces, so
// that is what this reads. A function nested inside another is its own scope.
func TestScriptsDeclareEachFunctionOnce(t *testing.T) {
	declared := regexp.MustCompile(`(?m)^  function ([A-Za-z_$][\w$]*)\(`)
	for _, name := range []string{"bough.js", "layout.js"} {
		b, err := assets.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		seen := map[string]bool{}
		for _, m := range declared.FindAllStringSubmatch(string(b), -1) {
			if seen[m[1]] {
				t.Errorf("%s declares function %s twice, so one replaces the other", name, m[1])
			}
			seen[m[1]] = true
		}
		if len(seen) == 0 {
			t.Errorf("%s declares no function at the top of its scope, so this reads nothing", name)
		}
	}
}

// wellFormed walks the source once, skipping over comments and string bodies,
// and reports the first thing that cannot be right.
func wellFormed(src string) error {
	var stack []byte
	closing := map[byte]byte{')': '(', ']': '[', '}': '{'}
	line := 1

	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\n' {
			line++
			continue
		}
		// Comments are not code and may hold anything at all.
		if c == '/' && i+1 < len(src) {
			if src[i+1] == '/' {
				for i < len(src) && src[i] != '\n' {
					i++
				}
				line++
				continue
			}
			if src[i+1] == '*' {
				end := strings.Index(src[i+2:], "*/")
				if end < 0 {
					return posErr(line, "a block comment is never closed")
				}
				line += strings.Count(src[i:i+2+end+2], "\n")
				i += 2 + end + 1
				continue
			}
		}
		if c == '"' || c == '\'' {
			j, ok := endOfString(src, i)
			if !ok {
				return posErr(line, "a string is left open, usually a newline written into it raw")
			}
			i = j
			continue
		}
		switch c {
		case '(', '[', '{':
			stack = append(stack, c)
		case ')', ']', '}':
			if len(stack) == 0 || stack[len(stack)-1] != closing[c] {
				return posErr(line, "a bracket closes something that was never opened")
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 {
		return posErr(line, "a bracket is never closed")
	}
	return nil
}

// endOfString finds the quote that closes the one at start. A quoted run may
// not cross a line: that is exactly the mistake this exists to catch.
func endOfString(src string, start int) (int, bool) {
	quote := src[start]
	for i := start + 1; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case '\n':
			return 0, false
		case quote:
			return i, true
		}
	}
	return 0, false
}

func posErr(line int, what string) error {
	return &scriptError{line: line, what: what}
}

type scriptError struct {
	line int
	what string
}

func (e *scriptError) Error() string {
	return "line " + itoa(e.line) + ": " + e.what
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
