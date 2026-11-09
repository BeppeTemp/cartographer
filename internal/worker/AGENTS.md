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
- **A prompt task's log lines and trailers come from the guard (`prompt_guard.go`), never from the model**: nothing
  under `worker/` in the log is written by a model, and its marker path stays apart from the budget's by the bracketed prefix.
- **Nothing but the model's closing step may write `kb-doctor`** (`doctor.go`): a worker's own entries pass `noMarker`, or an interrupted session would silence `doctor_suggested`.
- `synthesis/` defines its own `ToolCaller` and never imports this package: the kind (`synthesis.go`) imports it, so the other direction would be a cycle.
- **The beater never blocks the runner** (`beater.go`, D1259): `Beat` only copies a snapshot under a mutex, a
  separate goroutine sends it, and a failed beat or a `404` is a log line that leaves the idle signal unknown.
- **The beater bounds its wait on the runner** (`beater.go`, D1259): `Beat` copies a snapshot under a mutex and a
  separate goroutine sends it; only the first and the closing beat are sent synchronously (each at most 5 s,
  queued behind `sendMu`), and a failed beat or a `404` is a log line that leaves the idle signal unknown.
  Server timestamps are compared with each other (`now` against `last_activity_at`), never with the worker's clock.
- **A connector's tools are allowlisted by the config, never by the server** (`connector/`): deny by
  default, enforced on the call too; they reach the unit-execution task only. No KB tool name contains
  `__` (a test in `internal/mcpserver` pins it). A child process gets only the variables the config lists
  (`connector.ChildEnv`), so the model key never reaches third-party code. A feed's cursor is written only
  with `if_match` (`feed/`).
  A Source can name only a recipe or connector in the binary or the config, never code, a command or a
  path (`recipe/`); the git reader refuses any host outside `git.allow_hosts` before a clone, and
  `recipe_test.go` pins the refusals (file, ssh, userinfo, local path, `..`, symlinks).
- **An exec recipe is argv, no shell, with a listed environment** (`recipe/exec.go`, D1262): its command, args and env names come from the config
  only; the redaction and process-group-kill tests in `recipe/exec_test.go` pin it. Protocol v1 sends `mode: "list"` only.
