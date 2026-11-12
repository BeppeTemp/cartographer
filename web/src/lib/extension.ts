// What a plugin view's data becomes on screen (D1938): pure, total functions over any JSON value that give text (drawn as
// React text, never markup) and, for a badge, one of four tones. A URL that arrives as data is never made clickable.

import type { ExtensionView } from "../api/types";

export type CellTone = "ok" | "warning" | "error" | "info" | "neutral";

export interface Cell {
  text: string;
  /** Set only by the `badge` format. */
  tone?: CellTone;
  /** Set only by the `concept` format: a string the Atlas opens as a concept ID. */
  concept?: string;
}

const MAX_CELL = 300;
const TONES: Record<string, CellTone> = { ok: "ok", warn: "warning", error: "error", info: "info" };

function plain(value: unknown): string {
  let text = "";
  try {
    text = value === null || value === undefined ? "" : typeof value === "string" ? value : (JSON.stringify(value) ?? "");
  } catch {
    // An unserialisable value (a BigInt, a cycle) draws as nothing.
  }
  return text.length > MAX_CELL ? `${text.slice(0, MAX_CELL)}…` : text;
}

function relative(ms: number, now: number): string {
  const diff = Math.round((now - ms) / 1000);
  const abs = Math.abs(diff);
  const [size, unit] = abs >= 86400 ? [86400, "d"] : abs >= 3600 ? [3600, "h"] : abs >= 60 ? [60, "m"] : [1, "s"];
  if (abs < 5) return "now";
  return diff >= 0 ? `${Math.floor(abs / size)}${unit} ago` : `in ${Math.floor(abs / size)}${unit}`;
}

/** formatCell draws one value in one of the closed set of formats; an unknown format is `text`. */
export function formatCell(value: unknown, format: string, now: number = Date.now()): Cell {
  const text = plain(value);
  if (format === "number") {
    const n = typeof value === "number" || (typeof value === "string" && value.trim() !== "") ? Number(value) : NaN;
    return { text: Number.isFinite(n) ? n.toLocaleString("en") : text };
  }
  if (format === "date" || format === "relative") {
    const ms = typeof value === "string" || typeof value === "number" ? new Date(value).getTime() : NaN;
    if (!Number.isFinite(ms)) return { text };
    return { text: format === "date" ? new Date(ms).toISOString().slice(0, 10) : relative(ms, now) };
  }
  if (format === "badge") return { text, tone: TONES[text.toLowerCase()] ?? "neutral" };
  if (format === "concept" && typeof value === "string" && value !== "") return { text, concept: value };
  return { text };
}

/** valueAt follows a dotted path (`a.b.c`) through objects; anything else on the way is undefined. */
export function valueAt(root: unknown, path: string | undefined): unknown {
  if (!path) return root;
  let cur: unknown = root;
  for (const key of path.split(".")) {
    if (cur === null || typeof cur !== "object" || Array.isArray(cur) || !Object.prototype.hasOwnProperty.call(cur, key))
      return undefined;
    cur = (cur as Record<string, unknown>)[key];
  }
  return cur;
}

/** viewPath is the request path of a view for the picked KB: `{kb}` filled in, the declared query appended. */
export function viewPath(view: ExtensionView, kb: string | null): string | null {
  const fill = (text: string) => text.replaceAll("{kb}", encodeURIComponent(kb ?? ""));
  if (view.scope === "kb" && !kb) return null;
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(view.source.query ?? {})) query.set(key, value.replaceAll("{kb}", kb ?? ""));
  const qs = query.toString();
  return fill(view.source.route) + (qs ? `?${qs}` : "");
}

export type ViewData = { kind: "rows"; rows: unknown[]; more: number } | { kind: "object"; object: Record<string, unknown> };

/** shape reads the answer as the renderer needs it (rows capped at the limit, or one object); anything else is null. */
export function shape(view: ExtensionView, answer: unknown): ViewData | null {
  const data = valueAt(answer, view.source.items);
  if (view.renderer === "table" || view.renderer === "list") {
    if (!Array.isArray(data)) return null;
    const limit = view.limit > 0 ? view.limit : 100;
    return { kind: "rows", rows: data.slice(0, limit), more: Math.max(0, data.length - limit) };
  }
  return data !== null && typeof data === "object" && !Array.isArray(data) ? { kind: "object", object: data as Record<string, unknown> } : null;
}
