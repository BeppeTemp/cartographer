import { useMemo, useState } from "react";
import type {
  CheckCatalog,
  KBStatus,
  LintFinding,
  LintReport,
  MaintenanceQuestions,
  MaintenanceRepair,
  MaintenanceRun,
  MaintenanceSummary,
} from "../api/types";
import { checkLabel, groupFindings, type CheckGroup } from "../lib/health";
import {
  Count,
  Figures,
  Hero,
  Page,
  PageHeader,
  PageSection,
  Quiet,
  Segmented,
  SkeletonRows,
  relativeDay,
} from "./Page";
import { Icon } from "./Icon";
import { LintTrend } from "./LintTrend";
import { SeverityBadge } from "./SeverityBadge";
import { ErrorState } from "./States";

const DAY_MS = 86_400_000;

/**
 * Health (D338, D365): how the KB is doing, said by who acts on it. The
 * server repairs the mechanical findings by itself (D349, D355), a doctor
 * session decides the rest (D358), and only what neither can decide waits on
 * a person. So the page answers "does anything need me?" first, then shows
 * the findings grouped by cause -- one row per check, not one per page -- each
 * with who will deal with it, and what Cartographer already did. Severity is
 * a priority, not a pile of its own: an info finding is an improvement in the
 * doctor's queue (its Advice step), not homework for the reader.
 *
 * Like the rest of the Atlas it reads and never writes: a repair is undone, and
 * a question answered, from an agent session or the CLI, so a row carries the
 * text to copy rather than a button that acts.
 *
 * The findings follow the rail's Map selection. Everything else describes the
 * whole KB, so a principal that cannot see all of it gets no status and no
 * summary (both 404) and the page shows what it can.
 */
