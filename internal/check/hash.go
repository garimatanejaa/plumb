package check

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var rangeRe = regexp.MustCompile(`^(.*):(\d+)-(\d+)$`)

func splitRange(spec string) (path string, a, b int, err error) {
	m := rangeRe.FindStringSubmatch(spec)
	if m == nil {
		return spec, 0, 0, nil
	}
	a, _ = strconv.Atoi(m[2])
	b, _ = strconv.Atoi(m[3])
	if a < 1 || b < a {
		return "", 0, 0, fmt.Errorf("bad line range %d-%d", a, b)
	}
	return m[1], a, b, nil
}

// HashOf returns the sha256 (hex) of a file, or of lines a-b when spec is
// "path:a-b". Line endings are normalised so CRLF checkouts hash the same.
func (c *Checker) HashOf(spec string) (string, error) {
	p, a, b, err := splitRange(spec)
	if err != nil {
		return "", err
	}
	abs, ok, err := c.Resolve(p)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%s: path does not exist", p)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s: not a regular file", p)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if a > 0 {
		lines := strings.Split(text, "\n")
		if b > len(lines) {
			return "", fmt.Errorf("%s: lines %d-%d out of range (file has %d lines)", p, a, b, len(lines))
		}
		text = strings.Join(lines[a-1:b], "\n")
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:]), nil
}

// hash passes when the sha256 of the file (or line range) starts with the
// given prefix of at least 8 hex characters.
func (c *Checker) hash(a []string) error {
	spec, want := a[0], strings.ToLower(a[1])
	if len(want) < 8 || strings.Trim(want, "0123456789abcdef") != "" {
		return fmt.Errorf("hash %s: %q is not a hex prefix of at least 8 characters", spec, a[1])
	}
	got, err := c.HashOf(spec)
	if err != nil {
		return fmt.Errorf("hash %s: %v", spec, err)
	}
	if !strings.HasPrefix(got, want) {
		return fmt.Errorf("hash %s: content changed (now %s)", spec, got[:8])
	}
	return nil
}
