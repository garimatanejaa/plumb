# plumb

plumb checks that the claims in agent notes are still true. This note is checked by plumb itself (`plumb check CLAUDE.md`).

## Layout

- The CLI entry point is `cmd/plumb/main.go` <!-- plumb: exists cmd/plumb/main.go ; grep "func run\(" cmd/plumb/main.go -->
- Claims are parsed in `internal/note` <!-- plumb: exists internal/note/note.go ; grep "func Parse\(" internal/note/note.go -->
- Evidence kinds and their arity are the `Kinds` table in the note package <!-- plumb: grep "var Kinds" internal/note/note.go -->
- Only `internal/check` reads the repository, and every read goes through `Resolve` <!-- plumb: grep "func \(c \*Checker\) Resolve" internal/check/path.go -->
- Each evidence kind has one handler, registered in the `handlers` table <!-- plumb: grep "var handlers" internal/check/check.go -->
- Table rows are claims; the helpers are `IsTableRow` and `IsTableSeparator` in the note package <!-- plumb: grep "func IsTableRow" internal/note/note.go ; grep "func IsTableSeparator" internal/note/note.go -->
- `suggest` finds HTTP routes in code through `routeEvidence` <!-- plumb: grep "func routeEvidence" internal/suggest/suggest.go -->
- The `count` evidence kind lives in its own file <!-- plumb: exists internal/check/count.go ; grep "func \(c \*Checker\) count" internal/check/count.go ; grep "\"count\": 4" internal/note/note.go -->
- Output formats live in `internal/render` <!-- plumb: exists internal/render/render.go -->
- `suggest` only proposes evidence it has verified, and prints a diff <!-- plumb: grep "func Diff\(" internal/suggest/suggest.go -->
- Every MCP tool is marked read-only <!-- plumb: grep "readOnlyHint" internal/mcp/server.go -->

## Rules

- The module is named `plumb` <!-- plumb: exists go.mod ; grep "^module plumb" go.mod -->
- `internal/note` and `internal/render` must not touch the filesystem; only `internal/check` may.
- Standard library only: go.mod must have no `require`.
- Evidence never runs commands, and notes are untrusted input.
- A new evidence kind needs an entry in `note.Kinds`, a handler, and tests; a test fails if the two tables disagree <!-- plumb: grep "TestKindsHaveHandlers" internal/check/check_test.go -->
- Golden files live in `testdata/sample-repo/golden`; regenerate with `go test ./internal/render -update` <!-- plumb: exists testdata/sample-repo/golden -->

## Commands

- `go test ./...` runs everything
- `go build ./cmd/plumb` builds the single binary
