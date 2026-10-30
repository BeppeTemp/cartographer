import { useEffect, useState } from "react";
import { fetchFeeds, fetchMaintenanceSummary, fetchWorkers } from "../api/client";
import type { FeedRow, MaintenanceSummary, WorkerRow } from "../api/types";

/** A worker that stopped beating leaves the page within this, whatever the live counter does. */
const REFRESH_MS = 60_000;

/**
 * The live workers that declared the KB (design D22), read-only. Optional by
 * design: a failure or a 404 (a principal that does not see the whole KB, an
 * older server) is an empty list, and the page renders as it did before.
 */
function useWorkers(kb: string, live: number): WorkerRow[] {
  const [rows, setRows] = useState<WorkerRow[]>([]);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const timer = window.setInterval(() => setTick((n) => n + 1), REFRESH_MS);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    fetchWorkers(kb, controller.signal)
      .then((res) => setRows(Array.isArray(res?.workers) ? res.workers : []))
      .catch((err: unknown) => {
        if (!(err instanceof DOMException && err.name === "AbortError")) setRows([]);
      });
    return () => controller.abort();
  }, [kb, live, tick]);
  return rows;
}

function useFeeds(kb: string, live: number): FeedRow[] {
  const [rows, setRows] = useState<FeedRow[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    fetchFeeds(kb, controller.signal)
      .then((res) => setRows(Array.isArray(res?.feeds) ? res.feeds : []))
      .catch((err: unknown) => {
        if (!(err instanceof DOMException && err.name === "AbortError")) setRows([]);
      });
    return () => controller.abort();
  }, [kb, live]);
  return rows;
}

const day = (iso?: string) => (iso ? iso.slice(0, 10) : "");

function age(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  if (Number.isNaN(s)) return "";
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 129600) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} d ago`;
}

function WorkerItem({ w }: { w: WorkerRow }) {
  return (
    <li className="workers__row">
      <span className="workers__name">
        {w.perimeter} · {w.worker}
      </span>
      <span className="workers__state" data-state={w.state}>
        {w.state.replace("_", " ")}
      </span>
      {w.last_run && (
        <span className="workers__meta">
          last run {age(w.last_run.at)}: {w.last_run.units_done} units, {w.last_run.commits} commits
        </span>
      )}
      {w.budgets.map((b) => (
        <span key={b.profile} className="workers__budget" data-spent={b.limit > 0 && b.spent >= b.limit ? "all" : undefined}>
          {b.profile}
          {b.tier ? ` (${b.tier})` : ""}
          <progress max={b.limit || 1} value={Math.min(b.spent, b.limit || 1)} aria-label={`${b.profile} budget`} />
          <span>{`${b.spent} / ${b.limit}`}</span>
        </span>
      ))}
      <span className="workers__meta">seen {age(w.last_beat_at)}</span>
      {w.next_window_at && <span className="workers__meta">next window {day(w.next_window_at)}</span>}
    </li>
  );
}

/**
 * Work's strip: the live workers with their last run and budgets, then the
 * feeds and systems whose last poll failed. Nothing renders when both are empty.
 */
export function WorkersStrip({ kb, live, onOpen }: { kb: string; live: number; onOpen(conceptId: string): void }) {
  const workers = useWorkers(kb, live);
  const feeds = useFeeds(kb, live);
  if (workers.length === 0 && feeds.length === 0) return null;
  return (
    <section className="workers" aria-label="Workers and feeds">
      {workers.length > 0 && (
        <ul className="workers__list" aria-label="Workers">
          {workers.map((w) => (
            <WorkerItem key={w.worker} w={w} />
          ))}
        </ul>
      )}
      {feeds.length > 0 && (
        <ul className="workers__list" aria-label="Feeds in error">
          {feeds.map((f) => (
            <li key={f.id} className="workers__row" data-tone="error">
              <button type="button" className="workers__link" onClick={() => onOpen(f.id)}>
                {f.title || f.id}
              </button>
              {f.source_shape && <span className="workers__meta">{f.source_shape}</span>}
              <span className="workers__error">{f.feed_error}</span>
              {f.feed_checked_at && <span className="workers__meta">checked {day(f.feed_checked_at)}</span>}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/**
 * Health's one line (amends D948): which worker maintains the KB and when its
 * doctor last ran. Upkeep, not a finding; nothing renders without a live
 * worker that names the KB, or for a caller who cannot see the whole KB.
 */
export function MaintainedBy({ kb, live }: { kb: string; live: number }) {
  const workers = useWorkers(kb, live);
  const [summary, setSummary] = useState<MaintenanceSummary | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    fetchMaintenanceSummary(kb, controller.signal)
      .then(setSummary)
      .catch((err: unknown) => {
        if (!(err instanceof DOMException && err.name === "AbortError")) setSummary(null);
      });
    return () => controller.abort();
  }, [kb, live]);
  const first = workers[0];
  if (!first) return null;
  const same = workers.filter((w) => w.perimeter === first.perimeter);
  const busy = same.some((w) => w.state === "running") ? "running" : first.state.replace("_", " ");
  return (
    <p className="health__lede health__maintainer">
      Maintained by <strong>{first.perimeter}</strong> ({same.length} {same.length === 1 ? "replica" : "replicas"},{" "}
      {busy})
      {summary?.last_doctor ? ` · last doctor ${summary.last_doctor}` : ""}
      {summary?.next_doctor ? ` · next due ${summary.next_doctor}` : ""}
    </p>
  );
}
