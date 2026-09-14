---
topic: data-plane
---

# D87 — Fence-aware heading detection: shared line iterator for `ListHeadings`/`ExtractSection`/`SectionHashes`

**Status: implemented (2026-07-23).**

**Context.** `ListHeadings`, `ExtractSection` and `SectionHashes` (`internal/okf/okf.go`) each scanned body lines independently, calling `parseHeading` with no code-fence state. A `#` line inside a fenced code block (e.g. a bash comment `# Usage: ...` inside a ```` ```bash ```` fence) was parsed as a real heading, surfacing as a spurious entry in the `concept_read` outline (`internal/mcpserver/tools_read.go`) and potentially splitting sections/hashes mid-fence.

**Decision.** A shared helper (`headingEligibleLines`) computes, once per body, which lines are eligible for heading parsing; all three functions consult it before calling `parseHeading`, so they stay mutually consistent by construction. Fence rules (pragmatic CommonMark subset, not the full spec):

- a line whose trimmed content starts with ` ``` ` or `~~~` opens a fence (an info string after the marker, e.g. `bash`, is ignored);
- the fence closes on the next line whose trimmed content starts with the **same** marker; markers do not cross-close (a ` ``` ` fence is not closed by `~~~`);
- an **unclosed fence extends to the end of the document** — the rest of the body is treated as fenced content, not as a fallback to normal heading parsing. This is a deliberate simplification: a malformed/unclosed fence is far more likely in a document that also has trailing garbage than one that "resumes" cleanly, and erring toward under-detection (fewer headings) is safer for section extraction than guessing where the fence should have closed.

**Ripple.** The D78 size-guard outline counts headings; documents with fenced `#` lines now report a smaller (correct) heading count — the changed count is the fix, not a regression.

**Rationale.** A single shared iterator, rather than three ad-hoc fence-aware loops, is what actually guarantees the outline/section/hash consumers agree — duplicating fence-tracking logic three times would only move the risk of drift from "no fence awareness" to "three subtly different fence-aware implementations."
