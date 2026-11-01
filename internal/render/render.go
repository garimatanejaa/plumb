// Package render turns check results into text, JSON or a filtered note.
// It never touches the filesystem.
package render

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"plumb/internal/check"
	"plumb/internal/note"
)

// Counts returns the number of claims and how many are stale.
func Counts(rs []check.Result) (total, stale int) {
	for _, r := range rs {
		total++
		if r.Stale {
			stale++
		}
	}
	return total, stale
}

// Text is the human report for one note.
func Text(file string, rs []check.Result) string {
	var b strings.Builder
	fmt.Fprintln(&b, file)
	if len(rs) == 0 {
		fmt.Fprintln(&b, "  no claims found; add <!-- plumb: ... --> comments (plumb suggest can propose them)")
		return b.String()
	}
	for _, r := range rs {
		mark := "✓"
		if r.Stale {
			mark = "✗"
		}
		fmt.Fprintf(&b, "  %s line %d  %s\n", mark, r.Claim.Line, clip(r.Claim.Text, 100))
		for _, why := range r.Reasons {
			fmt.Fprintf(&b, "      %s\n", why)
		}
	}
	total, stale := Counts(rs)
	fmt.Fprintf(&b, "%d claims, %d stale\n", total, stale)
	return b.String()
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// FileReport is one note's results, for JSON output.
type FileReport struct {
	File    string         `json:"file"`
	Results []check.Result `json:"results"`
}

// JSON renders reports as an indented JSON array.
func JSON(reports []FileReport) ([]byte, error) {
	if reports == nil {
		reports = []FileReport{}
	}
	for i := range reports {
		if reports[i].Results == nil {
			reports[i].Results = []check.Result{}
		}
	}
	return json.MarshalIndent(reports, "", "  ")
}

var markerRe = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)

// Filter returns src with every stale claim line prefixed "[STALE]", or
// removed when drop is set. Every other byte is unchanged.
func Filter(src string, rs []check.Result, drop bool) string {
	stale := map[int]bool{}
	for _, r := range rs {
		if r.Stale {
			stale[r.Claim.Line] = true
		}
	}
	if len(stale) == 0 {
		return src
	}
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if !stale[i+1] {
			out = append(out, l)
			continue
		}
		if drop {
			continue
		}
		if note.IsTableRow(l) {
			// Keep the row a row: mark inside the first cell.
			q := strings.Index(l, "|") + 1
			for q < len(l) && l[q] == ' ' {
				q++
			}
			out = append(out, l[:q]+"[STALE] "+l[q:])
			continue
		}
		at := len(markerRe.FindString(l))
		out = append(out, l[:at]+"[STALE] "+l[at:])
	}
	return strings.Join(out, "\n")
}
