package check

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"plumb/internal/note"
)

// repo builds a fixture repository in a temp dir.
func repo(t *testing.T, files map[string]string) *Checker {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// verify runs one claim line and returns its result.
func verify(t *testing.T, c *Checker, evidence string) Result {
	t.Helper()
	cs := note.Parse("- claim <!-- plumb: " + evidence + " -->")
	if len(cs) != 1 {
		t.Fatalf("bad test claim %q", evidence)
	}
	return c.Verify(cs)[0]
}

func wantFresh(t *testing.T, c *Checker, evidence string) {
	t.Helper()
	if r := verify(t, c, evidence); r.Stale {
		t.Errorf("%s: want fresh, got stale: %v", evidence, r.Reasons)
	}
}

func wantStale(t *testing.T, c *Checker, evidence, reason string) {
	t.Helper()
	r := verify(t, c, evidence)
	if !r.Stale {
		t.Errorf("%s: want stale, got fresh", evidence)
		return
	}
	if !strings.Contains(strings.Join(r.Reasons, "|"), reason) {
		t.Errorf("%s: reasons %v do not contain %q", evidence, r.Reasons, reason)
	}
}

func TestKindsHaveHandlers(t *testing.T) {
	for k := range note.Kinds {
		if handlers[k] == nil {
			t.Errorf("kind %q has no handler", k)
		}
	}
	for k := range handlers {
		if _, ok := note.Kinds[k]; !ok {
			t.Errorf("handler %q is not a parsed kind", k)
		}
	}
}

func TestExistsAbsent(t *testing.T) {
	c := repo(t, map[string]string{"src/auth/login.go": "package auth\n"})
	wantFresh(t, c, "exists src/auth")
	wantFresh(t, c, "exists src/auth/login.go")
	wantStale(t, c, "exists src/identity", "does not exist")
	wantFresh(t, c, "absent src/billing")
	wantStale(t, c, "absent src/auth", "still exists")
}

func TestGrepRules(t *testing.T) {
	c := repo(t, map[string]string{
		"src/auth/login.go":         "package auth\nfunc Login() {}\n",
		"node_modules/dep/index.js": "func Hidden() {}\n",
		"src/bin.dat":               "func Binary\x00() {}\n",
	})
	wantFresh(t, c, `grep "func Login" src/auth`)
	wantFresh(t, c, `grep "func Login" src/auth/login.go`)
	wantStale(t, c, `grep "func Logout" src/auth`, "no match (searched 1 files)")
	wantStale(t, c, `grep "func Hidden" .`, "no match")   // node_modules skipped
	wantFresh(t, c, `grep "func Hidden" node_modules`)    // an explicit target is searched
	wantStale(t, c, `grep "func Binary" src`, "no match") // binary skipped
	wantStale(t, c, `grep "(" src`, "bad pattern")
	wantStale(t, c, `grep "x" nowhere`, "path does not exist")
}

func TestHash(t *testing.T) {
	c := repo(t, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	whole, err := c.HashOf("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	rng, err := c.HashOf("a.txt:2-3")
	if err != nil {
		t.Fatal(err)
	}
	wantFresh(t, c, "hash a.txt "+whole[:8])
	wantFresh(t, c, "hash a.txt:2-3 "+rng[:12])
	wantStale(t, c, "hash a.txt:1-2 "+rng[:8], "content changed")
	wantStale(t, c, "hash a.txt abc", "at least 8")
	wantStale(t, c, "hash a.txt zzzzzzzz", "hex prefix")
	wantStale(t, c, "hash missing.txt deadbeef", "does not exist")
	wantStale(t, c, "hash a.txt:2-99 deadbeef", "out of range")
	wantStale(t, c, "hash a.txt:3-2 deadbeef", "bad line range")
}

func TestHashNormalisesCRLF(t *testing.T) {
	lf := repo(t, map[string]string{"a.txt": "one\ntwo\n"})
	crlf := repo(t, map[string]string{"a.txt": "one\r\ntwo\r\n"})
	h1, _ := lf.HashOf("a.txt")
	h2, _ := crlf.HashOf("a.txt")
	if h1 != h2 {
		t.Fatalf("CRLF changed the hash: %s vs %s", h1, h2)
	}
}

func TestJail(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := repo(t, map[string]string{"src/a.go": "package a\n"})
	for _, p := range []string{"../x", "src/../../x", "/etc/passwd", ""} {
		if _, _, err := c.Resolve(p); err == nil {
			t.Errorf("Resolve(%q): want error", p)
		}
	}
	wantStale(t, c, "exists ../x", "leaves the repository")
	wantStale(t, c, "exists /etc/passwd", "relative")

	// A symlink pointing outside the repository must be refused.
	if err := os.Symlink(outside, filepath.Join(c.Root, "link")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	wantStale(t, c, "exists link/secret.txt", "outside the repository")
	wantStale(t, c, `grep "secret" link`, "outside the repository")

	// A dangling symlink counts as missing, not as an error.
	if err := os.Symlink(filepath.Join(outside, "nope"), filepath.Join(c.Root, "dangling")); err != nil {
		t.Fatal(err)
	}
	wantStale(t, c, "exists dangling", "does not exist")
	wantFresh(t, c, "absent dangling")
}

func TestSymlinkInsideRepoIsFine(t *testing.T) {
	c := repo(t, map[string]string{"real/a.go": "package a\n"})
	if err := os.Symlink(filepath.Join(c.Root, "real"), filepath.Join(c.Root, "alias")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	wantFresh(t, c, "exists alias/a.go")
}

func TestVerifyAnyFailureMakesClaimStale(t *testing.T) {
	c := repo(t, map[string]string{"src/a.go": "package a\n"})
	r := verify(t, c, "exists src ; exists nope ; absent src/a.go")
	if !r.Stale || len(r.Reasons) != 2 {
		t.Fatalf("want 2 reasons, got %+v", r)
	}
}

func TestParseErrorIsStale(t *testing.T) {
	c := repo(t, nil)
	cs := note.Parse("- x <!-- plumb: frobnicate a -->")
	r := c.Verify(cs)[0]
	if !r.Stale || !strings.Contains(r.Reasons[0], "invalid evidence") {
		t.Fatalf("%+v", r)
	}
}

func TestFilesMatching(t *testing.T) {
	c := repo(t, map[string]string{
		"a/x.go":              "func Login()\n",
		"a/y.go":              "func Login()\n",
		"b/z.go":              "nothing\n",
		"CLAUDE.md":           "Login is here\n",
		"node_modules/q/i.js": "Login\n",
	})
	re := regexp.MustCompile(`\bLogin\b`)
	got, err := c.FilesMatching(re, "", []string{"CLAUDE.md"}, 10)
	if err != nil || strings.Join(got, ",") != "a/x.go,a/y.go" {
		t.Fatalf("got %v err %v", got, err)
	}
	got, _ = c.FilesMatching(re, "", []string{"CLAUDE.md"}, 1)
	if len(got) != 1 {
		t.Fatalf("limit not honoured: %v", got)
	}
	if _, err := c.FilesMatching(re, "../x", nil, 1); err == nil {
		t.Fatal("scope outside repo must fail")
	}
}

func TestNewRejectsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	os.WriteFile(f, nil, 0o644)
	if _, err := New(f); err == nil {
		t.Fatal("want error for a file root")
	}
}

func TestFindPaths(t *testing.T) {
	c := repo(t, map[string]string{
		"src/main/java/app/controller/A.java":     "x",
		"src/main/java/app/kafka/consumer/B.go":   "x",
		"src/test/java/app/controller/ATest.java": "x",
		"node_modules/x/controller/n.js":          "x",
		".claude/controller/n.md":                 "x",
		"build/controller/g.java":                 "x",
		"docs/schema.sql":                         "x",
	})
	got := c.FindPaths("controller/", true, 10)
	want := "src/main/java/app/controller,src/test/java/app/controller"
	if strings.Join(got, ",") != want {
		t.Fatalf("dirs: %v", got)
	}
	if got := c.FindPaths("kafka/consumer", true, 10); len(got) != 1 || got[0] != "src/main/java/app/kafka/consumer" {
		t.Fatalf("multi-segment dir: %v", got)
	}
	if got := c.FindPaths("schema.sql", false, 10); len(got) != 1 || got[0] != "docs/schema.sql" {
		t.Fatalf("file: %v", got)
	}
	if got := c.FindPaths("controller", false, 10); len(got) != 0 {
		t.Fatalf("a directory matched as a file: %v", got)
	}
	if got := c.FindPaths("ontroller", true, 10); len(got) != 0 {
		t.Fatalf("matched mid-segment: %v", got)
	}
	if got := c.FindPaths("controller/", true, 1); len(got) != 1 {
		t.Fatalf("limit ignored: %v", got)
	}
	if c.FindPaths("", true, 5) != nil {
		t.Fatal("empty suffix must match nothing")
	}
}

func TestCount(t *testing.T) {
	files := map[string]string{
		"svc/A.java": "x", "svc/B.java": "x", "svc/sub/C.java": "x",
		"svc/notes.txt": "x", "svc/node_modules/D.java": "x", "svc/build/E.java": "x",
		"other/F.java": "x",
	}
	c := repo(t, files)
	wantFresh(t, c, `count "*.java" svc >= 3`)
	wantFresh(t, c, `count "*.java" svc == 3`)
	wantFresh(t, c, `count "*.java" svc > 2`)
	wantFresh(t, c, `count "*.java" svc <= 3`)
	wantFresh(t, c, `count "*.java" svc < 4`)
	wantFresh(t, c, `count "*.txt" svc == 1`)
	wantFresh(t, c, `count "*" . == 5`) // node_modules and build are not counted
	wantStale(t, c, `count "*.java" svc >= 115`, "found 3, want >= 115")
	wantStale(t, c, `count "*.java" svc == 4`, "found 3, want == 4")
	wantStale(t, c, `count "*.java" nowhere >= 1`, "path does not exist")
	wantStale(t, c, `count "*.java" svc/A.java >= 1`, "not a directory")
	wantStale(t, c, `count "*.java" svc ~= 1`, "unknown comparison")
	wantStale(t, c, `count "*.java" svc >= many`, "not a non-negative number")
	wantStale(t, c, `count "*.java" svc >= -1`, "not a non-negative number")
	wantStale(t, c, `count "svc/*.java" svc >= 1`, "file names, not paths")
	wantStale(t, c, `count "[" svc >= 1`, "bad glob")
	wantStale(t, c, `count "*.java" ../x >= 1`, "leaves the repository")
}

func TestCountDoesNotFollowSymlinks(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "X.java"), []byte("x"), 0o644)
	c := repo(t, map[string]string{"svc/A.java": "x"})
	if err := os.Symlink(outside, filepath.Join(c.Root, "svc", "link")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	wantFresh(t, c, `count "*.java" svc == 1`)
}

func TestExtCounts(t *testing.T) {
	c := repo(t, map[string]string{"d/a.java": "x", "d/b.JAVA": "x", "d/c.go": "x", "d/Makefile": "x", "d/.env": "x"})
	m, err := c.ExtCounts("d")
	if err != nil || m[".java"] != 2 || m[".go"] != 1 || len(m) != 2 {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := c.ExtCounts("d/a.java"); err == nil {
		t.Fatal("a file is not a directory")
	}
}

func TestFilesMatchingAll(t *testing.T) {
	c := repo(t, map[string]string{
		"a/One.java":   "\"/v2/catalog\"\n@GetMapping(\"/pricing\")\n",
		"a/Two.java":   "\"/v2/catalog\"\n",
		"a/Three.java": "@GetMapping(\"/pricing\")\n",
	})
	head := regexp.MustCompile(`"/v2/catalog["/]`)
	tail := regexp.MustCompile(`["/]pricing"`)
	got, err := c.FilesMatchingAll([]*regexp.Regexp{head, tail}, "", nil, 10)
	if err != nil || strings.Join(got, ",") != "a/One.java" {
		t.Fatalf("both: %v %v", got, err)
	}
	got, _ = c.FilesMatchingAll([]*regexp.Regexp{head}, "", nil, 10)
	if len(got) != 2 {
		t.Fatalf("one pattern: %v", got)
	}
	if got, _ := c.FilesMatchingAll(nil, "", nil, 10); len(got) != 3 {
		t.Fatalf("no patterns should match every readable file: %v", got)
	}
}

func TestGrepFailureShowsThePatternAsWrittenInANote(t *testing.T) {
	c := repo(t, map[string]string{"src/A.java": "x\n"})
	wantStale(t, c, `grep "\"/v1/coupons[\"/]" src`, `grep "\"/v1/coupons[\"/]" src: no match`)
	wantStale(t, c, `grep "\bFoo\b" src`, `grep "\bFoo\b" src: no match`)
	// The message must parse back into the same evidence.
	r := verify(t, c, `grep "\"/v1/x[\"/]" src`)
	msg := strings.SplitN(r.Reasons[0], ": no match", 2)[0]
	cs := note.Parse("- x <!-- plumb: " + msg + " -->")
	if len(cs) != 1 || cs[0].ParseErr != "" || cs[0].Evidence[0].Args[0] != `"/v1/x["/]` {
		t.Fatalf("message %q does not round-trip: %+v", msg, cs)
	}
}

func fixtureForSnapshot(t *testing.T) *Checker {
	return repo(t, map[string]string{
		"a/One.java":               "class Alpha {}\n\"/v1/pay\"\n",
		"a/Two.java":               "class Beta {}\n",
		"b/Three.go":               "func Alpha() {}\n",
		"node_modules/x/Four.java": "class Alpha {}\n",
		"bin.dat":                  "class Alpha\x00\n",
	})
}

func TestSnapshotAnswersLikeAWalk(t *testing.T) {
	c := fixtureForSnapshot(t)
	re := []*regexp.Regexp{regexp.MustCompile(`\bAlpha\b`)}
	want, _ := c.FilesMatchingAll(re, "", nil, 10)
	got, _ := c.Snapshot().FilesMatchingAll(re, "", nil, 10)
	if strings.Join(got, ",") != strings.Join(want, ",") || len(want) != 2 {
		t.Fatalf("snapshot %v, walk %v", got, want)
	}
	// limit and exclude behave the same on a snapshot
	got, _ = c.Snapshot().FilesMatchingAll(re, "", []string{"a/One.java"}, 10)
	if len(got) != 1 || got[0] != "b/Three.go" {
		t.Fatalf("exclude: %v", got)
	}
	if got, _ = c.Snapshot().FilesMatchingAll(re, "", nil, 1); len(got) != 1 {
		t.Fatalf("limit: %v", got)
	}
	// a scoped search is not served from the snapshot but still works
	if got, _ = c.Snapshot().FilesMatchingAll(re, "a", nil, 10); len(got) != 1 || got[0] != "a/One.java" {
		t.Fatalf("scope: %v", got)
	}
}

func TestSnapshotIsTakenOnceAndNotShared(t *testing.T) {
	c := fixtureForSnapshot(t)
	snap := c.Snapshot()
	re := []*regexp.Regexp{regexp.MustCompile(`Gamma`)}
	if got, _ := snap.FilesMatchingAll(re, "", nil, 10); len(got) != 0 {
		t.Fatal(got)
	}
	os.WriteFile(filepath.Join(c.Root, "a", "New.java"), []byte("class Gamma {}\n"), 0o644)
	if got, _ := snap.FilesMatchingAll(re, "", nil, 10); len(got) != 0 {
		t.Fatalf("a snapshot must not see later edits: %v", got)
	}
	if got, _ := c.FilesMatchingAll(re, "", nil, 10); len(got) != 1 {
		t.Fatalf("the plain Checker always reads the current files: %v", got)
	}
	if got, _ := c.Snapshot().FilesMatchingAll(re, "", nil, 10); len(got) != 1 {
		t.Fatalf("a new snapshot sees the current files: %v", got)
	}
}

func TestHintOnlySpeedsThingsUp(t *testing.T) {
	c := fixtureForSnapshot(t)
	re := []*regexp.Regexp{regexp.MustCompile(`\bAlpha\b`)}
	for _, snap := range []*Checker{c, c.Snapshot()} {
		with, _ := snap.FilesMatchingHint("Alpha", re, "", nil, 10)
		without, _ := snap.FilesMatchingHint("", re, "", nil, 10)
		if strings.Join(with, ",") != strings.Join(without, ",") {
			t.Fatalf("hint changed the answer: %v vs %v", with, without)
		}
	}
	// A hint that the pattern cannot match without is the caller's contract;
	// a wrong hint simply finds nothing.
	if got, _ := c.FilesMatchingHint("zzz", re, "", nil, 10); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestSnapshotFallsBackToWalkingWhenTooBig(t *testing.T) {
	old := maxSnapshotBytes
	maxSnapshotBytes = 10
	defer func() { maxSnapshotBytes = old }()
	c := fixtureForSnapshot(t)
	re := []*regexp.Regexp{regexp.MustCompile(`\bAlpha\b`)}
	got, _ := c.Snapshot().FilesMatchingAll(re, "", nil, 10)
	if len(got) != 2 {
		t.Fatalf("fallback walk should give the same answer: %v", got)
	}
}
