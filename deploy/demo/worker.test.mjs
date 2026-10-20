// node --test deploy/demo/worker.test.mjs (make demo-worker-test). The Worker's
// decisions are pure functions, so no Cloudflare runtime is needed (D800).
import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import { test } from "node:test";
import worker, {
  ATTEMPTS_PER_DAY,
  batchHasGuestbookWrite,
  buildNotification,
  cacheKey,
  checkRate,
  classify,
  corsHeaders,
  isGuestbookWrite,
  newMessageId,
  parseVerdict,
  refusal,
  responseIsError,
  rewriteCall,
  route,
  signWebhook,
  upstreamPath,
  validateMessage,
} from "./worker.js";

const args = (vars, extra = {}) => ({ template: "message", vars, ...extra });
const ok = { client: "Claude Code", message: "A shared wiki would keep my decisions." };

// --- shape rules -------------------------------------------------------------

test("a valid message is accepted and trimmed", () => {
  const r = validateMessage(args({ client: "  Claude Code ", message: "  hello\nworld  " }));
  assert.deepEqual(r, { ok: true, client: "Claude Code", message: "hello\nworld" });
});

test("the template must be message", () => {
  assert.equal(validateMessage({ template: "runbook", vars: ok }).ok, false);
  assert.equal(validateMessage({ vars: ok }).ok, false);
});

test("vars hold exactly client and message, both strings", () => {
  assert.equal(validateMessage(args({ client: "a" })).ok, false);
  assert.equal(validateMessage(args({ ...ok, title: "x" })).ok, false);
  assert.equal(validateMessage(args({ client: "a", message: 1 })).ok, false);
  assert.equal(validateMessage(args(null)).ok, false);
  assert.equal(validateMessage(args([])).ok, false);
});

test("client: 1 to 60 characters, one line", () => {
  assert.equal(validateMessage(args({ ...ok, client: "   " })).ok, false);
  assert.equal(validateMessage(args({ ...ok, client: "a".repeat(60) })).ok, true);
  assert.equal(validateMessage(args({ ...ok, client: "a".repeat(61) })).ok, false);
  assert.equal(validateMessage(args({ ...ok, client: "a\nb" })).ok, false);
});

test("message: 1 to 500 characters, at most 6 lines", () => {
  assert.equal(validateMessage(args({ ...ok, message: "" })).ok, false);
  assert.equal(validateMessage(args({ ...ok, message: "a".repeat(500) })).ok, true);
  assert.equal(validateMessage(args({ ...ok, message: "a".repeat(501) })).ok, false);
  assert.equal(validateMessage(args({ ...ok, message: "1\n2\n3\n4\n5\n6" })).ok, true);
  assert.equal(validateMessage(args({ ...ok, message: "1\n2\n3\n4\n5\n6\n7" })).ok, false);
});

test("links, markup and control characters are refused in both fields", () => {
  for (const bad of ["http://x", "see HTTPS://x", "www.example.com", "[a](b", "[[wiki]]", "a]]", "<b>", "a>b", "a\u0000b", "a\rb", "a\u001bb", "a\u007fb", "a\u0085b"]) {
    assert.equal(validateMessage(args({ ...ok, message: bad })).ok, false, `message ${JSON.stringify(bad)}`);
    assert.equal(validateMessage(args({ ...ok, client: bad })).ok, false, `client ${JSON.stringify(bad)}`);
  }
  const r = validateMessage(args({ ...ok, message: "see www.example.com" }));
  assert.match(r.reason, /www\./);
});

test("quotes, colons and braces are legal: the server escapes them", () => {
  assert.equal(validateMessage(args({ client: 'He said "hi": {{x}}', message: "a: b # c" })).ok, true);
});

// --- which calls are moderated -----------------------------------------------

const call = (name, kb, extra = {}) => ({
  jsonrpc: "2.0",
  id: 7,
  method: "tools/call",
  params: { name, arguments: { kb, ...extra } },
});

