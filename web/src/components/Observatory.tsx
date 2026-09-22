import { useMemo, useState } from "react";
import type { LintReport } from "../api/types";
import { SeverityBadge } from "./SeverityBadge";
import { EmptyState, ErrorState, Skeleton } from "./States";

const ORDER = ["error", "warning", "info"];

/**
 * The Observatory answers "what is wrong with this KB", grouped by severity.
 *
 * It reports the unfiltered totals next to the filtered list, because the API
 * does: a panel that showed only what cleared the floor would let a KB look
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
  const [openCheck, setOpenCheck] = useState<string | null>(null);

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

  return (
    <section className="observatory" aria-label="Observatory">
      <header className="observatory__head">
        <div className="observatory__totals">
          {ORDER.map((severity) => (
            <div key={severity} className={`observatory__total observatory__total--${severity}`}>
              <span className="observatory__count">{report.by_severity[severity] ?? 0}</span>
              <span className="observatory__label">{severity}</span>
            </div>
          ))}
        </div>
        <label className="observatory__filter">
          <span className="sr-only">Minimum severity</span>
          <select
            className="input"
            value={severityMin}
            onChange={(event) => onSeverityChange(event.target.value)}
          >
            <option value="info">All findings</option>
            <option value="warning">Warning and above</option>
            <option value="error">Errors only</option>
          </select>
        </label>
      </header>

      {report.count < report.total && (
        <p className="observatory__note">
          Showing {report.count} of {report.total} findings at this severity floor.
        </p>
      )}

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
          <section key={severity} className="observatory__group">
            <h3 className="observatory__group-title">
              <SeverityBadge severity={severity} />
              <span>{grouped.get(severity)!.length}</span>
            </h3>
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
                    <span className="observatory__check">{finding.check}</span>
                    <span className="observatory__message">{finding.message}</span>
                    <code className="observatory__path">{finding.path}</code>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        ))
      )}

      <details
        className="observatory__checks"
        open={openCheck !== null}
        onToggle={(event) => setOpenCheck(event.currentTarget.open ? "open" : null)}
      >
        <summary>Findings by check</summary>
        <dl className="observatory__bycheck">
          {Object.entries(report.by_check)
            .sort((a, b) => b[1] - a[1])
            .map(([check, count]) => (
              <div key={check}>
                <dt>{check}</dt>
                <dd>{count}</dd>
              </div>
            ))}
        </dl>
      </details>
    </section>
  );
}
