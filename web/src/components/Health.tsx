import { useMemo, useState } from "react";
import type {
  KBStatus,
  LintFinding,
  LintReport,
  MaintenanceQuestions,
  MaintenanceRepair,
  MaintenanceRun,
  MaintenanceSummary,
} from "../api/types";
import { Count, Figures, Hero, Page, PageHeader, PageSection, Quiet, Segmented, SkeletonRows, relativeDay } from "./Page";
import { SeverityBadge } from "./SeverityBadge";
import { ErrorState } from "./States";

const ORDER = ["error", "warning", "info"] as const;
const HEADING: Record<string, string> = { error: "Errors", warning: "Warnings", info: "Notes" };
const FLOORS = [
  ["info", "All"],
  ["warning", "Warnings and errors"],
  ["error", "Errors only"],
] as const;

/**
 * Health (D338): how the KB is doing, on one page. What is wrong with it (the
 * lint findings), what it does not know (gaps, unanswered searches, pages past
 * review), what waits on a person (the doctor's questions) and what keeps it
 * in repair (the background repairs, the doctor sessions and their log). It
 * merges the former Observatory and Maintenance panels: two pages that
 * answered the same question halfway each.
 *
 * Like the rest of the Atlas it reads and never writes: a repair is undone, and
 * a question answered, from an agent session or the CLI, so a row carries the
 * text to copy rather than a button that acts.
 *
 * The findings follow the rail's Map selection and report the unfiltered
 * totals beside the filtered list, as the API does: a page that showed only
 * what cleared the floor would let a KB look healthy by choosing a high
 * enough severity. Everything else describes the whole KB, so a principal
 * that cannot see all of it gets no status and no summary (both 404) and the
 * page shows what it can.
 */
