import { serverNow } from "./serverclock";

/** A run's length: ms under a second, then seconds, then minutes. */
export function duration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 90_000) return `${Math.round(ms / 1000)} s`;
  return `${Math.round(ms / 60_000)} min`;
}

/**
 * How long ago an instant was, on the server's clock: "just now", "5 min ago".
 * `span` gives the bare length ("5 min", "just now") for a sentence that
 * supplies its own "ago" or "up".
 */
export function age(iso: string, opts: { now?: number; span?: boolean } = {}): string {
  const now = opts.now ?? serverNow();
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  if (Number.isNaN(s)) return "";
  if (s < 90) return "just now";
  const ago = opts.span ? "" : " ago";
  if (s < 5400) return `${Math.round(s / 60)} min${ago}`;
  if (s < 129600) return `${Math.round(s / 3600)} h${ago}`;
  return `${Math.round(s / 86400)} d${ago}`;
}

/** An instant's exact local time, for a title. */
export function exactTime(iso: string): string {
  return new Date(iso).toLocaleString("en", { dateStyle: "medium", timeStyle: "medium" });
}

/** A snake_case code in words: "budget_exhausted" becomes "budget exhausted". */
export const words = (code: string): string => code.replace(/_/g, " ");
