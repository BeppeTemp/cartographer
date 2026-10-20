// Exports every Atlas API answer the static demo needs (D745) from a running
// `cartographer serve`, one JSON file per request, into <out>/api/.
//
// Usage: node demo/export.mjs <server-url> <kb> <out-dir>
//
// The static build of the Atlas (VITE_ATLAS_STATIC=1) reads these files in
// place of /api/ui/v1. Every request the UI can make is enumerable for one KB
// except search, which takes free text: for it the export writes
// search-index.json and the browser searches that.

import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const [server, kb, out] = process.argv.slice(2);
if (!server || !kb || !out) {
  console.error("usage: node demo/export.mjs <server-url> <kb> <out-dir>");
  process.exit(2);
}

// Must stay identical to staticKey in web/src/api/static.ts (pinned there by
// static.test.ts).
const staticKey = (path) => encodeURIComponent(path).replace(/~/g, "%7E").replace(/%/g, "~");

const dir = join(out, "api");
mkdirSync(dir, { recursive: true });
let written = 0;

function save(name, body) {
  writeFileSync(join(dir, `${name}.json`), JSON.stringify(body));
  written++;
}

async function getJSON(url) {
  const res = await fetch(url, { headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`${url}: status ${res.status}: ${await res.text()}`);
  return res.json();
}

/** Fetches an API path exactly as the UI spells it and saves it under its key. */
async function api(path) {
  const body = await getJSON(`${server}/api/ui/v1${path}`);
  save(staticKey(path), body);
  return body;
}

// Search snippets are plain text: a wiki-link reads as its label, or as the
// last segment of its id in words; a markdown link as its text.
const plain = (body) =>
  body
    .replace(/\[\[([^\]|]+)\|([^\]]+)\]\]/g, "$2")
    .replace(/\[\[([^\]]+)\]\]/g, (_, id) => id.split(/[/#]/).filter(Boolean).pop().replace(/-/g, " "))
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1");

const k = `/kbs/${encodeURIComponent(kb)}`;
const q = encodeURIComponent;

save("health", await getJSON(`${server}/health`));
await api("/kbs");
await api("/settings");
const overview = await api(`${k}/overview`);

// The whole graph, and the graph scoped to each Map the rail offers.
const graph = await api(`${k}/graph`);
for (const c of overview.collections) await api(`${k}/graph?scope=${q(c.name)}`);

// Every concept the graph draws, plus the search index built from them.
const index = [];
for (const node of graph.nodes) {
  const concept = await api(`${k}/concept?id=${q(node.id)}`);
  index.push({ id: concept.id, title: concept.title, body: plain(concept.body) });
}
save("search-index", index);

await api(`${k}/lint`);
// Activity's windows (web/src/components/Activity.tsx, SINCE).
for (const since of ["1d", "7d", "30d", "90d", "365d", "36500d"]) {
  await api(`${k}/changes?since=${q(since)}&limit=500`);
}
// Work (web/src/api/client.ts fetchWork): the static keys include the query string.
await api(`${k}/work?limit=200&fields=contradiction_kind`);
for (const p of ["maintenance/summary", "maintenance/review", "checks", "revision", "births", "status", "secrets", "placeholders", "contradictions", "contradictions?status=all", "maps", "journal", "sources?status=pending&limit=500", "services", "retired"]) {
  await api(`${k}/${p}`);
}
// Per-concept history (assets are only for expanded concepts, so they stay live-only).
for (const node of graph.nodes) await api(`${k}/history?id=${q(node.id)}`);

const artifacts = await api(`${k}/artifacts`);
for (const a of artifacts.artifacts) {
  await api(`${k}/artifact?kind=${q(a.kind)}&name=${q(a.name)}`);
}

console.log(`export: ${written} files, ${graph.nodes.length} concepts -> ${dir}`);