export function Health({
  report,
  status,
  summary,
  summaryError,
  questions,
  questionsError,
  scopeTitle,
  loading,
  error,
  severityMin,
  onSeverityChange,
  onReveal,
  onOpen,
  onRetry,
}: {
  report: LintReport | null;
  /** kb_status's knowledge signals; null when the principal cannot see the whole KB. */
  status: KBStatus | null;
  /** The maintenance summary (D323); null when not visible or not arrived. */
  summary: MaintenanceSummary | null;
  summaryError?: string | null;
  questions: MaintenanceQuestions | null;
  questionsError?: string | null;
  /** Title of the Map the findings are scoped to; null = whole KB. */
  scopeTitle: string | null;
  loading: boolean;
  error: unknown;
  severityMin: string;
  onSeverityChange(severity: string): void;
  onReveal(concept: string | null, message: string): void;
  onOpen(conceptId: string): void;
  onRetry(): void;
}) {
  const [copied, setCopied] = useState<string | null>(null);

  async function copy(key: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
    } catch {
      // No clipboard (an insecure origin, a denied permission): the text is
      // in the button's title, selectable from the tooltip, and that is the
      // fallback.
      setCopied(null);
    }
  }

  if (error && !report) return <ErrorState error={error} onRetry={onRetry} />;

  const open = questions?.questions ?? [];
  const errors = report?.by_severity.error ?? 0;
  const warnings = report?.by_severity.warning ?? 0;
  const notes = report?.by_severity.info ?? 0;
  const where = scopeTitle ? ` in ${scopeTitle}` : "";

  return (
    <Page label="Health" busy={loading} className="health">
      <PageHeader
        eyebrow="Health"
        title={<Verdict report={report} questions={open.length} where={where} />}
        subtitle={<Subtitle questions={questions} summary={summary} scopeTitle={scopeTitle} />}
        help={
          <>
            <p>
              <strong>Findings</strong> are what the deterministic lint checks say is wrong; they follow the Map chosen in the
              rail. <strong>Knowledge</strong> is what the KB does not know: gaps agents recorded, searches that found nothing,
              pages past their review date.
            </p>
            <p>
              <strong>Upkeep</strong> is what keeps the KB in repair: the server&apos;s background repairs and the doctor
              sessions an agent runs. Every repair is a commit, undone with the command on its row.
            </p>
          </>
        }
      />
      <p className="sr-only" role="status" aria-live="polite">
        {copied ? "Copied to the clipboard." : ""}
      </p>

      <Hero label="Summary">
        <div className="health__hero">
          <StateRing state={stateOf(report, open.length)} />
          <div className="health__hero-main">
            {!report ? (
              error ? null : <SkeletonRows label="Reading the findings" rows={1} />
            ) : report.total === 0 ? (
              <Quiet>
                {scopeTitle
                  ? `${scopeTitle} passes every deterministic lint check. Choose Whole atlas for the rest of the KB.`
                  : "This KB passes every deterministic lint check."}
              </Quiet>
            ) : (
              <Figures
                items={[
                  { label: errors === 1 ? "error" : "errors", value: errors, tone: errors ? "error" : "muted" },
                  { label: warnings === 1 ? "warning" : "warnings", value: warnings, tone: warnings ? "warning" : "muted" },
                  { label: notes === 1 ? "note" : "notes", value: notes, tone: notes ? undefined : "muted" },
                  ...(open.length
                    ? [{ label: open.length === 1 ? "question" : "questions", value: open.length, tone: "warning" }]
                    : []),
                ]}
              />
            )}
            {status && <KnowledgeLine status={status} />}
            {(summary || summaryError) && <Upkeep summary={summary} error={summaryError ?? null} />}
          </div>
        </div>
      </Hero>

      {/* Only what has something to say gets a section: an empty one would
          repeat what the title and the band already said. */}
      {(() => {
        const asks = !!questionsError || open.length > 0;
        const finds = !!error || (report !== null && report.total > 0);
        const knows = !!status && hasKnowledge(status);
        const log = !!(summary || summaryError);
        const main = asks || finds || knows;
        return (
          <div className={main && log ? "health__grid" : "health__grid health__grid--single"}>
            {main && (
              <div className="health__main">
                {asks && (
                  <PageSection title="Questions for you" id="health-questions" count={questions ? open.length : undefined}>
                    {questionsError ? (
                      <p className="page-note">Could not read the questions: {questionsError}</p>
                    ) : (
                      <>
                        <ul className="rows rows--actions" aria-label="Open questions">
                          {open.map((q) => (
                            <li key={q.id}>
                              <button type="button" className="row" onClick={() => onOpen(q.id)}>
                                <span className="health__glyph health__glyph--question" aria-hidden="true">
                                  ?
                                </span>
                                <span className="row__body">
                                  <span className="row__title">{q.title ?? q.id}</span>
                                  <span className="row__meta">
                                    <code>{q.id}</code>
                                    {q.involves?.length ? <span>involves {q.involves.join(", ")}</span> : null}
                                  </span>
                                </span>
                              </button>
                              <button
                                type="button"
                                className="row-action"
                                data-done={copied === `q:${q.id}`}
                                aria-label={`Copy ID of ${q.title ?? q.id}`}
                                title={q.id}
                                onClick={() => copy(`q:${q.id}`, q.id)}
                              >
                                {copied === `q:${q.id}` ? "Copied" : "Copy ID"}
                              </button>
                            </li>
                          ))}
                        </ul>
                        <p className="page-note">
                          Answer from an agent session: <code>concept_patch</code> the answer into the question, then set
                          its resolution_status to resolved.
                        </p>
                      </>
                    )}
                  </PageSection>
                )}
                {finds && (
                  <Findings
                    report={report}
                    loading={loading}
                    error={error}
                    severityMin={severityMin}
                    onSeverityChange={onSeverityChange}
                    onReveal={onReveal}
                    onRetry={onRetry}
                  />
                )}
                {knows && <Knowledge status={status!} onReveal={onReveal} />}
              </div>
            )}
            {log && (
              <aside className="health__side" aria-label="Upkeep log">
                <Repairs summary={summary} copied={copied} onCopy={copy} />
              </aside>
            )}
          </div>
        );
      })()}
    </Page>
  );
}

/** Whether there is anything to list below the Knowledge facet's counts. */
function hasKnowledge(status: KBStatus): boolean {
  return !!status.open_gaps?.total || (status.search_misses ?? []).length > 0 || (status.stale_count ?? 0) > 0;
}

type State = { tone: "ok" | "warning" | "error"; glyph: string; word: string };

