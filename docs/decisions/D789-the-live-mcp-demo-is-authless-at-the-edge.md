---
topic: deployment-release
---

# D789 — The live MCP demo is authless at the edge

**Decision.** The Cloudflare Worker in front of the live MCP demo (D751) sets the read-only bearer token on
every request it forwards, replacing whatever the client sent, and answers 404 under `/.well-known/`. The
public hostname takes the URL alone; the token becomes a secret held by Terraform (a `secret_text` Worker
binding and the Cloud Run environment) and is no longer published. The server is unchanged: auth stays on
with the one token scoped `kb:astronomy:r`.

**Why.** The clients most people would try first cannot send a custom header: a claude.ai or ChatGPT
connector (web, desktop, phone) takes a URL and either no auth or OAuth. A 401 sent them into OAuth
discovery, and the server's RFC 9728 metadata (D132) names the `*.run.app` URL as authorization server,
where none runs, so the flow could not finish. Since the token was already public, setting it at the edge
gives nothing away: the scope still refuses every write, and the instance cap, the rate limit and the
budget alert are unchanged. Not publishing it also closes the `*.run.app` URL that D751 left reachable
around Cloudflare to anyone without the token.

**Alternatives rejected.** An anonymous scope in the server (`auth.anonymous: kb:astronomy:r`): useful for
public read-only KBs in general, but it adds an unauthenticated path to the core authorization code for the
sake of one demo. `auth: off`: it grants admin. Setting the header only when it is missing: a stale or
mistyped token would still get a 401 and the OAuth detour.

**Consequences.** A client configured with the old public token keeps working, since the Worker overwrites
it. Rotating the token is a `terraform apply` and nothing else. The Worker owns `/.well-known/` on the demo
hostname: if the demo ever needs a well-known path, it is added there. The post-deploy check initializes an
MCP session on the hostname without a token, so a Worker that stops setting it fails the release's `demo`
job.
