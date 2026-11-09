import { Fragment, useEffect, useState } from "react";
import { fetchFeeds, fetchMaintenanceSummary, fetchSurveyors } from "../api/client";
import type { FeedRow, MaintenanceSummary, SurveyorRow } from "../api/types";

/** A surveyor that stopped beating leaves the page within this, whatever the live counter does. */
const REFRESH_MS = 60_000;

/**
 * The live surveyors that declared the KB (D1259), read-only. Optional by
 * design: a failure or a 404 (a principal that does not see the whole KB, an
 * older server) is an empty list, and the page renders as it did before.
 */
function useSurveyors(kb: string, live: number): SurveyorRow[] {
  const [rows, setRows] = useState<SurveyorRow[]>([]);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const timer = window.setInterval(() => setTick((n) => n + 1), REFRESH_MS);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    fetchSurveyors(kb, controller.signal)
      .then((res) => setRows(Array.isArray(res?.surveyors) ? res.surveyors : []))
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

function SurveyorItem({ w }: { w: SurveyorRow }) {
  return (
    <li className="surveyors__row">
      <span className="surveyors__name">
        {w.perimeter} · {w.surveyor}
      </span>
      <span className="surveyors__state" data-state={w.state}>
        {w.state.replace("_", " ")}
      </span>
      {w.last_run && (
        <span className="surveyors__meta">
          last run {age(w.last_run.at)}: {w.last_run.units_done} units, {w.last_run.commits} commits
        </span>
      )}
      {w.budgets.map((b) => (
        <span key={b.profile} className="surveyors__budget" data-spent={b.limit > 0 && b.spent >= b.limit ? "all" : undefined}>
          {b.profile}
          {b.tier ? ` (${b.tier})` : ""}
          <progress max={b.limit || 1} value={Math.min(b.spent, b.limit || 1)} aria-label={`${b.profile} budget`} />
          <span>{`${b.spent} / ${b.limit}`}</span>
        </span>
      ))}
      <span className="surveyors__meta">seen {age(w.last_beat_at)}</span>
      {w.next_window_at && <span className="surveyors__meta">next window {day(w.next_window_at)}</span>}
    </li>
  );
}

/**
 * Work's strip: the live surveyors with their last run and budgets, then the
 * feeds and systems whose last poll failed. Nothing renders when both are empty.
 */
export function SurveyorsStrip({ kb, live, onOpen }: { kb: string; live: number; onOpen(conceptId: string): void }) {
  const surveyors = useSurveyors(kb, live);
  const feeds = useFeeds(kb, live);
  if (surveyors.length === 0 && feeds.length === 0) return null;
  return (
    <section className="surveyors" aria-label="Surveyors and feeds">
      {surveyors.length > 0 && (
        <ul className="surveyors__list" aria-label="Surveyors">
          {surveyors.map((w) => (
            <SurveyorItem key={`${w.perimeter}/${w.surveyor}`} w={w} />
          ))}
        </ul>
      )}
      {feeds.length > 0 && (
        <ul className="surveyors__list" aria-label="Feeds in error">
          {feeds.map((f) => (
            <li key={f.id} className="surveyors__row" data-tone="error">
              <button type="button" className="surveyors__link" onClick={() => onOpen(f.id)}>
                {f.title || f.id}
              </button>
              {f.source_shape && <span className="surveyors__meta">{f.source_shape}</span>}
              <span className="surveyors__error">{f.feed_error}</span>
              {f.feed_checked_at && <span className="surveyors__meta">checked {day(f.feed_checked_at)}</span>}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/**
 * Health's one line (amends D948): which surveyor maintains the KB and when its
 * doctor last ran. Upkeep, not a finding; nothing renders without a live
 * surveyor that names the KB, or for a caller who cannot see the whole KB.
 */
export function MaintainedBy({ kb, live }: { kb: string; live: number }) {
  const surveyors = useSurveyors(kb, live);
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
  if (surveyors.length === 0) return null;
  // Every perimeter that has a live surveyor on the KB is named, in the order
  // the server sends them (sorted by surveyor): choosing one would be a guess.
  const perimeters = new Map<string, SurveyorRow[]>();
  for (const w of surveyors) perimeters.set(w.perimeter, [...(perimeters.get(w.perimeter) ?? []), w]);
  return (
    <p className="health__lede health__maintainer">
      Maintained by{" "}
      {[...perimeters].map(([name, ws], i) => {
        const busy = ws.some((w) => w.state === "running") ? "running" : ws[0]!.state.replace("_", " ");
        return (
          <Fragment key={name}>
            {i > 0 && ", "}
            <strong>{name}</strong> ({ws.length} {ws.length === 1 ? "replica" : "replicas"}, {busy})
          </Fragment>
        );
      })}
      {summary?.last_doctor ? ` · last doctor ${summary.last_doctor}` : ""}
      {summary?.next_doctor ? ` · next due ${summary.next_doctor}` : ""}
    </p>
  );
}
