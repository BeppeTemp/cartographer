---
topic: sync-provisioning
---

# D1699 — A client variant is applied only after a hash-bound approval that shows every path it writes

The number is provisional: it is the next unused one, to be renumbered after the PR (D672).

**Decision.** The kind `variant` joins the artifacts that wait for the client's approval (`ArtifactRequiresApproval`): `cartographer approve variant <name> --kb <kb>`, bound to source, name and the descriptor's full content hash, stored in `mcp_approvals` under `variant/<name>`. D1141 is not amended: `artifactAuthorized` is unchanged, and a verified signature under a pinned key is still never consent. The approval is **the** security control for where a variant writes, so its preview (and the `approve --pending` listing) shows the name, base, MCP mode, the detection rules and **every destination, global and project, per kind, after the anchors are resolved on this machine** (the `hook` directory with the shared-dispatcher sentence, the `seed` files with the sentence that servers added through the extension's UI are reset, and each path left out and why), every KB-chosen string through `escapeControl`. The hash covers the descriptor bytes, so an edit that changes only a path is a new hash and `needs_approval` again. `approve --pending` lists a variant with its paths but approves it only one by one, like an MCP descriptor. `approve revoke variant <name>` keeps what is installed and names `cartographer disconnect <name>` to remove it. An unapproved variant is reported `needs_approval` (never `unsupported`) on every sync; `InstalledBeforeApprovals` excludes it, as a variant never existed before D1141.

The same change adds the MCP mode `cli`: `Descriptor.MCPMode` replaces cli-call's `UsesCLI` bool (`UsesCLI(p)` is `MCPMode == "cli"`), a cli provider gets no MCP entry (connect, sync and disconnect skip it without a warning), receives the bundled `cartographer-cli` skill through the existing client-side filter, and its instructions block carries one client-side sentence naming `cartographer call --provider <name>`.

**Why.** The usual flow is a local server that clones the KB from its git URL and serves it unsigned, so a pinned signature buys nothing there, and any token that can write the KB can write a descriptor. What a descriptor controls is the set of directories Cartographer writes into and deletes from, so the person must see those directories, resolved, before saying yes. Reusing the D1141 contract adds no second trust mechanism.

**Alternatives rejected.**
- A pinned-key carve-out (a signed variant installs without asking): the pin does not exist in the git-URL flow, and the server signs whatever a token wrote.
- Showing the descriptor text only: anchors (`{xdg_config}`, `{local_app_data}`) resolve differently per machine, so the text does not say where the files land.
- Approving variants in bulk with `--pending`: the cheap path would hide the one decision that matters.

**Consequences.** `allow_call` (a permission rule merged into the fork's config) is not implemented here: its syntax must first be probed on the base client with the audit probes, and an unverified rule is not shipped. Registration, the cache and `--approve-variants` come in later changes.
