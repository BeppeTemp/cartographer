---
topic: control-plane
---

# D1957 — Hooks in the core: one HTTP webhook transport, input or deny

**Decision.** The core calls external hooks over one transport: a versioned HTTP
webhook (`POST` of a JSON envelope, protocol level 1, in the `Cartographer-Hook`,
`Cartographer-Hook-Version` and `Cartographer-Request-Id` headers and the body).
A hook answers `no_opinion` or `deny`; any other value, `allow` included, is read as
no opinion, so a hook can only turn a core allow into a deny, never replace a core
decision. A hook entry is namespaced `<plugin>/<name>` (the unprefixed namespace is
the core's) and carries an explicit `failure_policy` with no default: whether an
unreachable hook opens or closes is the operator's risk call, not ours.

**Why.** Enterprise governance (a change-approval veto, an audit sink, an external
vault, an SSO directory) must be writable as plugins without the core running
third-party code or ceding a decision. A separate process behind HTTP keeps the
plugin out of the server, and "input or deny" keeps the core's own checks final.
The call is bounded by a context deadline (default 2 s, at most 10 s) that covers
connect and body read; TLS is verified, a redirect is never followed, `http://` is
accepted only to a loopback host (the client's rule, D1023), a response body is
capped at 64 KiB, a deny reason is cut to 200 printable characters, and an error
names only a fixed class (never the URL or an address). A circuit breaker (5
consecutive failures open it for 30 s, then one probe) lets the failure policy apply
without calling, so a dead hook costs one timeout, not one per request; a call the
caller cancelled says nothing about the hook and is not counted.

**Alternatives rejected.** Exec or socket transports: more surface, and the server
would spawn what an operator did not list. A default failure policy: either default
is wrong for some operator. Letting a hook answer `allow`: it would let a plugin
widen what the core refused.

**Consequences.** `internal/hooks` is a leaf, standard library only, imported by the
points that call it, which must never call it under a KB lock. The registry that
applies the policy, the server config that declares hooks and the points that call
them arrive in later changes, each extending this record with its own choices.
