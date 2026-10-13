---
topic: deployment-release
---

# D754 — `kb rename` rewrites this machine's client bindings

**Decision.** After moving the KB, `kb rename` replaces the old name with the new
one in every client and workspace binding of this machine's `.cartographer.yaml`
(`clients.<provider>.kbs`, `workspaces.<provider>[].kbs`), keeping each list's
order. It does so only when that client points at a loopback server or none; the
preflight lists every binding it will rewrite. Amends D177, which left them to the
next sync.

**Why.** D177 assumed `sync` would realign every client on its own. A binding
(D169) is an explicit selection, and `sync` refuses a bound name the server no
longer advertises: after a rename every sync aborted, and the hint printed by the
rename was false (#754). A binding is a preference about *which* KB, not a
credential, so renaming it in place keeps what the user chose. The cost is a third
file the command writes, outside the rollback D177 claims: a failed rewrite leaves
the KB renamed and says how to rebind by hand.

**Alternatives rejected.**
- *`sync` maps the old name to the new one*: the client has no rename record to
  read, and a server name that disappeared is the very case the stale-selection
  check exists to catch.
- *Report the bindings, as for auth scopes*: the user then has to unbind and rebind
  every provider by hand for a change whose intent is unambiguous.
- *Rewrite even when the client points at a remote server*: those bindings name
  that server's KBs, which a local rename does not touch.

**Consequences.** Auth scopes, role rules, `signing_keys` and `mcp_approvals` stay
reported, not rewritten (D177). A future field that selects KBs by name on the
client side belongs in `bindingRefs`/`rewriteBindings`.