/** The worst thing first: broken, then attention, then a waiting question. */
function stateOf(report: LintReport | null, questions: number): State | null {
  if (!report) return null;
  if (report.by_severity.error) return { tone: "error", glyph: "✕", word: "Broken" };
  if (report.by_severity.warning) return { tone: "warning", glyph: "!", word: "Needs attention" };
  if (questions > 0) return { tone: "warning", glyph: "?", word: "Waiting on you" };
  return { tone: "ok", glyph: "✓", word: "Healthy" };
}

/** The state as a ring the eye finds first: its tone, its glyph, its word. */
function StateRing({ state }: { state: State | null }) {
  return (
    <div className={`state-ring state-ring--${state?.tone ?? "loading"}`} aria-hidden="true">
      <svg viewBox="0 0 120 120">
        <circle className="state-ring__track" cx="60" cy="60" r="52" />
        <circle className="state-ring__arc" cx="60" cy="60" r="52" />
      </svg>
      <span className="state-ring__glyph">{state?.glyph ?? "…"}</span>
      <span className="state-ring__word">{state?.word ?? ""}</span>
    </div>
  );
}

/** The title answers "how is it": the worst thing first, in words. */
function Verdict({ report, questions, where }: { report: LintReport | null; questions: number; where: string }) {
  if (!report) return <>Reading the KB&apos;s health…</>;
  const errors = report.by_severity.error ?? 0;
  const warnings = report.by_severity.warning ?? 0;
  if (errors > 0)
    return (
      <>
        <Count>{errors}</Count> {errors === 1 ? "thing is" : "things are"} broken{where}.
      </>
    );
  if (warnings > 0)
    return (
      <>
        <Count>{warnings}</Count> {warnings === 1 ? "thing needs" : "things need"} attention{where}.
      </>
    );
  if (questions > 0)
    return (
      <>
        <Count>{questions}</Count> {questions === 1 ? "question waits" : "questions wait"} for you.
      </>
    );
  return <>All clear{where}.</>;
}

/** What the title does not say: where the findings were looked for, whether
 *  anything waits on the reader, and when the doctor comes next. */
function Subtitle({
  questions,
  summary,
  scopeTitle,
}: {
  questions: MaintenanceQuestions | null;
  summary: MaintenanceSummary | null;
  scopeTitle: string | null;
}) {
  const parts: string[] = [scopeTitle ? `Findings over ${scopeTitle}` : "Findings over the whole KB"];
  if (questions && questions.questions.length === 0) parts.push("no open question");
  if (summary?.next_doctor && summary.doctor_interval_days !== 0) {
    parts.push(`next doctor session ${relativeDay(summary.next_doctor)}`);
  }
  return <>{parts.join(" · ")}.</>;
}

/**
 * The upkeep, as a line and a track: whether the background repair runs and
 * what it last did, then the doctor's cycle from the last session to the next
 * with today marked on it.
 */
function Upkeep({ summary, error }: { summary: MaintenanceSummary | null; error: string | null }) {
  if (error) return <p className="page-note">Could not read the maintenance summary: {error}</p>;
  if (!summary) return null;
  const on = summary.auto_repair.checks.length > 0 && summary.auto_repair.interval_days !== 0;
  return (
    <div className="upkeep">
      <p className="upkeep__line">
        <span className="upkeep__pulse" data-on={on} aria-hidden="true" />
        <span className="upkeep__label">Background repair</span>
        <span title={summary.auto_repair.checks.join(", ")}>{autoRepairLine(summary)}</span>
        <span className="upkeep__sep" aria-hidden="true">
          ·
        </span>
        <span className="upkeep__run" title={summary.last_auto_repair?.at}>
          {runLine(summary.last_auto_repair)}
        </span>
      </p>
      <DoctorTrack summary={summary} />
    </div>
  );
}

