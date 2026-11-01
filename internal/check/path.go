// Package check verifies evidence against a repository on disk. It is the
// only package that reads the repository, and every read goes through
// Resolve, the one path-safety gate.
package check

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Checker verifies evidence relative to Root.
type Checker struct {
	Root string
	snap *snapshot
}

// New returns a Checker for root, with symlinks in root itself resolved once.
func New(root string) (*Checker, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("root %s is not a directory", root)
	}
	return &Checker{Root: real}, nil
}

// Resolve maps a repo-relative path to a real path under Root.
//
// It rejects empty paths, absolute paths and any ".." segment, then resolves
// symlinks and re-checks the real path against Root so a link cannot lead out
// of the repository. A path that does not exist is not an error: ok is false.
// A dangling symlink counts as missing.
func (c *Checker) Resolve(p string) (abs string, ok bool, err error) {
	if p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return "", false, fmt.Errorf("path %q must be relative to the repository root", p)
	}
	slash := filepath.ToSlash(p)
	for _, seg := range strings.Split(slash, "/") {
		if seg == ".." {
			return "", false, fmt.Errorf("path %q leaves the repository", p)
		}
	}
	full := filepath.Join(c.Root, filepath.FromSlash(slash))
	if _, err := os.Lstat(full); err != nil {
		if os.IsNotExist(err) {
			return full, false, nil
		}
		return "", false, err
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return full, false, nil
	}
	if real != c.Root && !strings.HasPrefix(real, c.Root+string(filepath.Separator)) {
		return "", false, fmt.Errorf("path %q resolves outside the repository", p)
	}
	return real, true, nil
}

// Rel returns the slash-separated path of abs relative to Root.
func (c *Checker) Rel(abs string) string {
	r, err := filepath.Rel(c.Root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}

func isBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(b[:n], 0) >= 0
}