export function Health({
  report,
  status,
  summary,
  summaryError,
  checks,
  questions,
  questionsError,
  scopeTitle,
  loading,
  error,
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
  /** Every check the server runs (D365); null leaves the coverage out. */
  checks?: CheckCatalog | null;
  questions: MaintenanceQuestions | null;
  questionsError?: string | null;
  /** Title of the Map the findings are scoped to; null = whole KB. */
  scopeTitle: string | null;
  loading: boolean;
  error: unknown;
  onReveal(concept: string | null, message: string): void;
  onOpen(conceptId: string): void;
  onRetry(): void;
}) {
  const [copied, setCopied] = useState<string | null>(null);
  // Findings is what to act on; Checks is what the KB is checked for (D365).
  const [tab, setTab] = useState<"findings" | "checks">("findings");

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

  const groups = useMemo(() => groupFindings(report?.findings ?? []), [report]);

  if (error && !report) return <ErrorState error={error} onRetry={onRetry} />;

  const open = questions?.questions ?? [];
  const tally = tallyOf(report, summary);
  const where = scopeTitle ? ` in ${scopeTitle}` : "";

  return (
    <Page label="Health" busy={loading} className="health">
      <PageHeader
        eyebrow="Health"
        title={<Verdict report={report} tally={tally} questions={open.length} where={where} />}
        subtitle={scopeTitle ? `Over ${scopeTitle} only.` : undefined}
        help={
          <>
            <p>
              Cartographer looks after the KB in three hands. The <strong>background repair</strong> fixes the
              mechanical problems by itself, daily. The <strong>doctor</strong> is an agent session that decides what
              needs judgement: it runs by itself when an agent connects (unattended) or with you (assisted). What
              neither can decide becomes a <strong>question for you</strong>.
            </p>
            <p>
              <strong>Findings</strong> are grouped by cause and follow the Map chosen in the rail: problems (errors and
              warnings) first, then improvements (notes), which the doctor fixes or accepts as a deliberate choice.
              Every repair is a commit, undone with the command on its row.
            </p>
          </>
        }
      />
      <p className="sr-only" role="status" aria-live="polite">
        {copied ? "Copied to the clipboard." : ""}
      </p>

      <Hero label="Summary">
        <div className="health__hero">
          <StateRing state={stateOf(report, tally, open.length)} />
          <div className="health__hero-main">
            {!report ? (
              error ? null : (
                <SkeletonRows label="Reading the findings" rows={1} />
              )
            ) : (
              <Lanes tally={tally} summary={summary} summaryError={summaryError ?? null} questions={open.length} />
            )}
            {report?.total === 0 && (
              <Quiet>
                {scopeTitle
                  ? `${scopeTitle} passes every deterministic lint check. Choose All for the rest of the KB.`
                  : "This KB passes every deterministic lint check."}
              </Quiet>
            )}
            {status && <KnowledgeLine status={status} />}
          </div>
        </div>
      </Hero>

      {checks && report && (
        <div className="health__tabs">
          <Segmented<"findings" | "checks">
            label="Health view"
            value={tab}
            options={[
              ["findings", `Findings · ${report.findings.length}`],
              ["checks", `Checks · ${checks.checks.length}`],
            ]}
            onChange={setTab}
          />
        </div>
      )}

      {tab === "checks" && checks && report ? (
        <Coverage
          catalog={checks}
          report={report}
          onJump={(name) => {
            setTab("findings");
            // The row exists once the Findings tab has rendered.
            window.setTimeout(() => {
              const row = document.getElementById(`check-${name}`);
              const details = row?.querySelector("details");
              if (details) details.open = true;
              row?.scrollIntoView({ behavior: "smooth", block: "center" });
            }, 50);
          }}
        />
      ) : (
        <>
          {/* Only what has something to say gets a section: an empty one would
            repeat what the title and the lanes already said. */}
          {(() => {
            const asks = !!questionsError || open.length > 0;
            const finds = !!error || groups.length > 0;
            const knows = !!status && hasKnowledge(status);
            const log = !!(summary || summaryError);
            const main = asks || finds || knows;
            return (
              <div className={main && log ? "health__grid" : "health__grid health__grid--single"}>
                {main && (
                  <div className="health__main">
                    {asks && (
                      <PageSection
                        title="Questions for you"
                        id="health-questions"
                        count={questions ? open.length : undefined}
                      >
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
                              The doctor could not decide these alone. Answer from an agent session:{" "}
                              <code>concept_patch</code> the answer into the question, then set its resolution_status to
                              resolved.
                            </p>
                          </>
                        )}
                      </PageSection>
                    )}
                    {finds && (
                      <PageSection
                        title="Findings"
                        id="health-findings"
                        count={report ? report.findings.length : undefined}
                      >
                        {error ? (
                          <ErrorState error={error} onRetry={onRetry} />
                        ) : !report ? (
                          loading ? (
                            <SkeletonRows label="Loading lint findings" rows={3} />
                          ) : null
                        ) : (
                          <ul className="health__checks-list" aria-label="Findings by cause">
                            {groups.map((g) => (
                              <CheckRow key={g.check} group={g} onReveal={onReveal} />
                            ))}
                          </ul>
                        )}
                      </PageSection>
                    )}
                    {knows && <Knowledge status={status!} onReveal={onReveal} />}
                  </div>
                )}
                {log && (
                  <aside className="health__side" aria-label="Upkeep log">
                    <LintTrend history={summary?.lint_history} />
                    <Done summary={summary} copied={copied} onCopy={copy} />
                  </aside>
                )}
              </div>
            );
          })()}
        </>
      )}
    </Page>
  );
}

/** The findings counted by who acts on them. */
interface Tally {
  errors: number;
  /** Errors and warnings: what is wrong. */
  problems: number;
  /** Info findings: what could be better. */
  improvements: number;
  /** Findings the background repair will fix by itself. */
  auto: number;
  /** Problems a doctor session will decide. */
  doctor: number;
  /** Improvements a doctor session will fix or accept. */
  doctorImprovements: number;
  /** The doctor has findings to deal with and its session is overdue (or never ran). */
  doctorDue: boolean;
}

