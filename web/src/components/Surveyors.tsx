import { Fragment, useEffect, useRef, useState } from "react";
import { ApiError, fetchAllSources, fetchComponents, fetchFeeds, fetchMaintenanceSummary } from "../api/client";
import type { ComponentRow, ComponentsResponse, ConceptChange, MaintenanceSummary, ReplicaRow } from "../api/types";
import { touchedByRun } from "../lib/activity";
import { expectedSilent } from "../lib/health";
import { age, duration, exactTime, words } from "../lib/time";
import { PageSection } from "./Page";

export { age, duration };

/** A surveyor that stopped beating leaves the page within this, whatever the live counter does. */
const REFRESH_MS = 60_000;

/**
 * The components response for the KB (D1901), read-only. Optional by design: a
 * failure or a 404 (a principal that does not see the whole KB, an older
 * server) is `null` once `settled`, and the page renders as it did before.
 */
export function useComponentsResponse(
  kb: string | null,
  live: number,
  enabled = true,
): { data: ComponentsResponse | null; settled: boolean } {
  const [data, setData] = useState<ComponentsResponse | null>(null);
  const [settled, setSettled] = useState(false);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!enabled || !kb) return;
    // Never poll a hidden tab (docs/deployment.md): the beat is skipped, and the
    // page refreshes once when it becomes visible again.
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") setTick((n) => n + 1);
    }, REFRESH_MS);
    const onVisible = () => {
      if (document.visibilityState === "visible") setTick((n) => n + 1);
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [enabled, kb]);
  // Another KB shows nothing of the previous one while its answer is pending.
  useEffect(() => {
    setData(null);
    setSettled(false);
  }, [kb]);
  useEffect(() => {
    if (!enabled || !kb) {
      setData(null);
      return;
    }
    const controller = new AbortController();
    fetchComponents(kb, controller.signal)
      .then((res) => {
        setData(res && Array.isArray(res.components) ? res : null);
        setSettled(true);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setData(null);
        setSettled(true);
      });
    return () => controller.abort();
  }, [kb, live, tick, enabled]);
  return { data, settled };
}

/** The components alone: an empty list until the answer lands, or when it cannot. */
export function useComponents(kb: string | null, live: number, enabled = true): ComponentRow[] {
  const { data } = useComponentsResponse(kb, live, enabled);
  return data?.components ?? [];
}

/** Last and next doctor and who repairs; null until it answers, or when it cannot (a narrowed principal). */
export function useMaintenanceSummary(kb: string | null, live: number): MaintenanceSummary | null {
  const [summary, setSummary] = useState<MaintenanceSummary | null>(null);
  useEffect(() => {
    setSummary(null);
  }, [kb]);
  useEffect(() => {
    if (!kb) return;
    const controller = new AbortController();
    fetchMaintenanceSummary(kb, controller.signal)
      .then(setSummary)
      .catch((err: unknown) => {
        if (!(err instanceof DOMException && err.name === "AbortError")) setSummary(null);
      });
    return () => controller.abort();
  }, [kb, live]);
  return summary;
}

/** A live replica with the perimeter (the component's name) it belongs to. */
type SurveyorRow = ReplicaRow & { perimeter: string };

/** Every live Surveyor replica of the components, in the order the server sends them. */
function liveSurveyors(components: ComponentRow[]): SurveyorRow[] {
  return components.filter((c) => c.kind === "surveyor").flatMap((c) => c.replicas.map((r) => ({ ...r, perimeter: c.name })));
}

/**
 * Health's lines (amends D948): which Surveyor maintains the KB, who repairs
 * it and when the doctor last ran; and, apart, a notice when an expected
 * Surveyor is silent. Upkeep, not findings: none of it is in the lint report.
 * Nothing renders without a live or an expected Surveyor, or for a caller who
 * cannot see the whole KB.
 */
