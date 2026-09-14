---
topic: skills-services-secrets
---

# D26 — Bundled skills embedded in the binary

**Decision.** Recovery and operator skills are embedded through `internal/skillbundle`
using `//go:embed all:bundled`; `skill_list` and `skill_install` can therefore
serve a known bundle without relying on files beside the executable.

**Rationale.** The skills needed to bootstrap or recover a KB must remain available in
single-binary installations and after an incomplete client provisioning cycle.
