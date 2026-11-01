package check

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var countOps = map[string]func(got, want int) bool{
	">=": func(g, w int) bool { return g >= w },
	"<=": func(g, w int) bool { return g <= w },
	"==": func(g, w int) bool { return g == w },
	">":  func(g, w int) bool { return g > w },
	"<":  func(g, w int) bool { return g < w },
}

// count passes when the number of files under a directory whose name matches
// the glob satisfies the comparison, for example:
//
//	count "*.java" src/main/java/app/service >= 115
//
// Only file names are matched, never paths, and dependency and build
// directories are not counted.
func (c *Checker) count(a []string) error {
	glob, target, op, ns := a[0], a[1], a[2], a[3]
	cmp, ok := countOps[op]
	if !ok {
		return fmt.Errorf("count: unknown comparison %q (use >=, <=, ==, > or <)", op)
	}
	want, err := strconv.Atoi(ns)
	if err != nil || want < 0 {
		return fmt.Errorf("count: %q is not a non-negative number", ns)
	}
	got, err := c.CountFiles(glob, target)
	if err != nil {
		return fmt.Errorf("count %s %s: %v", glob, target, err)
	}
	if !cmp(got, want) {
		return fmt.Errorf("count %s %s: found %d, want %s %d", glob, target, got, op, want)
	}
	return nil
}

// CountFiles counts regular files under target whose base name matches glob.
func (c *Checker) CountFiles(glob, target string) (int, error) {
	if strings.ContainsAny(glob, "/\\") {
		return 0, fmt.Errorf("glob %q must match file names, not paths", glob)
	}
	if _, err := filepath.Match(glob, "x"); err != nil {
		return 0, fmt.Errorf("bad glob %q: %v", glob, err)
	}
	root, err := c.dirOf(target)
	if err != nil {
		return 0, err
	}
	n := 0
	walkNames(root, func(name string) {
		if ok, _ := filepath.Match(glob, name); ok {
			n++
		}
	})
	return n, nil
}

// ExtCounts returns how many files under target have each extension
// (lower-case, with the dot; files without one are ignored).
func (c *Checker) ExtCounts(target string) (map[string]int, error) {
	root, err := c.dirOf(target)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	walkNames(root, func(name string) {
		if ext := strings.ToLower(filepath.Ext(name)); ext != "" && ext != name {
			out[ext]++
		}
	})
	return out, nil
}

// dirOf resolves target and requires it to be a directory.
func (c *Checker) dirOf(target string) (string, error) {
	abs, ok, err := c.Resolve(target)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("path does not exist")
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("not a directory")
	}
	return abs, nil
}

// walkNames calls fn with the base name of every regular file under root,
// skipping dependency and build directories. It never follows a symlink and
// never reads file contents.
func walkNames(root string, fn func(name string)) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			fn(d.Name())
		}
		return nil
	})
}