export function MaintainedBy({
  components,
  summary,
  part,
}: {
  components: ComponentRow[];
  summary: MaintenanceSummary | null;
  /** Health shows the line under its header and the notices (an expected Surveyor that is silent or short) beside the list. */
  part: "line" | "notices";
}) {
  const surveyors = liveSurveyors(components);
  const repair = summary?.maintenance?.repair;
  const missing = expectedSilent(components);
  const fallback = repair?.state === "fallback";
  if (surveyors.length === 0 && missing.length === 0) return null;
  // Every perimeter that has a live surveyor on the KB is named, in the order
  // the server sends them (sorted by surveyor): choosing one would be a guess.
  const perimeters = new Map<string, SurveyorRow[]>();
  for (const w of surveyors) perimeters.set(w.perimeter, [...(perimeters.get(w.perimeter) ?? []), w]);
  return (
    <>
      {surveyors.length > 0 && part !== "notices" && (
        <p className="health__lede">
          Maintained by{" "}
          {[...perimeters].map(([name, ws], i) => {
            const busy = ws.some((w) => w.state === "running") ? "running" : words(ws[0]!.state);
            return (
              <Fragment key={name}>
                {i > 0 && ", "}
                <strong>{name}</strong> ({ws.length} {ws.length === 1 ? "replica" : "replicas"}, {busy})
              </Fragment>
            );
          })}
          {repair ? ` · repairs by ${repair.state === "surveyor" && repair.by ? repair.by : "Meridian"}` : ""}
          {summary?.last_doctor ? ` · last doctor ${summary.last_doctor}` : ""}
          {summary?.next_doctor ? ` · next due ${summary.next_doctor}` : ""}
        </p>
      )}
      {part !== "line" && missing.map((c) => (
        <p key={c.name} className="health__lede health__notice" role="status">
          {c.state === "silent"
            ? `Surveyor ${c.name} is expected and has not reported${c.last_seen_at ? `; last seen ${age(c.last_seen_at)}` : ""}.`
            : `Surveyor ${c.name} is expected to run ${c.min_replicas} replicas and ${c.replicas.length} ${c.replicas.length === 1 ? "is" : "are"} reporting.`}
          {fallback && c.state === "silent" && (!repair?.by || repair.by === c.name) && " Meridian is repairing this KB meanwhile."}
        </p>
      ))}
    </>
  );
}

/**
 * Settings, Surveyor tab: the KB's Sources by ingest status and the feeds whose
 * last poll failed. They are the KB's, not the Surveyor's, so the block shows
 * whether or not a Surveyor has reported. Absent with no KB, on a failed read
 * (a 404 for a narrowed principal, an older server) or while it loads. The
 * server caps the list at 500: a count at the cap says "500 or more".
 */
export function SourcesBlock({ kb, live = 0 }: { kb: string | null; live?: number }) {
  const sources = useResilient(kb, live, (k, signal) => fetchAllSources(k, signal).then((r) => (Array.isArray(r?.sources) ? r.sources : [])));
  const feeds = useResilient(kb, live, (k, signal) => fetchFeeds(k, signal).then((r) => (Array.isArray(r?.feeds) ? r.feeds : []))) ?? [];
  if (!kb || sources === null) return null;
  const capped = sources.length >= SOURCE_CAP;
  // A partial list proves nothing about a status it does not show: "0+" would say more than is known.
  const n = (status: string) => {
    const c = sources.filter((s) => s.ingest_status === status).length;
    return capped ? (c === 0 ? "—" : `${c}+`) : String(c);
  };
  return (
    <PageSection title="Sources" id="settings-sources" count={capped ? undefined : sources.length}>
      <p className="page-note">
        The Sources registered in {kb}, by ingest status, and the feeds whose last poll failed
        {capped ? `; the server lists at most ${SOURCE_CAP}, so these counts are lower bounds` : ""}.
      </p>
      <ul className="source-tiles" aria-label="Sources by status">
        {SOURCE_STATUSES.map(({ status, about }) => (
          <li key={status} className="source-tile" data-status={status} data-zero={n(status) === "0" || undefined}>
            <span className="source-tile__label">{status}</span>
            <strong className="source-tile__count">{n(status)}</strong>
            <span className="source-tile__about">{about}</span>
          </li>
        ))}
      </ul>
      {feeds.length > 0 && (
        <ul className="surveyors__list" aria-label="Sources whose last poll failed">
          {feeds.map((f) => (
            <li key={f.id} className="surveyors__row" data-tone="error">
              <span className="surveyors__name">{f.title || f.id}</span>
              {(f.recipe || f.source_shape) && <span className="surveyors__meta">{f.recipe || f.source_shape}</span>}
              <span className="surveyors__error">{f.feed_error}</span>
            </li>
          ))}
        </ul>
      )}
    </PageSection>
  );
}

