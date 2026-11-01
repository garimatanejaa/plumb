package check

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const maxFileSize = 2 << 20

// Directories a walk wanders into but nobody asked about.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	"target": true, ".venv": true, "venv": true, "__pycache__": true, ".git": true,
}

// walk visits every readable text file under root, skipping dependency and
// build directories, binaries and files over 2 MB. It never follows a
// symlink. fn returns true to stop. It reports how many files it opened.
func walk(root string, fn func(path string, data []byte) (stop bool)) (searched int) {
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
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileSize {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || isBinary(data) {
			return nil
		}
		searched++
		if fn(p, data) {
			return fs.SkipAll
		}
		return nil
	})
	return searched
}

// quoteArg writes a pattern the way it is written in a note: in double
// quotes, with only an inner double quote escaped. Backslashes are left alone
// because they are regex syntax, so the message can be pasted back into a note.
func quoteArg(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// grep passes when the RE2 pattern matches in the file, or in any file under
// the directory.
func (c *Checker) grep(a []string) error {
	pattern, target := a[0], a[1]
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("grep %s: bad pattern (RE2 syntax): %v", quoteArg(pattern), err)
	}
	root, ok, err := c.Resolve(target)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("grep %s %s: path does not exist", quoteArg(pattern), target)
	}
	found := false
	searched := walk(root, func(_ string, data []byte) bool {
		found = re.Match(data)
		return found
	})
	if !found {
		return fmt.Errorf("grep %s %s: no match (searched %d files)", quoteArg(pattern), target, searched)
	}
	return nil
}

// snapshot is every readable text file under Root, read once. It makes the
// many searches one `suggest` run performs cost one read of the repository
// instead of one read per search.
type snapshot struct {
	once  sync.Once
	files []snapFile
	off   bool // the repository was too big to hold; search by walking instead
}

type snapFile struct {
	rel  string
	data []byte
}

const maxSnapshotBytes = 512 << 20

// Snapshot returns a Checker that reads the repository once and then answers
// FilesMatching, FilesMatchingAll and FilesMatchingHint from memory. It is
// for a short batch of searches: it never sees later changes to the files, so
// do not keep it around.
func (c *Checker) Snapshot() *Checker { return &Checker{Root: c.Root, snap: &snapshot{}} }

func (s *snapshot) load(c *Checker) {
	s.once.Do(func() {
		total := 0
		walk(c.Root, func(p string, data []byte) bool {
			total += len(data)
			if total > maxSnapshotBytes {
				s.files, s.off = nil, true
				return true
			}
			s.files = append(s.files, snapFile{c.Rel(p), data})
			return false
		})
	})
}

// each calls fn for every readable text file under root, stopping when fn
// returns true. From a snapshot when there is one and the search is
// repository-wide, otherwise by walking.
func (c *Checker) each(root string, fn func(rel string, data []byte) (stop bool)) {
	if c.snap != nil && root == c.Root {
		c.snap.load(c)
		if !c.snap.off {
			for _, f := range c.snap.files {
				if fn(f.rel, f.data) {
					return
				}
			}
			return
		}
	}
	walk(root, func(p string, data []byte) bool { return fn(c.Rel(p), data) })
}

// FilesMatching returns the repo-relative paths of files under scope ("" for
// the whole repository) whose contents match re, skipping the excluded paths.
// It stops after limit matches, so a caller can tell when there are more.
func (c *Checker) FilesMatching(re *regexp.Regexp, scope string, exclude []string, limit int) ([]string, error) {
	return c.FilesMatchingHint("", []*regexp.Regexp{re}, scope, exclude, limit)
}

// FilesMatchingAll is FilesMatching for files that match every pattern.
func (c *Checker) FilesMatchingAll(res []*regexp.Regexp, scope string, exclude []string, limit int) ([]string, error) {
	return c.FilesMatchingHint("", res, scope, exclude, limit)
}

// FilesMatchingHint is FilesMatchingAll for patterns that can only match a
// file containing the literal string hint. Files without it are skipped
// without running a regular expression, which is much faster. An empty hint
// disables the shortcut.
func (c *Checker) FilesMatchingHint(hint string, res []*regexp.Regexp, scope string, exclude []string, limit int) ([]string, error) {
	root := c.Root
	if scope != "" {
		abs, ok, err := c.Resolve(scope)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%s: path does not exist", scope)
		}
		root = abs
	}
	skip := map[string]bool{}
	for _, e := range exclude {
		skip[e] = true
	}
	h := []byte(hint)
	var out []string
	c.each(root, func(rel string, data []byte) bool {
		if skip[rel] || (len(h) > 0 && !bytes.Contains(data, h)) {
			return false
		}
		for _, re := range res {
			if !re.Match(data) {
				return false
			}
		}
		out = append(out, rel)
		return len(out) >= limit
	})
	return out, nil
}

// FindPaths returns the repo-relative paths of files (or directories when dir
// is true) whose path equals suffix or ends with "/"+suffix. It skips
// dependency, build and hidden directories, and stops after limit matches.
// It never reads file contents.
func (c *Checker) FindPaths(suffix string, dir bool, limit int) []string {
	suffix = strings.Trim(filepath.ToSlash(suffix), "/")
	if suffix == "" {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(c.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != c.Root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if !dir || p == c.Root {
				return nil
			}
		} else if dir || !d.Type().IsRegular() {
			return nil
		}
		rel := c.Rel(p)
		if rel == suffix || strings.HasSuffix(rel, "/"+suffix) {
			out = append(out, rel)
			if len(out) >= limit {
				return fs.SkipAll
			}
		}
		return nil
	})
	return out
}
