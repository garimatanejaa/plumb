// Package note parses claims out of a markdown note. It never touches the
// filesystem: a claim is a line carrying a plumb comment,
//
//   - Login lives in the auth package <!-- plumb: exists src/auth ; grep "func Login" src/auth -->
package note

import (
	"fmt"
	"regexp"
	"strings"
)

// Kinds maps each evidence kind to the number of arguments it takes.
// internal/check must provide a handler for every kind listed here.
var Kinds = map[string]int{"exists": 1, "absent": 1, "grep": 2, "hash": 2, "count": 4}

type Evidence struct {
	Kind string   `json:"kind"`
	Args []string `json:"args"`
}

// String renders evidence the way it is written in a note.
func (e Evidence) String() string {
	parts := []string{e.Kind}
	for _, a := range e.Args {
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}

func quote(a string) string {
	if a == "" || strings.ContainsAny(a, " \t\";\\") {
		return `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
	}
	return a
}

type Claim struct {
	Line     int        `json:"line"`
	Text     string     `json:"text"`
	Evidence []Evidence `json:"evidence"`
	ParseErr string     `json:"parseError,omitempty"`
}

var commentRe = regexp.MustCompile(`<!--\s*plumb:\s*(.*?)\s*-->`)

// IsTableRow reports whether the line is a row of a markdown table.
func IsTableRow(line string) bool {
	t := strings.TrimSpace(line)
	return len(t) > 1 && t[0] == '|'
}

// IsTableSeparator reports whether the line is a table's |---|---| divider.
func IsTableSeparator(line string) bool {
	t := strings.TrimSpace(line)
	return IsTableRow(line) && strings.Contains(t, "-") && strings.Trim(t, "|:- \t") == ""
}

// HasComment reports whether the line carries a plumb comment.
func HasComment(line string) bool { return commentRe.MatchString(line) }

// Comment renders evidence as a plumb comment.
func Comment(evs []Evidence) string {
	parts := make([]string, len(evs))
	for i, e := range evs {
		parts[i] = e.String()
	}
	return "<!-- plumb: " + strings.Join(parts, " ; ") + " -->"
}

// Parse returns every claim in src. Lines inside fenced code blocks are
// never claims, so a note can show the syntax without being judged by it.
func Parse(src string) []Claim {
	var out []Claim
	fence := ""
	for i, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		t := strings.TrimLeft(line, " ")
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			if marker := t[:3]; fence == "" {
				fence = marker
			} else if marker == fence {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		m := commentRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		c := Claim{Line: i + 1, Text: claimText(commentRe.ReplaceAllString(line, ""))}
		inner := m[1]
		if IsTableRow(line) {
			// A raw "|" would split the table cell, so evidence in a row
			// writes a pipe as \| (the markdown convention) and it is undone here.
			inner = strings.ReplaceAll(inner, `\|`, `|`)
		}
		for _, raw := range SplitUnquoted(inner, ';') {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			ev, err := parseEvidence(raw)
			if err != nil {
				c.ParseErr = err.Error()
				break
			}
			c.Evidence = append(c.Evidence, ev)
		}
		if c.ParseErr == "" && len(c.Evidence) == 0 {
			c.ParseErr = "plumb comment has no evidence"
		}
		out = append(out, c)
	}
	return out
}

func claimText(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "|") {
		var cells []string
		for _, cell := range strings.Split(strings.Trim(s, "|"), "|") {
			if cell = strings.TrimSpace(cell); cell != "" {
				cells = append(cells, cell)
			}
		}
		return strings.Join(cells, " — ")
	}
	for _, p := range []string{"- ", "* ", "+ "} {
		s = strings.TrimPrefix(s, p)
	}
	return strings.TrimSpace(s)
}

func parseEvidence(raw string) (Evidence, error) {
	toks, err := Tokens(raw)
	if err != nil {
		return Evidence{}, err
	}
	if len(toks) == 0 {
		return Evidence{}, fmt.Errorf("empty evidence")
	}
	e := Evidence{Kind: toks[0], Args: toks[1:]}
	n, ok := Kinds[e.Kind]
	if !ok {
		return e, fmt.Errorf("unknown evidence %q (use exists, absent, grep, hash or count)", e.Kind)
	}
	if len(e.Args) != n {
		return e, fmt.Errorf("%s takes %d argument(s), got %d: %s", e.Kind, n, len(e.Args), raw)
	}
	return e, nil
}
