---
topic: client-configurator
---

# D246 — `cartographer setup` chains the existing commands, and plans before it changes anything

**Decision.** `cartographer setup` is the first-run command: native service,
first KB, agent clients, health check. It runs no logic of its own. Every step
is the existing command (`service install`/`start`, `kb create` or `kb clone`
with `--restart`, `connect --no-input`), and setup only decides *which* ones.
Deciding comes before doing: setup reads the machine, asks the remote with
`git ls-remote` whether it is empty (create) or has content (clone) and whether
this machine's credentials reach it, and renders the whole plan before any step
runs. The plan is split into a pure `planSetup(facts, options)`. Interactively
it interviews the operator (remote, agents), shows the plan and asks
`Proceed? [Y/n]`. Unattended, every answer is a flag, and `--dry-run` prints the
plan and changes nothing. A rerun skips finished steps. The agent runbook and
the `cartographer-ops` skill wrap it in a one-message interview: the remote,
the agents, and the visibility scope (everywhere or one workspace). The install
scripts, the Homebrew cask and the dashboard all point a fresh machine at it.

**Why.** First run was five commands — install, `service install`,
`kb create` *or* `kb clone`, `connect`, restart. Choosing between the middle two
needed a fact the operator often lacked: is the repository empty? Most failures
also surfaced late. A rejected SSH key showed up at the push, after the service
was already installed and a scaffold already existed (D156 keeps it for exactly
that reason). Every agent install re-derived the same sequence from the runbook.
Asking the remote first turns the commonest failures (wrong URL, missing key,
unknown host key, non-empty "empty" repository) into a stop with nothing changed.
Chaining the real commands means setup cannot say or do something different from
them, and every guarantee they carry (D134 local-only is explicit, D156 the
scaffold survives a failed push, D173 no host key is auto-accepted, D190 the KB
choice is deliberate) holds in setup unchanged. The cost: setup's output is the
concatenation of theirs, so it is less uniform than a purpose-built renderer.

**Alternatives rejected.**

- *A wizard only in the skill and the runbook.* It helps an agent and no
  one else. It also cannot help the first install at all: the skill is
  provisioned by `connect`, the last step of the sequence it would drive.
- *A full-screen bubbletea wizard.* Unusable by an agent, which has no TTY. It
  would need the same flag surface anyway. The line-based interview works in
  every terminal and over any pipe, and the agent passes flags instead.
- *Reimplementing the steps inside setup.* It would fork the behaviour of four
  commands that each carry decisions (D134, D156, D173, D190), and every future
  change would have to land twice.
- *Making `connect` do everything, since it already offers to install the
  service.* `connect` also serves remote servers, where installing a service or
  creating a KB is wrong. That would overload one command with two jobs.
- *Binding every KB when the choice is ambiguous.* Defaulting to all KBs is the
  over-exposure D190 closed. Setup binds only the KB it adds, keeps an existing
  binding, and otherwise asks (interactive) or refuses naming `--kb`
  (unattended).

**Consequences.**

- `planSetup` is the one place the rules live, and it is table-tested. The
  runner is tested against stubs that record the order of the real commands,
  including that a failed probe or a declined interview runs none of them.
- A new first-run step must be an existing command setup calls, not code
  inside setup; the "already done" test for it goes into `planSetup`.
- Setup refuses to act when the client points at a non-loopback server. It
  provisions a local server and must never silently re-point a machine at one.
- The runbook's step 2 is a `--dry-run`, and an agent checks its KB line against
  what the user said. A *mount* for a repository the user called empty is a
  stop, because it means the user is wrong about their own repository.
