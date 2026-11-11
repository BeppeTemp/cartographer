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

**The registry.** One `Registry` is built once from the resolved entries and decides
per call through the breaker and the operator's policy: `fail_closed` turns a
failure into a deny (marked unavailable, so the caller can code it apart from the
hook's own deny), `fail_open` continues with the failure recorded. Choices:

- An unknown hook name is a wiring error and denies as unavailable; it never passes
  silently. A nil registry means no hooks and answers an empty outcome, so the
  call sites need no branch.
- `dry_run` calls the hook and records `dry_run_deny`, but hands back nothing: an
  empty answer and no opinion, with a flag on the outcome. An identity or resolver
  answer must not take effect through a hook that is only being observed.
- A call the caller cancelled is not a failure of the hook: the breaker and the
  counters are untouched and a probe slot is released, so a client cannot open the
  breaker by aborting requests. It still has no answer, so the failure policy
  decides the request (`fail_closed` denies): a client must not be able to turn a
  slow veto into an allow by cancelling.
- A credential file is read at start and can be reread without dropping the one in
  use; a missing or empty file refuses the start.

**Consequences.** `internal/hooks` is a leaf, standard library only, imported by the
points that call it, which must never call it under a KB lock. The server config
that declares hooks and the points that call the registry arrive in later changes,
each extending this record with its own choices.
