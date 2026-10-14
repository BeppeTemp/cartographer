// The edge of the live MCP demo (D751, D789, D800): it forwards to the Cloud Run
// service and is the only thing that can grant a write.
//
// Cloud Run routes by Host, so a proxied CNAME would reach it with the wrong
// one: the Worker rewrites the host and passes method, path, query and body
// through, and returns the response untouched, streaming included (MCP
// Streamable HTTP).
//
// The hostname is authless (D789): the Worker sets a bearer token on every
// request, replacing whatever the client sent, so a client that cannot send a
// header (a claude.ai or ChatGPT connector, a phone) connects with the URL
// alone. There are two tokens (D800). DEMO_TOKEN is read-only
// (kb:astronomy:r;kb:guestbook:r) and goes on everything. GUESTBOOK_TOKEN
// (kb:guestbook:rw) goes on exactly one thing: a concept_new on the guestbook
// that passed moderation. The Worker keeps no list of write tools: anything it
// does not approve reaches the server with the read token and is refused by the
// server's scope check, so a new write tool fails closed.
//
// /.well-known/ answers 404: the server's RFC 9728 metadata names the *.run.app
// URL as authorization server, where none runs, and a client that finds it
// starts an OAuth flow that cannot finish.
//
// Bindings, all set by Terraform: UPSTREAM, ATLAS_ORIGIN, GUESTBOOK_OPEN
// (plain_text); DEMO_TOKEN, GUESTBOOK_TOKEN, NOTIFY_URL, NOTIFY_SECRET
// (secret_text); GUESTBOOK_RL (KV namespace); AI (Workers AI).
//
// The decisions are pure exported functions so that worker.test.mjs can run
// them in Node without Cloudflare; the default export only wires them to I/O.

export const MAX_BODY_BYTES = 16 * 1024;
export const MAX_CLIENT_CHARS = 60;
export const MAX_MESSAGE_CHARS = 500;
export const MAX_MESSAGE_LINES = 6;
export const IP_ATTEMPTS_PER_HOUR = 3;
export const ATTEMPTS_PER_DAY = 100;
export const CLASSIFIER_MODEL = "@cf/meta/llama-guard-3-8b";
export const CLASSIFIER_TIMEOUT_MS = 5000;
export const API_CACHE_SECONDS = 5;

const FORBIDDEN_TEXT = ["http://", "https://", "www.", "](", "[[", "]]", "<", ">"];
// Control characters other than \n (C0, DEL, C1).
const CONTROL = /[\u0000-\u0009\u000B-\u001F\u007F-\u009F]/;
const ID_ALPHABET = "0123456789abcdefghijklmnopqrstuvwxyz";

// --- routing -----------------------------------------------------------------

/** What a request is, from its method and path alone. */
export function route(method, pathname) {
  if (pathname.startsWith("/.well-known/")) return "wellknown";
  if (pathname === "/mcp" || pathname === "/mcp/") return "mcp";
  const isAPI = pathname.startsWith("/api/ui/v1/") || pathname === "/health";
  if (isAPI && method === "OPTIONS") return "api-preflight";
  if (isAPI && method === "GET") return "api";
  return "other";
}

/** The path the public /mcp is served from upstream (docs/transport-auth.md
 *  §One routed topology): bare /mcp on a multi-KB server asks for a kb. */