test("only concept_new on the guestbook is moderated", () => {
  assert.equal(isGuestbookWrite(call("concept_new", "guestbook")), true);
  assert.equal(isGuestbookWrite(call("concept_new", "astronomy")), false);
  assert.equal(isGuestbookWrite(call("concept_write", "guestbook")), false);
  assert.equal(isGuestbookWrite(call("search", "guestbook")), false);
  assert.equal(isGuestbookWrite({ jsonrpc: "2.0", id: 1, method: "tools/list" }), false);
  assert.equal(isGuestbookWrite(null), false);
  assert.equal(isGuestbookWrite([call("concept_new", "guestbook")]), false);
});

test("a batch is refused only when it holds a guestbook write", () => {
  assert.equal(batchHasGuestbookWrite([call("search", "astronomy"), call("concept_new", "guestbook")]), true);
  assert.equal(batchHasGuestbookWrite([call("search", "astronomy")]), false);
  assert.equal(batchHasGuestbookWrite(call("concept_new", "guestbook")), false);
});

// --- id and rewrite ------------------------------------------------------------

test("the id is messages/<UTC date>-<6 base36>", () => {
  const id = newMessageId(new Date("2026-03-04T23:59:59Z"));
  assert.match(id, /^messages\/2026-03-04-[0-9a-z]{6}$/);
  assert.equal(newMessageId(new Date("2026-03-04T00:00:00Z"), () => 0), "messages/2026-03-04-000000");
  assert.equal(newMessageId(new Date("2026-03-04T00:00:00Z"), () => 0.999999), "messages/2026-03-04-zzzzzz");
});

test("the rewrite assigns the id, drops index_entry and anything unexpected", () => {
  const msg = call("concept_new", "guestbook", {
    id: "../../data/index",
    index_entry: "- [x](y)",
    template: "message",
    vars: { client: " Claude ", message: " hi ", extra: "z" },
    content: "overwrite",
  });
  const checked = validateMessage({ template: "message", vars: { client: " Claude ", message: " hi " } });
  const out = rewriteCall(msg, checked, "messages/2026-03-04-abc123");
  assert.equal(out.id, 7);
  assert.equal(out.params.name, "concept_new");
  assert.deepEqual(out.params.arguments, {
    kb: "guestbook",
    id: "messages/2026-03-04-abc123",
    template: "message",
    vars: { client: "Claude", message: "hi" },
  });
});

// --- refusal -------------------------------------------------------------------

test("a refusal is a JSON-RPC result carrying the request id", () => {
  assert.deepEqual(refusal(7, "no"), {
    jsonrpc: "2.0",
    id: 7,
    result: { isError: true, content: [{ type: "text", text: "no" }] },
  });
  assert.equal(refusal(undefined, "no").id, null);
});

// --- rate limits ---------------------------------------------------------------

function fakeKV() {
  const m = new Map();
  return {
    m,
    puts: 0,
    async get(k) {
      return m.get(k) ?? null;
    },
    async put(k, v, opts) {
      assert.ok(opts.expirationTtl >= 60);
      this.puts++;
      m.set(k, v);
    },
  };
}

test("three attempts per IP per hour, each costing two KV writes", async () => {
  const kv = fakeKV();
  const t = new Date("2026-03-04T10:15:00Z");
  for (let i = 0; i < 3; i++) assert.equal((await checkRate(kv, "203.0.113.1", t)).ok, true);
  assert.equal(kv.puts, 6);
  const fourth = await checkRate(kv, "203.0.113.1", t);
  assert.equal(fourth.ok, false);
  assert.match(fourth.reason, /address/);
  assert.equal(kv.puts, 6, "a refused attempt writes nothing");
  assert.equal((await checkRate(kv, "203.0.113.2", t)).ok, true, "another IP");
  assert.equal((await checkRate(kv, "203.0.113.1", new Date("2026-03-04T11:00:00Z"))).ok, true, "next hour");
});

test("the daily cap is overall and resets at 00:00 UTC", async () => {
  const kv = fakeKV();
  kv.m.set("day:2026-03-04", String(ATTEMPTS_PER_DAY));
  const refused = await checkRate(kv, "203.0.113.1", new Date("2026-03-04T23:59:00Z"));
  assert.equal(refused.ok, false);
  assert.match(refused.reason, /today/);
  assert.equal((await checkRate(kv, "203.0.113.1", new Date("2026-03-05T00:00:00Z"))).ok, true);
});

