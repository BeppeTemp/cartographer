// The edge of the live MCP demo (D751, D789, D800): it forwards to the Cloud Run
// service, caches the Atlas API and bounds what a public hostname accepts. It
// grants nothing: the demo is read-only (the guestbook it once moderated is
// gone, D2053).
//
// Cloud Run routes by Host, so a proxied CNAME would reach it with the wrong
// one: the Worker rewrites the host and passes method, path, query and body
// through, and returns the response untouched, streaming included (MCP
// Streamable HTTP).
//
// The hostname is authless (D789): the Worker sets the read token
// (kb:astronomy:r;kb:cartographer-docs:r) on every request, replacing whatever
// the client sent, so a client that cannot send a header (a claude.ai or
// ChatGPT connector, a phone) connects with the URL alone. The Worker keeps no
// list of tools: a write reaches the server with the read token and is refused
// by the server's scope check, so a new write tool fails closed.
//
// /.well-known/ answers 404: the server's RFC 9728 metadata names the *.run.app
// URL as authorization server, where none runs, and a client that finds it
// starts an OAuth flow that cannot finish.
//
// Bindings, all set by Terraform: UPSTREAM, ATLAS_ORIGIN (plain_text);
// DEMO_TOKEN (secret_text).
//
// The decisions are pure exported functions so that worker.test.mjs can run
// them in Node without Cloudflare; the default export only wires them to I/O.

export const MAX_BODY_BYTES = 16 * 1024;
export const API_CACHE_SECONDS = 5;

// --- routing -----------------------------------------------------------------

/** What a request is, from its method and path alone. */
export function route(method, pathname) {
  if (pathname.startsWith("/.well-known/")) return "wellknown";
  if (pathname === "/mcp" || pathname.startsWith("/mcp/")) return "mcp";
  const isAPI = pathname.startsWith("/api/ui/v1/") || pathname === "/health";
  if (isAPI && method === "OPTIONS") return "api-preflight";
  if (isAPI && method === "GET") return "api";
  return "other";
}

/** The path the public /mcp is served from upstream (docs/transport-auth.md
 *  §One routed topology): bare /mcp on a multi-KB server asks for a kb. Any
 *  other path under /mcp/ keeps its own name but goes through the same capped
 *  handling. */
export function upstreamPath(kind, pathname) {
  return kind === "mcp" && (pathname === "/mcp" || pathname === "/mcp/") ? "/mcp/routed" : pathname;
}

/** The edge cache key: the URL alone. Every visitor shares the read token, so
 *  every answer is the same for all of them. */
export function cacheKey(url) {
  return `${url.origin}${url.pathname}${url.search}`;
}

export function corsHeaders(atlasOrigin) {
  return {
    "Access-Control-Allow-Origin": atlasOrigin,
    "Access-Control-Allow-Methods": "GET, OPTIONS",
    "Access-Control-Allow-Headers": "Accept",
    "Access-Control-Max-Age": "86400",
    Vary: "Origin",
  };
}

// --- the handler ----------------------------------------------------------------

function tooLarge() {
  return new Response("Request too large", { status: 413 });
}

function withBearer(request, url, token, body) {
  const init = body === undefined ? request : { method: request.method, headers: request.headers, body };
  const upstream = new Request(url, init);
  upstream.headers.set("Authorization", `Bearer ${token}`);
  return upstream;
}

async function handleMCP(request, url, env) {
  if (request.method !== "POST") return fetch(withBearer(request, url, env.DEMO_TOKEN));

  // A public surface keeps explicit, cheap bounds even when the server has its own.
  const declared = Number(request.headers.get("content-length") ?? 0);
  if (declared > MAX_BODY_BYTES) return tooLarge();
  const text = await request.text();
  if (new TextEncoder().encode(text).length > MAX_BODY_BYTES) return tooLarge();
  return fetch(withBearer(request, url, env.DEMO_TOKEN, text));
}

async function handleAPI(request, url, env, ctx) {
  const cors = corsHeaders(env.ATLAS_ORIGIN);
  const cache = caches.default;
  // Keyed on the public URL, not the rewritten *.run.app one: the Cache API
  // stores per zone, and the demo hostname is the zone this Worker serves.
  const key = new Request(cacheKey(new URL(request.url)));
  const hit = await cache.match(key);
  if (hit) return hit;
  const upstream = withBearer(request, url, env.DEMO_TOKEN);
  const fetched = await fetch(upstream);
  const response = new Response(fetched.body, fetched);
  for (const [name, value] of Object.entries(cors)) response.headers.set(name, value);
  if (fetched.ok) {
    response.headers.set("Cache-Control", `public, max-age=${API_CACHE_SECONDS}`);
    ctx.waitUntil(cache.put(key, response.clone()));
  }
  return response;
}

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    const kind = route(request.method, url.pathname);
    if (kind === "wellknown") return new Response("Not Found", { status: 404 });
    if (kind === "api-preflight") return new Response(null, { status: 204, headers: corsHeaders(env.ATLAS_ORIGIN) });

    url.hostname = new URL(env.UPSTREAM).hostname;
    url.protocol = "https:";
    url.port = "";
    url.pathname = upstreamPath(kind, url.pathname);

    if (kind === "mcp") return handleMCP(request, url, env);
    if (kind === "api") return handleAPI(request, url, env, ctx);
    return fetch(withBearer(request, url, env.DEMO_TOKEN));
  },
};
