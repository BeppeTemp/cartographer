import type { SearchHit, SearchResponse } from "./types";

/**
 * The static demo build (D745): `VITE_ATLAS_STATIC=1 vite build` makes a
 * bundle with no server behind it. Every /api/ui/v1 answer it needs was
 * exported to a JSON file by demo/export.mjs, and the API client reads those
 * files instead. In the binary's own build the flag is unset, so this code is
 * never reached.
 */
export const STATIC_MODE = import.meta.env.VITE_ATLAS_STATIC === "1";

/**
 * The live source of the static build (D800): `VITE_ATLAS_LIVE_ORIGIN` names
 * a running demo (the Worker's origin). Unset, which is every build but the
 * Pages one, the static build behaves exactly as D745 describes. Set, the page
 * asks the origin once, before its first API read, and uses it for the whole
 * session if it answers.
 */
export const LIVE_ORIGIN: string = (import.meta.env.VITE_ATLAS_LIVE_ORIGIN ?? "").replace(/\/+$/, "");

/** How long the probe waits: enough for a Cloud Run cold start. */
export const PROBE_TIMEOUT_MS = 6000;

/** "live": the origin answered; "snapshot": the exported files (no origin
 *  configured, or it did not answer). */
export type Source = "live" | "snapshot";

/** Asks `origin` for its KB list. Any failure (network, timeout, a non-2xx
 *  status, a body that is not the KB list) is "snapshot". */
export async function probeSource(
  origin: string,
  fetchFn: typeof fetch = fetch,
  timeoutMs = PROBE_TIMEOUT_MS,
): Promise<Source> {
  if (!origin) return "snapshot";
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetchFn(`${origin}/api/ui/v1/kbs`, {
      headers: { Accept: "application/json" },
      signal: controller.signal,
    });
    if (!response.ok) return "snapshot";
    const body = (await response.json()) as { kbs?: unknown };
    return Array.isArray(body.kbs) ? "live" : "snapshot";
  } catch {
    return "snapshot";
  } finally {
    clearTimeout(timer);
  }
}

let decided: Promise<Source> | null = null;
let settled: Source | null = null;

/** The session's source, decided once per page load. No mid-session switching:
 *  a later failure of the live origin is an ordinary offline error. */
export function resolveSource(): Promise<Source> {
  decided ??= probeSource(LIVE_ORIGIN).then((source) => (settled = source));
  return decided;
}

/** The source once it is decided, else null. */
export function currentSource(): Source | null {
  return settled;
}

/** The URL of an API read (or of /health) on the live origin. */
export function liveURL(path: string): string {
  return `${LIVE_ORIGIN}/api/ui/v1${path}`;
}

export function liveHealthURL(): string {
  return `${LIVE_ORIGIN}/health`;
}

/** Revision poll period (D337). A live session on the static build polls every
 *  20 s instead of 10 s: each poll is a Worker invocation, and the free plan
 *  allows 100k a day. A server's own Atlas keeps 10 s. */
export const LIVE_POLL_MS = 10_000;
export const LIVE_ORIGIN_POLL_MS = 20_000;

export function pollIntervalMs(source: Source | null, liveOrigin: string = LIVE_ORIGIN): number {
  return source === "live" && liveOrigin ? LIVE_ORIGIN_POLL_MS : LIVE_POLL_MS;
}

/** The file name an API path (with its query) is exported under. It must stay
 *  identical to staticKey in demo/export.mjs: the two sides never talk. The
 *  mapping is injective: encodeURIComponent leaves `~` alone, so it is encoded
 *  first and then free to stand for `%`, which a static host would otherwise
 *  decode on the way in. */
export function staticKey(path: string): string {
  return encodeURIComponent(path).replace(/~/g, "%7E").replace(/%/g, "~");
}

/** The URL of an exported file, next to the bundle's index.html. */
export function staticURL(name: string): string {
  return new URL(`api/${name}.json`, new URL(import.meta.env.BASE_URL, window.location.href)).pathname;
}

/** One concept as the export writes it into search-index.json. */
export interface IndexedConcept {
  id: string;
  title: string;
  body: string;
}

let index: Promise<IndexedConcept[]> | null = null;

function loadIndex(): Promise<IndexedConcept[]> {
  index ??= fetch(staticURL("search-index")).then((r) => {
    if (!r.ok) throw new Error(`search index: status ${r.status}`);
    return r.json() as Promise<IndexedConcept[]>;
  });
  index.catch(() => {
    index = null;
  });
  return index;
}

const fold = (s: string) =>
  s
    .normalize("NFD")
    .replace(/\p{M}/gu, "")
    .toLowerCase();

/**
 * Answers a search over the exported index, in the browser. It is not the
 * server's ranking (no glossary expansion, no archived fallback): every query
 * word must appear in the title, id or body, and a title or id match ranks
 * above a body one. Good enough to find a page in a demo of a hundred.
 */
export function searchIndex(concepts: IndexedConcept[], query: string, limit: number): SearchResponse {
  const words = fold(query).split(/\s+/).filter(Boolean);
  const results: SearchHit[] = [];
  if (words.length > 0) {
    for (const c of concepts) {
      const head = fold(`${c.title} ${c.id}`);
      const body = fold(c.body);
      let score = 0;
      let missing = false;
      for (const w of words) {
        if (head.includes(w)) score += 3;
        else if (body.includes(w)) score += 1;
        else missing = true;
      }
      if (missing) continue;
      results.push({ id: c.id, title: c.title, score, snippet: snippet(c.body, words) });
    }
  }
  results.sort((a, b) => b.score - a.score || a.id.localeCompare(b.id));
  const top = results.slice(0, limit);
  return { query, count: top.length, results: top };
}

/** About 160 characters of body around the first word found, on one line. */
function snippet(body: string, words: string[]): string | undefined {
  const flat = body.replace(/\s+/g, " ").trim();
  const folded = fold(flat);
  const at = words.map((w) => folded.indexOf(w)).find((i) => i >= 0);
  if (at === undefined) return undefined;
  const start = Math.max(0, at - 60);
  const text = flat.slice(start, start + 160);
  return `${start > 0 ? "…" : ""}${text}${start + 160 < flat.length ? "…" : ""}`;
}

/** The local answer to a search path, or null for any other path. */
export async function staticSearch(path: string): Promise<SearchResponse | null> {
  const search = /^\/kbs\/[^/]+\/search\?(.*)$/.exec(path);
  if (!search) return null;
  const params = new URLSearchParams(search[1]);
  return searchIndex(await loadIndex(), params.get("q") ?? "", Number(params.get("limit") ?? "20"));
}

/**
 * Whether a search can include archived pages (D820). The exported index keeps
 * no status, so a snapshot cannot tell them apart; a live origin can.
 */
export function canSearchArchived(): boolean {
  return !STATIC_MODE || currentSource() === "live";
}