function tallyOf(report: LintReport | null, summary: MaintenanceSummary | null): Tally {
  const t: Tally = {
    errors: 0,
    problems: 0,
    improvements: 0,
    auto: 0,
    doctor: 0,
    doctorImprovements: 0,
    doctorDue: false,
  };
  for (const f of report?.findings ?? []) {
    if (f.severity === "info") t.improvements++;
    else t.problems++;
    if (f.severity === "error") t.errors++;
    if (f.handler === "auto") t.auto++;
    else if (f.severity !== "info") t.doctor++;
    else t.doctorImprovements++;
  }
  if (t.doctor + t.doctorImprovements > 0 && summary && summary.doctor_interval_days !== 0) {
    const next = summary.next_doctor ? Date.parse(summary.next_doctor) : NaN;
    // Never ran, or past the day it was due: nobody is on these findings.
    t.doctorDue = !summary.last_doctor || (Number.isFinite(next) && next + DAY_MS <= Date.now());
  }
  return t;
}

/** Whether there is anything to list below the Knowledge facet's counts. */
function hasKnowledge(status: KBStatus): boolean {
  return !!status.open_gaps?.total || (status.search_misses ?? []).length > 0 || (status.stale_count ?? 0) > 0;
}

type State = { tone: "ok" | "tending" | "warning" | "error"; glyph: string; word: string };

/**
 * The worst thing first: broken, then waiting on a person, then a doctor
 * nobody runs, then work in hand. Each word sits on one line inside the ring,
 * whose chord at that height is ~85px: about nine uppercase characters.
 */
function stateOf(report: LintReport | null, tally: Tally, questions: number): State | null {
  if (!report) return null;
  if (tally.errors) return { tone: "error", glyph: "✕", word: "Broken" };
  if (questions > 0) return { tone: "warning", glyph: "?", word: "Waiting" };
  if (tally.doctorDue) return { tone: "warning", glyph: "!", word: "Attention" };
  if (tally.problems > 0 || tally.improvements > 0) return { tone: "tending", glyph: "↻", word: "Tending" };
  return { tone: "ok", glyph: "✓", word: "Healthy" };
}

/** The title answers "does anything need me?": the worst thing first, in words. */
function Verdict({
  report,
  tally,
  questions,
  where,
}: {
  report: LintReport | null;
  tally: Tally;
  questions: number;
  where: string;
}) {
  if (!report) return <>Reading the KB&apos;s health…</>;
  if (tally.errors > 0)
    return (
      <>
        <Count>{tally.errors}</Count> {tally.errors === 1 ? "thing is" : "things are"} broken{where}.
      </>
    );
  if (questions > 0)
    return (
      <>
        <Count>{questions}</Count> {questions === 1 ? "question waits" : "questions wait"} for you.
      </>
    );
  if (tally.doctorDue) {
    const [n, what] = tally.doctor ? [tally.doctor, "problem"] : [tally.doctorImprovements, "improvement"];
    return (
      <>
        <Count>{n}</Count> {n === 1 ? `${what} waits` : `${what}s wait`} for the doctor{where}.
      </>
    );
  }
  if (tally.problems > 0 || tally.improvements > 0) return <>Nothing needs you{where}: Cartographer is on it.</>;
  return <>All clear{where}.</>;
}

/**
 * The three hands, side by side: what the background repair has queued and
 * when it runs, what the doctor has to decide and when it last came, and what
 * waits on the reader.
 */
