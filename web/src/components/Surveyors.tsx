import { Fragment, useEffect, useRef, useState } from "react";
import { ApiError, fetchAllSources, fetchComponents, fetchFeeds, fetchMaintenanceSummary } from "../api/client";
import type { ComponentRow, FeedRow, MaintenanceSummary, ReplicaRow } from "../api/types";
import { expectedSilent } from "../lib/health";
import { serverNow } from "../lib/serverclock";
import { PageSection } from "./Page";

/** A surveyor that stopped beating leaves the page within this, whatever the live counter does. */
const REFRESH_MS = 60_000;

/**
 * The components that concern the KB (D1901), read-only. Optional by design: a
 * failure or a 404 (a principal that does not see the whole KB, an older
 * server) is an empty list, and the page renders as it did before.
 */
export function useComponents(kb: string | null, live: number, enabled = true): ComponentRow[] {
  const [rows, setRows] = useState<ComponentRow[]>([]);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!enabled || !kb) return;
    const timer = window.setInterval(() => setTick((n) => n + 1), REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [enabled, kb]);
  // Another KB shows nothing of the previous one while its answer is pending.
  useEffect(() => {
    setRows([]);
  }, [kb]);
  useEffect(() => {
    if (!enabled || !kb) {
      setRows([]);
      return;
    }
    const controller = new AbortController();
    fetchComponents(kb, controller.signal)
      .then((res) => setRows(Array.isArray(res?.components) ? res.components : []))
      .catch((err: unknown) => {
        if (!(err instanceof DOMException && err.name === "AbortError")) setRows([]);
      });
    return () => controller.abort();
  }, [kb, live, tick, enabled]);
  return rows;
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

/** One line of the Work strip's alarms: a cause and, when the server knows one, when it heard of it. */
interface SurveyorAlarm {
  key: string;
  cause: string;
  at?: string;
}

/** What a server or reported alarm code says, in words. Unknown codes are shown as their code. */
const ALARM_TEXT: Record<string, string> = {
  silent: "is expected and has not reported",
  degraded: "is reporting fewer replicas than expected",
  protocol_skew: "has a replica on another protocol version",
  feed_failing: "has a feed that fails",
  auth_refused: "was refused by the server",
  model_unreachable: "cannot reach its model",
};

/**
 * The alarms of the Surveyors that concern the KB, from one source: the
 * `alarms` of the components read (the server's own silent, degraded and
 * protocol-skew alarms, and the ones a Surveyor reported), plus a replica whose
 * budget is spent (once per replica; the read carries no alarm for it).
 * Times are the server's (`received_at`, or `since` of a server alarm), never
 * one a Surveyor declared. A reported alarm of another KB is left out (its
 * detail is free text).
 */
