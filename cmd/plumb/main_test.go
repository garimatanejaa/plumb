package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = "../../testdata/sample-repo"

func runCLI(t *testing.T, stdin string, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, strings.NewReader(stdin), &o, &e)
	return code, o.String(), e.String()
}

func tmpNote(t *testing.T, body string, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x\n"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(body), 0o644)
	return dir
}

func TestCheckExitCodes(t *testing.T) {
	code, out, _ := runCLI(t, "", "check", filepath.Join(sample, "CLAUDE.md"))
	if code != 1 || !strings.Contains(out, "7 claims, 4 stale") {
		t.Fatalf("stale sample: code=%d\n%s", code, out)
	}
	dir := tmpNote(t, "- ok <!-- plumb: exists a -->\n", "a")
	if code, out, _ := runCLI(t, "", "check", filepath.Join(dir, "CLAUDE.md")); code != 0 {
		t.Fatalf("fresh note: code=%d\n%s", code, out)
	}
	if code, _, errOut := runCLI(t, "", "check", filepath.Join(dir, "missing.md")); code != 2 || errOut == "" {
		t.Fatalf("missing note: code=%d err=%q", code, errOut)
	}
	if code, _, _ := runCLI(t, "", "bogus"); code != 2 {
		t.Fatalf("unknown command: code=%d", code)
	}
	if code, _, _ := runCLI(t, ""); code != 2 {
		t.Fatalf("no command: code=%d", code)
	}
}

func TestCheckFlagsMayFollowFiles(t *testing.T) {
	code, out, _ := runCLI(t, "", "check", filepath.Join(sample, "CLAUDE.md"), "--json")
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	var reports []struct {
		File    string `json:"file"`
		Results []struct {
			Stale bool `json:"stale"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &reports); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	if len(reports) != 1 || len(reports[0].Results) != 7 {
		t.Fatalf("%+v", reports)
	}
}

func TestCheckDefaultFiles(t *testing.T) {
	dir := tmpNote(t, "- ok <!-- plumb: exists a -->\n", "a")
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)
	if code, out, _ := runCLI(t, "", "check"); code != 0 || !strings.Contains(out, "CLAUDE.md") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	os.Remove("CLAUDE.md")
	if code, _, _ := runCLI(t, "", "check"); code != 2 {
		t.Fatalf("no default files: code=%d", code)
	}
}

func TestRootFlag(t *testing.T) {
	dir := tmpNote(t, "- ok <!-- plumb: exists a -->\n", "a")
	note := filepath.Join(t.TempDir(), "n.md")
	os.WriteFile(note, []byte("- ok <!-- plumb: exists a -->\n"), 0o644)
	if code, _, _ := runCLI(t, "", "check", note); code != 1 {
		t.Fatalf("default root should be the note's dir: code=%d", code)
	}
	if code, _, _ := runCLI(t, "", "check", "--root", dir, note); code != 0 {
		t.Fatalf("--root not honoured: code=%d", code)
	}
}

func TestFilter(t *testing.T) {
	code, out, _ := runCLI(t, "", "filter", filepath.Join(sample, "CLAUDE.md"))
	if code != 0 || !strings.Contains(out, "- [STALE] Config lives") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	_, out, _ = runCLI(t, "", "filter", "--drop", filepath.Join(sample, "CLAUDE.md"))
	if strings.Contains(out, "Config lives") || !strings.Contains(out, "Login is handled") {
		t.Fatalf("--drop output wrong:\n%s", out)
	}
	if code, _, _ := runCLI(t, "", "filter"); code != 2 {
		t.Fatalf("filter without file: code=%d", code)
	}
}

func TestHashProducesFreshEvidence(t *testing.T) {
	code, out, _ := runCLI(t, "", "hash", "--root", sample, "src/auth/login.go:3-5")
	if code != 0 || strings.TrimSpace(out) != "hash src/auth/login.go:3-5 072740ab" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if code, _, _ := runCLI(t, "", "hash", "--root", sample, "nope.go"); code != 2 {
		t.Fatalf("missing file: code=%d", code)
	}
	if code, _, _ := runCLI(t, "", "hash", "--root", sample, "../x"); code != 2 {
		t.Fatalf("escape: code=%d", code)
	}
}

func TestSuggestPrintsDiffAndWritesNothing(t *testing.T) {
	note := filepath.Join(sample, "CLAUDE.md")
	before, _ := os.ReadFile(note)
	code, out, errOut := runCLI(t, "", "suggest", note)
	after, _ := os.ReadFile(note)
	if code != 0 || !bytes.Equal(before, after) {
		t.Fatalf("code=%d, note changed=%v", code, !bytes.Equal(before, after))
	}
	if !strings.Contains(out, "@@ -12,1 +12,1 @@") || !strings.Contains(errOut, "1 proposal(s)") {
		t.Fatalf("out=%q err=%q", out, errOut)
	}
}

func TestMCPViaRun(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_fresh_notes","arguments":{"file":"CLAUDE.md"}}}` + "\n"
	code, out, errOut := runCLI(t, in, "mcp", "--root", sample)
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if strings.Contains(out, "Config lives") || !strings.Contains(out, "Login is handled") {
		t.Fatalf("%s", out)
	}
	if code, _, _ := runCLI(t, "", "mcp", "--root", "/definitely/not/here"); code != 2 {
		t.Fatalf("bad root: code=%d", code)
	}
}

func TestVersionAndHelp(t *testing.T) {
	if code, out, _ := runCLI(t, "", "version"); code != 0 || !strings.HasPrefix(out, "plumb ") {
		t.Fatalf("%d %q", code, out)
	}
	if code, out, _ := runCLI(t, "", "help"); code != 0 || !strings.Contains(out, "plumb check") {
		t.Fatalf("%d %q", code, out)
	}
}
