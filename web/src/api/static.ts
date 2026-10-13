import type { SearchHit, SearchResponse } from "./types";

/**
 * The static demo build (D745): `VITE_ATLAS_STATIC=1 vite build` makes a
 * bundle with no server behind it. Every /api/ui/v1 answer it needs was
 * exported to a JSON file by demo/export.mjs, and the API client reads those
 * files instead. In the binary's own build the flag is unset, so this code is
 * never reached.
 */
export const STATIC_MODE = import.meta.env.VITE_ATLAS_STATIC === "1";

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