export function surveyorAlarms(components: ComponentRow[], kb: string): SurveyorAlarm[] {
  const out: SurveyorAlarm[] = [];
  for (const c of components.filter((c) => c.kind === "surveyor")) {
    for (const [i, a] of c.alarms.entries()) {
      if (a.kb !== undefined && a.kb !== "" && a.kb !== kb) continue;
      const what = ALARM_TEXT[a.code] ?? a.code.replace(/_/g, " ");
      const detail = a.origin === "server" ? [a.source, a.detail] : a.kb ? [a.source, a.detail] : [];
      out.push({
        key: `alarm/${c.name}/${i}/${a.code}`,
        cause: `Surveyor ${c.name} ${what}${detail.filter((t) => t && t.trim()).length ? ` (${detail.filter((t) => t && t.trim()).join(": ")})` : ""}`,
        at: a.received_at ?? (a.origin === "server" ? a.since : undefined),
      });
    }
    for (const r of c.replicas.filter((r) => r.state === "budget_exhausted")) {
      out.push({ key: `budget/${c.name}/${r.id}`, cause: `Surveyor ${c.name} · ${r.id} has spent its budget` });
    }
  }
  return out;
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

export function age(iso: string, now = serverNow()): string {
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
        {w.perimeter} · {w.id}
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
export function SurveyorsStrip({
  kb,
  live,
  components,
  onOpen,
}: {
  kb: string;
  live: number;
  components: ComponentRow[];
  onOpen(conceptId: string): void;
}) {
  const surveyors = liveSurveyors(components);
  const feeds = useFeeds(kb, live);
  const alarms = surveyorAlarms(components, kb);
  if (surveyors.length === 0 && feeds.length === 0 && alarms.length === 0) return null;
  return (
    <section className="surveyors" aria-label="Surveyors and feeds">
      {alarms.length > 0 && (
        <ul className="surveyors__list" aria-label="Surveyor alarms">
          {alarms.map((a) => (
            <li key={a.key} className="surveyors__row" data-tone="error">
              <span className="surveyors__error">{a.cause}</span>
              {a.at && <span className="surveyors__meta">{age(a.at)}</span>}
            </li>
          ))}
        </ul>
      )}
      {surveyors.length > 0 && (
        <ul className="surveyors__list" aria-label="Surveyors">
          {surveyors.map((w) => (
            <SurveyorItem key={`${w.perimeter}/${w.id}`} w={w} />
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
 * Health's lines (amends D948): which Surveyor maintains the KB, who repairs
 * it and when the doctor last ran; and, apart, a notice when an expected
 * Surveyor is silent. Upkeep, not findings: none of it is in the lint report.
 * Nothing renders without a live or an expected Surveyor, or for a caller who
 * cannot see the whole KB.
 */
export function MaintainedBy({
  components,
  summary,
}: {
  components: ComponentRow[];
  summary: MaintenanceSummary | null;
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
      {surveyors.length > 0 && (
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
          {repair ? ` · repairs by ${repair.state === "surveyor" && repair.by ? repair.by : "Meridian"}` : ""}
          {summary?.last_doctor ? ` · last doctor ${summary.last_doctor}` : ""}
          {summary?.next_doctor ? ` · next due ${summary.next_doctor}` : ""}
        </p>
      )}
      {missing.map((c) => (
        <p key={c.name} className="health__lede health__maintainer health__notice" role="status">
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
        {capped ? `; the server lists at most ${SOURCE_CAP}, so these counts are lower bounds` : ""}. A Surveyor&apos;s
        recipes and connectors are not listed: it reports its typed values only.
      </p>
      <ul className="surveyors__list" aria-label="Sources by status">
        {["pending", "ingested", "skipped"].map((status) => (
          <li key={status} className="surveyors__row">
            <span className="surveyors__name">{status}</span>
            <span className="surveyors__meta">{n(status)}</span>
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
 * Activity's "Surveyor runs", only when there are runs: the runs the Surveyors
 * of the components reported for this KB, newest first by the server's clock
 * (`received_at`), and each replica's last run counts. Nothing renders
 * (no header, no empty state) without a run or when the read failed.
 */
export function SurveyorRunsSection({
  kb,
  components,
  authors = [],
  onAuthor,
}: {
  kb: string;
  components: ComponentRow[];
  /** The authors the timeline knows: a Surveyor's changes are `surveyor/...`. */
  authors?: string[];
  onAuthor?(author: string): void;
}) {
  const surveyors = components.filter((c) => c.kind === "surveyor");
  const runs = surveyors
    .flatMap((c) => c.runs.filter((r) => r.kb === kb).map((r) => ({ ...r, perimeter: c.name })))
    .sort((a, b) => Date.parse(b.received_at) - Date.parse(a.received_at))
    .slice(0, 20);
  // A replica's last run is not per KB: the section exists only when this KB has runs of its own,
  // and the replica rows are labelled as any KB's.
  if (runs.length === 0) return null;
  const lasts = surveyors.flatMap((c) => c.replicas.filter((r) => r.last_run).map((r) => ({ perimeter: c.name, replica: r })));
  // The exact path segment: w-1 is not w-10.
  const authorOf = (id: string) => authors.find((a) => a.startsWith("surveyor/") && a.split("/").slice(1).includes(id));
  return (
    <PageSection title="Surveyor runs" id="activity-surveyor-runs" count={runs.length}>
      <ul className="surveyors__list" aria-label="Surveyor runs">
        {runs.map((r) => (
          <li key={`${r.perimeter}/${r.run_id}`} className="surveyors__row" data-tone={r.outcome === "ok" ? undefined : "error"}>
            <span className="surveyors__name">
              {r.perimeter} · {r.replica}
            </span>
            <span>{r.task}</span>
            <span className="surveyors__meta">{age(r.received_at)}</span>
            <span className="surveyors__meta">{r.outcome}</span>
            <span className="surveyors__meta">{r.tokens_spent} tokens</span>
            {r.error && <span className="surveyors__error">{r.error}</span>}
            {onAuthor && authorOf(r.replica) && (
              <button type="button" className="surveyors__link" onClick={() => onAuthor(authorOf(r.replica)!)}>
                Its changes
              </button>
            )}
          </li>
        ))}
        {lasts.map(({ perimeter, replica: r }) => (
          <li key={`last/${perimeter}/${r.id}`} className="surveyors__row">
            <span className="surveyors__name">
              {perimeter} · {r.id}
            </span>
            <span>last run (any KB)</span>
            <span className="surveyors__meta">
              {r.last_run!.units_done} units, {r.last_run!.commits} commits, {r.last_run!.tokens_spent} tokens
            </span>
          </li>
        ))}
      </ul>
    </PageSection>
  );
}
