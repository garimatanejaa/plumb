// Package suggest proposes evidence for note lines that have none.
//
// It is deliberately conservative and heuristic: it only proposes evidence
// it has just verified against the repository, and it proposes nothing when a
// token is ambiguous. It never writes; callers print a diff for a human.
package suggest

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"plumb/internal/check"
	"plumb/internal/note"
)

const (
	maxEvidence    = 3
	maxIdentFiles  = 3
	maxPathMatches = 1000
	maxScan        = 40
)

var (
	bulletRe   = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+\S`)
	tickRe     = regexp.MustCompile("`([^`\\n]+)`")
	pathWordRe = regexp.MustCompile(`^[\w.\-]+(?:/[\w.\-]+)+/?$`)
	pathCharRe = regexp.MustCompile(`^[\w.\-/]+$`)
	identRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{2,}(?:\(\))?$`)
	snakeRe    = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)+$`)
	fileExtRe  = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,5}$`)
	plusRe     = regexp.MustCompile(`\b(\d+)\+`)
	verbRe     = regexp.MustCompile(`^(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+`)
	routeSegRe = regexp.MustCompile(`^[\w.\-~]+$`)
	varSegRe   = regexp.MustCompile(`^(?:\{[^{}/]+\}|:[A-Za-z_]\w*)$`)
)

// Proposal is one line and the comment suggested for it.
type Proposal struct {
	Line     int
	Old, New string
	Evidence []note.Evidence
}

// Report is the outcome of Suggest.
type Report struct {
	Proposals []Proposal
	// Unmatched counts claim-shaped lines (list items and table rows) that
	// carry no evidence and got no proposal.
	Unmatched int
}

// Suggest scans src for list items and table rows without a plumb comment and
// proposes evidence for each, verified against c. noteRel is the note's own
// path relative to the root ("" if outside it); it is excluded from searches
// so a note cannot vouch for itself.
func Suggest(c *check.Checker, src, noteRel string) Report {
	c = c.Snapshot() // one read of the repository serves every search below
	var rep Report
	var exclude []string
	if noteRel != "" {
		exclude = []string{noteRel}
	}
	fence := ""
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i, line := range lines {
		t := strings.TrimLeft(line, " ")
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			if marker := t[:3]; fence == "" {
				fence = marker
			} else if marker == fence {
				fence = ""
			}
			continue
		}
		if fence != "" || note.HasComment(line) {
			continue
		}
		row := note.IsTableRow(line)
		switch {
		case row:
			// A header row and the divider under it are never claims.
			if note.IsTableSeparator(line) || (i+1 < len(lines) && note.IsTableSeparator(lines[i+1])) {
				continue
			}
		case !bulletRe.MatchString(line):
			continue
		}
		var evs []note.Evidence
		seen := map[string]bool{}
		for _, tok := range candidates(line) {
			var add []note.Evidence
			for _, ev := range evidenceFor(c, tok, exclude) {
				if !seen[ev.String()] {
					add = append(add, ev)
				}
			}
			// An endpoint's two greps only make sense together.
			if len(add) == 0 || len(evs)+len(add) > maxEvidence {
				continue
			}
			for _, ev := range add {
				seen[ev.String()] = true
				evs = append(evs, ev)
			}
			if len(evs) == maxEvidence {
				break
			}
		}
		if len(evs) == 0 {
			rep.Unmatched++
			continue
		}
		if ev, ok := countEvidence(c, line, evs); ok {
			evs = append(evs, ev)
		}
		old := strings.TrimRight(line, " \t")
		rep.Proposals = append(rep.Proposals, Proposal{
			Line: i + 1, Old: old, New: withComment(old, note.Comment(evs), row), Evidence: evs,
		})
	}
	return rep
}

// withComment appends a plumb comment to a line. In a table row it goes inside
// the last cell, so it stays hidden when the table is rendered, and any "|" in
// it is escaped so it cannot split the cell.
func withComment(line, comment string, row bool) string {
	if !row {
		return line + " " + comment
	}
	comment = strings.ReplaceAll(comment, "|", `\|`)
	if strings.HasSuffix(line, "|") {
		body := strings.TrimRight(line[:len(line)-1], " \t")
		return body + " " + comment + " |"
	}
	return line + " " + comment
}

// countEvidence turns a claim like "115+ services" into
// count "*.java" <dir> >= 115, but only when the directory is already one of
// the line's proposed paths and holds at least that many files of its most
// common extension right now. The number is a floor the claim itself states,
// so the evidence can only go stale if the directory really shrinks.
func countEvidence(c *check.Checker, line string, evs []note.Evidence) (note.Evidence, bool) {
	m := plusRe.FindStringSubmatch(line)
	if m == nil {
		return note.Evidence{}, false
	}
	want, err := strconv.Atoi(m[1])
	if err != nil || want < 2 {
		return note.Evidence{}, false
	}
	for _, ev := range evs {
		if ev.Kind != "exists" {
			continue
		}
		counts, err := c.ExtCounts(ev.Args[0])
		if err != nil || len(counts) == 0 {
			continue // not a directory, or nothing countable in it
		}
		ext, got := "", 0
		for e, n := range counts {
			if n > got || (n == got && e < ext) {
				ext, got = e, n
			}
		}
		if got >= want {
			return note.Evidence{Kind: "count", Args: []string{"*" + ext, ev.Args[0], ">=", m[1]}}, true
		}
	}
	return note.Evidence{}, false
}

// candidates extracts backticked tokens and path-like words from a line.
func candidates(line string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, m := range tickRe.FindAllStringSubmatch(line, -1) {
		add(m[1])
	}
	plain := tickRe.ReplaceAllString(line, " ")
	for _, w := range strings.Fields(plain) {
		w = strings.TrimRight(strings.TrimLeft(w, `(["'`), `.,;:)]"'`)
		if strings.Contains(w, "/") && !strings.Contains(w, "://") && pathWordRe.MatchString(w) {
			add(w)
		}
	}
	return out
}

// evidenceFor turns one token into verified evidence, or nothing.
func evidenceFor(c *check.Checker, tok string, exclude []string) []note.Evidence {
	if r, ok := parseRoute(tok); ok {
		return routeEvidence(c, r, exclude)
	}
	if p, ok := pathToken(tok); ok {
		if _, found, err := c.Resolve(p); err == nil && found {
			return []note.Evidence{{Kind: "exists", Args: []string{p}}}
		}
		if rel, ok := findRelative(c, tok, p); ok {
			return []note.Evidence{{Kind: "exists", Args: []string{rel}}}
		}
	}
	if !identRe.MatchString(tok) {
		return nil
	}
	name := strings.TrimSuffix(tok, "()")
	// A plain lowercase word (name, listings, skills) is a field or a
	// parameter, not a symbol with a definition worth anchoring to.
	if !looksLikeSymbol(name) {
		return nil
	}
	if ev, ok := identEvidence(c, name, exclude); ok {
		return []note.Evidence{ev}
	}
	return nil
}

// looksLikeSymbol reports whether name has a shape that code symbols have and
// ordinary words do not: a capital, an underscore or a digit.
func looksLikeSymbol(name string) bool {
	return strings.ContainsAny(name, "_0123456789") || name != strings.ToLower(name)
}

// pathToken cleans a token that could be a repo path.
func pathToken(tok string) (string, bool) {
	p := strings.TrimSuffix(strings.TrimPrefix(tok, "./"), "/")
	if p == "" || !pathCharRe.MatchString(p) || strings.HasPrefix(p, "-") ||
		strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return "", false
	}
	return p, true
}

// findRelative resolves a package-relative path such as "controller/" or
// "kafka/consumer/" to a place it exists in the repository. A trailing slash
// means a directory; otherwise the token must look like a file name.
//
// Test copies are ignored. If several matches remain, the shallowest one wins
// provided it is the only one at that depth: a note that says "pojo/" almost
// always means the top-level package, not one buried inside a client. Two
// matches at the same depth stay ambiguous and nothing is proposed.
func findRelative(c *check.Checker, tok, p string) (string, bool) {
	dir := strings.HasSuffix(tok, "/")
	if !dir && !fileExtRe.MatchString(p) {
		return "", false
	}
	var best []string
	depth := -1
	for _, m := range c.FindPaths(p, dir, maxPathMatches) {
		if inTestTree(m) {
			continue
		}
		d := strings.Count(m, "/")
		switch {
		case depth == -1 || d < depth:
			best, depth = []string{m}, d
		case d == depth:
			best = append(best, m)
		}
	}
	if len(best) != 1 {
		return "", false
	}
	return best[0], true
}

var testSegments = map[string]bool{
	"test": true, "tests": true, "testdata": true, "__tests__": true,
	"testfixtures": true, "androidtest": true, "integrationtest": true,
}

func inTestTree(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if testSegments[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

// identEvidence proposes a grep for an identifier, but only where the
// identifier is defined: a class, type, func, CREATE TABLE and so on. A
// passing mention in a comment or a plan is not evidence, and would raise a
// false alarm the moment that file changes.
//
// A snake_case name is usually a database table or column. If nothing
// defines it, a quoted use of it in source code counts, preferring generated
// jOOQ classes, which are the table's definition on the Java side.
func identEvidence(c *check.Checker, name string, exclude []string) (note.Evidence, bool) {
	files := lookup(c, name, definitionRe(name), exclude)
	if len(files) == 0 && snakeRe.MatchString(name) {
		files = tableMentions(c, name, exclude)
	}
	scope, ok := scopeOf(files)
	if !ok {
		return note.Evidence{}, false
	}
	return note.Evidence{Kind: "grep", Args: []string{`\b` + name + `\b`, scope}}, true
}

// definitionRe matches a declaration of name in common languages.
func definitionRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`\b(?:class|interface|enum|record|struct|trait|object|type|func|function|def|fun|fn|const|(?i:create\s+(?:or\s+replace\s+)?(?:table|view|index|function)(?:\s+if\s+not\s+exists)?))\s+(?:[\w."]+\.)?` + name + `\b`)
}

var sourceExts = map[string]bool{
	".java": true, ".kt": true, ".scala": true, ".go": true, ".py": true, ".rb": true,
	".ts": true, ".tsx": true, ".js": true, ".sql": true, ".xml": true, ".cs": true,
	".php": true, ".rs": true, ".swift": true,
}

// tableMentions finds source files that use name as a quoted string, such as
// DSL.name("order_items") in a generated jOOQ class or a SQL string.
func tableMentions(c *check.Checker, name string, exclude []string) []string {
	var src, gen []string
	for _, f := range lookup(c, name, regexp.MustCompile("[\"'`]"+name+"[\"'`]"), exclude) {
		if !sourceExts[strings.ToLower(path.Ext(f))] {
			continue // fixtures, csv, json and the like
		}
		src = append(src, f)
		if hasSegment(f, "generated") {
			gen = append(gen, f)
		}
	}
	if len(gen) > 0 {
		return gen
	}
	return src
}

// lookup returns the code files (no docs, hidden folders or tests) matching re.
// hint is a literal every matching file must contain.
func lookup(c *check.Checker, hint string, re *regexp.Regexp, exclude []string) []string {
	files, err := c.FilesMatchingHint(hint, []*regexp.Regexp{re}, "", exclude, maxScan)
	if err != nil {
		return nil
	}
	return codeFiles(files)
}

func codeFiles(files []string) []string {
	var out []string
	for _, f := range codeOnly(files) {
		if !inTestTree(f) {
			out = append(out, f)
		}
	}
	return out
}

// scopeOf picks what to grep: the one file, or the one directory when up to
// maxIdentFiles files in the same directory qualify. Anything more spread out
// is too loose, and gets no evidence. Files under a controller directory win
// over the rest.
func scopeOf(files []string) (string, bool) {
	var ctl []string
	for _, f := range files {
		if strings.Contains(strings.ToLower(f), "controller") {
			ctl = append(ctl, f)
		}
	}
	if len(ctl) > 0 {
		files = ctl
	}
	if len(files) == 0 || len(files) > maxIdentFiles {
		return "", false
	}
	if len(files) == 1 {
		return files[0], true
	}
	dir := path.Dir(files[0])
	for _, f := range files[1:] {
		if path.Dir(f) != dir {
			return "", false
		}
	}
	if dir == "." {
		return "", false
	}
	return dir, true
}

var docExts = map[string]bool{".md": true, ".markdown": true, ".rst": true, ".txt": true, ".adoc": true}

// codeOnly drops documentation files, anything under a documentation
// directory, and anything under a hidden directory.
func codeOnly(files []string) []string {
	var out []string
	for _, f := range files {
		if docExts[strings.ToLower(path.Ext(f))] || hasHiddenSegment(f) || inDocsTree(f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

var docDirs = map[string]bool{"docs": true, "doc": true, "documentation": true, "wiki": true}

func inDocsTree(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if docDirs[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

func hasSegment(p, want string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.EqualFold(seg, want) {
			return true
		}
	}
	return false
}

func hasHiddenSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") && seg != "." {
			return true
		}
	}
	return false
}

// route is an HTTP path split into segments; isVar marks {id} and :id.
type route struct {
	segs  []string
	isVar []bool
}

// parseRoute reads "/v2/catalog/{id}", "GET /v1/region?lng=&lat=" and the like.
// The verb and query string are ignored.
func parseRoute(tok string) (route, bool) {
	t := verbRe.ReplaceAllString(strings.TrimSpace(tok), "")
	if i := strings.IndexAny(t, "?#"); i >= 0 {
		t = t[:i]
	}
	if !strings.HasPrefix(t, "/") || strings.Contains(t, "//") || strings.Contains(t, "://") {
		return route{}, false
	}
	t = strings.TrimRight(t, "/")
	if t == "" {
		return route{}, false
	}
	var r route
	static := 0
	for _, seg := range strings.Split(t[1:], "/") {
		switch {
		case varSegRe.MatchString(seg):
			r.segs, r.isVar = append(r.segs, seg), append(r.isVar, true)
		case routeSegRe.MatchString(seg):
			r.segs, r.isVar = append(r.segs, seg), append(r.isVar, false)
			static++
		default:
			return route{}, false
		}
	}
	return r, static > 0
}

// routeEvidence finds where a route is declared in code.
//
// Spring and similar frameworks split a route across a class-level mapping
// ("/v2/catalog") and a method-level one ("/pricing"), so the full path rarely
// appears as one string. For the longest static prefix it can find, it
// proposes a grep for that prefix as a string literal, plus one for the static
// tail after the last variable, both in the same file or directory. Variable
// names are ignored because code and notes rarely agree on them.
func routeEvidence(c *check.Checker, r route, exclude []string) []note.Evidence {
	for k := len(r.segs); k >= 1; k-- {
		if anyTrue(r.isVar[:k]) {
			continue
		}
		head := `"` + regexp.QuoteMeta("/"+strings.Join(r.segs[:k], "/")) + `["/]`
		res := []*regexp.Regexp{regexp.MustCompile(head)}
		tail, tailPat := trailingStatic(r.segs[k:], r.isVar[k:]), ""
		if len(tail) > 0 {
			tailPat = `["/]` + regexp.QuoteMeta(strings.Join(tail, "/")) + `"`
			res = append(res, regexp.MustCompile(tailPat))
		}
		files, err := c.FilesMatchingHint("/"+strings.Join(r.segs[:k], "/"), res, "", exclude, maxScan)
		if err != nil {
			return nil
		}
		files = codeFiles(files)
		if len(files) == 0 {
			continue // try a shorter prefix
		}
		scope, ok := scopeOf(files)
		if !ok {
			return nil // declared in several places: do not guess
		}
		evs := []note.Evidence{{Kind: "grep", Args: []string{head, scope}}}
		if tailPat != "" {
			evs = append(evs, note.Evidence{Kind: "grep", Args: []string{tailPat, scope}})
		}
		return evs
	}
	return nil
}

func anyTrue(b []bool) bool {
	for _, v := range b {
		if v {
			return true
		}
	}
	return false
}

// trailingStatic returns the run of static segments after the last variable.
func trailingStatic(segs []string, isVar []bool) []string {
	i := len(segs)
	for i > 0 && !isVar[i-1] {
		i--
	}
	return segs[i:]
}

// Diff renders the proposals as a zero-context unified diff that
// `patch -p1` accepts. It returns "" when there is nothing to propose.
func Diff(file string, rep Report) string {
	if len(rep.Proposals) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", file, file)
	for _, p := range rep.Proposals {
		fmt.Fprintf(&b, "@@ -%d,1 +%d,1 @@\n-%s\n+%s\n", p.Line, p.Line, p.Old, p.New)
	}
	return b.String()
}
