---
topic: control-plane
---

# D1202 — A credential pasted into a concept is refused at write

**Status: implemented.**

**Decision.** `potential_secret` flags a known token format (GitHub, GitLab, Anthropic, OpenAI, AWS access key id, Slack, Google API key, Stripe, age) or a PEM private-key header followed by key material, in a concept's body (code fences included) or frontmatter values. It is `AvoidableAtWrite` (D907), so a write that introduces one is refused, and `lint` reports the existing ones. Nothing accepts or suppresses it, it has no fix and `kb_repair` never touches it, and `origin: external` pages are scanned too (D1195 skips only three other checks). The finding names the pattern kind and the location, never the matched text, a fragment or its length.

**Why.** A token in a page lands in git history and in every client; no check looked at concept text. It is the first check that refuses a write on a content heuristic (token formats) instead of structure, so it is kept to high-confidence patterns with their real length and charset: a false positive refuses a legitimate write and D907 gives no way around it. A placeholder (a repeated character, a run of `x`, `X` or `*`) does not match, and a bare PEM header quoted in documentation is not a finding: the next non-empty line must hold at least 16 base64 characters that are not a placeholder run. The no-echo rule is enforced at the source: the matcher returns only (kind, line), because every output (lint, the write response, the refusal, Atlas Health) is built from `Finding.Message`.

The PEM rule skips blank lines and RFC 1421 header lines (`Proc-Type:`, `DEK-Info:`) after the header and strips quote and list prefixes (`>`, `-`, `*`), so an encrypted legacy key and a quoted key are found; a header with no key material after it is not. Nested frontmatter blocks are scanned as text.

**Alternatives rejected.** A generic `password:`/`token:` assignment or entropy match: too many false positives for a check that refuses writes. A bare PEM header match: it flags every page that explains PEM. Skipping `origin: external`: a real credential inside a verbatim document is still a credential. An acceptance for examples: the fix for an example is a placeholder. Echoing a masked prefix: the plan keeps even the first characters out of every output.

**Consequences.** The write gate keys a finding on `{path, check, message}` and the message carries the line number, so an edit that shifts a pre-existing secret down reads as introduced and is refused; this is wanted (the page holds a secret) and the line must not be dropped from the message, since the value may not appear in it. A real token with four consecutive `x` is not flagged (accepted). Test tokens are assembled at runtime from parts, never literal (push protection, D259). A new token format is a row in `secretPatterns` with a positive and a negative test row.