export function upstreamPath(kind, pathname) {
  return kind === "mcp" ? "/mcp/routed" : pathname;
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

// --- moderation: shape ---------------------------------------------------------

/** The guestbook write the Worker moderates: a single JSON-RPC tools/call of
 *  concept_new on kb "guestbook". Anything else is not its business. */
export function isGuestbookWrite(msg) {
  if (msg === null || typeof msg !== "object" || Array.isArray(msg)) return false;
  const args = msg.params?.arguments;
  return (
    msg.method === "tools/call" &&
    msg.params?.name === "concept_new" &&
    args !== null &&
    typeof args === "object" &&
    args.kb === "guestbook"
  );
}

/** Parses a request body into JSON, or undefined when it is not JSON. */
export function parseBody(text) {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

/** A batch is a body the Worker cannot approve call by call: refused when any
 *  element is a guestbook write. A batch of anything else goes through with the
 *  read token. */
export function batchHasGuestbookWrite(parsed) {
  return Array.isArray(parsed) && parsed.some(isGuestbookWrite);
}

function checkText(label, value) {
  if (CONTROL.test(value)) return `${label} has a control character`;
  const lower = value.toLowerCase();
  for (const bad of FORBIDDEN_TEXT) {
    if (lower.includes(bad)) return `${label} may not contain "${bad}" (no links or markup)`;
  }
  return null;
}

/** Checks the arguments of a guestbook concept_new against the message rules.
 *  Returns {ok: true, client, message} (both trimmed) or {ok: false, reason}. */
export function validateMessage(args) {
  if (args.template !== "message") {
    return { ok: false, reason: 'Only the "message" template can be used in the guestbook.' };
  }
  const vars = args.vars;
  if (vars === null || typeof vars !== "object" || Array.isArray(vars)) {
    return { ok: false, reason: "vars must be an object with client and message." };
  }
  const keys = Object.keys(vars).sort();
  if (keys.length !== 2 || keys[0] !== "client" || keys[1] !== "message") {
    return { ok: false, reason: "vars must hold exactly client and message." };
  }
  if (typeof vars.client !== "string" || typeof vars.message !== "string") {
    return { ok: false, reason: "client and message must be strings." };
  }
  const client = vars.client.trim();
  const message = vars.message.trim();
  if (client.length < 1 || client.length > MAX_CLIENT_CHARS) {
    return { ok: false, reason: `client must be 1 to ${MAX_CLIENT_CHARS} characters.` };
  }
  if (client.includes("\n")) return { ok: false, reason: "client must be one line." };
  if (message.length < 1 || message.length > MAX_MESSAGE_CHARS) {
    return { ok: false, reason: `message must be 1 to ${MAX_MESSAGE_CHARS} characters.` };
  }
  if (message.split("\n").length > MAX_MESSAGE_LINES) {
    return { ok: false, reason: `message may have at most ${MAX_MESSAGE_LINES} lines.` };
  }
  const bad = checkText("client", client) ?? checkText("message", message);
  if (bad) return { ok: false, reason: `${bad}.` };
  return { ok: true, client, message };
}

// --- moderation: ids, rewrite ---------------------------------------------------

/** messages/<YYYY-MM-DD>-<6 random base36>, date in UTC. `random` returns a
 *  number in [0, 1), Math.random by default. */
export function newMessageId(now, random = Math.random) {
  let suffix = "";
  for (let i = 0; i < 6; i++) suffix += ID_ALPHABET[Math.floor(random() * ID_ALPHABET.length)];
  return `messages/${now.toISOString().slice(0, 10)}-${suffix}`;
}

/** The JSON-RPC message to forward: a server-assigned id, no index_entry, the
 *  validated template and trimmed vars. Nothing else the visitor sent survives
 *  in the arguments except kb. */
export function rewriteCall(msg, checked, id) {
  return {
    ...msg,
    params: {
      ...msg.params,
      arguments: {
        kb: "guestbook",
        id,
        template: "message",
        vars: { client: checked.client, message: checked.message },
      },
    },
  };
}

// --- moderation: rate limits ----------------------------------------------------

/** Counts one attempt in KV and says whether it may go on. At most
 *  IP_ATTEMPTS_PER_HOUR per IP per hour and ATTEMPTS_PER_DAY overall per UTC
 *  day; both keys expire by themselves, so one attempt is two KV writes (the
 *  free tier allows 1,000 a day). KV is eventually consistent: the limits are
 *  approximate. A KV error is a refusal. */
export async function checkRate(kv, ip, now) {
  const hour = now.toISOString().slice(0, 13);
  const day = now.toISOString().slice(0, 10);
  const ipKey = `ip:${ip}:${hour}`;
  const dayKey = `day:${day}`;
  try {
    const [ipCount, dayCount] = await Promise.all([kv.get(ipKey), kv.get(dayKey)]);
    const ipN = Number(ipCount ?? 0);
    const dayN = Number(dayCount ?? 0);
    if (dayN >= ATTEMPTS_PER_DAY) {
      return { ok: false, reason: "The guestbook has reached its limit for today, try again tomorrow." };
    }
    if (ipN >= IP_ATTEMPTS_PER_HOUR) {
      return { ok: false, reason: "Too many messages from your address, try again in an hour." };
    }
    await Promise.all([
      kv.put(ipKey, String(ipN + 1), { expirationTtl: 2 * 3600 }),
      kv.put(dayKey, String(dayN + 1), { expirationTtl: 2 * 86400 }),
    ]);
    return { ok: true };
  } catch {
    return { ok: false, reason: "The guestbook is unavailable, try again later." };
  }
}

// --- moderation: classifier -----------------------------------------------------

/** Reads a Llama Guard answer: the model returns text ("safe", or "unsafe"
 *  then a line of category codes), or an object with `safe`/`categories` on
 *  newer runtimes. Anything else throws, which the caller treats as a refusal. */
export function parseVerdict(result) {
  const r = result?.response ?? result;
  if (r !== null && typeof r === "object") {
    if (typeof r.safe === "boolean") {
      return { safe: r.safe, categories: Array.isArray(r.categories) ? r.categories.map(String) : [] };
    }
    throw new Error("unrecognised classifier answer");
  }
  if (typeof r !== "string") throw new Error("unrecognised classifier answer");
  const lines = r
    .trim()
    .split(/\s*\n\s*/)
    .filter(Boolean);
  const first = (lines[0] ?? "").toLowerCase();
  if (first === "safe") return { safe: true, categories: [] };
  if (first === "unsafe") return { safe: false, categories: (lines[1] ?? "").split(/[\s,]+/).filter(Boolean) };
  throw new Error("unrecognised classifier answer");
}

/** Runs the classifier on the client and message. Fails closed: an error, a
 *  timeout or a quota refusal is a refusal. `ai.run` is env.AI.run. */
export async function classify(ai, client, message, timeoutMs = CLASSIFIER_TIMEOUT_MS) {
  let timer;
  try {
    const timeout = new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error("timeout")), timeoutMs);
    });
    const run = ai.run(CLASSIFIER_MODEL, {
      messages: [{ role: "user", content: `${client}\n${message}` }],
    });
    const verdict = parseVerdict(await Promise.race([run, timeout]));
    if (verdict.safe) return { ok: true };
    const named = verdict.categories.length ? ` (${verdict.categories.join(", ")})` : "";
    return { ok: false, reason: `The message was flagged by moderation${named}.` };
  } catch {
    return { ok: false, reason: "Moderation is unavailable, try again later." };
  } finally {
    clearTimeout(timer);
  }
}