function Lanes({
  tally,
  summary,
  summaryError,
  questions,
}: {
  tally: Tally;
  summary: MaintenanceSummary | null;
  summaryError: string | null;
  questions: number;
}) {
  const auto = summary?.auto_repair;
  const autoOn = !!auto && auto.checks.length > 0 && auto.interval_days !== 0;
  const assisted = summary?.doctor_mode === "assisted";
  return (
    <div className="lanes">
      <section className="lane" aria-labelledby="lane-auto" data-tone={autoOn ? "auto" : "off"}>
        <h3 className="lane__title" id="lane-auto">
          <span className="lane__icon" aria-hidden="true">
            <Icon name="repair" size={14} />
          </span>
          Automatic
          {summary && <span className="lane__mode">{autoOn ? every(summary.auto_repair.interval_days) : "off"}</span>}
        </h3>
        <p className="lane__figure">
          <span className="lane__value">{tally.auto}</span> {tally.auto === 1 ? "fix" : "fixes"} queued
        </p>
        {summaryError ? (
          <p className="lane__note">Could not read the maintenance summary: {summaryError}</p>
        ) : summary && !autoOn ? (
          <p className="lane__note">{autoRepairOff(summary)}</p>
        ) : summary ? (
          <dl className="lane__times">
            <div>
              <dt>Next</dt>
              <dd>{nextAutoRun(summary)}</dd>
            </div>
            {summary.last_auto_repair && (
              <div title={summary.last_auto_repair.at}>
                <dt>Last</dt>
                <dd>
                  {when(Date.parse(summary.last_auto_repair.at))}
                  <RunOutcome run={summary.last_auto_repair} />
                </dd>
              </div>
            )}
          </dl>
        ) : null}
      </section>

      <section className="lane" aria-labelledby="lane-doctor" data-tone={tally.doctorDue ? "due" : "doctor"}>
        <h3 className="lane__title" id="lane-doctor">
          <span className="lane__icon" aria-hidden="true">
            <Icon name="doctor" size={14} />
          </span>
          Doctor
          {summary && <span className="lane__mode">{assisted ? "assisted" : "unattended"}</span>}
        </h3>
        <p className="lane__figure">
          <span className="lane__value">{tally.doctor}</span> {tally.doctor === 1 ? "problem" : "problems"}
          {tally.doctorImprovements > 0 && (
            <span className="lane__extra"> + {plural(tally.doctorImprovements, "improvement")}</span>
          )}
        </p>
        {summary && (
          <dl className="lane__times">
            <div>
              <dt>Last</dt>
              <dd>{summary.last_doctor ? <Day iso={summary.last_doctor} /> : "never"}</dd>
            </div>
          </dl>
        )}
        {tally.doctorDue && (
          <p className="lane__note lane__note--due">
            {assisted
              ? "Run the kb-doctor skill with an agent."
              : summary?.doctor_schedule
                ? `Next doctor session: ${when(Date.parse(summary.doctor_schedule.next_run))}.`
                : "Starts when an agent next connects."}
          </p>
        )}
      </section>

      <section className="lane" aria-labelledby="lane-you" data-tone={questions ? "you" : "idle"}>
        <h3 className="lane__title" id="lane-you">
          <span className="lane__icon" aria-hidden="true">
            <Icon name="person" size={14} />
          </span>
          You
        </h3>
        <p className="lane__figure">
          <span className="lane__value">{questions}</span> {questions === 1 ? "question" : "questions"}
        </p>
        <p className="lane__note">{questions ? "The doctor could not decide these alone." : "Nothing waits on you."}</p>
      </section>
    </div>
  );
}

/** What a background run did, as a chip: its fixes, or why it did nothing. */
function RunOutcome({ run }: { run: MaintenanceRun }) {
  if (run.skipped) return <span className="lane__chip">skipped</span>;
  const applied = (run.checks ?? []).reduce((n, c) => n + c.applied, 0);
  const failed = (run.checks ?? []).filter((c) => c.error).length;
  return (
    <>
      <span className="lane__chip">{applied ? plural(applied, "fix", "fixes") : "nothing to fix"}</span>
      {failed > 0 && <span className="lane__chip lane__chip--error">{plural(failed, "check")} failed</span>}
    </>
  );
}

function every(days: number): string {
  return days === 1 ? "daily" : `every ${days} days`;
}

/** "today · 19:05": the day as a person says it, then the time. */
function when(ms: number): string {
  if (!Number.isFinite(ms)) return "";
  return `${relativeDay(new Date(ms).toISOString())} · ${clock(ms)}`;
}

/** When the background repair runs next: an interval after its last run. */
function nextAutoRun(s: MaintenanceSummary): string {
  const last = s.last_auto_repair ? Date.parse(s.last_auto_repair.at) : NaN;
  if (!Number.isFinite(last)) return "shortly";
  const next = last + s.auto_repair.interval_days * DAY_MS;
  if (next <= Date.now()) return "shortly";
  return when(next);
}

function autoRepairOff(s: MaintenanceSummary): string {
  if (s.auto_repair.checks.length === 0) return "Off: auto_repair is explicitly empty";
  return "Off: doctor_auto_interval is 0";
}

