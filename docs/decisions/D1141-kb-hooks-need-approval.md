---
topic: sync-provisioning
---

# D1141 — What a KB runs on the machine is installed only after an approval

**Decision.** A hook a KB ships, any KB artifact that ships a file with the execute bit, and a skill or agent whose
frontmatter declares `hooks` or MCP servers (`mcpServers`, `mcp_servers`, `mcp-servers`, at any depth, so also inside a
per-client `providers:` block) are installed only after a local approval bound to the artifact's content hash, the
same contract as an MCP descriptor (D906). A content change needs a new approval. Until then the artifact is reported
as `needs_approval` (by `sync`, `status` and `sync_apply`) and is neither written nor registered. An artifact the
provider cannot receive is `unsupported`, never pending (D50). Bundled artifacts are not subject to it.
`cartographer approve [revoke] <kind> <name> --kb <kb>` extends the MCP command to every kind, shows the files, the
`hook.json` and the content of the executable files, and stores the approval in the same `mcp_approvals` map, under
`<kind>/<name>` for everything but an MCP descriptor (whose key keeps its D115 form).

The frontmatter check parses the block as YAML into a node tree and compares every key after decoding, following
anchors, aliases and merge keys, so quoting, escapes, flow mappings and complex keys all count. It tolerates the
delimiter variants a client may accept (a BOM, trailing whitespace, a `...` close, CR-only line endings, leading blank
lines). When it cannot tell, it asks: a file that opens like frontmatter but never closes, or whose YAML does not
parse, requires approval.

**Why.** Approval is the one point where the person whose machine runs a hook or a script decides that it should. A
hook and a script are at least as powerful as an MCP descriptor, so they share its single mechanism instead of a
second one. A signature cannot stand in for that decision: it proves which server an artifact came from, and the
server signs every artifact it serves, whoever wrote it. A skill or agent with neither an executable file nor such a
frontmatter key runs nothing of its own and keeps installing without a prompt.

**Alternatives rejected.**
- A separate approval store or command for hooks: two places to look for one kind of consent.
- Treating a signature verified against a pinned key as approval: it proves origin, not consent (above).
- Gating only the hook kind and the execute bit: frontmatter `hooks` or `mcpServers` has the client run commands with
  neither.
- A line scan of the frontmatter: YAML has more spellings of one key than a scan can list.
- Deleting what is already installed: the client never removes a user's files on its own.

**Consequences.** A hook installed before this decision is reported pending on its first sync and by `status`, and is
left as it is: its files are not refreshed, its registration is not redone and its lockfile entry stays, until it is
approved or the KB drops it. Revoking an approval does the same: what is installed stays installed, registered and
running, and `revoke` says how to remove it by hand. `chmod` changes the content hash, so adding the execute bit to a
file asks for approval. A future change here must keep: no install of what runs on the machine without an approval for
its exact hash, no signature counted as consent, a frontmatter check that decodes before it compares and asks when it
cannot tell, and no silent deletion of installed files.
