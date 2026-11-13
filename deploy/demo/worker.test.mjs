// node --test deploy/demo/worker.test.mjs (make demo-worker-test). The Worker's
// decisions are pure functions, so no Cloudflare runtime is needed (D800).
import assert from "node:assert/strict";
import { test } from "node:test";
import worker, { cacheKey, corsHeaders, route, upstreamPath } from "./worker.js";

const call = (name, kb, extra = {}) => ({
  jsonrpc: "2.0",
  id: 7,
  method: "tools/call",
  params: { name, arguments: { kb, ...extra } },
});

// --- routing, CORS, cache ------------------------------------------------------

test("the routing table", () => {
  assert.equal(route("POST", "/mcp"), "mcp");
  assert.equal(route("GET", "/mcp"), "mcp");
  assert.equal(route("GET", "/.well-known/oauth-protected-resource"), "wellknown");
  assert.equal(route("GET", "/api/ui/v1/kbs"), "api");
  assert.equal(route("GET", "/health"), "api");
  assert.equal(route("OPTIONS", "/api/ui/v1/kbs/astronomy/graph"), "api-preflight");
  assert.equal(route("POST", "/api/ui/v1/kbs"), "other");
  assert.equal(route("GET", "/ui/"), "other");
  assert.equal(route("POST", "/mcp/routed"), "mcp");
  assert.equal(upstreamPath("mcp", "/mcp/routed"), "/mcp/routed");
  assert.equal(upstreamPath("mcp", "/mcp"), "/mcp/routed");
  assert.equal(upstreamPath("api", "/api/ui/v1/kbs"), "/api/ui/v1/kbs");
  assert.equal(upstreamPath("other", "/kbs/x"), "/kbs/x");
});

test("CORS names the Atlas origin and varies on Origin", () => {
  const h = corsHeaders("https://atlas.example.com");
  assert.equal(h["Access-Control-Allow-Origin"], "https://atlas.example.com");
  assert.equal(h.Vary, "Origin");
  assert.equal(h["Access-Control-Allow-Methods"], "GET, OPTIONS");
});

test("the cache key is the URL alone", () => {
  const a = cacheKey(new URL("https://demo.example.com/api/ui/v1/kbs/x/search?q=a&limit=5"));
  assert.equal(a, "https://demo.example.com/api/ui/v1/kbs/x/search?q=a&limit=5");
  assert.equal(cacheKey(new URL("https://demo.example.com/health#frag")), "https://demo.example.com/health");
});

// --- the handler, end to end with a fake upstream -------------------------------

function harness() {
  const seen = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (req, init) => {
    const r = req instanceof Request ? req : new Request(req, init);
    seen.push({
      url: r.url,
      auth: r.headers.get("Authorization"),
      author: r.headers.get("Cartographer-Author"),
      body: r.method === "POST" ? await r.text() : null,
    });
    return new Response('{"jsonrpc":"2.0","id":7,"result":{"content":[]}}', {
      headers: { "content-type": "application/json" },
    });
  };
  const env = {
    UPSTREAM: "https://svc-abc.a.run.app",
    ATLAS_ORIGIN: "https://atlas.example.com",
    DEMO_TOKEN: "read-token",
  };
  const ctx = { waitUntil() {} };
  const send = (path, init = {}) => worker.fetch(new Request(`https://demo.example.com${path}`, init), env, ctx);
  return { seen, send, restore: () => (globalThis.fetch = realFetch) };
}

const post = (obj) => ({
  method: "POST",
  headers: { "content-type": "application/json", Authorization: "Bearer visitor-supplied" },
  body: JSON.stringify(obj),
});

test("handler: every request goes to the routed path with the read token, the client's replaced", async () => {
  const h = harness();
  try {
    for (const msg of [
      call("search", "cartographer-docs", { query: "workspace bind" }),
      { jsonrpc: "2.0", id: 1, method: "tools/list" },
      [call("search", "astronomy", { query: "x" })],
    ]) {
      await h.send("/mcp", post(msg));
    }
    await h.send("/mcp", { method: "GET", headers: { Authorization: "Bearer visitor-supplied" } });
    await h.send("/ui/", { method: "GET" });
    await h.send("/mcp", { method: "POST", body: "not json" });
    assert.equal(h.seen.length, 6);
    for (const s of h.seen) assert.equal(s.auth, "Bearer read-token");
    assert.equal(h.seen[0].url, "https://svc-abc.a.run.app/mcp/routed");
    assert.equal(h.seen[0].body, JSON.stringify(call("search", "cartographer-docs", { query: "workspace bind" })));
  } finally {
    h.restore();
  }
});

test("handler: a write is forwarded untouched with the read token: the server refuses it", async () => {
  const h = harness();
  try {
    const write = call("concept_write", "cartographer-docs", { path: "data/x.md", content: "x" });
    const forged = post(write);
    forged.headers["Cartographer-Author"] = "Someone Else <someone@example.com>";
    await h.send("/mcp", forged);
    await h.send("/mcp", post(call("concept_new", "astronomy", { template: "message", vars: {} })));
    assert.equal(h.seen.length, 2);
    for (const s of h.seen) assert.equal(s.auth, "Bearer read-token");
    assert.equal(h.seen[0].body, JSON.stringify(write));
    assert.equal(h.seen[0].url, "https://svc-abc.a.run.app/mcp/routed");
  } finally {
    h.restore();
  }
});

test("handler: the Worker names no author and an oversized body is refused", async () => {
  const h = harness();
  try {
    await h.send("/mcp", { method: "POST", headers: { "Cartographer-Author": "x <x@example.com>" }, body: "{}" });
    assert.equal(h.seen[0].author, "x <x@example.com>", "the Worker neither sets nor strips it: the server decides");
    const big = await h.send("/mcp", { method: "POST", body: JSON.stringify({ pad: "x".repeat(17 * 1024) }) });
    assert.equal(big.status, 413);
    const bigRouted = await h.send("/mcp/routed", { method: "POST", body: JSON.stringify({ pad: "x".repeat(17 * 1024) }) });
    assert.equal(bigRouted.status, 413, "every path under /mcp/ is capped too");
    assert.equal(h.seen.length, 1);
  } finally {
    h.restore();
  }
});

test("handler: /.well-known is 404 and an API preflight is 204 with CORS", async () => {
  const h = harness();
  try {
    assert.equal((await h.send("/.well-known/oauth-protected-resource")).status, 404);
    const pre = await h.send("/api/ui/v1/kbs", { method: "OPTIONS" });
    assert.equal(pre.status, 204);
    assert.equal(pre.headers.get("Access-Control-Allow-Origin"), "https://atlas.example.com");
    assert.equal(pre.headers.get("Vary"), "Origin");
    assert.equal(h.seen.length, 0);
  } finally {
    h.restore();
  }
});
