// Command plumb checks whether the claims in agent notes are still true.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"plumb/internal/check"
	"plumb/internal/mcp"
	"plumb/internal/note"
	"plumb/internal/render"
	"plumb/internal/suggest"
)

const version = "0.1.0"

const usage = `plumb ` + version + ` - notes that know when they are stale

Usage:
  plumb check   [--root DIR] [--json] [FILE...]   verify claims; exit 1 if any is stale
  plumb filter  [--root DIR] [--drop] FILE        print the note with stale claims marked or dropped
  plumb hash    [--root DIR] PATH[:A-B]           print hash evidence to paste into a note
  plumb suggest [--root DIR] FILE                 propose evidence for bullets that have none (diff only)
  plumb mcp     [--root DIR]                      serve notes to agents over MCP on stdio
  plumb version

A claim is a line with evidence in a comment:
  - Login lives in the auth package <!-- plumb: exists src/auth ; grep "func Login" src/auth -->

Evidence: exists PATH | absent PATH | grep "RE2" PATH | hash PATH[:A-B] HEX8+ | count "GLOB" DIR OP N
Default files: CLAUDE.md and AGENTS.md in the current directory.
Default root: the directory containing the note (the current directory for hash and mcp).
Exit codes: 0 all fresh, 1 stale claims found, 2 usage or I/O error.
`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "check":
		return cmdCheck(rest, stdout, stderr)
	case "filter":
		return cmdFilter(rest, stdout, stderr)
	case "hash":
		return cmdHash(rest, stdout, stderr)
	case "suggest":
		return cmdSuggest(rest, stdout, stderr)
	case "mcp":
		return cmdMCP(rest, stdin, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "plumb", version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "plumb: unknown command %q\n\n%s", cmd, usage)
	return 2
}

// parse allows flags after positional arguments, like most CLIs.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func defaultFiles() []string {
	var out []string
	for _, f := range []string{"CLAUDE.md", "AGENTS.md"} {
		if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() {
			out = append(out, f)
		}
	}
	return out
}

// loaded is a note read from disk with a checker for its repository.
type loaded struct {
	path   string
	src    string
	rel    string // path relative to the root, "" when outside it
	claims []note.Claim
	c      *check.Checker
}

func load(path, root string) (*loaded, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if root == "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		root = filepath.Dir(abs)
	}
	c, err := check.New(root)
	if err != nil {
		return nil, err
	}
	l := &loaded{path: path, src: string(data), claims: note.Parse(string(data)), c: c}
	if abs, err := filepath.Abs(path); err == nil {
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			if r := c.Rel(real); !strings.HasPrefix(r, "..") && !filepath.IsAbs(r) {
				l.rel = r
			}
		}
	}
	return l, nil
}

func cmdCheck(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("check", stderr)
	root := fs.String("root", "", "repository root (default: the note's directory)")
	asJSON := fs.Bool("json", false, "print results as JSON")
	files, err := parse(fs, args)
	if err != nil {
		return 2
	}
	if len(files) == 0 {
		files = defaultFiles()
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "plumb check: no note given and no CLAUDE.md or AGENTS.md in the current directory")
		return 2
	}
	var reports []render.FileReport
	anyStale := false
	for i, f := range files {
		l, err := load(f, *root)
		if err != nil {
			fmt.Fprintf(stderr, "plumb check: %v\n", err)
			return 2
		}
		rs := l.c.Verify(l.claims)
		if _, stale := render.Counts(rs); stale > 0 {
			anyStale = true
		}
		if *asJSON {
			reports = append(reports, render.FileReport{File: f, Results: rs})
			continue
		}
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprint(stdout, render.Text(f, rs))
	}
	if *asJSON {
		b, err := render.JSON(reports)
		if err != nil {
			fmt.Fprintf(stderr, "plumb check: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(b))
	}
	if anyStale {
		return 1
	}
	return 0
}

func cmdFilter(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("filter", stderr)
	root := fs.String("root", "", "repository root (default: the note's directory)")
	drop := fs.Bool("drop", false, "remove stale claim lines instead of marking them")
	files, err := parse(fs, args)
	if err != nil {
		return 2
	}
	if len(files) != 1 {
		fmt.Fprintln(stderr, "plumb filter: give exactly one note file")
		return 2
	}
	l, err := load(files[0], *root)
	if err != nil {
		fmt.Fprintf(stderr, "plumb filter: %v\n", err)
		return 2
	}
	fmt.Fprint(stdout, render.Filter(l.src, l.c.Verify(l.claims), *drop))
	return 0
}

func cmdHash(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("hash", stderr)
	root := fs.String("root", ".", "repository root")
	specs, err := parse(fs, args)
	if err != nil {
		return 2
	}
	if len(specs) == 0 {
		fmt.Fprintln(stderr, "plumb hash: give at least one PATH or PATH:A-B")
		return 2
	}
	c, err := check.New(*root)
	if err != nil {
		fmt.Fprintf(stderr, "plumb hash: %v\n", err)
		return 2
	}
	for _, s := range specs {
		h, err := c.HashOf(s)
		if err != nil {
			fmt.Fprintf(stderr, "plumb hash: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, note.Evidence{Kind: "hash", Args: []string{s, h[:8]}})
	}
	return 0
}

func cmdSuggest(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("suggest", stderr)
	root := fs.String("root", "", "repository root (default: the note's directory)")
	files, err := parse(fs, args)
	if err != nil {
		return 2
	}
	if len(files) != 1 {
		fmt.Fprintln(stderr, "plumb suggest: give exactly one note file")
		return 2
	}
	l, err := load(files[0], *root)
	if err != nil {
		fmt.Fprintf(stderr, "plumb suggest: %v\n", err)
		return 2
	}
	rep := suggest.Suggest(l.c, l.src, l.rel)
	name := files[0]
	if l.rel != "" {
		name = l.rel
	}
	fmt.Fprint(stdout, suggest.Diff(name, rep))
	fmt.Fprintf(stderr, "plumb suggest: %d proposal(s); %d bullet(s) without evidence had nothing checkable\n",
		len(rep.Proposals), rep.Unmatched)
	return 0
}

func cmdMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlags("mcp", stderr)
	root := fs.String("root", ".", "repository root; notes and evidence are read only from here")
	if _, err := parse(fs, args); err != nil {
		return 2
	}
	srv, err := mcp.New(*root, version)
	if err != nil {
		fmt.Fprintf(stderr, "plumb mcp: %v\n", err)
		return 2
	}
	if err := srv.Serve(stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "plumb mcp: %v\n", err)
		return 2
	}
	return 0
}