/** One cause: its checks' findings folded into one row, opened to the pages. */
function CheckRow({ group, onReveal }: { group: CheckGroup; onReveal(concept: string | null, message: string): void }) {
  const auto = group.handler === "auto";
  return (
    <li className="health__check" id={`check-${group.check}`}>
      <details>
        <summary className="health__check-summary">
          <SeverityBadge severity={group.severity} />
          <span className="health__check-body">
            <span className="health__check-title">
              {checkLabel(group.check)}
              <span className="health__check-count">{group.count}</span>
            </span>
            <span className="health__check-meta">
              <code>{group.check}</code>
              {group.messages[0] && (
                <span className="health__check-sample">
                  {group.messages[0].message}
                  {group.messages.length > 1 ? " …" : ""}
                </span>
              )}
            </span>
          </span>
          <span
            className="pill health__handler"
            data-handler={group.handler}
            title={auto ? "Fixed by the background repair" : "Decided by a doctor session"}
          >
            {auto ? "Automatic" : group.handler === "mixed" ? "Partly automatic" : "Doctor"}
          </span>
        </summary>
        <ul className="rows health__check-rows">
          {group.messages.slice(0, MESSAGE_CAP).map((m) => (
            <li key={m.message}>
              <MessageRow message={m.message} findings={m.findings} onReveal={onReveal} />
            </li>
          ))}
        </ul>
        {group.messages.length > MESSAGE_CAP && (
          <p className="page-note">
            and {plural(group.messages.length - MESSAGE_CAP, "more")}: ask an agent for <code>lint</code> on this check.
          </p>
        )}
      </details>
    </li>
  );
}

const MESSAGE_CAP = 30;
const PAGE_CAP = 12;

/** A message and the pages that carry it: one page opens directly, several list. */
function MessageRow({
  message,
  findings,
  onReveal,
}: {
  message: string;
  findings: LintFinding[];
  onReveal(concept: string | null, message: string): void;
}) {
  const reveal = (f: LintFinding) =>
    onReveal(f.concept ?? null, f.concept ? "" : "This finding is not about a concept, so there is no node to reveal.");
  if (findings.length === 1) {
    const f = findings[0]!;
    return (
      <button type="button" className="row health__finding" onClick={() => reveal(f)}>
        <span className="row__body">
          <span className="health__message">{message}</span>
          <span className="row__meta">
            <code className="health__path">{f.path}</code>
          </span>
        </span>
        <span className="row__end health__go">{f.concept ? "Open" : ""}</span>
      </button>
    );
  }
  return (
    <div className="row health__finding health__finding--many">
      <span className="row__body">
        <span className="health__message">
          {message} <span className="health__times">× {findings.length}</span>
        </span>
        <span className="health__pages">
          {findings.slice(0, PAGE_CAP).map((f, i) => (
            <button
              key={`${f.path}-${i}`}
              type="button"
              className="health__page"
              onClick={() => reveal(f)}
              title={f.path}
            >
              {f.concept ?? f.path}
            </button>
          ))}
          {findings.length > PAGE_CAP && <span className="health__page-more">+{findings.length - PAGE_CAP}</span>}
        </span>
      </span>
    </div>
  );
}

/** A category's name for a reader. */
const CATEGORY: Record<string, string> = {
  pages: "Pages",
  templates: "Templates",
  validity: "Valid pages",
  links: "Links and graph",
  values: "Vocabularies",
  maps: "Maps and indexes",
  artifacts: "Artifacts",
  kb: "KB files",
};

/**
 * What the KB is checked for (D365): every check the server runs, by
 * category, with its count -- a zero too, because a check that finds nothing
 * is as much an answer as one that does. It has a tab of its own, beside the
 * findings; a check with findings jumps to its row there.
 */
