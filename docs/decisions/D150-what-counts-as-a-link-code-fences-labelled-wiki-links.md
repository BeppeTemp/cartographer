---
topic: data-plane
---

# D150 — What counts as a link: code fences, labelled wiki-links, extensionless assets

**Status: implemented (2026-08-28).** Follows [D149](D149-one-link-base-and-one-concept-resolver-for-lint.md). Closes #171.

**Context.** `kb.ExtractLinks` is the single function deciding what counts as a link, for both
the backlink graph and lint's `broken_link`. Three of its rules produced findings no author
could clear.

1. **It matched inside code.** `mdLinkRe` and `wikiLinkRe` ran over the raw body. A Mermaid
   subroutine node — `N1[["a label"]]` inside a ` ```mermaid ` block — yielded the target
   `"a label"`; a POSIX character class — `grep -nE "listen[[:space:]]"` — yielded `:space:`;
   a documented `[label](path)` in an example block was extracted as a real link. In the
   field **every surviving `broken_link` finding had this single root cause**. The corollary
   was worse than the noise: a KB could not document this behaviour without triggering it.
2. **No labelled wiki-link.** The old comment said the alias form was "deliberately not
   matched". That was a defensible economy while wiki-links were merely an alternative; after
   D149 they are the only base-independent form and the one `docs/data-plane.md` recommends
   inside an expanded concept's `index.md`, so choosing them cost the label — a list entry
   `- [Some readable title](target.md): description` became `- [[map/concept]]: description`.
3. **An extensionless href was forced into a ConceptID.** Citing an extensionless *asset* — a
   `Dockerfile`, a `Makefile`, a `LICENSE` — produced a link to a nonexistent concept and left
   the asset `orphan_asset` permanently. Renaming the file to satisfy the linter would have
   been worse than the finding.

**Decision.**

- **Fences are masked, not filtered.** `maskCodeSpans` blanks every byte inside a fenced block
  or inline span, keeping newlines and **total length identical**. Offsets stay valid, which
  matters because `firstDisallowedMachinePath` and `urlRe` already reason about spans over the
  same body; a length-changing transformation would silently break that pattern for any future
  span-based check. Fence rules follow CommonMark's run-length semantics (3+ backticks or
  tildes, closed by the same character at least as long, or by end of body — an unclosed fence
  masks to the end, the safer direction for a malformed document).
- **Indented four-space blocks are NOT code here.** They are indistinguishable from a
  continuation line inside a list, which is how most KB bodies indent, so masking them would
  hide real links. Stated explicitly, with a test, so it is not "fixed" later.
- **`[[id|text]]` is supported**, one `|`, label free-form except `]`, and the label is
  preserved verbatim by `RewriteLinks` so a rename never costs it. `ExtractLinks` still returns
  `[]okf.ConceptID`: no caller needs the text.
- **An extensionless href is an asset only when it resolves to one**, via an optional resolver
  (`kb.AssetExists`) passed by the graph and by lint. Scoped to the resolved path, so a stray
  file elsewhere cannot absorb a shorthand. The parameter is **variadic**, so every existing
  caller and test keeps compiling and keeps today's behaviour: with no resolver, an
  extensionless href stays a ConceptID shorthand. `ExtractAssetLinks` gained the same
  parameter — it was the actual gate for `orphan_asset`, since it skipped extensionless hrefs
  outright.
- **`RewriteLinks` deliberately does not mask.** A fenced example that cites a real concept ID
  stays accurate after a rename, which is what an author wants; the asymmetry with
  `ExtractLinks` is intentional rather than an oversight. `ExtractAssetLinks` likewise keeps
  scanning code spans, so an asset cited only inside an example does not newly become orphan —
  a behaviour change nobody asked for.

**Consequences.** Graph edges previously derived from code samples disappear, so a concept
whose only inbound link came from a fenced example may newly report `orphan`. The alias form is
new syntax, so nothing existing changes shape. `TestExtractLinks_WikiLinkAliasNotExtracted`
asserted the old behaviour and became `TestExtractLinks_WikiLinkAliasIsExtracted`.