test("a KV error is a refusal", async () => {
  const broken = {
    async get() {
      throw new Error("kv down");
    },
    async put() {},
  };
  assert.equal((await checkRate(broken, "203.0.113.1", new Date())).ok, false);
  assert.equal((await checkRate(undefined, "203.0.113.1", new Date())).ok, false);
});

// --- classifier ----------------------------------------------------------------

const ai = (result) => ({ run: async () => result });

test("parseVerdict reads the text and the object forms", () => {
  assert.deepEqual(parseVerdict({ response: "safe" }), { safe: true, categories: [] });
  assert.deepEqual(parseVerdict({ response: "\n\nunsafe\nS1, S10" }), { safe: false, categories: ["S1", "S10"] });
  assert.deepEqual(parseVerdict({ response: { safe: false, categories: ["S2"] } }), { safe: false, categories: ["S2"] });
  assert.throws(() => parseVerdict({ response: "maybe" }));
  assert.throws(() => parseVerdict({}));
});

test("the classifier passes safe, names the category of unsafe, and fails closed", async () => {
  assert.equal((await classify(ai({ response: "safe" }), "c", "m")).ok, true);
  const unsafe = await classify(ai({ response: "unsafe\nS10" }), "c", "m");
  assert.equal(unsafe.ok, false);
  assert.match(unsafe.reason, /S10/);
  const err = await classify({ run: async () => { throw new Error("quota"); } }, "c", "m");
  assert.deepEqual(err, { ok: false, reason: "Moderation is unavailable, try again later." });
  const slow = await classify({ run: () => new Promise(() => {}) }, "c", "m", 20);
  assert.equal(slow.ok, false);
  assert.match(slow.reason, /unavailable/);
  assert.equal((await classify(ai({ response: "???" }), "c", "m")).ok, false);
  assert.equal((await classify(undefined, "c", "m")).ok, false);
});

// --- routing, CORS, cache ------------------------------------------------------

