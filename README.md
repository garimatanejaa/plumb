# plumb

**Notes that know when they are stale.**

Agents read `CLAUDE.md`, `AGENTS.md` and memory files, and nothing checks them against the code. After a refactor they go quietly wrong, and the agent acts on false information with full confidence.

plumb lets a note's claims carry **evidence**, and re-checks it against the repository. No embeddings, no LLM judgment: real files, real grep, exact answers. One static binary, standard library only.

## Quick start

Prebuilt binaries are in `bin/` (macOS and Linux, arm64). Or build one:

```
go build -o plumb ./cmd/plumb
```

Try it on the sample repository:

```
plumb check testdata/sample-repo/CLAUDE.md
```

```
CLAUDE.md
  ✓ line 5  Login is handled in the auth package
  ✗ line 6  Config lives in config/app.yaml
      exists config/app.yaml: path does not exist
  ...
7 claims, 4 stale
```

The exit code is `1` when anything is stale, so it works in CI and git hooks.

## Writing evidence

Add evidence as a hidden comment on the claim's line. The note renders unchanged.

```markdown
- Login lives in the auth package <!-- plumb: exists src/auth ; grep "func Login" src/auth -->
- The old billing module is gone <!-- plumb: absent src/billing -->
```

A claim is fresh only if **all** its evidence holds. Lines without a plumb comment are never judged, and fenced code blocks are ignored.

**Table rows work too.** Put the comment inside the last cell so it stays hidden when the table is rendered:

```markdown
| Client | Purpose |
|---|---|
| `PaymentClient` | User profiles <!-- plumb: grep "class PaymentClient" src/main/java/app/PaymentClient.java --> |
```

A raw `|` would split the cell, so inside a table row write a pipe as `\|` (plumb undoes the escape before reading it). The header row and the `|---|` divider are never claims.

### Evidence kinds

| Evidence | Passes when |
|---|---|
| `exists PATH` | the path exists under the repo root |
| `absent PATH` | the path does not exist |
| `grep "RE2" PATH` | the pattern matches in the file, or any file under the directory |
| `hash PATH[:A-B] HEX8+` | the sha256 of the file (or lines A to B) starts with the prefix |
| `count "GLOB" DIR OP N` | the number of files under DIR whose name matches GLOB satisfies OP (`>=`, `<=`, `==`, `>`, `<`) N |

Arguments are space-separated; `"double quotes"` group, and `\"` is the only escape, so regex backslashes pass through. `grep` skips `node_modules`, `vendor`, `dist`, `build`, `target`, `.venv`, `.git`, binaries and files over 2 MB. `count` matches file names only and never reads contents.

## Commands

```
plumb check   [--root DIR] [--json] [FILE...]   verify claims; exit 1 if any is stale
plumb filter  [--root DIR] [--drop] FILE        print the note with stale claims marked [STALE] or dropped
plumb hash    [--root DIR] PATH[:A-B]           print hash evidence to paste into a note
plumb suggest [--root DIR] FILE                 propose evidence for list items and table rows that have none
plumb mcp     [--root DIR]                      serve notes to agents over MCP on stdio
```

Default files are `CLAUDE.md` and `AGENTS.md` in the current directory; the default root is the note's directory. Exit codes: `0` all fresh, `1` stale claims found, `2` usage or I/O error.

`plumb filter` puts `[STALE]` at the start of a list item, or inside the first cell of a table row, so tables stay tables.

## Getting started on an existing note

`plumb suggest` reads a note you already have and prints a diff that adds evidence. It writes nothing:

```
plumb suggest CLAUDE.md            # read the proposals
plumb suggest CLAUDE.md | patch -p1   # apply them, after you have read them
plumb check CLAUDE.md
```

It is conservative: it proposes only evidence it has just verified against the repository, and nothing when a token is ambiguous. Every proposal is something a human accepts.

| Token in the note | What it proposes |
|---|---|
| a path at the repo root | `exists` |
| a package-relative path such as `controller/` | `exists` on the one place it lives. The shallowest match wins if it is the only one at that depth; test copies and dependency, build and hidden folders are ignored; two matches at the same depth propose nothing |
| a class, type or function name | `grep` where it is **defined** (`class`, `type`, `func`, `CREATE TABLE`, and so on), never from a passing mention in a comment, a doc, a test or a tool folder. Plain lowercase words like `name` or `listings` are fields, not symbols, and get nothing |
| a snake_case table name | `grep` in the `CREATE TABLE`, or in a source file that quotes it, generated jOOQ classes first. JSON, CSV, tests and docs never count |
| an HTTP route like `/v2/catalog/{id}` or `GET /v1/region?lng=&lat=` | `grep` for the route as a string literal. Frameworks split a route across a class-level and a method-level mapping, so plumb finds the longest static prefix and the static tail after the last variable, in the same file. Verbs, query strings and variable names are ignored, and a route declared in several places gets nothing |
| "115+ services" next to a directory | `count "*.java" DIR >= 115`, but only if the directory already holds that many files. The number is a floor taken from the claim itself |

Plain and camelCase field names such as `skillId` have no declaration to anchor to, so they get nothing. That is deliberate: no evidence is better than evidence that raises false alarms.

## For agents

```
claude mcp add plumb -- plumb mcp --root /path/to/repo
```

Three read-only tools: `check_notes`, `read_fresh_notes` (the note with stale claims removed, checked on every call, so an agent never receives a stale line) and `suggest_evidence`.

## In CI

```
plumb check CLAUDE.md AGENTS.md
```

This repository does that on its own `CLAUDE.md`.

## Status and limits

plumb 0.1.0. It has been run on its own notes and on three real Java and Gradle service repositories. On those it proposed evidence that all stayed fresh, and in a controlled test (a route prefix changed, a route and a class renamed, a table class deleted, on a copy of the code) it flagged exactly the claims those changes broke and nothing else. It has not yet caught a real stale claim in the real repositories, because their notes describe structure that had not changed since they were written.

What to expect:

- **Evidence proves location, not behavior.** Checking that `controller/` exists does not show it is organized by domain. plumb catches renames, moves and deletions, not changes in what code does.
- **Some evidence is loose.** When a route or column appears in several files of one folder, plumb greps the folder, which is weaker than a single file. A shared route prefix means one rename flags every row that uses it.
- **`count` is a proxy.** It counts files, not classes, and a floor like "115+" stays true for a long time.
- **`suggest` is a helper.** It misses many claims, and conventions or behavior ("use SLF4J") cannot be checked mechanically at all.

## Safety

Notes are untrusted input, since a cloned repo's `CLAUDE.md` is written by whoever can push to it. Evidence never runs commands. Paths must be relative, `..` is rejected, and symlinks are resolved and re-checked so a link cannot lead outside the repo. The MCP server is read-only and cannot be asked to read outside its root. plumb opens no network connections.

## Layout

```
cmd/plumb/            the CLI
internal/note/        parses claims from a note (no filesystem)
internal/check/       verifies evidence; the only code that reads the repo
internal/render/      text, JSON and filtered output (no filesystem)
internal/suggest/     evidence proposals
internal/mcp/         the MCP server
testdata/             a sample repository and golden outputs
```

## Build and test

```
go build ./cmd/plumb     # one static binary, standard library only
go test ./...
```
