package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/auth/login.go", "func Login() {}\n")
	write("CLAUDE.md", "# Notes\n"+
		"- Auth lives in src/auth <!-- plumb: exists src/auth -->\n"+
		"- Config lives in config.yaml <!-- plumb: exists config.yaml -->\n"+
		"- Sign-in is `Login`\n")
	s, err := New(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// session runs scripted lines through the server and returns decoded replies.
func session(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var replies []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad reply %q: %v", l, err)
		}
		replies = append(replies, m)
	}
	return replies
}

func call(id int, tool, args string) string {
	return `{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func text(t *testing.T, reply map[string]any) (string, bool) {
	t.Helper()
	res, ok := reply["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", reply)
	}
	content := res["content"].([]any)[0].(map[string]any)
	return content["text"].(string), res["isError"].(bool)
}

func TestHandshakeAndList(t *testing.T) {
	r := session(t, fixture(t),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
	)
	if len(r) != 3 {
		t.Fatalf("a notification must get no reply; got %d replies: %v", len(r), r)
	}
	init := r[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" || init["serverInfo"].(map[string]any)["name"] != "plumb" {
		t.Fatalf("%v", init)
	}
	tools := r[2]["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, x := range tools {
		tool := x.(map[string]any)
		names = append(names, tool["name"].(string))
		if tool["annotations"].(map[string]any)["readOnlyHint"] != true {
			t.Errorf("%v is not marked read-only", tool["name"])
		}
	}
	if strings.Join(names, ",") != "check_notes,read_fresh_notes,suggest_evidence" {
		t.Fatalf("tools = %v", names)
	}
}

func TestCheckNotes(t *testing.T) {
	r := session(t, fixture(t), call(1, "check_notes", `{}`))
	got, isErr := text(t, r[0])
	if isErr || !strings.Contains(got, "✓ line 2") || !strings.Contains(got, "✗ line 3") {
		t.Fatalf("%q", got)
	}
}

func TestReadFreshNotesWithholdsStaleClaims(t *testing.T) {
	r := session(t, fixture(t), call(1, "read_fresh_notes", `{"file":"CLAUDE.md"}`))
	got, isErr := text(t, r[0])
	if isErr {
		t.Fatal(got)
	}
	if strings.Contains(got, "config.yaml") && strings.Contains(got, "Config lives") {
		t.Fatalf("stale claim reached the agent:\n%s", got)
	}
	if !strings.Contains(got, "Auth lives in src/auth") || !strings.Contains(got, "1 stale claim(s) withheld") {
		t.Fatalf("%s", got)
	}
}

func TestReadFreshNotesMark(t *testing.T) {
	r := session(t, fixture(t), call(1, "read_fresh_notes", `{"mark":true}`))
	got, _ := text(t, r[0])
	if !strings.Contains(got, "- [STALE] Config lives") || !strings.Contains(got, "marked [STALE]") {
		t.Fatalf("%s", got)
	}
}

func TestSuggestEvidence(t *testing.T) {
	r := session(t, fixture(t), call(1, "suggest_evidence", `{}`))
	got, isErr := text(t, r[0])
	if isErr || !strings.Contains(got, "+- Sign-in is `Login` <!-- plumb: grep") {
		t.Fatalf("%q", got)
	}
}

func TestPathEscapeIsRefused(t *testing.T) {
	for _, f := range []string{"../etc/passwd", "/etc/passwd", "src/../../x"} {
		r := session(t, fixture(t), call(1, "read_fresh_notes", `{"file":"`+f+`"}`))
		got, isErr := text(t, r[0])
		if !isErr || strings.Contains(got, "root:") {
			t.Errorf("%s: want an error, got isError=%v %q", f, isErr, got)
		}
	}
}

func TestSymlinkEscapeIsRefused(t *testing.T) {
	s := fixture(t)
	outside := filepath.Join(t.TempDir(), "secret.md")
	os.WriteFile(outside, []byte("- leaked <!-- plumb: exists x -->\n"), 0o644)
	if err := os.Symlink(outside, filepath.Join(s.c.Root, "evil.md")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	r := session(t, s, call(1, "read_fresh_notes", `{"file":"evil.md"}`))
	got, isErr := text(t, r[0])
	if !isErr || strings.Contains(got, "leaked") {
		t.Fatalf("isError=%v %q", isErr, got)
	}
}

func TestMissingNoteAndDirectory(t *testing.T) {
	s := fixture(t)
	for _, f := range []string{"nope.md", "src"} {
		r := session(t, s, call(1, "check_notes", `{"file":"`+f+`"}`))
		if _, isErr := text(t, r[0]); !isErr {
			t.Errorf("%s: want error result", f)
		}
	}
}

func TestProtocolErrors(t *testing.T) {
	r := session(t, fixture(t),
		`{"jsonrpc":"2.0","id":1,"method":"no/such"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"write_file","arguments":{}}}`,
		`not json`,
	)
	code := func(m map[string]any) float64 { return m["error"].(map[string]any)["code"].(float64) }
	if code(r[0]) != -32601 || code(r[1]) != -32602 || code(r[2]) != -32700 {
		t.Fatalf("%v", r)
	}
}

func TestNoToolWrites(t *testing.T) {
	s := fixture(t)
	before, _ := os.ReadFile(filepath.Join(s.c.Root, "CLAUDE.md"))
	session(t, s,
		call(1, "check_notes", `{}`),
		call(2, "read_fresh_notes", `{}`),
		call(3, "suggest_evidence", `{}`),
	)
	after, _ := os.ReadFile(filepath.Join(s.c.Root, "CLAUDE.md"))
	if !bytes.Equal(before, after) {
		t.Fatal("a tool modified the note")
	}
}
