package note

import (
	"errors"
	"strings"
)

// Tokens splits s on spaces, honouring "double quotes". Inside quotes only
// \" is an escape, so regex backslashes pass through untouched.
func Tokens(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in, has := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case in && c == '\\' && i+1 < len(s) && s[i+1] == '"':
			cur.WriteByte('"')
			i++
		case c == '"':
			in, has = !in, true
		case !in && (c == ' ' || c == '\t'):
			if cur.Len() > 0 || has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteByte(c)
		}
	}
	if in {
		return nil, errors.New("unterminated quote")
	}
	if cur.Len() > 0 || has {
		out = append(out, cur.String())
	}
	return out, nil
}

// SplitUnquoted splits s on sep, ignoring separators inside double quotes.
// It uses the same escape rule as Tokens: only \" is an escape.
func SplitUnquoted(s string, sep byte) []string {
	var out []string
	in, start := false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case in && s[i] == '\\' && i+1 < len(s) && s[i+1] == '"':
			i++
		case s[i] == '"':
			in = !in
		case s[i] == sep && !in:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
