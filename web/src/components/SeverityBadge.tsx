/**
 * Severity is carried by a word, with a coloured mark as reinforcement only.
 * A badge that is "the red one" is invisible to a colour-blind reader and to
 * anyone printing the page, so the text is never decorative. The mark's shape
 * differs too (square, triangle, circle), for the same reason.
 */
export function SeverityBadge({ severity, count }: { severity: string; count?: number }) {
  return (
    <span className={`severity severity--${severity}`}>
      <span className="severity__mark" aria-hidden="true" />
      {count !== undefined && <span className="severity__count">{count}</span>}
      <span>{severity}</span>
    </span>
  );
}
