# AGENTS.md — the worker

The model client, the agent loop and the runner of `cartographer worker` (D1258). A defect here spends
money and writes to KBs when nobody is looking.

- **No secret anywhere**: the config names environment variables (`*_env`); a value never reaches a
  log line, a `log_append` entry or an error. Types with a key redact in `String`/`GoString`.
- **A KB never selects code**: the runner dispatches registered task kinds only; their instructions
  come from the config directory or the binary, never from what a KB contains.
- **The budget is rebuilt from the KBs' logs, never from local disk** (`budget.go`): a worker has no
  volume. The closing entry's format is the contract the reader parses.
- **`serve` never links this package**, and this package imports no `internal/mcpserver` or
  `internal/kb` (`imports_test.go` pins both).
- **Shutdown writes the closing entries through the short-timeout caller** (`RunnerOptions.CloseCallers`):
  client calls take no context and time out at 30 s, more than Kubernetes' grace period.
- **Adding a task kind**: declare its options in `internal/workerconfig/schema.go`, write the kind
  (`Name`, `Options`, `Validate`, `Build` returning an `Instance`) and add one line to `DefaultKinds()`.
- **A synthesis write never targets an existing concept and never uses a blocking contradiction kind**
  (`synthesis/guard.go`, D1263): hypotheses are gap Contradictions created at a deterministic id; the guard,
  not the server, keeps D11.