// --- refusals -------------------------------------------------------------------

/** A refusal is a JSON-RPC result with isError, not an HTTP error: the
 *  visitor's agent reads the reason and relays it. */
export function refusal(id, reason) {
  return {
    jsonrpc: "2.0",
    id: id ?? null,
    result: { isError: true, content: [{ type: "text", text: reason }] },
  };
}

export const CLOSED = "The guestbook is closed right now.";

// --- notification ---------------------------------------------------------------

function b64decode(s) {
  const bin = atob(s);
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

function b64encode(bytes) {
  let bin = "";
  for (const b of new Uint8Array(bytes)) bin += String.fromCharCode(b);
  return btoa(bin);
}

/** The Standard Webhooks signature (https://www.standardwebhooks.com):
 *  "v1," + base64(HMAC-SHA256(key, "<id>.<timestamp>.<body>")), where the key
 *  is the secret base64-decoded after its "whsec_" prefix. */
export async function signWebhook(secret, id, timestamp, body) {
  const raw = secret.startsWith("whsec_") ? secret.slice("whsec_".length) : secret;
  const key = await crypto.subtle.importKey("raw", b64decode(raw), { name: "HMAC", hash: "SHA-256" }, false, [
    "sign",
  ]);
  const mac = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(`${id}.${timestamp}.${body}`));
  return `v1,${b64encode(mac)}`;
}

