import { useMemo } from "react";
import type { KBStatus, LintReport } from "../api/types";
import { SeverityBadge } from "./SeverityBadge";
import { EmptyState, ErrorState, Skeleton } from "./States";

const ORDER = ["error", "warning", "info"];
const HEADING: Record<string, string> = { error: "Errors", warning: "Warnings", info: "Notes" };
const FLOORS: [string, string][] = [
  ["info", "All"],
  ["warning", "Warnings and errors"],
  ["error", "Errors only"],
];

/** The headline says what the reader should feel, in words: the counts follow.
 *  A scoped report names its scope in the headline itself, so a Map with no
 *  findings never reads as a healthy KB. */
function headline(report: LintReport, scopeTitle: string | null): string {
  const errors = report.by_severity.error ?? 0;
  const warnings = report.by_severity.warning ?? 0;
  const where = scopeTitle ? ` in ${scopeTitle}` : "";
  if (report.total === 0) return `Nothing to report${where}.`;
  if (errors > 0) return (errors === 1 ? "One thing is broken" : `${errors} things are broken`) + `${where}.`;
  if (warnings > 0) return (warnings === 1 ? "One thing needs attention" : `${warnings} things need attention`) + `${where}.`;
  if (scopeTitle) return report.total === 1 ? `One note${where}.` : `${report.total} notes${where}.`;
  return report.total === 1 ? "One note on this atlas." : `${report.total} notes on this atlas.`;
}

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

/**
 * The Observatory answers "what is wrong with this KB" -- as a page to read,
 * not a dashboard (brand kit: a sober list with severity, explanation and a
 * way to the concept; no decorative KPIs). A headline says it in words, one
 * line gives the counts, and the findings follow as rows grouped by severity,
 * each opening its concept. It speaks the shell's language -- sans type,
 * surface cards, the segmented switch of the legend -- so it reads as a view
 * of the same app, not a separate document.
 *
 * It reports the unfiltered totals next to the filtered list, because the API
 * does: a page that showed only what cleared the floor would let a KB look
 * healthy by choosing a high enough severity. For the same reason a report
 * scoped to one Map (the rail's selection) says so wherever it states a
 * total: a scoped page that read as KB-wide would hide everything outside it.
 */