/** The ingest statuses of a Source, in the order work goes through them. */
const SOURCE_STATUSES = [
  { status: "pending", about: "registered, not merged yet" },
  { status: "ingested", about: "merged into the KB" },
  { status: "skipped", about: "read and left out" },
];

/** The server's cap on a Source list. */
const SOURCE_CAP = 500;

/** Back-off of a read that failed for a reason that may pass (a 5xx, the network): this, doubled, up to a minute. */
export const retry = { baseMs: 5000, maxMs: 60_000 };

/**
 * A KB-scoped optional read. Another KB shows nothing of the previous one. The
 * failure `hides` names (by default any 404: a narrowed principal, an older
 * server) is "nothing"; any other failure keeps the last good value and tries
 * again with back-off, so a restart or a blip never leaves the block empty for
 * good.
 */
export function useResilient<T>(
  kb: string | null,
  live: number,
  load: (kb: string, signal: AbortSignal) => Promise<T>,
  /** The failure that means "nothing to show" (default: any 404); every other one is ridden out. */
  hides: (err: ApiError) => boolean = (err) => err.status === 404,
): T | null {
  const [value, setValue] = useState<T | null>(null);
  const [attempt, setAttempt] = useState(0);
  const fails = useRef(0);
  const loader = useRef(load);
  loader.current = load;
  useEffect(() => {
    setValue(null);
    fails.current = 0;
  }, [kb]);
  useEffect(() => {
    if (!kb) return;
    const controller = new AbortController();
    let timer: number | undefined;
    loader
      .current(kb, controller.signal)
      .then((v) => {
        fails.current = 0;
        setValue(v);
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted || (err instanceof DOMException && err.name === "AbortError")) return;
        if (err instanceof ApiError && hides(err)) {
          setValue(null);
          return;
        }
        const delay = Math.min(retry.baseMs * 2 ** fails.current, retry.maxMs);
        fails.current += 1;
        timer = window.setTimeout(() => setAttempt((n) => n + 1), delay);
      });
    return () => {
      controller.abort();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [kb, live, attempt]);
  return value;
}

/**
 * The Surveyor tab's runs: the runs the Surveyors of the components reported
 * for this KB since `from` (ms; 0 for all), newest first by the server's clock
 * (`received_at`), one row each with its detail and the concepts of the
 * Surveyor's own it covers. Nothing renders without a run.
 */
export function SurveyorRunsList({
  kb,
  components,
  from = 0,
  changes = [],
  titles,
  onOpen,
}: {
  kb: string;
  components: ComponentRow[];
  from?: number;
  changes?: ConceptChange[];
  titles?: ReadonlyMap<string, string>;
  onOpen?(conceptId: string): void;
}) {
  const runs = surveyorRuns(kb, components, from);
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());
  const [allDays, setAllDays] = useState(false);
  if (runs.length === 0) return <p className="page-note">No Surveyor run in this window.</p>;
  const today = dayKey(new Date());
  const days: { key: string; date: Date; runs: typeof runs }[] = [];
  for (const r of runs) {
    const date = new Date(r.started_at);
    const key = dayKey(date);
    const last = days[days.length - 1];
    const day = last?.key === key ? last : days.find((d) => d.key === key);
    if (day) day.runs.push(r);
    else days.push({ key, date, runs: [r] });
  }
  const toggle = (key: string) => setOpen((s) => new Set(s.has(key) ? [...s].filter((k) => k !== key) : [...s, key]));
  const row = (r: (typeof runs)[number]) => (
    <li key={`${r.perimeter}/${r.run_id}`} className="surveyors__row" data-tone={r.outcome === "ok" ? undefined : "error"}>
      <span className="surveyors__name">
        {r.perimeter} · {r.replica}
      </span>
      <span>{r.task}</span>
      <span className="surveyors__meta" title={exactTime(r.started_at)}>
        started {age(r.started_at)}
      </span>
      <span className="surveyors__meta">{duration(r.duration_ms)}</span>
      <span className="surveyors__meta">{r.outcome}</span>
      <span className="surveyors__meta">{r.tokens_spent ?? 0} tokens</span>
      {r.error && <span className="surveyors__error">{r.error}</span>}
      {touchedByRun(r, changes).map((c) => (
        <button key={c.id} type="button" className="surveyors__link" onClick={() => onOpen?.(c.id)} disabled={!onOpen || c.change === "deleted"}>
          {c.added || c.change === "added" ? "new: " : ""}
          {titles?.get(c.id) ?? c.id}
        </button>
      ))}
    </li>
  );
  const shown = allDays ? days : days.slice(0, DAY_GROUPS);
  return (
    <>
      <ul className="surveyors__list" aria-label="Surveyor runs">
        {shown.map((d) => {
          const expanded = d.key === today ? !open.has(d.key) : open.has(d.key);
          const bad = d.runs.filter(isBad);
          const tokens = d.runs.reduce((n, r) => n + (r.tokens_spent ?? 0), 0);
          const label = runDate(d.date);
          return (
            <li key={d.key} className="surveyors__day">
              <button type="button" className="surveyors__daybtn" aria-expanded={expanded} onClick={() => toggle(d.key)}>
                {label} · {d.runs.length} {d.runs.length === 1 ? "run" : "runs"} · {d.runs.length - bad.length} ok
                {bad.length > 0 && ` · ${bad.length} failed`} ·{" "}
                <span title={`${tokens} tokens`}>{shortTokens(tokens)} tokens</span>
              </button>
              {(expanded || bad.length > 0) && (
                <ul className="surveyors__list" aria-label={`Runs of ${label}`}>
                  {(expanded ? d.runs : bad).map(row)}
                </ul>
              )}
            </li>
          );
        })}
      </ul>
      {!allDays && days.length > DAY_GROUPS && (
        <button type="button" className="surveyors__link" onClick={() => setAllDays(true)}>
          Show more
        </button>
      )}
    </>
  );
}

