---
topic: deployment-release
---

# D1071 — `kb create` replaces a forge's boilerplate README

**Status: implemented.** Amends [D710](D710-kb-create-adopts-a-forge-initial-commit.md) ("the forge's files are kept"). Closes #1071.

**Decision.** When `kb create --remote` adopts a forge's initial commit, a `README.md` that is a recognised forge boilerplate is replaced by a short generated one, as one extra commit on top of the rebased scaffold; any other README is kept. `--readme generate` replaces it whatever it holds, `--readme keep` never replaces it.

**Why.** The scaffold writes no README, so every KB born from a forge-initialised repository shipped the forge's text (on GitLab a long "Getting started" template) and, as the README is not an artifact, `artifact_write` cannot change it. Recognition is strict because the two mistakes do not cost the same: a wrong "keep" costs one manual edit, a wrong "replace" destroys the operator's text.

- **Recognition.** One top-level `README.md` only (any other README file, or two, means keep), compared after dropping a BOM, CRLF and trailing blanks. Boilerplate is exactly `# <repo name>` plus at most one description line (GitHub, Gitea, Forgejo; the description is carried into the generated file), or `# <repo name>` plus GitLab's three fixed markers. The shapes are pinned by the real templates in `internal/gitx/testdata/forge-readmes/`; a forge changing its template falls to "keep", never to a wrong replace.
- **A commit on top, never a rewrite.** The forge's commit stays in the history and the push stays a fast-forward (D710). The commit has the rebased commit's author and committer (D265, D950); a failure before the push discards the local KB like every pre-push refusal.
- **The README does not become an artifact.** `artifact_write` keeps refusing it; the change is CLI-only.

**Alternatives rejected.**

- Making the README writable through MCP: widens the artifact contract for a file only `kb create` ever needs to touch.
- Always replacing, or fuzzy recognition (length, headings): destroys a README an operator wrote before running `kb create`.
- A flag with no default behaviour: leaves the common case, an untouched boilerplate, to a manual edit on every new KB.

**Consequences.** One visible behaviour change: a recognised boilerplate README is replaced by default. `--readme` has no effect on an empty remote and is a usage error with `--no-remote`. A new forge template has to be added with a fixture and a recognition rule here.
