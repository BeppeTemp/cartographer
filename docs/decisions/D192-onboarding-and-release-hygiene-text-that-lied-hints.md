---
topic: deployment-release
---

# D192 — Onboarding and release hygiene: text that lied, hints that could not be run, paths that ended half-done

**Status: implemented.** Closes #246.

**Context.** A read-only audit of installation and onboarding found the machinery solid — one
binary for server and client, Homebrew plus a POSIX installer, four platform targets, an idempotent
`service install`, CI running vet/test/smoke/E2E/installer — and the **documentation and edge
paths** lagging behind it. Fifteen items, each verified against the code, none a design question.
They are one decision rather than fifteen because they share no invariant and none blocks another:
grouping them kept fifteen trivial PRs from competing with the substantive work.

**Decisions.**

- **Text that was false is corrected at the source, not paraphrased.** `bindingNotYetEnforcedNote`
  still told the user "bindings are recorded but not yet enforced during sync" — true under D169,
  false from D170 on, and pinned by a test asserting its presence. The constant and its three call
  sites are gone, and the test now asserts its **absence**, which is what stops it coming back.
  `docs/deployment.md` claimed `install.sh update` restarts a running service, contradicting the
  paragraph immediately above it describing `upgrade-repair` (D121). `docs/agent-install.md` said a
  failed `kb create --remote` scaffold "was already removed" while D156 deliberately keeps it and
  prints how to fix or remove it. The `cartographer-ops` bundled skill prescribed
  `brew upgrade` + `service restart`, when the Cask's own post-install hook runs `upgrade-repair`
  and no follow-up is needed. `kb-create` told the operator to hand-edit `.cartographer.yaml`,
  never mentioned `cartographer client bind`, and contradicted itself on KB naming.
- **Every hint the tools print can be pasted and run.** The no-KB message named only
  `kb create --remote <url>`, leaving an operator without a remote with no working form; it now
  names `--no-remote` too, with its cost stated. The installer's `PATH` warning now gives the two
  concrete next steps — invoke the printed path, or add it to `PATH` — instead of stating a fact
  and stopping.
- **`uninstall` refuses rather than leaving a half state.** It removed the binary only, so a
  machine with a service installed kept launchd/systemd units pointing at a missing executable. It
  now detects the units, names them and the commands that remove them, and exits non-zero — unless
  `--binary-only` says the operator meant exactly that, in which case it proceeds and states what
  it left behind. A coordinated teardown that deletes a user's KB data is not something an
  installer should do implicitly, so it still does not.
- **A checksum file that does not cover the asset is an error.** `install.sh` skipped verification
  when the entry was missing, which is the shape a truncated or tampered manifest has. A release
  that ships no `sha256sums.txt` at all remains installable — older tags have none, and refusing
  them would break a legitimate downgrade. Four scenarios now cover the matrix; the mismatch case
  asserts no binary is left behind.
- **The Cask deprecation is upstream's, and is recorded as such.** Item 13 of the plan prescribed
  replacing `postflight` with `postflight_steps` in `.goreleaser.yaml`. Verified and **corrected
  during implementation**: that file contains no `postflight`. It uses
  `homebrew_casks[].hooks.post.install`, GoReleaser's current API since v2.13; the deprecated
  stanza is what GoReleaser *emits*, confirmed by generating the Cask locally with GoReleaser
  2.18.1, which still writes `postflight do`. No setting here changes it. The finding is recorded
  as a comment next to the hooks block with the version and date, to be re-checked after a
  GoReleaser bump. Hand-writing a stanza the template does not own was rejected. *Superseded by
  [D199](D199-the-cask-s-install-steps-are-postflight-steps-the-next.md): `custom_block` is GoReleaser's supported way to emit such a stanza.*
- **The two undocumented destinations get an alarm, not a move.** Codex skills materialize under
  `.codex/skills` while the vendor documents `$HOME/.agents/skills`; OpenCode agents under
  `.opencode/agent` while the vendor prefers `.opencode/agents`. Both work against the real
  clients. A destination change is a migration — prune the old files, re-key the lockfile — and for
  Codex the right target is the *repository* path, which only exists once a workspace scope does
  (D193), so moving it now would mean doing it twice. What was missing was the alarm: a test
  asserts the **declared** destination against the client's own discovery output, so it survives
  D193 changing that destination.
- **The compatibility test skips where it cannot answer, and only that.** It skips when the client
  is absent, and also when the client errors, times out or prints nothing — an unrelated client
  problem must not turn this into a red suite everyone learns to ignore. Only a successful run
  whose output does not mention the declared directory is a signal. On the machine that
  implemented this, `codex debug prompt-input` answered and `opencode agent list` did not, which is
  exactly the case the skip exists for.

**Invariants kept.** The installer stays POSIX `sh` and network-free under test. The GoReleaser
guard keeps asserting `upgrade-repair` through the stable linked binary. Bundled skills keep their
frontmatter contract. `uninstall` on a machine with no units behaves exactly as before.

**Consequences.** `install.sh uninstall` can now exit non-zero where it used to succeed —
behaviour change, release-note it. Everything else is documentation, corrected hints and test
coverage. The provider list in `docs/agent-install.md` (item 6) was already fixed by the
Antigravity work (D194) and needed no change here.