function Coverage({
  catalog,
  report,
  onJump,
}: {
  catalog: CheckCatalog;
  report: LintReport;
  onJump(check: string): void;
}) {
  const count = (name: string) => report.by_check[name] ?? 0;
  const dirty = catalog.checks.filter((c) => count(c.name) > 0).length;
  const auto = catalog.checks.filter((c) => c.auto).length;
  return (
    <section className="health__coverage" id="health-coverage" aria-label="Checks">
      <p className="page-note health__coverage-line">
        {plural(catalog.checks.length - dirty, "check")} clean · {plural(dirty, "check")} with findings · {auto} fixed
        by the background repair
      </p>
      <div className="coverage">
        {catalog.categories.map((category) => {
          const list = catalog.checks
            .filter((c) => c.category === category)
            .sort((a, b) => count(b.name) - count(a.name) || checkLabel(a.name).localeCompare(checkLabel(b.name)));
          if (list.length === 0) return null;
          const found = list.filter((c) => count(c.name) > 0).length;
          return (
            <details key={category} className="coverage__group" open>
              <summary className="coverage__title">
                {CATEGORY[category] ?? category}
                <span className="coverage__tally" data-clean={found === 0}>
                  {found === 0 ? `all ${list.length} clean` : `${found} of ${list.length} with findings`}
                </span>
              </summary>
              <ul className="coverage__checks">
                {list.map((c) => {
                  const n = count(c.name);
                  const label = checkLabel(c.name);
                  return (
                    <li
                      key={c.name}
                      className="coverage__check"
                      data-found={n > 0 || undefined}
                      data-severity={c.severity}
                    >
                      <span className="coverage__mark" aria-hidden="true">
                        {n > 0 ? "" : "✓"}
                      </span>
                      {n > 0 ? (
                        <button type="button" className="coverage__name" title={c.name} onClick={() => onJump(c.name)}>
                          {label}
                        </button>
                      ) : (
                        <span className="coverage__name" title={c.name}>
                          {label}
                        </span>
                      )}
                      {c.auto && (
                        <span className="coverage__auto" title="Fixed by the background repair">
                          <Icon name="repair" size={12} />
                          <span className="sr-only">automatic</span>
                        </span>
                      )}
                      <span className="coverage__count">{n}</span>
                    </li>
                  );
                })}
              </ul>
            </details>
          );
        })}
      </div>
    </section>
  );
}

/** One entry of the upkeep timeline: a background run, or a repair someone ran. */
type Entry =
  | { kind: "run"; at: string; run: MaintenanceRun; fixes: number; commit?: MaintenanceRepair }
  | { kind: "manual"; at: string; repair: MaintenanceRepair };

/**
 * What Cartographer did by itself, as a timeline of the last 30 days: one row
 * per background run that fixed something -- its fixes as a bar split by check,
 * opened to the list -- and per repair someone ran by hand, each with the
 * command that undoes it. Runs that found nothing are one line at the end.
 */
