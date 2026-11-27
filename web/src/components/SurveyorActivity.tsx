import { useMemo, type CSSProperties, type ReactNode } from "react";
import { Avatar } from "./Page";
import type { ComponentRow, ConceptChange } from "../api/types";
import { touchedByRun } from "../lib/activity";
import { duration, exactTime } from "../lib/time";
import { runDate, shortTokens, surveyorRuns } from "./Surveyors";

type Run = ReturnType<typeof surveyorRuns>[number];

/** 14:52, the local time of day: the day is the group's label. */
const clock = (iso: string) => new Date(iso).toLocaleTimeString("en", { hour: "2-digit", minute: "2-digit" });

const isBad = (r: { outcome: string }) => r.outcome === "failed" || r.outcome === "budget_exhausted";

const dayStart = (t: number) => {
  const d = new Date(t);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
};

function dayLabel(day: number): string {
  const today = dayStart(Date.now());
  if (day === today) return "Today";
  if (day === dayStart(today - 3_600_000)) return "Yesterday";
  return runDate(new Date(day));
}

const TASK_WORD: Record<string, string> = { poll: "Poll", ingest: "Ingest", doctor: "Doctor", repair: "Repair", synthesis: "Synthesis" };

/**
 * The Surveyor's runs, in the same timeline the Changes tab uses: a day
 * heading with its count, then a row per run — its time, a node in the task's
 * hue on the rail, the task with its outcome, duration and tokens, the error
 * or what it wrote, and the replica where Changes shows the authors. Runs carry no page of their own: pages are
 * matched to them by run id, else by author and time (`touchedByRun`). A bar sizes the
 * run's tokens on the window's busiest run, and the pages it wrote are listed
 * under it, open.
 */
export function SurveyorActivity({
  task,
  kb,
  components,
  from,
  changes,
  hue,
  map,
  renderPage,
}: {
  /** Only runs that wrote a page this keeps, and only those pages (the
   *  Where chips filter by Map). */
  map?(change: ConceptChange): boolean;
  /** One page the run wrote, drawn as the Changes tab draws it, its diff
   *  spanning the run (from and to, RFC3339). */
  renderPage(change: ConceptChange, from: string, to: string): ReactNode;
  /** Only this task's runs, when the What chips filter. */
  task?: string | null;
  kb: string;
  components: ComponentRow[];
  /** Start of the window, ms; 0 for all. */
  from: number;
  changes: ConceptChange[];
  /** The task's hue, the same as its chip above. */
  hue(task: string): string | undefined;
}) {
  const days = useMemo(() => {
    const byDay = new Map<number, Run[]>();
    for (const r of surveyorRuns(kb, components, from)) {
      if (task && r.task !== task) continue;
      const day = dayStart(Date.parse(r.started_at));
      byDay.set(day, [...(byDay.get(day) ?? []), r]);
    }
    for (const runs of byDay.values()) runs.sort((a, b) => Date.parse(b.started_at) - Date.parse(a.started_at));
    return [...byDay].sort((a, b) => b[0] - a[0]);
  }, [kb, components, from, task]);

  // A page belongs to one run: the newest whose span and replica claim it
  // (the changes are per concept, so overlapping runs would each list it).
  const pagesOf = useMemo(() => {
    const taken = new Set<string>();
    const out = new Map<Run, ConceptChange[]>();
    for (const [, runs] of days)
      for (const r of runs) {
        const mine = touchedByRun(r, changes).filter((c) => !taken.has(c.id));
        mine.forEach((c) => taken.add(c.id));
        out.set(r, map ? mine.filter(map) : mine);
      }
    return out;
  }, [days, changes, map]);
  const shownDays = map
    ? days.map(([d, runs]) => [d, runs.filter((r) => (pagesOf.get(r) ?? []).length > 0)] as const).filter(([, runs]) => runs.length > 0)
    : days;

  // Token bars share one scale across the window, so runs compare at a glance.
  const peak = Math.max(1, ...shownDays.flatMap(([, runs]) => runs.map((r) => r.tokens_spent ?? 0)));
  if (shownDays.length === 0) return <p className="page-note">No Surveyor run in this window.</p>;
  return (
    <ol className="timeline__days">
      {shownDays.map(([day, runs]) => (
        <li key={day} className="timeline__day">
          <h2 className="timeline__date">
            <span>{dayLabel(day)}</span>
            <span className="timeline__date-count">{runs.length}</span>
          </h2>
          <ol className="timeline__entries">
            {runs.map((r) => (
              <RunEntry
                key={`${r.perimeter}/${r.run_id}`}
                run={r}
                peak={peak}
                pages={pagesOf.get(r) ?? []}
                hue={hue(r.task)}
                renderPage={renderPage}
              />
            ))}
          </ol>
        </li>
      ))}
    </ol>
  );
}

