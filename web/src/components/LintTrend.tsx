import type { LintSample } from "../api/types";
import { trendOf } from "../lib/health";
import { PageSection } from "./Page";

const W = 120;
const H = 28;

/**
 * Is the KB getting better (D370)? The total of findings then and now over the
 * last week, and a sparkline of the daily samples the server keeps. Nothing with
 * fewer than two samples: a line through one point says nothing.
 */
export function LintTrend({ history }: { history: LintSample[] | undefined }) {
  const t = trendOf(history ?? [], Date.now());
  if (!t) return null;
  const max = Math.max(...t.points, 1);
  const step = W / (t.points.length - 1);
  const pts = t.points.map((v, i) => `${(i * step).toFixed(1)},${(H - 2 - (v / max) * (H - 4)).toFixed(1)}`).join(" ");
  const tone = t.to < t.from ? "better" : t.to > t.from ? "worse" : "same";
  return (
    <PageSection title="Trend" id="health-trend">
      <p className="page-note health__trend" data-trend={tone}>
        <span className="health__trend-text">
          {t.from} → {t.to} {t.span}
        </span>
        <svg className="health__spark" width={W} height={H} viewBox={`0 0 ${W} ${H}`} role="img" aria-label={`Findings over the last ${t.points.length} samples`}>
          <polyline points={pts} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" />
        </svg>
      </p>
    </PageSection>
  );
}