function Done({
  summary,
  copied,
  onCopy,
}: {
  summary: MaintenanceSummary | null;
  copied: string | null;
  onCopy(key: string, text: string): void;
}) {
  const repairs = summary?.repairs ?? [];
  const runs = summary?.runs ?? (summary?.last_auto_repair ? [summary.last_auto_repair] : []);
  const bySha = new Map(repairs.map((r) => [r.sha, r]));
  const entries: Entry[] = [];
  let idle = 0;
  for (const run of runs) {
    const fixes = (run.checks ?? []).reduce((n, c) => n + c.applied, 0);
    if (!fixes) {
      idle++;
      continue;
    }
    const sha = run.checks?.find((c) => c.commit)?.commit;
    entries.push({ kind: "run", at: run.at, run, fixes, commit: sha ? bySha.get(sha) : undefined });
  }
  for (const r of repairs) if (!r.background) entries.push({ kind: "manual", at: r.at, repair: r });
  entries.sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
  const total = entries.reduce((n, e) => n + (e.kind === "run" ? e.fixes : 0), 0);

  const revert = (r: MaintenanceRepair | undefined) =>
    r && (
      <button
        type="button"
        className="row-action"
        data-done={copied === `r:${r.sha}`}
        aria-label={`Copy revert command for ${r.sha.slice(0, 7)}`}
        title={r.revert}
        onClick={() => onCopy(`r:${r.sha}`, r.revert)}
      >
        {copied === `r:${r.sha}` ? "Copied" : "Copy revert"}
      </button>
    );

  return (
    <PageSection title="Done by Cartographer" id="health-repairs" count={summary ? entries.length : undefined}>
      {!summary ? null : entries.length === 0 ? (
        <Quiet tone="neutral">Nothing repaired in the last 30 days.</Quiet>
      ) : (
        <>
          <p className="page-note health__done-line">
            Last 30 days: {plural(total, "fix", "fixes")} in {plural(entries.length, "run")}
          </p>
          <ol className="upkeep-log" aria-label="Recent repairs">
            {entries.map((e) =>
              e.kind === "run" ? (
                <li key={e.at} className="upkeep-log__entry">
                  <details>
                    <summary className="upkeep-log__head" title={e.at}>
                      <span className="health__glyph health__glyph--auto" aria-hidden="true">
                        <Icon name="repair" size={13} />
                      </span>
                      <span className="upkeep-log__body">
                        <span className="upkeep-log__when">{when(Date.parse(e.at))}</span>
                        <span className="upkeep-log__what">
                          {plural(e.fixes, "fix", "fixes")}
                          {e.commit && <> · {plural(e.commit.files, "page")}</>}
                        </span>
                        <RunBar run={e.run} fixes={e.fixes} />
                      </span>
                    </summary>
                    <ul className="health__fixed" aria-label="Fixed in this run">
                      {(e.run.checks ?? [])
                        .filter((c) => c.applied > 0)
                        .sort((a, b) => b.applied - a.applied)
                        .map((c) => (
                          <li key={c.check} title={c.check}>
                            <span className="health__fixed-label">{checkLabel(c.check)}</span>
                            <span className="health__fixed-count">{c.applied}</span>
                          </li>
                        ))}
                    </ul>
                  </details>
                  {revert(e.commit)}
                </li>
              ) : (
                <li key={e.repair.sha} className="upkeep-log__entry">
                  <div className="upkeep-log__head" title={e.repair.subject}>
                    <span className="health__glyph" aria-hidden="true">
                      ✎
                    </span>
                    <span className="upkeep-log__body">
                      <span className="upkeep-log__when">{when(Date.parse(e.at))}</span>
                      <span className="upkeep-log__what">
                        {checkLabel(parseRepair(e.repair).check)} ·{" "}
                        {plural(parseRepair(e.repair).concepts ?? e.repair.files, "page")} · by hand
                      </span>
                    </span>
                  </div>
                  {revert(e.repair)}
                </li>
              ),
            )}
          </ol>
          {idle > 0 && <p className="page-note">and {plural(idle, "run")} with nothing to fix.</p>}
        </>
      )}
    </PageSection>
  );
}

/** A run's fixes as one bar split by check, the largest first. */
function RunBar({ run, fixes }: { run: MaintenanceRun; fixes: number }) {
  const parts = (run.checks ?? []).filter((c) => c.applied > 0).sort((a, b) => b.applied - a.applied);
  return (
    <span className="upkeep-log__bar" aria-hidden="true">
      {parts.map((c, i) => (
        <span
          key={c.check}
          title={`${checkLabel(c.check)}: ${c.applied}`}
          style={{ flexGrow: c.applied / fixes, opacity: Math.max(0.35, 1 - i * 0.18) }}
        />
      ))}
    </span>
  );
}

/** "kb_repair: duplicate_link (67 concepts)" → the check and the count. */
export function parseRepair(r: Pick<MaintenanceRepair, "subject">): { check: string; concepts: number | null } {
  const m = /^kb_repair:\s*([\w-]+)\s*(?:\((\d+) concepts?\))?/.exec(r.subject);
  if (!m) return { check: r.subject, concepts: null };
  return { check: m[1]!, concepts: m[2] ? Number(m[2]) : null };
}

function clock(ms: number): string {
  return new Date(ms).toLocaleTimeString("en", { hour: "2-digit", minute: "2-digit", hour12: false });
}

function plural(n: number, word: string, many = `${word}s`): string {
  return `${n} ${n === 1 ? word : many}`;
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

/**
 * What the KB does not know, beside what is wrong with it: the knowledge gaps
 * agents recorded, the searches that found nothing, and the pages past their
 * review date. Lint says a page is malformed; these say a page is missing or
 * stale.
 */
function Knowledge({
  status,
  onReveal,
}: {
  status: KBStatus;
  onReveal(concept: string | null, message: string): void;
}) {
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
              What agents searched for in the last 30 days and the KB could not answer. A ticked one finds something
              now.
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
