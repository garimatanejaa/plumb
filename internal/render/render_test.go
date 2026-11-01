package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"plumb/internal/check"
	"plumb/internal/note"
)

var update = flag.Bool("update", false, "rewrite golden files")

const sample = "../../testdata/sample-repo"

func results(t *testing.T) (string, []check.Result) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(sample, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := check.New(sample)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), c.Verify(note.Parse(string(b)))
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join(sample, "golden", name)
	if *update {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run: go test ./internal/render -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from golden:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestGoldenText(t *testing.T) {
	_, rs := results(t)
	golden(t, "check.txt", Text("CLAUDE.md", rs))
}

func TestGoldenJSON(t *testing.T) {
	_, rs := results(t)
	b, err := JSON([]FileReport{{File: "CLAUDE.md", Results: rs}})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "check.json", string(b)+"\n")
}

func TestGoldenFilterMark(t *testing.T) {
	src, rs := results(t)
	golden(t, "filter-mark.md", Filter(src, rs, false))
}

func TestGoldenFilterDrop(t *testing.T) {
	src, rs := results(t)
	golden(t, "filter-drop.md", Filter(src, rs, true))
}

func TestFilterChangesOnlyStaleLines(t *testing.T) {
	src, rs := results(t)
	in := strings.Split(src, "\n")
	out := strings.Split(Filter(src, rs, false), "\n")
	if len(in) != len(out) {
		t.Fatalf("line count changed: %d -> %d", len(in), len(out))
	}
	stale := map[int]bool{}
	for _, r := range rs {
		if r.Stale {
			stale[r.Claim.Line] = true
		}
	}
	for i := range in {
		if stale[i+1] {
			if !strings.Contains(out[i], "[STALE] ") || !strings.HasPrefix(out[i], "- [STALE] ") {
				t.Errorf("line %d not marked after the list marker: %q", i+1, out[i])
			}
		} else if in[i] != out[i] {
			t.Errorf("line %d changed but was not stale:\n%q\n%q", i+1, in[i], out[i])
		}
	}
}

func TestFilterNoStaleIsIdentity(t *testing.T) {
	src := "- a\n- b\n"
	if got := Filter(src, nil, true); got != src {
		t.Fatalf("got %q", got)
	}
}

func TestTextNoClaims(t *testing.T) {
	if got := Text("x.md", nil); !strings.Contains(got, "no claims found") {
		t.Fatal(got)
	}
}

func TestJSONEmptyIsArray(t *testing.T) {
	b, err := JSON(nil)
	if err != nil || string(b) != "[]" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestFilterTableRows(t *testing.T) {
	src := "| Name | Purpose |\n|---|---|\n" +
		"| `a` | fine <!-- plumb: exists src --> |\n" +
		"| `b` | gone <!-- plumb: exists nope --> |\n"
	c, err := check.New(sample)
	if err != nil {
		t.Fatal(err)
	}
	rs := c.Verify(note.Parse(src))
	mark := Filter(src, rs, false)
	want := "| Name | Purpose |\n|---|---|\n" +
		"| `a` | fine <!-- plumb: exists src --> |\n" +
		"| [STALE] `b` | gone <!-- plumb: exists nope --> |\n"
	if mark != want {
		t.Fatalf("mark:\n%s\nwant:\n%s", mark, want)
	}
	if drop := Filter(src, rs, true); strings.Contains(drop, "gone") || !strings.Contains(drop, "fine") || !strings.Contains(drop, "|---|---|") {
		t.Fatalf("drop:\n%s", drop)
	}
}

func TestTextShowsTableRowClaimAsCells(t *testing.T) {
	c, _ := check.New(sample)
	rs := c.Verify(note.Parse("| `a` | b <!-- plumb: exists src --> |\n"))
	if got := Text("n.md", rs); !strings.Contains(got, "`a` — b") {
		t.Fatalf("%s", got)
	}
}
