---
topic: deployment-release
---

# D280 — The Cask caveat names both the first-install and the upgrade next step

**Decision.** The Homebrew Cask's `caveats` is two labelled lines: `First install:
cartographer setup` (D253) and `Upgrade: nothing to do`, naming the next `cartographer sync`
that switches the service to the new binary and `cartographer upgrade-repair` that does it now
(D199, D121).

**Why.** Homebrew prints a caveat after `brew install`, after every `brew upgrade` and under
`brew info`, and a Cask cannot tell them apart. The single line `Next: cartographer setup`,
right for a first install, told every upgrading user to run setup — harmless, since setup is
idempotent, but wrong, and it never named `upgrade-repair`. `install.sh` does know (`fresh=1`)
and keeps printing only the first-install line. The cost is one extra line on a first install.

**Alternatives rejected.**
- A `postflight_steps` that detects an existing install and prints accordingly: the steps run in
  Homebrew's sandbox with a temporary `HOME` (D199), so they cannot see the existing install.
- Dropping the caveat: a first install would end with no pointer to `setup`, which is what D253
  made the one next command.

**Consequences.** The `test-install` guard's "no `upgrade-repair`" check (D199) now reads the
template without its `caveats:` line — the caveat names the command to the user, while what must
not carry it is a step Homebrew runs — and asserts that the caveat has both labels.
