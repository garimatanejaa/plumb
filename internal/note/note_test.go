package note

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseBasic(t *testing.T) {
	src := "# Notes\n" +
		"- Login lives in the auth package <!-- plumb: exists src/auth ; grep \"func Login\" src/auth -->\n" +
		"- plain line, never judged\n" +
		"* Old module is gone <!--plumb:absent src/billing-->\n"
	cs := Parse(src)
	if len(cs) != 2 {
		t.Fatalf("want 2 claims, got %d: %+v", len(cs), cs)
	}
	c := cs[0]
	if c.Line != 2 || c.Text != "Login lives in the auth package" {
		t.Fatalf("claim 0 = %+v", c)
	}
	want := []Evidence{{"exists", []string{"src/auth"}}, {"grep", []string{"func Login", "src/auth"}}}
	if !reflect.DeepEqual(c.Evidence, want) {
		t.Fatalf("evidence = %+v", c.Evidence)
	}
	if cs[1].Line != 4 || cs[1].Evidence[0].Kind != "absent" {
		t.Fatalf("claim 1 = %+v", cs[1])
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"- a <!-- plumb: frobnicate x -->":          "unknown evidence",
		"- a <!-- plumb: exists -->":                "takes 1",
		"- a <!-- plumb: grep one two three -->":    "takes 2",
		"- a <!-- plumb: grep \"unterminated x -->": "unterminated",
		"- a <!-- plumb: -->":                       "no evidence",
		"- a <!-- plumb: ; ; -->":                   "no evidence",
	}
	for src, want := range cases {
		cs := Parse(src)
		if len(cs) != 1 || !strings.Contains(cs[0].ParseErr, want) {
			t.Errorf("%q: got %+v, want error containing %q", src, cs, want)
		}
	}
}

func TestQuotingKeepsRegexBackslashes(t *testing.T) {
	cs := Parse(`- x <!-- plumb: grep "\bLogin\b" src/auth -->`)
	if got := cs[0].Evidence[0].Args[0]; got != `\bLogin\b` {
		t.Fatalf("pattern = %q", got)
	}
}

func TestSemicolonInsideQuotes(t *testing.T) {
	cs := Parse(`- x <!-- plumb: grep "a;b" src ; exists src -->`)
	if len(cs[0].Evidence) != 2 || cs[0].Evidence[0].Args[0] != "a;b" {
		t.Fatalf("%+v", cs[0])
	}
}

func TestEscapedQuote(t *testing.T) {
	cs := Parse(`- x <!-- plumb: grep "say \"hi\"" src -->`)
	if cs[0].Evidence[0].Args[0] != `say "hi"` {
		t.Fatalf("%+v", cs[0])
	}
}

func TestFencedCodeIsNotClaims(t *testing.T) {
	src := "```markdown\n- x <!-- plumb: exists a -->\n```\n- y <!-- plumb: exists b -->\n~~~\n- z <!-- plumb: exists c -->\n~~~\n"
	cs := Parse(src)
	if len(cs) != 1 || cs[0].Line != 4 {
		t.Fatalf("%+v", cs)
	}
}

func TestEvidenceRoundTrip(t *testing.T) {
	evs := []Evidence{
		{"exists", []string{"src/auth"}},
		{"grep", []string{`\bLogin\b`, "src/auth dir"}},
		{"grep", []string{`say "hi"; ok`, "src"}},
	}
	cs := Parse("- x " + Comment(evs))
	if len(cs) != 1 || !reflect.DeepEqual(cs[0].Evidence, evs) {
		t.Fatalf("round trip: %q -> %+v", Comment(evs), cs)
	}
}

func TestCRLF(t *testing.T) {
	cs := Parse("a\r\n- x <!-- plumb: exists a -->\r\n")
	if len(cs) != 1 || cs[0].Line != 2 || cs[0].Text != "x" {
		t.Fatalf("%+v", cs)
	}
}

func TestCountParses(t *testing.T) {
	cs := Parse(`- x <!-- plumb: count "*.java" src/main/service >= 115 -->`)
	want := Evidence{"count", []string{"*.java", "src/main/service", ">=", "115"}}
	if len(cs) != 1 || cs[0].ParseErr != "" || !reflect.DeepEqual(cs[0].Evidence[0], want) {
		t.Fatalf("%+v", cs)
	}
	if got := want.String(); got != "count *.java src/main/service >= 115" {
		t.Fatalf("String() = %q", got)
	}
	if rt := Parse("- x " + Comment([]Evidence{want})); len(rt) != 1 || !reflect.DeepEqual(rt[0].Evidence[0], want) {
		t.Fatalf("round trip: %+v", rt)
	}
}

func TestTableRowClaim(t *testing.T) {
	src := "| Client | Purpose |\n|---|---|\n" +
		"| `PaymentClient` | User profiles <!-- plumb: exists src/payments ; grep \"\\bPaymentClient\\b\" src/payments --> |\n"
	cs := Parse(src)
	if len(cs) != 1 || cs[0].Line != 3 {
		t.Fatalf("%+v", cs)
	}
	if cs[0].Text != "`PaymentClient` — User profiles" {
		t.Fatalf("row text = %q", cs[0].Text)
	}
	if len(cs[0].Evidence) != 2 || cs[0].Evidence[1].Args[0] != `\bPaymentClient\b` {
		t.Fatalf("%+v", cs[0].Evidence)
	}
}

func TestEscapedPipeInTableRow(t *testing.T) {
	// In a row a pipe is written \| so it cannot split the cell; outside a
	// row the backslash is kept, because it is meaningful in a regex.
	row := Parse(`| a | b <!-- plumb: grep "foo\|bar" src --> |`)
	if got := row[0].Evidence[0].Args[0]; got != "foo|bar" {
		t.Fatalf("row pattern = %q", got)
	}
	bullet := Parse(`- a <!-- plumb: grep "foo\|bar" src -->`)
	if got := bullet[0].Evidence[0].Args[0]; got != `foo\|bar` {
		t.Fatalf("bullet pattern = %q", got)
	}
}

func TestTableRowAndSeparatorDetection(t *testing.T) {
	for line, want := range map[string]bool{
		"| a | b |": true, "  | a |": true, "|---|---|": true, "| --- | :-: |": true,
		"a | b": false, "- | x": false, "|": false, "": false,
	} {
		if got := IsTableRow(line); got != want {
			t.Errorf("IsTableRow(%q) = %v", line, got)
		}
	}
	for line, want := range map[string]bool{
		"|---|---|": true, "| --- | :-: |": true, "|:--|--:|": true,
		"| a | b |": false, "| - |": true, "|  |": false, "": false,
	} {
		if got := IsTableSeparator(line); got != want {
			t.Errorf("IsTableSeparator(%q) = %v", line, got)
		}
	}
}