const DAY_GROUPS = 7;
const isBad = (r: { outcome: string }) => r.outcome === "failed" || r.outcome === "budget_exhausted";
const dayKey = (d: Date) => `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;

/** "Nov 12", with the year when it is not this one. */
export function runDate(d: Date): string {
  return d.toLocaleDateString("en", { month: "short", day: "numeric", ...(d.getFullYear() === new Date().getFullYear() ? {} : { year: "numeric" }) });
}

/** 48k, 1.2M: the exact figure goes in a title. */
export function shortTokens(n: number): string {
  const f = (v: number, u: string) => `${Number(v.toFixed(v < 10 ? 1 : 0))}${u}`;
  if (n >= 1_000_000) return f(n / 1_000_000, "M");
  if (n >= 1000) return f(n / 1000, "k");
  return String(n);
}

/** The runs the Surveyors reported for the KB, started since `from`, newest first. */
export function surveyorRuns(kb: string, components: ComponentRow[], from = 0) {
  return components
    .filter((c) => c.kind === "surveyor")
    .flatMap((c) => c.runs.filter((r) => r.kb === kb).map((r) => ({ ...r, perimeter: c.name })))
    .filter((r) => !(Date.parse(r.started_at) < from))
    .sort((a, b) => Date.parse(b.received_at) - Date.parse(a.received_at));
}
