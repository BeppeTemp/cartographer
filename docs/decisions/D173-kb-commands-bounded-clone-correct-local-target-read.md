---
topic: deployment-release
---

# D173 — `kb` commands: bounded clone, correct local target, read-only list

**Decision.** `gitx.Clone` takes a context and a non-interactive environment;
`kb create`/`kb clone` accept `--config` and refuse to act on the local data dir
when this machine's client points at a remote server (`--local` opts out); `kb
list` reports what is on disk and what the server serves, writing nothing.

**Context.** Three defects in the same family: a command that can hang with no
output, one that silently acts on the wrong directory, and a state nothing on the
CLI could observe.

- **Bounded execution over a diagnosis.** `Clone` was `exec.Command` +
  `CombinedOutput()`: no deadline, no progress, nothing stopping git from opening
  a credential or host-key prompt against a stdin that cannot answer. A hang was
  observed; which prompt fired is not asserted, because the remedy is the same
  either way — `GIT_TERMINAL_PROMPT=0`, `BatchMode=yes -o ConnectTimeout=10` for
  ssh remotes, `--timeout` (default 120s), and streamed progress.
- **Never force host-key acceptance.** `StrictHostKeyChecking=accept-new` would
  trade a hang for a silent trust-on-first-use decision on an operator's machine.
  Git fails quickly and says why; the operator accepts the key themselves.
- **An operator's `GIT_SSH_COMMAND` wins.** Someone who configured a proxy
  command, an identity file or a jump host has said how to reach their forge.
  Overwriting that breaks a working setup to prevent a hypothetical one, so the
  default is added only when neither the process environment nor the caller
  provides one.
- **Cleanup removes only what the command created.** The destination is checked
  not to exist before the clone, so anything under it afterwards is ours. An
  interrupt now cancels the context — killing git and waiting for it to exit —
  before removing the tree: deleting a directory a running git is still writing
  produces a second, more confusing failure, and the previous `defer` never ran
  on SIGINT at all.
- **The wrong target is an error, not a warning.** `resolveServerDataDir` read
  the standard service config path while `service install --config` accepts
  another, so a service installed at a custom path was invisible and the command
  reported `KB "x" mounted at …` about a directory nothing reads. `--config`
  fixes the resolution; the guard covers the other half — a client pointed at a
  remote server means these commands would act on a server nobody is talking to.
  A command that claims to have mounted something it did not is the worst outcome
  available, so it fails, with `--local` as the declared opt-out and no opinion at
  all when `--data` already names the target or no client config exists.
- **`kb list` is strictly read-only.** It must not call `kb.Open`, which
  self-migrates the local git-exclude entry of every repository it touches: a
  listing command that mutates what it lists is not one. Validity is read
  directly from `data/index.md`, which is what `Open` itself checks. A missing
  data dir is reported, never created — that is `serve`'s job, not a listing's.
- **"Not mounted" and "could not ask" are different answers.** When `/health` does
  not respond the `MOUNTED` column disappears and the reason is printed. Rendering
  an unreachable server as "nothing is mounted" would invent a fact.

**Consequences.** A new subcommand, new flags (`--config`, `--local`,
`--timeout`), and a guard that starts **failing** a usage which is silently
ineffective today — worth naming in the release notes together with `--local`.
`kb create --remote` stays mandatory (D134); this changes nothing about it.
