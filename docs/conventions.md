# Conventions — Cartographer

Rules to keep the code consistent. New code should resemble the existing code.

## Language

- **Everything in English**: code (comments, error messages, identifiers, doc-comments), documentation (`docs/`, `README.md`, `AGENTS.md`), plan issues and D entries.
- MCP tool `Description`s in English (consumed by LLM agents).
  They have a size budget (D285, `TestServer_ToolDescriptionBudget`): state what the tool does, when to use it instead of a neighbour and the rules needed to call it; no `D<n>` references, rationale or response-field listings; put the detail in `docs/control-plane.md` §Tool descriptions and their size budget.

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
- Every tool declares an `InputSchema` (JSON Schema) and a short, keyword-rich `Description` (in English, within the D285 budget) for discovery by the LLM agent.

## Settings

- A new key in the server `config.yaml` (`rawConfig`, `KBSpec`) or in the client `.cartographer.yaml` needs an entry in the
  schema of its package (`internal/config/schema.go`, `internal/clientconfig/schema.go`): description, default, values,
  and the env var and flag that override it. A reflection test fails the build without it. Run `make settings-example` for
  the server file. Machine-written caches are not settings: they go in `.cartographer-state.json`, not in the YAML.
- Code that rewrites a user-edited YAML file edits its `yaml.Node` tree; it never marshals a map or a struct over the file,
  which drops every comment (D747).

## Tests

- Tests use the stdlib `testing` package, alongside the code (`*_test.go`).
- Tests that use `git` must `t.Skip` if `git` is not in the PATH.
- Server tests feed `Run` via `io.Pipe`/buffer with JSON-RPC sequences.
- In `internal/mcpserver`, `internal/kb` and `internal/lint` a top-level test
  starts with `t.Parallel()` (D786): their tests spawn git, and run serially
  they were most of CI's wait. A test stays serial, with no `t.Parallel()`, when
  it or a helper it calls writes process-global state: a package-level var or
  hook (in any package, e.g. `gitx.FetchTimeout`), `t.Setenv`, `os.Chdir`,
  `os.Stdout`/`os.Stderr`, or the `kb.InitOn*`/`InitWith*` setters. Go never
  runs a serial test alongside parallel ones, so serial is always the safe
  default; `go test -race` catches most mistakes.

## Dependencies

- Before adding an external import, check
  [D1](decisions/D1-stdlib-only-constraint-removed.md) and record a new dependency decision
  in the topic that owns it.
- Current default: stdlib preferred; external dependencies allowed when the benefit is clear.
- Active external dependencies: `modernc.org/sqlite` (persisted search index, D32 — pure-Go, no cgo); `charmbracelet/bubbletea`+`bubbles`+`lipgloss`+`x/term` (client TUI dashboard, D35/D37 — `cmd/cartographer`, TTY detection); `gopkg.in/yaml.v3` (server YAML config and client `.cartographer.yaml`, D38); `github.com/modelcontextprotocol/go-sdk` (the MCP wire format itself, [D168](decisions/D168-the-mcp-wire-format-comes-from-the-official-sdk.md) — the one dependency taken *to stop* maintaining an implementation of someone else's moving specification); `golang.org/x/sys` (direct since [D215](decisions/D215-the-kb-lock-splits-by-platform-and-windows-is-a-gate.md) — `windows.LockFileEx` and `windows.OpenProcess` for the per-KB lock in `internal/kb`, which `syscall` does not expose on Windows; it was already in the module graph as an indirect dependency); `golang.org/x/sync` (direct since [D736](decisions/D736-a-large-kb-is-served-from-every-core-and-drawn-at-a-fixed-rate.md) — `singleflight`, so concurrent Atlas reads of a cold KB share one whole-KB lint; already an indirect dependency).
- **Frontend dependencies** (`web/`, D227), pinned by `web/package-lock.json` and
  built only by the `web` CI job: `react`+`react-dom` (UI runtime), `vite`+
  `@vitejs/plugin-react`+`typescript` (build), `three`+`d3-force-3d` (the
  WebGL graph and its force layout; community colouring comes from the server,
  D244), `react-markdown`+
  `remark-gfm`+`rehype-sanitize` (concept bodies, with raw HTML disabled),
  `vitest`+`@testing-library/*`+`jsdom`+`@playwright/test`+`@axe-core/playwright`
  (tests). The shipped bundle vendors
  every asset: no CDN, no remote font, no runtime network call outside the
  Cartographer origin.
- `web/` formatting: `web/.prettierrc` sets Prettier's print width to 120, the style of `web/src`; `web/.prettierignore` leaves out `web/src/test`. Prettier is not a dependency and no gate checks it: format the files you touch (`cd web && npx prettier --write <file>`), never the whole tree (#706).
- Platform-specific code is a build-tagged file pair with an identical helper surface (`internal/kb/lockfile_unix.go` and `lockfile_windows.go`), never a `runtime.GOOS` branch inside shared code: the compiler then checks both platforms and `GOOS=windows go vet ./...` is a real gate.
