import { useMemo } from "react";
import type { LintReport } from "../api/types";
import { Icon } from "./Icon";
import { SeverityBadge } from "./SeverityBadge";
import { EmptyState, ErrorState, Skeleton } from "./States";

const ORDER = ["error", "warning", "info"];
const HEADING: Record<string, string> = { error: "Errors", warning: "Warnings", info: "Notes" };

/** The headline says what the reader should feel, in words: the counts follow. */
function headline(report: LintReport): string {
  const errors = report.by_severity.error ?? 0;
  const warnings = report.by_severity.warning ?? 0;
  if (report.total === 0) return "Nothing to report.";
  if (errors > 0) return errors === 1 ? "One thing is broken." : `${errors} things are broken.`;
  if (warnings > 0) return warnings === 1 ? "One thing needs attention." : `${warnings} things need attention.`;
  return report.total === 1 ? "One note on this atlas." : `${report.total} notes on this atlas.`;
}

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

/**
 * The Observatory answers "what is wrong with this KB" -- as a page to read,
 * not a dashboard (brand kit: a sober list with severity, explanation and a
 * way to the concept; no decorative KPIs). A serif headline says it in words,
 * one line gives the counts, and the findings follow as rows grouped by
 * severity, each opening its concept.
 *
 * It reports the unfiltered totals next to the filtered list, because the API
 * does: a page that showed only what cleared the floor would let a KB look
 * healthy by choosing a high enough severity.
 */
export function Observatory({
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
        <p className="eyebrow">Observatory</p>
        <h1 className="observatory__title">{headline(report)}</h1>
        <p className="observatory__summary">
          {ORDER.map((severity, i) => (
            <span key={severity}>
              {i > 0 && <span aria-hidden="true"> · </span>}
              <SeverityBadge severity={severity} count={report.by_severity[severity] ?? 0} />
            </span>
          ))}
          <span className="observatory__summary-rest">
            {" "}
            — from {plural(checks.length, "check")} run over the whole KB.
          </span>
        </p>
      </header>

      <div className="observatory__bar">
        <label className="field observatory__filter">
          <span className="sr-only">Minimum severity</span>
          <select
            className="field__control"
            value={severityMin}
            onChange={(event) => onSeverityChange(event.target.value)}
          >
            <option value="info">All findings</option>
            <option value="warning">Warnings and errors</option>
            <option value="error">Errors only</option>
          </select>
          <span className="field__adornment">
            <Icon name="chevron" size={16} />
          </span>
        </label>
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