/** Last doctor session → today → next one, as a track that fills as the next approaches. */
function DoctorTrack({ summary }: { summary: MaintenanceSummary }) {
  const last = summary.last_doctor ? Date.parse(summary.last_doctor) : NaN;
  const next = summary.next_doctor ? Date.parse(summary.next_doctor) : NaN;
  const span = next - last;
  const pct = Number.isFinite(span) && span > 0 ? Math.min(100, Math.max(0, ((Date.now() - last) / span) * 100)) : null;
  return (
    <div className="doctor">
      <div className="doctor__ends">
        <span>
          <span className="upkeep__label">Last doctor</span>{" "}
          {summary.last_doctor ? <Day iso={summary.last_doctor} /> : "never"}
        </span>
        <span>
          <span className="upkeep__label">Next doctor</span>{" "}
          {summary.doctor_interval_days === 0 ? (
            "not proposed (doctor_interval is 0)"
          ) : summary.next_doctor ? (
            <Day iso={summary.next_doctor} />
          ) : (
            "as soon as the KB has debt"
          )}
        </span>
      </div>
      {pct !== null && (
        <div className="doctor__track" aria-hidden="true">
          <span className="doctor__fill" style={{ width: `${pct}%` }} />
          <span className="doctor__today" style={{ left: `${pct}%` }} />
        </div>
      )}
    </div>
  );
}

/** A date said as a person would, the calendar date beside it, the ISO in the tooltip. */
function Day({ iso }: { iso: string }) {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return <>{iso}</>;
  return (
    <time dateTime={iso} title={iso}>
      {relativeDay(iso)}{" "}
      <span className="day__aside">· {new Date(t).toLocaleDateString("en", { day: "numeric", month: "short" })}</span>
    </time>
  );
}

/** What the KB does not know, as figures — or one line when nothing is missing. */
function KnowledgeLine({ status }: { status: KBStatus }) {
  const gaps = status.open_gaps?.total ?? 0;
  const misses = (status.search_misses ?? []).filter((m) => !m.resolved).length;
  const stale = status.stale_count ?? 0;
  if (gaps + misses + stale === 0) {
    return <Quiet>Nothing missing: no open gap, no unanswered search, nothing past its review date.</Quiet>;
  }
  return (
    <Figures
      items={[
        { label: gaps === 1 ? "open gap" : "open gaps", value: gaps, tone: gaps ? "warning" : "muted" },
        { label: "unanswered searches", value: misses, tone: misses ? "warning" : "muted" },
        { label: "past review", value: stale, tone: stale ? "warning" : "muted" },
      ]}
    />
  );
}

