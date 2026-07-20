# Conventions — Cartographer

Rules to keep the code consistent. New code should resemble the existing code.

## Language

- **Everything in English**: code (comments, error messages, identifiers, doc-comments), documentation (`docs/`, `README.md`, `AGENTS.md`), plan issues and D entries.
- MCP tool `Description`s in English (consumed by LLM agents).

## Go style

- `gofmt` always clean (`make fmt`). `go vet` with no warnings.
- Errors: wrap with `fmt.Errorf("context: %w", err)`; export sentinel errors in `okf` and compare them with `errors.Is`. No `panic` in the normal flow.
- Comments: only when the WHY is not obvious. No comments that repeat what the code does. No TODOs left half-done, no temporary fixes: minimal and complete changes.

## OKF in practice

- **The path is the identity**: the ConceptID is the path relative to the KB root without `.md`.
- **Only one required field: `type`**; everything else is optional.
- **Permissive consumption**: tolerate unknown fields, unrecognized types and broken links (stubs are legitimate, not errors).
- **Reserved files**: `index.md`, `log.md`, `_map.md`, `_archive.md` (legacy, read-compat), `AGENTS.md` (see `okf.IsReserved`).
- **kebab-case file names** (validated by `okf.PathToID`).
- **Bundle-relative links** starting with `/`, internal to the single KB.

## Data-plane security

- Every file access goes through `kb.ResolvePath`: rejects absolute paths and any escape from the root (`../`).
- **Atomic** writes (write to temp + rename): use `kb.WriteFileAtomic`, never `os.WriteFile` directly on content.
- `log.md` is append-only, newest-on-top (`kb.AppendLog`).

## MCP server

- Diagnostic logs on **stderr**; **stdout** reserved for the JSON-RPC protocol.
- Tool application errors go in the `ToolResult` (`errorResult`, `isError`), not as a JSON-RPC protocol error.
- Every tool declares an `InputSchema` (JSON Schema) and a keyword-rich `Description` (in English) for discovery by the LLM agent.

## Tests

- Tests use the stdlib `testing` package, alongside the code (`*_test.go`).
- Tests that use `git` must `t.Skip` if `git` is not in the PATH.
- Server tests feed `Run` via `io.Pipe`/buffer with JSON-RPC sequences.

## Dependencies

- Before adding an external import, check decision D1 in `decisions.md`.
- Current default: stdlib preferred; external dependencies allowed when the benefit is clear.
- Active external dependencies: `modernc.org/sqlite` (persisted search index, D32 — pure-Go, no cgo); `charmbracelet/bubbletea`+`bubbles`+`lipgloss`+`x/term` (client TUI dashboard, D35/D37 — `cmd/cartographer`, TTY detection); `gopkg.in/yaml.v3` (server YAML config and client `.cartographer.yaml`, D38).
