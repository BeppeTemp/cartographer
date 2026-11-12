---
topic: control-plane
---

# D1977 — A token bound to a plugin has a forced, limited identity

**Decision.** A token with `plugin: <name>` (a plugin pinned and enabled in `plugins`) is kind
`client`, expiring and never admin or proxy, and its principal id is forced to
`plugin:<name>/<id>`. The token store withholds secrets from it; the compiler withholds
artifact writes unless a role rule says `artifacts: rw`. The `plugin:` id prefix is reserved.

**Why.** What a hook-side plugin does when it calls the server back must be labelled in the
audit and in the commit trailer, and the label must not be claimable: forcing the id at
compile time, and refusing it on every other token, makes the principal the proof. Secrets
and artifacts are what a compromised plugin would turn into code execution on every client,
so both are off by default and secrets cannot be given back.

**Alternatives rejected.** A new token kind `plugin`: every place that switches on the kind
would grow a case, and the plugin rules are the `client` rules plus a label. A label field
beside the free id: the audit and the trailer read the id, and a field they ignore would
label nothing. Letting `secrets` be granted by a role: a plugin approved to veto writes has
no business reading secrets.

**Consequences.** Removing a plugin's pin refuses the start while a token still names it.
An operator who wants a plugin to change artifacts writes `artifacts: rw` in the role rule,
visibly. The pin is the approval available today; the approval by digest of plan G is the
same pin.