function Findings({
  report,
  loading,
  error,
  severityMin,
  onSeverityChange,
  onReveal,
  onRetry,
}: {
  report: LintReport | null;
  loading: boolean;
  error: unknown;
  severityMin: string;
  onSeverityChange(severity: string): void;
  onReveal(concept: string | null, message: string): void;
  onRetry(): void;
}) {
  const grouped = useMemo(() => {
    const map = new Map<string, LintFinding[]>();
    for (const finding of report?.findings ?? []) {
      const list = map.get(finding.severity) ?? [];
      list.push(finding);
      map.set(finding.severity, list);
    }
    return map;
  }, [report]);
  const checks = Object.entries(report?.by_check ?? {}).sort((a, b) => b[1] - a[1]);

  return (
    <PageSection
      title="Findings"
      id="health-findings"
      count={report?.total}
      actions={
        report && report.total > 0 ? (
          <Segmented label="Minimum severity" value={severityMin} options={FLOORS} onChange={onSeverityChange} />
        ) : null
      }
    >
      {error ? (
        <ErrorState error={error} onRetry={onRetry} />
      ) : !report ? (
        loading ? <SkeletonRows label="Loading lint findings" rows={3} /> : null
      ) : report.findings.length === 0 ? (
        <>
          <Quiet tone="neutral">No findings at or above this severity. Lower the floor to see the rest.</Quiet>
          {report.count < report.total && (
            <p className="page-note">
              Showing {report.count} of {report.total} findings at this severity floor.
            </p>
          )}
        </>
      ) : (
        <>
          {report.count < report.total && (
            <p className="page-note health__floor-note">
              Showing {report.count} of {report.total} findings at this severity floor.
            </p>
          )}
          {ORDER.filter((s) => grouped.has(s)).map((severity) => (
            <div key={severity} className="health__group" aria-labelledby={`health-${severity}`} role="group">
              <h3 className="health__group-title" id={`health-${severity}`}>
                {HEADING[severity] ?? severity}
                <span className="page-section__count">{grouped.get(severity)!.length}</span>
              </h3>
              <ul className="rows">
                {grouped.get(severity)!.map((finding, index) => (
                  <li key={`${finding.path}-${index}`}>
                    <button
                      type="button"
                      className="row health__finding"
                      onClick={() =>
                        onReveal(
                          finding.concept ?? null,
                          finding.concept ? "" : "This finding is not about a concept, so there is no node to reveal.",
                        )
                      }
                    >
                      <SeverityBadge severity={finding.severity} />
                      <span className="row__body">
                        <span className="health__message">{finding.message}</span>
                        <span className="row__meta">
                          <code>{finding.check}</code>
                          <AcceptBadge level={report.acceptability?.[finding.check]} />
                          <code className="health__path">{finding.path}</code>
                        </span>
                      </span>
                      <span className="row__end health__go">{finding.concept ? "Open concept" : "No concept"}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </>
      )}
      {checks.length > 0 && (
        <details className="health__checks">
          <summary>Findings by check</summary>
          <dl className="health__bycheck">
            {checks.map(([check, count]) => (
              <div key={check}>
                <dt>
                  <code>{check}</code> <AcceptBadge level={report?.acceptability?.[check]} />
                </dt>
                <dd>{count}</dd>
              </div>
            ))}
          </dl>
        </details>
      )}
    </PageSection>
  );
}

/**
 * What the KB does not know, beside what is wrong with it: the knowledge gaps
 * agents recorded, the searches that found nothing, and the pages past their
 * review date. Lint says a page is malformed; these say a page is missing or
 * stale.
 */
function Knowledge({ status, onReveal }: { status: KBStatus; onReveal(concept: string | null, message: string): void }) {
  const gaps = status.open_gaps;
  const misses = status.search_misses ?? [];
  const stale = status.stale_count ?? 0;
  const total = (gaps?.total ?? 0) + misses.filter((m) => !m.resolved).length + stale;
  return (
    <PageSection title="Knowledge" id="health-gaps" count={total}>
      <>
        {!!gaps?.total && (
          <div className="health__group" role="group" aria-labelledby="health-open-gaps">
            <h3 className="health__group-title" id="health-open-gaps">
              Open knowledge gaps
              <span className="page-section__count">{gaps.total}</span>
            </h3>
            <ul className="rows">
              {(gaps.recent ?? []).map((g) => (
                <li key={g.id}>
                  <button type="button" className="row" onClick={() => onReveal(g.id, "")}>
                    <span className="health__glyph" aria-hidden="true">
                      ◌
                    </span>
                    <span className="row__body">
                      <span className="row__title">{g.title || g.id}</span>
                      <span className="row__meta">
                        <span className="pill">{(g.kind ?? "gap").replace(/_/g, " ")}</span>
                        <code>{g.id}</code>
                      </span>
                    </span>
                    <span className="row__end health__go">Open concept</span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
        {misses.length > 0 && (
          <div className="health__group" role="group" aria-labelledby="health-misses">
            <h3 className="health__group-title" id="health-misses">
              Searched for, not found
              <span className="page-section__count">{misses.length}</span>
            </h3>
            <p className="page-note">
              What agents searched for in the last 30 days and the KB could not answer. A ticked one finds something now.
            </p>
            <ul className="health__misses">
              {misses.map((m) => (
                <li
                  key={m.query}
                  className={m.resolved ? "chip chip--resolved" : "chip"}
                  title={m.resolved ? "This search finds something now" : undefined}
                >
                  {m.resolved && (
                    <span className="chip__resolved" aria-hidden="true">
                      ✓
                    </span>
                  )}
                  {m.query}
                  {m.resolved && <span className="sr-only"> (now found)</span>}
                  <span className="chip__count">{m.count}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
        {stale > 0 && (
          <Quiet tone="neutral">
            {stale === 1 ? "One concept is" : `${stale} concepts are`} past its review date (<code>review_after</code>).
          </Quiet>
        )}
      </>
    </PageSection>
  );
}

/** The repair log: every repair commit of the last 30 days, newest first. */
function Repairs({
  summary,
  copied,
  onCopy,
}: {
  summary: MaintenanceSummary | null;
  copied: string | null;
  onCopy(key: string, text: string): void;
}) {
  const repairs = summary?.repairs ?? [];
  const most = Math.max(1, ...repairs.map((r) => parseRepair(r).concepts ?? r.files));
  return (
    <PageSection title="Repairs, last 30 days" id="health-repairs" count={summary ? repairs.length : undefined}>
      {!summary ? null : repairs.length === 0 ? (
        <Quiet tone="neutral">No repair commit in the last 30 days.</Quiet>
      ) : (
        <ul className="rows rows--actions health__repairs" aria-label="Recent repairs">
          {repairs.map((r) => {
            const { check, concepts } = parseRepair(r);
            const short = r.sha.slice(0, 7);
            return (
              <li key={r.sha}>
                <div className="row health__repair" title={r.subject}>
                  <span className={`health__glyph ${r.background ? "health__glyph--auto" : ""}`} aria-hidden="true">
                    {r.background ? "↻" : "✎"}
                  </span>
                  <span className="row__body">
                    <span className="row__title">{check}</span>
                    <span className="row__meta">
                      {concepts !== null && <span>{plural(concepts, "concept")}</span>}
                      <span>{r.background ? "background" : "by hand"}</span>
                      <time dateTime={r.at} title={r.at}>
                        {relativeDay(r.at)}
                      </time>
                      <code title={r.sha}>{short}</code>
                    </span>
                  </span>
                  <span className="health__repair-bar" aria-hidden="true">
                    {/* A square-root scale: one sweeping repair must not flatten the rest. */}
                    <span style={{ width: `${Math.sqrt((concepts ?? r.files) / most) * 100}%` }} />
                  </span>
                </div>
                <button
                  type="button"
                  className="row-action"
                  data-done={copied === `r:${r.sha}`}
                  aria-label={`Copy revert command for ${short}`}
                  title={r.revert}
                  onClick={() => onCopy(`r:${r.sha}`, r.revert)}
                >
                  {copied === `r:${r.sha}` ? "Copied" : "Copy revert"}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </PageSection>
  );
}

/** "kb_repair: duplicate_link (67 concepts)" → the check and the count. */
export function parseRepair(r: Pick<MaintenanceRepair, "subject">): { check: string; concepts: number | null } {
  const m = /^kb_repair:\s*([\w-]+)\s*(?:\((\d+) concepts?\))?/.exec(r.subject);
  if (!m) return { check: r.subject, concepts: null };
  return { check: m[1]!, concepts: m[2] ? Number(m[2]) : null };
}

function autoRepairLine(s: MaintenanceSummary): string {
  const a = s.auto_repair;
  if (a.checks.length === 0) return "off: auto_repair is explicitly empty";
  if (a.interval_days === 0) return "off: doctor_auto_interval is 0";
  const every = a.interval_days === 1 ? "daily" : `every ${a.interval_days} days`;
  return `On, ${every} · ${plural(a.checks.length, "check")}${a.default ? " (default)" : ""}`;
}

function runLine(run: MaintenanceRun | null): string {
  if (!run) return "none yet";
  const t = Date.parse(run.at);
  const when = Number.isFinite(t)
    ? `${relativeDay(run.at)}, ${new Date(t).toLocaleTimeString("en", { hour: "2-digit", minute: "2-digit", hour12: false })}`
    : run.at;
  if (run.skipped) return `${when} · skipped (${run.skipped})`;
  const applied = (run.checks ?? []).reduce((n, c) => n + c.applied, 0);
  const failed = (run.checks ?? []).filter((c) => c.error).length;
  const repaired = applied === 0 ? "nothing to repair" : `${plural(applied, "concept")} repaired`;
  return `${when} · ${repaired}${failed ? `, ${plural(failed, "check")} failed` : ""}`;
}

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

const ACCEPT: Record<string, { glyph: string; label: string; title: string }> = {
  none: { glyph: "⊘", label: "fix", title: "Cannot be accepted: fix it" },
  concept: { glyph: "◌", label: "concept", title: "Accept with lint_ignore on the concept, or on its map" },
  map: { glyph: "◎", label: "map", title: "Accept with lint_ignore in the map's _map.md" },
  artifact: { glyph: "◇", label: "artifact", title: "Accept with lint_accept in instructions.md, keyed by the artifact's path" },
};

/** Who can accept a finding of this check (D313, D332): nobody, the concept, its map, or instructions.md for an artifact. */
function AcceptBadge({ level }: { level?: string }) {
  const entry = level ? ACCEPT[level] : undefined;
  if (!entry) return null;
  return (
    <span className="pill" title={entry.title}>
      <span aria-hidden="true">{entry.glyph}</span> {entry.label}
    </span>
  );
}
