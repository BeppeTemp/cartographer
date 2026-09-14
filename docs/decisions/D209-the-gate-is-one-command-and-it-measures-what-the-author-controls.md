---
topic: project-governance
---

# D209 — The gate is one command, CI runs that command, and a budget measures what the author controls

**Decision.** `make gate` is `fmt-check vet test`, and `ci.yml` runs **`make
gate`** instead of restating its steps. `fmt` and `fmt-check` are given the two
directories that hold this module's Go code (`cmd internal`) rather than `.`. The
`AGENTS.md` line budget is measured on the file with its generated blocks
stripped, while the character budget is measured on the whole file. A decision
file may not keep a placeholder from the template.

**Why.** Four small things, each of which made a gate say something other than
what it meant.

**"What must be green" had three answers.** `AGENTS.md` and `CONTRIBUTING.md` said
`make gate`; `docs/testing.md` §Before a pull request listed five commands with no
gofmt in them; `ci.yml` ran those same five. So `gofmt` was inside the gate and
outside CI, and the commit that added it stated that the omission "cannot land
again" — which was not true for anyone who did not run `make gate` locally. The
premise of these gates (D204) is that a gate you have to remember is not a gate,
and it was being violated by the gate about formatting. With CI running the one
command, a step can only be added in one place.

**`gofmt -l .` walks `.worktrees/`.** The same refactoring that introduced
`make worktree-add` put each plan's worktree *inside* the main working copy, and
gofmt does not skip hidden directories. Measured: with one worktree present,
`make gate` at the root fails naming a file in someone else's unfinished branch,
and `make fmt` would rewrite it — which is precisely what the `implement-issue`
mandate forbids the coordinator to do ("sibling subagents are active on other
worktrees: never touch their files"). `ChainBytes` already skipped `.worktrees`,
so the interaction had been considered on the documentation side and missed on the
build side.

**The line budget counted generated lines.** `AGENTS.md` was at 115 of 120 lines
with a 26-package generated code map inside it. Adding five Go packages would have
failed a documentation test whose message reads *"move a section to the page that
owns it"* — the wrong instruction for that change, and the kind of misdirection
that gets a budget raised rather than respected. Lines are a proxy for how much
prose a reader follows, so they are measured on prose; characters are what a client
loads, so they are measured on the file.

**A half-written decision indexes as a blank line.** `make decisions-new` fills in
the number and leaves the title as the template's angle-bracketed placeholder — it
reserves a file, it does not write it — and `LoadDecisions` accepts that heading.
The generated index then renders the placeholder as an HTML tag, so the entry
appears empty on GitHub and on the site. The check belongs at commit time, not in
the generator.

**Alternatives rejected.**

- *Keep the CI steps explicit, for readability.* Explicitness is exactly what
  drifted: two lists of the same thing, one of which was missing a step for a
  release cycle. The steps are still visible — in the `Makefile`, which is also
  where a contributor reads them.
- *`git ls-files '*.go'` for gofmt.* Excludes `.worktrees` correctly and misses a
  file that has been written but not staged, so the gate would pass locally and
  fail in CI. Naming the source directories has neither problem, and a third
  directory of Go code would be a visible one-line change.
- *Exclude `.worktrees` with a `find`/`grep` pipeline.* Longer, platform-sensitive,
  and it encodes the exclusion instead of the inclusion — the list of things to
  skip is the list that goes stale.
- *Raise `MaxRootLines`.* It would be raised again every few packages, which is
  what a budget nobody believes looks like. The number is right for prose; it was
  measuring the wrong text.
- *Move the worktrees outside the repository* (`../cartographer-worktrees/`). It
  would fix gofmt and break the thing the current layout buys: `.worktrees/` is
  git-ignored, visible in one `ls`, and removable with one target. The scoping fix
  is smaller than the relocation.
- *Validate the placeholders inside `make decisions-new`.* That is the one moment
  when the placeholders are wanted.

**Consequences.** `make gate` is the definition of "green" and `docs/testing.md`
says so rather than listing an alternative. A new directory of Go code has to be
added to `GO_DIRS` or it is silently unformatted — the same class of omission this
decision fixes, so it is called out in the `Makefile` next to the variable. The
generated code map can now grow to the character budget without producing a
misleading failure, which is what made room for the two nested `AGENTS.md` files
D203 added.
