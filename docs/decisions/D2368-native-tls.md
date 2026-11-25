---
topic: transport-auth
---

# D2368 — The server serves TLS 1.3 natively

**Decision.** `cartographer serve --http` can be the `https://` endpoint itself
(`tls.enabled`): TLS 1.3 only, HTTP/1.1 only, with the fixed files `tls.crt` and
`tls.key` of `credentials_dir` (D905), reloaded by poll and `SIGHUP` without a
restart. The public listener is TLS-only: no sniffing, no redirect, a plain
request gets Go's own `400`. Probes that must stay in clear are served by a
separate loopback health listener (`tls.health_addr`, default public port + 1,
`off` allowed) that answers `GET`/`HEAD` of `/health` and `/ready` only. HSTS
(`tls.hsts_max_age`, default `4380h`, `0` off) is sent on the TLS listener only,
without `includeSubDomains` or `preload`. The RFC 9728 base is `https://` whenever
the connection is TLS, whatever `X-Forwarded-Proto` says, and the announced Atlas
URL (`kb_status` `ui_url`, `service status`, the tray) takes the scheme from the
setting.

**Why.** A deployment without a reverse proxy (dev, LAN, sidecar, port-forward)
sent bearer tokens and KB content in clear, while the client already refuses a
non-loopback `http://` URL (D1023). A certificate is a file, so it follows the
rule of the other secrets (D905) and the reload of tokens (D1894, D2110): a
failed reload keeps the certificate in force, an expired one is served with a
warning (a crash-looping pod for a dated file is worse, and the clients see the
expiry), and a missing or mismatched pair at start stops the server before any
port is bound, so a server that asked for TLS never starts in clear. Loading the
pair before `net.Listen` is what makes that last rule hold.

**Alternatives rejected.**
- Same-port sniffing (TLS and plain on one port): a clear-text path on the public
  address, which is what the feature removes; loopback cannot share the port
  number with a wildcard bind either, so a second port is needed anyway.
- A `tls.refuse_plain_http` option: with a TLS-only public listener it has no meaning.
- Self-signed generation or ACME: certificate lifecycle is the operator's (a
  Kubernetes `kubernetes.io/tls` secret mounts as is, hence the fixed names).
- Configurable certificate and key paths: one more path to validate for no case found.
- HTTP/2: the transport is request/response JSON-RPC plus one event stream.

**Consequences.** A client limited to TLS 1.2 stops connecting, and anything that
probed the public port in clear must use the health listener. The health
listener must never grow a route. mTLS fits later without moving a key
(`tls.client_auth`, `tls.client_ca`, `auth.tls_identity`), set only in
`newServerTLSConfig`. An announced URL is built only by `webui.URL` and
`webui.LoopbackURL` with the TLS flag. A non-atomic replace of `tls.crt` and
`tls.key` may log one transient refused line, fixed at the next poll. Amends no
earlier decision.
