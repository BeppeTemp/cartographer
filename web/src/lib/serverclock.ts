/**
 * The server's clock against the browser's: ages are worded from the server's
 * stamps (received_at, last_seen_at), so "now" is the server's `now` plus the
 * time elapsed since the answer arrived, not the browser's own clock. Zero
 * until an answer carries `server.now` (an older server).
 */
let offsetMs = 0;

/** Records the server's `now` from an answer received at this moment. */
export function setServerNow(iso: string | undefined, received = Date.now()): void {
  const t = iso ? Date.parse(iso) : NaN;
  if (!Number.isNaN(t)) offsetMs = t - received;
}

/** The server's current time, in ms. */
export function serverNow(): number {
  return Date.now() + offsetMs;
}
