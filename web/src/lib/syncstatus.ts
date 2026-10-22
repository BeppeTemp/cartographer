import { useEffect, useRef, useState } from "react";
import { ApiError, fetchStatus } from "../api/client";
import { currentSource, pollIntervalMs } from "../api/static";
import type { KBStatus } from "../api/types";
import { syncState } from "./syncstate";

/** Poll period while the state is settled (ok, no remote, not versioned). */
export const SETTLED_INTERVAL_MS = 60_000;

/**
 * The selected KB's /status for the top bar's sync indicator (plan 1092).
 *
 * The live revision (D337) is keyed on the concept files, and a push that
 * completes, a fetch or a failure changes none of them, so it cannot drive
 * this on its own. The status is refetched when the KB changes, when the live
 * tick moves (a write or a pull probably left a push pending), when the tab
 * becomes visible again, and on a timer: the live poll period while the state
 * is unsettled (attention, problem), a minute otherwise. A hidden tab does not
 * poll. A failed read keeps the last value (the Disconnected status reports
 * the server); a 404, a principal that does not see the whole KB, hides the
 * indicator for this KB.
 */
export function useSyncStatus(
  kb: string | null,
  enabled: boolean,
  live: number,
): { status: KBStatus | null; hidden: boolean } {
  const [status, setStatus] = useState<KBStatus | null>(null);
  const [hidden, setHidden] = useState(false);
  const refresh = useRef<(() => void) | null>(null);
  const lastLive = useRef(live);

  const tone = syncState(status)?.tone;
  const unsettled = tone === "attention" || tone === "problem";

  useEffect(() => {
    setStatus(null);
    setHidden(false);
    if (!kb || !enabled) return;
    let stopped = false;
    let controller: AbortController | null = null;

    const run = async () => {
      if (stopped || document.visibilityState !== "visible") return;
      controller?.abort();
      const mine = new AbortController();
      controller = mine;
      try {
        const next = await fetchStatus(kb, mine.signal);
        if (mine.signal.aborted) return;
        setStatus(next);
        setHidden(false);
      } catch (err) {
        if (mine.signal.aborted) return;
        if (err instanceof ApiError && err.status === 404) {
          stopped = true;
          setStatus(null);
          setHidden(true);
        }
        // Anything else keeps the last value on screen.
      }
    };
    refresh.current = () => void run();
    void run();
    return () => {
      stopped = true;
      controller?.abort();
      refresh.current = null;
    };
  }, [kb, enabled]);

  useEffect(() => {
    if (!kb || !enabled) return;
    const period = unsettled ? pollIntervalMs(currentSource()) : SETTLED_INTERVAL_MS;
    const timer = window.setInterval(() => refresh.current?.(), period);
    const onVisible = () => {
      if (document.visibilityState === "visible") refresh.current?.();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [kb, enabled, unsettled]);

  useEffect(() => {
    if (live === lastLive.current) return;
    lastLive.current = live;
    refresh.current?.();
  }, [live]);

  return { status, hidden };
}
