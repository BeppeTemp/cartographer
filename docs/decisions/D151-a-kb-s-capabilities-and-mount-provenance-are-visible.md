---
topic: control-plane
---

# D151 — A KB's capabilities and mount provenance are visible from a session

**Status: implemented (2026-08-28).** Closes #172.

**Context.** The server knows its own configuration and never volunteered it. An agent could
enumerate 33 tools and read 600 concepts and still not answer *"what am I allowed to do in this
KB, and what is switched off?"* In the field that was the single largest block of time in a whole
migration: 23 skills, 11 templates, 2 agents and `instructions.md` were written by editing files
in the KB clone and committing, and removing an agent was a `git rm` — because `artifact_write`
appeared not to exist. It existed, behind a per-KB flag defaulting to false.

Four client-visible surfaces could each have revealed it; none did. `tools/list` omits the tool.
`tools/call` fails as unknown — collapsing *not registered*, *hidden by profile* and *absent from
this build* into one message. `kb_status` returned 20 fields and **not one was a capability**:
exhaustive about git, silent about permissions. `doctor` reported missing hooks and unverifiable
managed files, never disabled capabilities. Meanwhile `artifact_read`'s own description named
`artifact_write`/`artifact_delete` unconditionally — so when `tools/list` did not contain them,
the natural inference was that **the docstring was stale**, not that a hidden flag existed. The
signal pointed the wrong way.

Behind several of those gates being off at once sat one invisible cause: `serve.go` mounts every
subdirectory of `data:` it finds, and such a mount has **no `KBSpec`**, so `AllowArtifactWrite`
is false, there is no `tool_prefix`, no `sops_age_key_file` and no
`machine_path_allow_prefixes` — while `kb_status` reported a remote, an unpushed count and
concept totals identical in shape to a configured KB.

**Decision.**

- **One capability surface, and everything else references it.** `kb_status` gains a
  `capabilities` object: each gate's state plus **the configuration key that controls it**,
  because a state without the name of the switch is only half an answer. `/health` serves the
  same map from the same derivation (`KBCapabilitiesFor`), so the MCP tool and the HTTP endpoint
  cannot drift.
- `kb_status` stays **read-only and offline**: every input is in-process state, so its own
  promise still holds. **No path and no secret material appears** — the SOPS key is reported as
  configured or not, never as a path, so the host's layout does not leak into a transcript.
- **An unknown-tool failure for a name this build implements but did not register says which
  setting enables it.** The table lives next to `advancedToolNames`, because the two mechanisms
  are indistinguishable from a client and are not the same thing: an advanced tool is registered
  and callable, merely unlisted; a gated one is not registered at all. Keeping them in one file
  is how that difference stays documented.
- **`artifact_read`'s description names the gate**, so it stops reading as a stale docstring.
- **Mount provenance is reported, not corrected.** A discovered KB keeps working exactly as
  before; startup warns once per discovered KB, `kb_status` says `mount: discovered`, and
  `doctor` raises an info `capability` finding. Auto-promoting it to a synthetic `KBSpec` is
  deliberately out of scope: it would invent configuration the operator never wrote. `Discovered`
  is carried explicitly rather than inferred from an empty `Spec`, because
  `kbs: [{path: ...}]` with no options also has a zero `Spec` and that KB *is* configured — it
  chose the defaults.
- **The default of `allow_artifact_write` does not change.** Writing a provisioning artifact
  injects instructions a client agent will execute: the gate is a security boundary, and D71's
  rationale is about the pair's *visibility in the tools profile*, not about the gate. This entry
  removes the invisibility, not the gate. Flipping the default is a separate decision.
- `doctor` stays **silent when it cannot observe**: an unreachable server, or one that advertises
  no capabilities, produces no finding. A missing signal must not become "all good" — the same
  rule the sync-timer check follows.

**Consequences.** Additive: one new `kb_status` field, one optional `/health` field, new info
findings in `doctor`, one new startup warning, one changed error message. No config migration and
no default changed.
