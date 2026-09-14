---
topic: project-governance
---

# D205 — The code map is generated from the package doc comments

**Decision.** The code map in `AGENTS.md` is a generated block, produced by
`make codemap` from the first sentence of each package's doc comment, and
verified by `internal/repodocs`. A package with no doc comment fails the build.
The hand-written map, with its per-file listings, is gone: the file-level hints
that were load-bearing moved *into* the package doc comments.

**Why.** The map was ~40 lines of directory listing inside a file that is loaded
on every session, describing a layout only a human kept in sync. It is the part
of a context file that goes stale first, and the failure is invisible: a package
that moved or changed responsibility leaves a map that still reads plausibly.

Generating it also inverts the cost. Writing the map used to be maintenance;
now it is a by-product of documenting the package, which is where a Go developer
already looks (`go doc`) and where the compiler's own tooling surfaces it. And the
generator audited the thing it generates from on its first run: it found three
packages with no doc comment at all (`auth`, `skillbundle`, `sops`) and four whose
package doc was actually a *file* comment that happened to sit above a `package`
clause — `cmd/cartographer` described `connectform.go`, `internal/kb` described
the conflict registry, `internal/mcpserver` described `tools_artifact.go`,
`internal/provisioning` described `bootstrap.go`. All seven are fixed; the four
file comments were kept and detached from the `package` clause by a blank line,
which is all that was ever wrong with them.

**On whether a code map belongs in a context file at all.** The evidence usually
cited against it — natural-language summaries answer 4 of 45 behavioural questions
where the source answers 27 — was produced with localisation held fixed by an
oracle, and measured the agent *acting*. It says nothing about finding the right
package, and the same body of work finds that localisation is where the savings
are. So a one-line-per-package index is kept deliberately: it serves localisation,
which was never tested, and not behaviour, which was. What it must not become is
a summary of what the code *does* — that is what `go doc` and the source are for,
and the ~104-character cut enforces the distinction by construction.

**Alternatives rejected.**

- *Keep the hand-written map.* It was already partly wrong when this was written,
  and nothing would have reported it.
- *Delete the map entirely*, as the strict reading of the pattern suggests. That
  throws away the localisation aid on the strength of a study that held
  localisation fixed. Removing it is a bigger claim than the evidence supports.
- *Generate a full file-level tree* (`go list` plus the files of each package).
  Longer than the hand-written version it replaces, and file names are the part
  an agent can list for itself in one command.
- *Put the map in a separate page and link it.* It would stop being loaded at turn
  zero, which is precisely when localisation is worth something.

**Consequences.** Adding a package means writing its doc comment, or CI fails
with the package named. Adding a *file* comment directly above a `package` clause
in a package that already has a doc will silently make one of the two the package
doc, chosen by file order — the generator makes that visible instead of leaving it
to `go doc` to surprise someone. The map is now 25 lines instead of ~40, which is
what paid for the two nested `AGENTS.md` files added by D203 without breaching the
size budget.
