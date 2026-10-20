/**
 * A count of problems (D947): a lint finding is a problem, so there is no level
 * to tell apart, only how many. The word carries the meaning and the coloured
 * mark only reinforces it: a badge that is "the amber one" is invisible to a
 * colour-blind reader and to anyone printing the page.
 */
export function FindingBadge({ count }: { count: number }) {
  return (
    <span className="finding-badge">
      <span className="finding-badge__mark" aria-hidden="true" />
      <span className="finding-badge__count">{count}</span>
      <span>{count === 1 ? "problem" : "problems"}</span>
    </span>
  );
}