/** The notification request for an accepted message. */
export async function buildNotification(env, fields, now, webhookId) {
  const body = JSON.stringify({
    kb: "guestbook",
    id: fields.id,
    client: fields.client,
    message: fields.message,
    at: now.toISOString(),
  });
  const timestamp = String(Math.floor(now.getTime() / 1000));
  return {
    url: env.NOTIFY_URL,
    init: {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "webhook-id": webhookId,
        "webhook-timestamp": timestamp,
        "webhook-signature": await signWebhook(env.NOTIFY_SECRET ?? "", webhookId, timestamp, body),
      },
      body,
    },
  };
}

/** Whether an MCP response (JSON, or SSE with `data:` lines) carries an error. */
export function responseIsError(text) {
  const payloads = [];
  const whole = parseBody(text);
  if (whole !== undefined) payloads.push(whole);
  else {
    for (const line of text.split("\n")) {
      if (line.startsWith("data:")) {
        const one = parseBody(line.slice(5).trim());
        if (one !== undefined) payloads.push(one);
      }
    }
  }
  if (payloads.length === 0) return true;
  return payloads.some((p) => p?.error !== undefined || p?.result?.isError === true);
}

// --- the handler ----------------------------------------------------------------

function jsonResponse(obj) {
  return new Response(JSON.stringify(obj), { headers: { "content-type": "application/json" } });
}

function withBearer(request, url, token, body) {
  const init = body === undefined ? request : { method: request.method, headers: request.headers, body };
  const upstream = new Request(url, init);
  upstream.headers.set("Authorization", `Bearer ${token}`);
  return upstream;
}

async function handleMCP(request, url, env, ctx) {
  const target = new URL(url);
  const readOnly = (body) => withBearer(request, target, env.DEMO_TOKEN, body);
  if (request.method !== "POST") return fetch(readOnly());

  const declared = Number(request.headers.get("content-length") ?? 0);
  if (declared > MAX_BODY_BYTES) {
    return jsonResponse(refusal(null, "Request too large."));
  }
  const text = await request.text();
  if (new TextEncoder().encode(text).length > MAX_BODY_BYTES) {
    return jsonResponse(refusal(null, "Request too large."));
  }
  const parsed = parseBody(text);
  if (batchHasGuestbookWrite(parsed)) {
    return jsonResponse(refusal(null, "Send one request at a time."));
  }
  if (!isGuestbookWrite(parsed)) return fetch(readOnly(text));

  const refuse = (reason) => jsonResponse(refusal(parsed.id, reason));
  if (env.GUESTBOOK_OPEN !== "true") return refuse(CLOSED);
  const checked = validateMessage(parsed.params.arguments);
  if (!checked.ok) return refuse(checked.reason);
  const now = new Date();
  const ip = request.headers.get("CF-Connecting-IP") ?? "unknown";
  const rate = await checkRate(env.GUESTBOOK_RL, ip, now);
  if (!rate.ok) return refuse(rate.reason);
  const verdict = await classify(env.AI, checked.client, checked.message);
  if (!verdict.ok) return refuse(verdict.reason);

  const id = newMessageId(now);
  const body = JSON.stringify(rewriteCall(parsed, checked, id));
  const response = await fetch(withBearer(request, target, env.GUESTBOOK_TOKEN, body));
  if (env.NOTIFY_URL) {
    const copy = response.clone();
    ctx.waitUntil(
      (async () => {
        try {
          if (!response.ok || responseIsError(await copy.text())) return;
          const webhookId = `msg_${crypto.randomUUID()}`;
          const note = await buildNotification(env, { id, ...checked }, now, webhookId);
          const sent = await fetch(note.url, note.init);
          if (!sent.ok) console.log(`notify: status ${sent.status}`);
        } catch (err) {
          console.log(`notify failed: ${err}`);
        }
      })(),
    );
  }
  return response;
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

    if (kind === "mcp") return handleMCP(request, url, env, ctx);
    if (kind === "api") return handleAPI(request, url, env, ctx);
    return fetch(withBearer(request, url, env.DEMO_TOKEN));
  },
};