test("the routing table", () => {
  assert.equal(route("POST", "/mcp"), "mcp");
  assert.equal(route("GET", "/mcp"), "mcp");
  assert.equal(route("GET", "/.well-known/oauth-protected-resource"), "wellknown");
  assert.equal(route("GET", "/api/ui/v1/kbs"), "api");
  assert.equal(route("GET", "/health"), "api");
  assert.equal(route("OPTIONS", "/api/ui/v1/kbs/guestbook/graph"), "api-preflight");
  assert.equal(route("POST", "/api/ui/v1/kbs"), "other");
  assert.equal(route("GET", "/ui/"), "other");
  assert.equal(route("POST", "/mcp/routed"), "other");
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

// --- notification --------------------------------------------------------------

test("the webhook signature matches a vector computed by hand", async () => {
  // Computed outside this code with openssl:
  //   printf '%s' 'msg_p5jXN8AQM9LWM0D4loKWxJek.1614265330.{"test": 2432232314}' |
  //     openssl dgst -sha256 -mac HMAC -macopt hexkey:<hex of the key> -binary | base64
  // The key is the base64 of whsec_ decoded: the 32 ASCII bytes below.
  const secret = "whsec_TWZLUTlyOEdLWXFyVHdqVVBEOElMUFpJbzJMYUxhU3c=";
  assert.equal(Buffer.from(secret.slice(6), "base64").toString(), "MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw");
  assert.equal(
    await signWebhook(secret, "msg_p5jXN8AQM9LWM0D4loKWxJek", "1614265330", '{"test": 2432232314}'),
    "v1,ELhqG0Ku1gwOc1f4jyKdp3SFGFLAOdJ9bvpWLciCakI=",
  );
  // The same value via node:crypto, so a divergence names the broken side.
  const key = Buffer.from("MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw");
  const viaNode = createHmac("sha256", key).update('msg_p5jXN8AQM9LWM0D4loKWxJek.1614265330.{"test": 2432232314}').digest("base64");
  assert.equal(`v1,${viaNode}`, "v1,ELhqG0Ku1gwOc1f4jyKdp3SFGFLAOdJ9bvpWLciCakI=");
});

test("the notification carries the message and signed headers", async () => {
  const secret = `whsec_${Buffer.from("k".repeat(24)).toString("base64")}`;
  const now = new Date("2026-03-04T10:15:00Z");
  const n = await buildNotification(
    { NOTIFY_URL: "https://hooks.example.com/x", NOTIFY_SECRET: secret },
    { id: "messages/2026-03-04-abc123", client: "Claude", message: "hi" },
    now,
    "msg_1",
  );
  assert.equal(n.url, "https://hooks.example.com/x");
  assert.deepEqual(JSON.parse(n.init.body), {
    kb: "guestbook",
    id: "messages/2026-03-04-abc123",
    client: "Claude",
    message: "hi",
    at: "2026-03-04T10:15:00.000Z",
  });
  assert.equal(n.init.headers["webhook-id"], "msg_1");
  assert.equal(n.init.headers["webhook-timestamp"], String(Math.floor(now.getTime() / 1000)));
  assert.equal(
    n.init.headers["webhook-signature"],
    await signWebhook(secret, "msg_1", n.init.headers["webhook-timestamp"], n.init.body),
  );
});

test("responseIsError reads JSON and SSE answers", () => {
  assert.equal(responseIsError('{"jsonrpc":"2.0","id":1,"result":{"content":[]}}'), false);
  assert.equal(responseIsError('{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[]}}'), true);
  assert.equal(responseIsError('{"jsonrpc":"2.0","id":1,"error":{"code":-1}}'), true);
  assert.equal(responseIsError('event: message\ndata: {"jsonrpc":"2.0","id":1,"result":{"content":[]}}\n\n'), false);
  assert.equal(responseIsError('event: message\ndata: {"jsonrpc":"2.0","id":1,"result":{"isError":true}}\n\n'), true);
  assert.equal(responseIsError("not json"), true);
});

// --- the handler, end to end with a fake upstream -------------------------------

function harness(envOverride = {}, answer = '{"jsonrpc":"2.0","id":7,"result":{"content":[]}}') {
  const seen = [];
  const waits = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (req, init) => {
    const r = req instanceof Request ? req : new Request(req, init);
    seen.push({
      url: r.url,
      auth: r.headers.get("Authorization"),
      author: r.headers.get("Cartographer-Author"),
      body: r.method === "POST" ? await r.text() : null,
    });
    return new Response(answer, { headers: { "content-type": "application/json" } });
  };
  const kv = fakeKV();
  const env = {
    UPSTREAM: "https://svc-abc.a.run.app",
    ATLAS_ORIGIN: "https://atlas.example.com",
    DEMO_TOKEN: "read-token",
    GUESTBOOK_TOKEN: "write-token",
    GUESTBOOK_OPEN: "true",
    GUESTBOOK_RL: kv,
    AI: ai({ response: "safe" }),
    ...envOverride,
  };
  const ctx = { waitUntil: (p) => waits.push(p) };
  const send = (path, init = {}) =>
    worker.fetch(new Request(`https://demo.example.com${path}`, init), env, ctx);
  return { seen, waits, env, send, restore: () => (globalThis.fetch = realFetch) };
}

const post = (obj) => ({
  method: "POST",
  headers: { "content-type": "application/json", Authorization: "Bearer visitor-supplied" },
  body: JSON.stringify(obj),
});

test("handler: an approved message is forwarded with the write token and a server id", async () => {
  const h = harness();
  try {
    const res = await h.send("/mcp", post(call("concept_new", "guestbook", { template: "message", vars: ok, id: "x", index_entry: "y" })));
    assert.equal(res.status, 200);
    assert.equal(h.seen.length, 1);
    assert.equal(h.seen[0].url, "https://svc-abc.a.run.app/mcp/routed");
    assert.equal(h.seen[0].auth, "Bearer write-token");
    const sent = JSON.parse(h.seen[0].body);
    assert.match(sent.params.arguments.id, /^messages\/\d{4}-\d{2}-\d{2}-[0-9a-z]{6}$/);
    assert.equal("index_entry" in sent.params.arguments, false);
  } finally {
    h.restore();
  }
});

test("handler: every request is authored as the guestbook, whatever the visitor sends", async () => {
  const h = harness();
  try {
    const forged = (obj) => {
      const init = post(obj);
      init.headers["Cartographer-Author"] = "Someone Else <someone@example.com>";
      return init;
    };
    await h.send("/mcp", forged(call("concept_new", "guestbook", { template: "message", vars: ok })));
    await h.send("/mcp", forged(call("search", "guestbook", { query: "x" })));
    assert.equal(h.seen.length, 2);
    for (const s of h.seen) assert.equal(s.author, "Cartographer guestbook <noreply@example.com>");
  } finally {
    h.restore();
  }
});

test("handler: the kill switch, a bad message and a refusing classifier never reach the server", async () => {
  const cases = [
    [{ GUESTBOOK_OPEN: "false" }, ok, /closed/],
    [{}, { client: "a", message: "see https://x.example" }, /links/],
    [{ AI: ai({ response: "unsafe\nS1" }) }, ok, /S1/],
    [{ AI: undefined }, ok, /unavailable/],
  ];
  for (const [env, vars, pattern] of cases) {
    const h = harness(env);
    try {
      const res = await h.send("/mcp", post(call("concept_new", "guestbook", { template: "message", vars })));
      const body = await res.json();
      assert.equal(body.id, 7);
      assert.equal(body.result.isError, true);
      assert.match(body.result.content[0].text, pattern);
      assert.equal(h.seen.length, 0);
    } finally {
      h.restore();
    }
  }
});

test("handler: every other call goes with the read token", async () => {
  const h = harness();
  try {
    for (const msg of [
      call("concept_write", "guestbook", { path: "data/x.md", content: "x" }),
      call("concept_new", "astronomy", { template: "message", vars: ok }),
      call("search", "guestbook", { query: "x" }),
      { jsonrpc: "2.0", id: 1, method: "tools/list" },
      [call("search", "guestbook", { query: "x" })],
    ]) {
      await h.send("/mcp", post(msg));
    }
    await h.send("/mcp", { method: "GET" });
    await h.send("/ui/", { method: "GET" });
    await h.send("/mcp", { method: "POST", body: "not json" });
    assert.equal(h.seen.length, 8);
    for (const s of h.seen) assert.equal(s.auth, "Bearer read-token");
    assert.equal(h.seen[0].body, JSON.stringify(call("concept_write", "guestbook", { path: "data/x.md", content: "x" })));
  } finally {
    h.restore();
  }
});

test("handler: a batch with a guestbook write and an oversized body are refused", async () => {
  const h = harness();
  try {
    const batch = await h.send("/mcp", post([call("concept_new", "guestbook", { template: "message", vars: ok })]));
    assert.equal((await batch.json()).result.isError, true);
    const big = await h.send("/mcp", { method: "POST", body: JSON.stringify({ pad: "x".repeat(17 * 1024) }) });
    assert.equal((await big.json()).result.isError, true);
    assert.equal(h.seen.length, 0);
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

test("handler: an accepted message notifies, a failed one does not", async () => {
  const secret = `whsec_${Buffer.from("k".repeat(24)).toString("base64")}`;
  const h = harness({ NOTIFY_URL: "https://hooks.example.com/x", NOTIFY_SECRET: secret });
  try {
    await h.send("/mcp", post(call("concept_new", "guestbook", { template: "message", vars: ok })));
    await Promise.all(h.waits);
    assert.equal(h.seen.length, 2);
    assert.equal(h.seen[1].url, "https://hooks.example.com/x");
    assert.equal(JSON.parse(h.seen[1].body).client, "Claude Code");
  } finally {
    h.restore();
  }
});

test("handler: a message the server refused is not notified", async () => {
  const secret = `whsec_${Buffer.from("k".repeat(24)).toString("base64")}`;
  const h = harness(
    { NOTIFY_URL: "https://hooks.example.com/x", NOTIFY_SECRET: secret },
    '{"jsonrpc":"2.0","id":7,"result":{"isError":true,"content":[]}}',
  );
  try {
    await h.send("/mcp", post(call("concept_new", "guestbook", { template: "message", vars: ok })));
    await Promise.all(h.waits);
    assert.equal(h.seen.length, 1);
  } finally {
    h.restore();
  }
});