export function Observatory({
  report,
  status = null,
  scopeTitle,
  loading,
  error,
  severityMin,
  onSeverityChange,
  onReveal,
  onRetry,
}: {
  report: LintReport | null;
  /** Title of the Map or Journal the findings are scoped to; null = whole KB. */
  scopeTitle: string | null;
  /** kb_status's knowledge signals (gaps, misses, stale pages); null when
   *  the principal cannot see the whole KB or it has not arrived. */
  status?: KBStatus | null;
  loading: boolean;
  error: unknown;
  severityMin: string;
  onSeverityChange(severity: string): void;
  onReveal(concept: string | null, message: string): void;
  onRetry(): void;
}) {
  const grouped = useMemo(() => {
    const map = new Map<string, LintReport["findings"]>();
    for (const finding of report?.findings ?? []) {
      const list = map.get(finding.severity) ?? [];
      list.push(finding);
      map.set(finding.severity, list);
    }
    return map;
  }, [report]);

  if (loading) return <Skeleton lines={6} label="Loading lint findings" />;
  if (error) return <ErrorState error={error} onRetry={onRetry} />;
  if (!report) return null;

  const checks = Object.entries(report.by_check).sort((a, b) => b[1] - a[1]);

  return (
    <section className="observatory" aria-label="Observatory">
      <header className="observatory__intro">
        <p className="observatory__eyebrow">Observatory</p>
        <h1 className="observatory__title">{headline(report, scopeTitle)}</h1>
        <p className="observatory__summary">
          {ORDER.map((severity, i) => (
            <span key={severity}>
              {i > 0 && <span aria-hidden="true"> · </span>}
              <SeverityBadge severity={severity} count={report.by_severity[severity] ?? 0} />
            </span>
          ))}
          <span className="observatory__summary-rest">
            {" "}
            — from {plural(checks.length, "check")} run over{" "}
            {scopeTitle ? <strong className="observatory__scope">{scopeTitle}</strong> : "the whole KB"}.
          </span>
        </p>
      </header>

      <div className="observatory__bar">
        <div className="segmented" role="group" aria-label="Minimum severity">
          {FLOORS.map(([value, label]) => (
            <button
              key={value}
              type="button"
              className="segmented__option"
              aria-pressed={severityMin === value}
              onClick={() => onSeverityChange(value)}
            >
              {label}
            </button>
          ))}
        </div>
        {report.count < report.total && (
          <p className="observatory__note">
            Showing {report.count} of {report.total} findings at this severity floor.
          </p>
        )}
      </div>

      {report.findings.length === 0 ? (
        <EmptyState
          title="Nothing to report"
          detail={
            report.total > 0
              ? "No findings at or above this severity. Lower the floor to see the rest."
              : scopeTitle
                ? `${scopeTitle} passes every deterministic lint check. Choose Whole atlas for the rest of the KB.`
                : "This KB passes every deterministic lint check."
          }
        />
      ) : (
        ORDER.filter((severity) => grouped.has(severity)).map((severity) => (
          <section key={severity} className="observatory__group" aria-labelledby={`obs-${severity}`}>
            <h2 className="observatory__group-title" id={`obs-${severity}`}>
              {HEADING[severity] ?? severity}
              <span className="observatory__group-count">{grouped.get(severity)!.length}</span>
            </h2>
            <ul className="observatory__findings">
              {grouped.get(severity)!.map((finding, index) => (
                <li key={`${finding.path}-${index}`}>
                  <button
                    type="button"
                    className="observatory__finding"
                    onClick={() =>
                      onReveal(
                        finding.concept ?? null,
                        finding.concept
                          ? ""
                          : "This finding is not about a concept, so there is no node to reveal.",
                      )
                    }
                  >
                    <SeverityBadge severity={finding.severity} />
                    <span className="observatory__body">
                      <span className="observatory__message">{finding.message}</span>
                      <span className="observatory__meta">
                        <code className="observatory__check">{finding.check}</code>
                        <code className="observatory__path">{finding.path}</code>
                      </span>
                    </span>
                    <span className="observatory__go">{finding.concept ? "Open concept" : "No concept"}</span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        ))
      )}

      <Knowledge status={status} onReveal={onReveal} />

      {checks.length > 0 && (
        <details className="observatory__checks">
          <summary>Findings by check</summary>
          <dl className="observatory__bycheck">
            {checks.map(([check, count]) => (
              <div key={check}>
                <dt>
                  <code>{check}</code>
                </dt>
                <dd>{count}</dd>
              </div>
            ))}
          </dl>
        </details>
      )}
    </section>
  );
}

/**
 * What the KB does not know, beside what is wrong with it: the knowledge gaps
 * agents recorded, the searches that found nothing, and the pages past their
 * review date. Lint says a page is malformed; these say a page is missing or
 * stale -- the other half of what a maintainer comes to the Observatory for.
 */
function Knowledge({
  status,
  onReveal,
}: {
  status: KBStatus | null;
  onReveal(concept: string | null, message: string): void;
}) {
  if (!status) return null;
  const gaps = status.open_gaps;
  const misses = status.search_misses ?? [];
  const stale = status.stale_count ?? 0;
  if (!gaps?.total && misses.length === 0 && stale === 0) return null;
  return (
    <>
      {!!gaps?.total && (
        <section className="observatory__group" aria-labelledby="obs-gaps">
          <h2 className="observatory__group-title" id="obs-gaps">
            Open knowledge gaps
            <span className="observatory__group-count">{gaps.total}</span>
          </h2>
          <ul className="observatory__findings">
            {(gaps.recent ?? []).map((g) => (
              <li key={g.id}>
                <button type="button" className="observatory__finding" onClick={() => onReveal(g.id, "")}>
                  <span className="observatory__body">
                    <span className="observatory__message">{g.title || g.id}</span>
                    <span className="observatory__meta">
                      <code className="observatory__check">{(g.kind ?? "gap").replace(/_/g, " ")}</code>
                      <code className="observatory__path">{g.id}</code>
                    </span>
                  </span>
                  <span className="observatory__go">Open concept</span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
      {misses.length > 0 && (
        <section className="observatory__group" aria-labelledby="obs-misses">
          <h2 className="observatory__group-title" id="obs-misses">
            Searched for, not found
            <span className="observatory__group-count">{misses.length}</span>
          </h2>
          <p className="observatory__note">What agents searched for in the last 30 days and the KB could not answer.</p>
          <ul className="observatory__misses">
            {misses.map((m) => (
              <li key={m.query} className="chip">
                {m.query}
                <span className="chip__count">{m.count}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {stale > 0 && (
        <p className="observatory__note observatory__stale">
          {stale === 1 ? "One concept is" : `${stale} concepts are`} past its review date (<code>review_after</code>).
        </p>
      )}
    </>
  );
}
