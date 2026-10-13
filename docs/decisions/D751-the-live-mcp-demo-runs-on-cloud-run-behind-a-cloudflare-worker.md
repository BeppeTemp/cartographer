---
topic: deployment-release
---

# D751 — The live MCP demo runs on Cloud Run behind a Cloudflare Worker

**Decision.** A public, read-only MCP endpoint over `demo/astronomy` runs as a stateless Cloud Run service
(KB baked into the image, one instance at most, scale to zero), published at a hostname whose DNS stays on
Cloudflare through a Worker that forwards to the `*.run.app` URL, with one per-IP rate-limit rule. Auth stays
on with a single public token scoped `kb:astronomy:r`. Everything is Terraform in `deploy/demo/`, and every
release redeploys it through Workload Identity Federation.

**Why.** Trying the read tools should need no install. Cloud Run cold-starts in 1-2 s, which an MCP client
tolerates; hosts that sleep 30-60 s are not usable. The token is public by design, so the safety lies in the
scope (only read tools are callable, writes are refused) and in the caps (one instance, low concurrency,
rate limit, 5 EUR budget alert) rather than in secrecy. The image is rebuilt on each release because the
replayed history is relative to the build date.

**Alternatives rejected.** A proxied CNAME to `*.run.app`: Cloud Run routes by Host, so it would see the wrong
one. Cloud Run domain mapping: needs Search Console verification and a non-proxied record. A Google external
load balancer: about 18 USD a month. Both are worse than a free Worker. `auth: off`: it grants admin. A
service-account key in CI: Workload Identity Federation needs none.

**Consequences.** The `*.run.app` URL stays reachable around Cloudflare; closing it needs the paid load
balancer, so the instance cap is the backstop. `/ready` requires the token once auth is on, so probes (Cloud
Run, the post-deploy check, the smoke test) use `/health`, which is public and carries `ready`. The `demo`
job of the release workflow is skipped until the repository variables exist and is not awaited by
`mcp-registry`. A change to the read tools or to `demo/astronomy` reaches the endpoint at the next release.
`demo/replay.sh` is shared with the static Atlas demo (D745) and must stay bash 3.2 compatible with only git
and coreutils.