/**
 * One run: a head row in the task's hue with what the agent did — the task,
 * its outcome, how long, the tokens on the window's scale, the replica — and
 * under it the pages it wrote, each the same row the Changes tab shows, whose
 * diff spans the run.
 */
function RunEntry({
  run: r,
  peak,
  pages,
  hue,
  renderPage,
}: {
  run: Run;
  /** The most tokens one run spent in the window: the bars' full width. */
  peak: number;
  pages: ConceptChange[];
  hue?: string;
  renderPage(change: ConceptChange, from: string, to: string): ReactNode;
}) {
  const bad = isBad(r);
  const tk = r.tokens_spent ?? 0;
  // The run's span, a minute of slack either side for the clocks of the
  // Surveyor and the server.
  const start = Date.parse(r.started_at);
  const from = new Date(start - 60_000).toISOString();
  const to = new Date(start + (r.duration_ms ?? 0) + 60_000).toISOString();
  const created = pages.filter((c) => c.added || c.change === "added").length;
  return (
    <li
      className="timeline__entry sv-run"
      data-tone={bad ? "error" : undefined}
      data-empty={pages.length === 0 || undefined}
      style={{ "--map": bad ? "var(--sev-error)" : hue ?? "var(--text-muted)" } as CSSProperties}
    >
      <div className="timeline__item sv-run__head">
        <time className="timeline__time" dateTime={r.started_at} title={exactTime(r.started_at)}>
          {clock(r.started_at)}
        </time>
        <span className="timeline__track" aria-hidden="true">
          <span className="timeline__node" />
        </span>
        <span className="timeline__item-body">
          <span className="timeline__item-head">
            <span className="timeline__title">{TASK_WORD[r.task] ?? r.task} run</span>
            <span className={`pill ${bad ? "pill--error" : r.outcome === "ok" ? "pill--ok" : ""}`}>{r.outcome.replace(/_/g, " ")}</span>
            <span className="sv-run__fact">{duration(r.duration_ms)}</span>
            <span className="sv-run__fact">
              {pages.length === 0 ? "nothing written" : `${pages.length} ${pages.length === 1 ? "page" : "pages"}${created ? `, ${created} new` : ""}`}
            </span>
          </span>
          {r.error && (
            <span className="timeline__reason sv-run__error" title={r.error}>
              {r.error}
            </span>
          )}
        </span>
        <span className="sv-run__tokens" title={`${tk} tokens`}>
          <span className="sv-run__bar">
            <span style={{ width: `${tk === 0 ? 0 : Math.max(2, (tk / peak) * 100)}%` }} />
          </span>
          <span className="sv-run__tk">{tk === 0 ? "—" : shortTokens(tk)}</span>
        </span>
        <span className="timeline__authors" title={`${r.perimeter} · ${r.replica}`}>
          <Avatar name={r.replica} size="small" />
        </span>
      </div>
      {pages.length > 0 && <ol className="sv-run__pages">{pages.map((c) => renderPage(c, from, to))}</ol>}
    </li>
  );
}
