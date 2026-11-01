package suggest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"plumb/internal/check"
	"plumb/internal/note"
)

func repo(t *testing.T, files map[string]string) *check.Checker {
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
	c, err := check.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func one(t *testing.T, c *check.Checker, line string) (Proposal, bool) {
	t.Helper()
	rep := Suggest(c, line, "CLAUDE.md")
	if len(rep.Proposals) == 0 {
		return Proposal{}, false
	}
	return rep.Proposals[0], true
}

func TestExistingPathBecomesExists(t *testing.T) {
	c := repo(t, map[string]string{"src/auth/login.go": "package auth\n"})
	p, ok := one(t, c, "- Auth lives in `src/auth/`")
	if !ok || p.Evidence[0].String() != "exists src/auth" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
}

func TestMissingPathGetsNoProposal(t *testing.T) {
	c := repo(t, map[string]string{"src/a.go": "package a\n"})
	if _, ok := one(t, c, "- Config in `config/app.yaml`"); ok {
		t.Fatal("proposed evidence for a path that does not exist")
	}
}

func TestIdentifierInOneFile(t *testing.T) {
	c := repo(t, map[string]string{
		"src/auth/login.go": "func Login() {}\n",
		"src/other.go":      "nothing\n",
	})
	p, ok := one(t, c, "- Sign-in is `Login()`")
	if !ok || p.Evidence[0].Kind != "grep" || p.Evidence[0].Args[1] != "src/auth/login.go" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
}

func TestIdentifierInSeveralFilesOfOneDirScopesToDir(t *testing.T) {
	c := repo(t, map[string]string{
		"src/auth/a.go": "func Login() {}\n",
		"src/auth/b.go": "type Login struct{}\n",
	})
	p, ok := one(t, c, "- Sign-in is `Login`")
	if !ok || p.Evidence[0].Args[1] != "src/auth" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
}

func TestAmbiguousIdentifierGetsNoProposal(t *testing.T) {
	c := repo(t, map[string]string{
		"a/x.go": "func Login() {}\n",
		"b/y.go": "func Login() {}\n",
	})
	if _, ok := one(t, c, "- Sign-in is `Login`"); ok {
		t.Fatal("proposed evidence for an identifier spread across directories")
	}
	c = repo(t, map[string]string{"a.go": "func Login() {}\n", "b.go": "func Login() {}\n"})
	if _, ok := one(t, c, "- Sign-in is `Login`"); ok {
		t.Fatal("proposed a repo-root scope")
	}
	c = repo(t, map[string]string{"a/1.go": "func Login() {}\n", "a/2.go": "func Login() {}\n", "a/3.go": "func Login() {}\n", "a/4.go": "func Login() {}\n"})
	if _, ok := one(t, c, "- Sign-in is `Login`"); ok {
		t.Fatal("proposed evidence for an identifier in too many files")
	}
}

func TestNoteCannotVouchForItself(t *testing.T) {
	c := repo(t, map[string]string{"CLAUDE.md": "type Zebra struct{}\n"})
	if _, ok := one(t, c, "- the `Zebra` type"); ok {
		t.Fatal("matched the note itself")
	}
}

func TestSkipsClaimsFencesAndPlainLines(t *testing.T) {
	c := repo(t, map[string]string{"src/a.go": "package a\n"})
	src := "- done `src` <!-- plumb: exists src -->\n" +
		"plain line mentioning `src`\n" +
		"```\n- fenced `src`\n```\n" +
		"- real `src`\n"
	rep := Suggest(c, src, "CLAUDE.md")
	if len(rep.Proposals) != 1 || rep.Proposals[0].Line != 6 {
		t.Fatalf("%+v", rep)
	}
}

func TestBarePathWordInProse(t *testing.T) {
	c := repo(t, map[string]string{"docs/guide.md": "x\n"})
	p, ok := one(t, c, "- See docs/guide.md for details.")
	if !ok || p.Evidence[0].String() != "exists docs/guide.md" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
}

func TestRejectsUnsafeTokens(t *testing.T) {
	c := repo(t, map[string]string{"a.go": "x\n"})
	for _, line := range []string{
		"- see `../etc/passwd`",
		"- see `/etc/passwd`",
		"- see `https://example.com/a.go`",
		"- run `rm -rf src`",
	} {
		if _, ok := one(t, c, line); ok {
			t.Errorf("proposed evidence for %q", line)
		}
	}
}

func TestProposedEvidenceIsAlwaysFreshAndRoundTrips(t *testing.T) {
	c := repo(t, map[string]string{
		"src/auth/login.go": "func Login() {}\n",
		"src/http/c.go":     "type Client struct{}\n",
	})
	src := "- Auth: `src/auth/`, `Login`\n- HTTP: `Client` in `src/http`\n"
	rep := Suggest(c, src, "CLAUDE.md")
	if len(rep.Proposals) != 2 {
		t.Fatalf("%+v", rep)
	}
	for _, p := range rep.Proposals {
		cs := note.Parse(p.New)
		if len(cs) != 1 || cs[0].ParseErr != "" {
			t.Fatalf("proposal does not parse: %q -> %+v", p.New, cs)
		}
		if r := c.Verify(cs)[0]; r.Stale {
			t.Fatalf("proposed evidence is stale: %q %v", p.New, r.Reasons)
		}
	}
}

func TestEvidenceCap(t *testing.T) {
	c := repo(t, map[string]string{"a": "x", "b": "x", "c": "x", "d": "x"})
	p, ok := one(t, c, "- `a` `b` `c` `d`")
	if !ok || len(p.Evidence) != maxEvidence {
		t.Fatalf("%+v", p)
	}
}

func TestDiffFormat(t *testing.T) {
	c := repo(t, map[string]string{"src/a.go": "x\n"})
	rep := Suggest(c, "intro\n- real `src`\n", "CLAUDE.md")
	d := Diff("CLAUDE.md", rep)
	want := "--- a/CLAUDE.md\n+++ b/CLAUDE.md\n@@ -2,1 +2,1 @@\n-- real `src`\n+- real `src` <!-- plumb: exists src -->\n"
	if d != want {
		t.Fatalf("got:\n%q\nwant:\n%q", d, want)
	}
	if Diff("x", Report{}) != "" || !strings.HasPrefix(d, "--- a/") {
		t.Fatal("diff contract broken")
	}
}

func TestDocsAreNeverEvidenceForIdentifiers(t *testing.T) {
	c := repo(t, map[string]string{
		"docs/plan.md": "CREATE TABLE event_outbox (id int);\n",
		"README.txt":   "CREATE TABLE event_outbox\n",
	})
	if _, ok := one(t, c, "- events go through `event_outbox`"); ok {
		t.Fatal("proposed evidence that rests only on documentation")
	}
	c = repo(t, map[string]string{
		"docs/plan.md":      "CREATE TABLE event_outbox (id int);\n",
		"src/db/outbox.sql": "create table if not exists public.event_outbox (id int);\n",
	})
	p, ok := one(t, c, "- events go through `event_outbox`")
	if !ok || p.Evidence[0].Args[1] != "src/db/outbox.sql" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
}

func TestToolStateDirectoriesAreNeverEvidence(t *testing.T) {
	c := repo(t, map[string]string{
		".superpowers/sdd/notes": "class DSLContext {}\n",
		".claude/memory":         "class DSLContext {}\n",
	})
	if _, ok := one(t, c, "- queries use `DSLContext`"); ok {
		t.Fatal("proposed evidence that rests only on a hidden tool directory")
	}
	// A hidden directory is still fine for exists evidence: .github is real.
	c = repo(t, map[string]string{".github/workflows/ci.yml": "x\n"})
	p, ok := one(t, c, "- CI lives in `.github/workflows/`")
	if !ok || p.Evidence[0].String() != "exists .github/workflows" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
}

func TestIncidentalMentionIsNotEvidence(t *testing.T) {
	// "generateJooq" only appears in a comment: no definition, no proposal.
	c := repo(t, map[string]string{
		"src/Model.java": "// run generateJooq after ALTER TABLE\npublic class Model {}\n",
	})
	if _, ok := one(t, c, "- re-run `generateJooq` after schema changes"); ok {
		t.Fatal("proposed evidence for an identifier that is only mentioned")
	}
}

func TestPackageRelativeDirectory(t *testing.T) {
	c := repo(t, map[string]string{
		"src/main/java/app/controller/A.java":     "x\n",
		"src/test/java/app/controller/T.java":     "x\n",
		"src/main/java/app/kafka/consumer/C.java": "x\n",
	})
	p, ok := one(t, c, "- `controller/` holds REST endpoints")
	if !ok || p.Evidence[0].String() != "exists src/main/java/app/controller" {
		t.Fatalf("test mirror should be ignored: %+v ok=%v", p, ok)
	}
	p, ok = one(t, c, "- `kafka/consumer/` handles events")
	if !ok || p.Evidence[0].String() != "exists src/main/java/app/kafka/consumer" {
		t.Fatalf("multi-segment: %+v ok=%v", p, ok)
	}
}

func TestPackageRelativeAmbiguousOrMissing(t *testing.T) {
	c := repo(t, map[string]string{
		"a/util/x.go": "x\n",
		"b/util/y.go": "x\n",
	})
	if _, ok := one(t, c, "- helpers live in `util/`"); ok {
		t.Fatal("proposed evidence for a directory name that exists twice")
	}
	if _, ok := one(t, c, "- helpers live in `nowhere/`"); ok {
		t.Fatal("proposed evidence for a directory that does not exist")
	}
}

func TestPackageRelativeFile(t *testing.T) {
	c := repo(t, map[string]string{"src/main/resources/schema.sql": "x\n"})
	p, ok := one(t, c, "- DDL is in `schema.sql`")
	if !ok || p.Evidence[0].String() != "exists src/main/resources/schema.sql" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
	// A bare word with no extension and no slash is never treated as a path.
	if _, ok := one(t, c, "- see `schema`"); ok {
		t.Fatal("bare word treated as a path")
	}
}

func TestRelativeSearchSkipsDependenciesAndHiddenDirs(t *testing.T) {
	c := repo(t, map[string]string{
		"node_modules/pkg/controller/i.js": "x\n",
		".cursor/controller/n.md":          "x\n",
	})
	if _, ok := one(t, c, "- see `controller/`"); ok {
		t.Fatal("resolved a path inside node_modules or a hidden directory")
	}
}

func TestDocsDirectoriesAreNeverEvidenceEvenForNonMarkdownFiles(t *testing.T) {
	c := repo(t, map[string]string{
		"docs/schema/tables.sql": "CREATE TABLE inventory_db (id int);\n",
	})
	if _, ok := one(t, c, "- schema is `inventory_db`"); ok {
		t.Fatal("proposed evidence that rests only on a docs directory")
	}
}

func TestShallowestUniqueMatchWins(t *testing.T) {
	c := repo(t, map[string]string{
		"src/app/pojo/A.java":                           "x",
		"src/app/pojo/request/R.java":                   "x",
		"src/app/openfeign/walletClient/request/W.java": "x",
		"src/app/openfeign/questClient/request/Q.java":  "x",
		"src/app/service/wage/x/pojo/P.java":            "x",
	})
	for line, want := range map[string]string{
		"- `pojo/` holds DTOs":        "exists src/app/pojo",
		"- `request/` holds requests": "exists src/app/pojo/request",
	} {
		p, ok := one(t, c, line)
		if !ok || p.Evidence[0].String() != want {
			t.Errorf("%s: got %+v ok=%v, want %s", line, p, ok, want)
		}
	}
}

func TestSameDepthStaysAmbiguous(t *testing.T) {
	c := repo(t, map[string]string{
		"src/app/a/client/x.java":         "x",
		"src/app/b/client/y.java":         "x",
		"src/app/c/deep/er/client/z.java": "x",
	})
	if _, ok := one(t, c, "- see `client/`"); ok {
		t.Fatal("two shallowest matches at the same depth must stay ambiguous")
	}
}

func TestCountProposedForPlusClaims(t *testing.T) {
	files := map[string]string{"src/svc/notes.txt": "x"}
	for _, n := range []string{"A", "B", "C", "D"} {
		files["src/svc/"+n+".java"] = "x"
	}
	c := repo(t, files)
	p, ok := one(t, c, "- `src/svc/` — business logic (3+ services)")
	if !ok || len(p.Evidence) != 2 || p.Evidence[1].String() != "count *.java src/svc >= 3" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
	cs := note.Parse(p.New)
	if r := c.Verify(cs)[0]; r.Stale {
		t.Fatalf("proposed count evidence is stale: %v", r.Reasons)
	}
}

func TestCountNotProposedWhenTheNumberDoesNotHoldToday(t *testing.T) {
	c := repo(t, map[string]string{"src/svc/A.java": "x", "src/svc/B.java": "x"})
	p, ok := one(t, c, "- `src/svc/` — business logic (115+ services)")
	if !ok {
		t.Fatal("the directory itself should still get exists evidence")
	}
	for _, ev := range p.Evidence {
		if ev.Kind == "count" {
			t.Fatalf("proposed a count that is already false: %v", ev)
		}
	}
}

func TestCountNeedsADirectoryAndANumber(t *testing.T) {
	c := repo(t, map[string]string{"src/svc/A.java": "x", "src/svc/B.java": "x", "src/svc/C.java": "x"})
	// no "N+" in the line
	if p, _ := one(t, c, "- `src/svc/` — business logic"); len(p.Evidence) != 1 {
		t.Fatalf("%+v", p)
	}
	// "N+" but the only path is a file, not a directory
	if p, ok := one(t, c, "- `src/svc/A.java` — one of 3+ services"); !ok || len(p.Evidence) != 1 {
		t.Fatalf("%+v ok=%v", p, ok)
	}
	// "1+" is meaningless
	if p, _ := one(t, c, "- `src/svc/` — 1+ services"); len(p.Evidence) != 1 {
		t.Fatalf("%+v", p)
	}
}

// ---- table rows ----

func TestTableRowsAreClaims(t *testing.T) {
	c := repo(t, map[string]string{"src/svc/A.java": "x\n", "src/cfg/B.java": "x\n"})
	src := "| Folder | Purpose |\n" +
		"|---|---|\n" +
		"| `src/svc/` | services |\n" +
		"| `src/cfg/` | config |\n"
	rep := Suggest(c, src, "CLAUDE.md")
	if len(rep.Proposals) != 2 {
		t.Fatalf("header and separator must not be claims: %+v", rep)
	}
	want := "| `src/svc/` | services <!-- plumb: exists src/svc --> |"
	if rep.Proposals[0].Line != 3 || rep.Proposals[0].New != want {
		t.Fatalf("got %q at line %d", rep.Proposals[0].New, rep.Proposals[0].Line)
	}
}

func TestRowProposalParsesAsOneClaimWithCells(t *testing.T) {
	c := repo(t, map[string]string{"src/svc/A.java": "x\n"})
	rep := Suggest(c, "| `src/svc/` | services |\n", "CLAUDE.md")
	cs := note.Parse(rep.Proposals[0].New)
	if len(cs) != 1 || cs[0].ParseErr != "" || cs[0].Text != "`src/svc/` — services" {
		t.Fatalf("%+v", cs)
	}
	if r := c.Verify(cs)[0]; r.Stale {
		t.Fatalf("%v", r.Reasons)
	}
}

func TestPipeInEvidenceIsEscapedInRowsButNotBullets(t *testing.T) {
	ev := note.Comment([]note.Evidence{{Kind: "grep", Args: []string{"a|b", "src"}}})
	row := withComment("| x | y |", ev, true)
	if !strings.Contains(row, `a\|b`) || strings.Count(strings.ReplaceAll(row, `\|`, ""), "|") != 3 {
		t.Fatalf("a raw pipe would split the cell: %s", row)
	}
	if cs := note.Parse(row); len(cs) != 1 || cs[0].Evidence[0].Args[0] != "a|b" {
		t.Fatalf("escape did not round-trip: %+v", cs)
	}
	if b := withComment("- x", ev, false); !strings.Contains(b, "a|b") {
		t.Fatalf("bullets keep the pipe: %s", b)
	}
}

func TestRowWithoutTrailingPipe(t *testing.T) {
	got := withComment("| `a` | b", "<!-- plumb: exists a -->", true)
	if got != "| `a` | b <!-- plumb: exists a -->" {
		t.Fatalf("%q", got)
	}
}

func TestRowsInsideFencesAreSkipped(t *testing.T) {
	c := repo(t, map[string]string{"src/a.go": "x\n"})
	src := "```\n| `src` | in a fence |\n```\n"
	if rep := Suggest(c, src, ""); len(rep.Proposals) != 0 || rep.Unmatched != 0 {
		t.Fatalf("%+v", rep)
	}
}

// ---- generic words ----

func TestPlainLowercaseWordsAreNeverSymbols(t *testing.T) {
	c := repo(t, map[string]string{
		"src/a.go": "func name() {}\nfunc listings() {}\nconst skills = 1\n",
		"gradlew":  "type name\n",
	})
	for _, line := range []string{
		"- the `name` field", "- the `listings` array", "| `skills` | list |",
	} {
		if _, ok := one(t, c, line); ok {
			t.Errorf("proposed evidence for a generic word: %q", line)
		}
	}
	// A word with a capital, an underscore or a digit does look like a symbol.
	for _, tok := range []string{"Login", "skillId", "event_outbox", "oauth2"} {
		if !looksLikeSymbol(tok) {
			t.Errorf("%q should look like a symbol", tok)
		}
	}
	for _, tok := range []string{"name", "listings", "skills"} {
		if looksLikeSymbol(tok) {
			t.Errorf("%q should not look like a symbol", tok)
		}
	}
}

// ---- DB tables ----

func TestTableFromCreateTable(t *testing.T) {
	c := repo(t, map[string]string{"db/schema.sql": "CREATE TABLE order_items (id int);\n"})
	p, ok := one(t, c, "| `order_items` | slots |")
	if !ok || p.Evidence[0].String() != `grep "\border_items\b" db/schema.sql` {
		t.Fatalf("%+v ok=%v", p, ok)
	}
}

func TestTableFromGeneratedJooqPreferredOverOtherMentions(t *testing.T) {
	c := repo(t, map[string]string{
		"src/generated/java/db/tables/OrderItems.java": "super(DSL.name(\"order_items\"), null);\n",
		"src/main/java/app/Repo.java":                  "String q = \"select * from order_items\";\n",
		"src/main/java/app/Dao.java":                   "\"order_items\"\n",
		"src/main/java/app/Other.java":                 "\"order_items\"\n",
		"src/main/java/app/More.java":                  "\"order_items\"\n",
	})
	p, ok := one(t, c, "| `order_items` | slots |")
	if !ok || p.Evidence[0].Args[1] != "src/generated/java/db/tables/OrderItems.java" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
}

func TestTableNotFromJSONTestsOrDocs(t *testing.T) {
	c := repo(t, map[string]string{
		"fixtures.json":              "{\"order_items\": 1}\n",
		"src/test/java/AppTest.java": "\"order_items\"\n",
		"docs/design.sql":            "\"order_items\"\n",
		"data/export.csv":            "\"order_items\"\n",
		"README.md":                  "`order_items`\n",
	})
	if _, ok := one(t, c, "| `order_items` | slots |"); ok {
		t.Fatal("proposed evidence that rests on fixtures, tests or docs")
	}
}

func TestTableMentionNeedsFewFiles(t *testing.T) {
	files := map[string]string{}
	for _, n := range []string{"a", "b", "c", "d"} {
		files["src/"+n+"/Q.java"] = "\"order_items\"\n"
	}
	c := repo(t, files)
	if _, ok := one(t, c, "| `order_items` | x |"); ok {
		t.Fatal("a name used all over the code is not evidence in one place")
	}
}

// ---- endpoints ----

const catalogController = `package app.controller;
@RestController
@RequestMapping("/v2/catalog")
class CatalogController {
  @GetMapping("/{itemId}")      Item get() {}
  @GetMapping("/{itemId}/pricing") Pricing pricing() {}
  @PostMapping("/create-new")      void create() {}
}
`

func routeRepo(t *testing.T) *check.Checker {
	return repo(t, map[string]string{
		"src/main/java/app/controller/CatalogController.java": catalogController,
		"src/main/java/app/controller/RegionController.java":  "@RequestMapping(\"/v1/region\")\n",
	})
}

func TestRouteClassLevelOnly(t *testing.T) {
	c := routeRepo(t)
	p, ok := one(t, c, "| `/v2/catalog/{itemId}` | one item |")
	if !ok || len(p.Evidence) != 1 ||
		p.Evidence[0].String() != `grep "\"/v2/catalog[\"/]" src/main/java/app/controller/CatalogController.java` {
		t.Fatalf("%+v ok=%v", p, ok)
	}
}

func TestRouteWithStaticTailAfterVariable(t *testing.T) {
	c := routeRepo(t)
	p, ok := one(t, c, "| `GET /v2/catalog/{itemId}/pricing` | price |")
	if !ok || len(p.Evidence) != 2 || p.Evidence[1].Args[0] != `["/]pricing"` {
		t.Fatalf("%+v ok=%v", p, ok)
	}
	if r := c.Verify(note.Parse(p.New))[0]; r.Stale {
		t.Fatalf("proposed route evidence is stale: %v\n%s", r.Reasons, p.New)
	}
}

func TestRouteSplitAcrossClassAndMethodLevel(t *testing.T) {
	c := routeRepo(t)
	p, ok := one(t, c, "| `/v2/catalog/create-new` | create |")
	if !ok || len(p.Evidence) != 2 || p.Evidence[0].Args[0] != `"/v2/catalog["/]` || p.Evidence[1].Args[0] != `["/]create-new"` {
		t.Fatalf("%+v ok=%v", p, ok)
	}
	if r := c.Verify(note.Parse(p.New))[0]; r.Stale {
		t.Fatalf("%v", r.Reasons)
	}
}

func TestRouteQueryStringAndVerbAreIgnored(t *testing.T) {
	c := routeRepo(t)
	p, ok := one(t, c, "| `/v1/region?lng=&lat=` | region |")
	if !ok || p.Evidence[0].Args[0] != `"/v1/region["/]` {
		t.Fatalf("%+v ok=%v", p, ok)
	}
}

func TestRouteNeverDeclaredGetsNoProposal(t *testing.T) {
	c := routeRepo(t)
	if _, ok := one(t, c, "| `/v9/nothing/here` | x |"); ok {
		t.Fatal("proposed evidence for a route that is not in the code")
	}
}

func TestRouteDeclaredInSeveralDirectoriesStaysAmbiguous(t *testing.T) {
	c := repo(t, map[string]string{
		"src/a/AController.java": "@RequestMapping(\"/v1/pay\")\n",
		"src/b/BController.java": "@RequestMapping(\"/v1/pay\")\n",
	})
	if _, ok := one(t, c, "| `/v1/pay` | pay |"); ok {
		t.Fatal("guessed between two controllers")
	}
}

func TestRouteIgnoresTestsAndDocs(t *testing.T) {
	c := repo(t, map[string]string{
		"src/test/java/ListingTest.java": "get(\"/v2/catalog\")\n",
		"docs/api.java":                  "\"/v2/catalog\"\n",
	})
	if _, ok := one(t, c, "| `/v2/catalog` | x |"); ok {
		t.Fatal("proposed evidence that rests on tests or docs")
	}
}

func TestRoutePrefersControllersOverClients(t *testing.T) {
	c := repo(t, map[string]string{
		"src/app/controller/PayController.java": "@RequestMapping(\"/v1/pay\")\n",
		"src/app/client/PayClient.java":         "@GetMapping(\"/v1/pay\")\n",
		"src/app/other/X.java":                  "\"/v1/pay\"\n",
	})
	p, ok := one(t, c, "| `/v1/pay` | x |")
	if !ok || p.Evidence[0].Args[1] != "src/app/controller/PayController.java" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
}

func TestParseRoute(t *testing.T) {
	good := map[string]string{
		"/v2/catalog":                         "v2,catalog",
		"GET /v2/catalog/{id}":                "v2,catalog,{id}",
		"/v1/region/serviceability?lng=&lat=": "v1,region,serviceability",
		"/v1/users/:userId/orders/":           "v1,users,:userId,orders",
		"POST   /health":                      "health",
	}
	for tok, want := range good {
		r, ok := parseRoute(tok)
		if !ok || strings.Join(r.segs, ",") != want {
			t.Errorf("parseRoute(%q) = %v %v", tok, r.segs, ok)
		}
	}
	for _, tok := range []string{"/", "/{id}", "v2/listing", "https://x.com/a", "//a", "/a b", "/a/*", "src/main", ""} {
		if _, ok := parseRoute(tok); ok {
			t.Errorf("parseRoute(%q) should not be a route", tok)
		}
	}
}

func TestEndpointPairIsAllOrNothingWithinTheCap(t *testing.T) {
	c := repo(t, map[string]string{
		"src/a/AController.java": "@RequestMapping(\"/v1/a\")\n@GetMapping(\"/x\")\n",
		"src/b/Q.java":           "x\n",
	})
	// Two plain path tokens fill two of the three slots, so the endpoint's
	// pair (two greps) does not fit and is skipped, not half-added.
	p, ok := one(t, c, "| `src/a` `src/b` `/v1/a/x` |")
	if !ok || len(p.Evidence) != 2 {
		t.Fatalf("%+v ok=%v", p, ok)
	}
	for _, ev := range p.Evidence {
		if ev.Kind != "exists" {
			t.Fatalf("half an endpoint was added: %v", ev)
		}
	}
}
