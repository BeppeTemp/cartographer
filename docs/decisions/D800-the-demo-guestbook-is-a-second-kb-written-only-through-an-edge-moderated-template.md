---
topic: deployment-release
---

# D800 — The demo guestbook is a second KB, written only through an edge-moderated template

**Decision.** The live MCP demo (D751, D789) mounts a second KB, `guestbook`, beside `astronomy`, behind the same
URL. The only write the demo accepts is a `concept_new` from the `message` template on `guestbook`. The
Cloudflare Worker is the only holder of the write token: it attaches it to that one call after moderation (shape
rules, a per-IP and a daily rate limit, a Llama Guard classifier that fails closed, a kill switch) and assigns the
message id itself. Every other request, write tools included, carries the read-only token and is refused by the
server's own scope check. The guestbook KB lives in a private git remote, cloned by the image's entrypoint and
pushed synchronously after each write. The public Atlas on GitHub Pages reads this live demo and falls back to the
D745 snapshot when it does not answer. No server code changes, and nothing leaves the free tiers.

**Why.** A read-only demo shows what an agent can read, not the point of Cartographer: many agents writing to one
knowledge base through rules they cannot break. The server already enforces the hard part (per-KB scopes,
`concept_new` creating only and with exactly the template's variables), so the edge only adds what a server cannot
know about a public audience: rate, content, a kill switch. Granting the write token per request, rather than
issuing one to visitors, keeps the demo authless (D789), and an approve-list of one call means a write tool added
to the server later fails closed instead of opening a hole. Messages are one git commit each in a private
repository, so removal is a `git revert` there. Keeping the Atlas on Pages, not behind the Worker, means static
assets cost no Worker invocation (100k a day on the free plan); only API calls and revision polls (every 20 s on
that build) do, and an edge cache of 5 s shares one upstream answer between visitors. The cost: a determined
attacker can exhaust the daily Worker quota, which the free plan accepts because the failure is graceful (the Atlas
shows the snapshot, MCP is unavailable until 00:00 UTC), and an open guestbook relies on a third-party classifier.

**Alternatives rejected.**
- A write token given to visitors, or a Worker blocklist of write tools: the first cannot be revoked per visitor,
  the second fails open on every new tool.
- The guestbook inside the astronomy KB or on its branch: astronomy is rebuilt with replayed history on every image
  build, so its commit hashes change and it cannot share a remote.
- Cloning the guestbook through the server (`kbs[].remote`): a clone failure is fatal at startup, so a GitHub or
  token outage would take the astronomy demo down; the entrypoint clones with a timeout and starts without it.
- Serving the Atlas from the Worker: every asset of every page load would count against the free quota.
- A paid plan or an always-on instance: the demo is meant to cost nothing when idle.
- A built-in maintainer notification service: a signed generic webhook (Standard Webhooks) leaves what receives it
  to the operator.

**Consequences.** The Pages Atlas depends on the Worker's CORS answer for its origin and on a cold start under 6 s,
else it shows the snapshot with a notice. The repository behind `GUESTBOOK_REMOTE` must be named `guestbook` (the KB
name comes from the remote's basename). The tools of `/mcp` now carry a required `kb` argument while the guestbook
is mounted, which is the one visible change for existing users; with the guestbook missing, `/mcp` serves astronomy
alone and the tools carry none. Rotating the git token and removing a message are manual
(`deploy/demo/README.md`). Current behaviour: `docs/deployment.md` §The hosted MCP demo.
