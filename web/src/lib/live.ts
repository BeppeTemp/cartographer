import { useEffect, useState } from "react";
import { ApiError, fetchRevision } from "../api/client";
import { LIVE_POLL_MS, currentSource, pollIntervalMs } from "../api/static";

/** How often an open tab asks whether the KB moved. The answer is a stat walk
 *  on the server and a few bytes on the wire, so ten seconds stays cheap. A
 *  live session on the public static build (D800) polls every 20 s instead:
 *  see pollIntervalMs. */
export const LIVE_INTERVAL_MS = LIVE_POLL_MS;

/**
 * Polls the KB's revision (D337) while the tab is visible and returns a
 * counter that bumps each time it changes: data effects list it among their
 * dependencies and refetch in place. A hidden tab does not poll, and comes
 * back with an immediate check. A 404 is a principal that cannot see the
 * whole KB: there is no revision for it, so polling stops for this KB.
 */
export function useLiveRevision(kb: string | null, enabled: boolean): number {
  const [tick, setTick] = useState(0);

  useEffect(() => {
    if (!kb || !enabled) return;
    let last: string | null = null;
    let stopped = false;
    let controller: AbortController | null = null;

    const check = async () => {
      if (stopped || document.visibilityState !== "visible") return;
      controller?.abort();
      controller = new AbortController();
      try {
        const { revision } = await fetchRevision(kb, controller.signal);
        if (last !== null && revision !== last) setTick((t) => t + 1);
        last = revision;
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) stopped = true;
        // Anything else (offline, a restart) is retried on the next beat;
        // the data effects own error reporting.
      }
    };
    const onVisible = () => {
      if (document.visibilityState === "visible") void check();
    };

    void check();
    const timer = window.setInterval(() => void check(), pollIntervalMs(currentSource()));
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      stopped = true;
      controller?.abort();
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [kb, enabled]);

  return tick;
}
