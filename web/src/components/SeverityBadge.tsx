/**
 * Severity is carried by a glyph and a word, with colour as reinforcement
 * only. A badge that is "the red one" is invisible to a colour-blind reader
 * and to anyone printing the page, so the text is never decorative.
 */
const GLYPHS: Record<string, string> = {
  error: "✖",
  warning: "⚠",
  info: "ℹ",
};

export function SeverityBadge({ severity }: { severity: string }) {
  const glyph = GLYPHS[severity] ?? "•";
  return (
    <span className={`severity severity--${severity}`}>
      <span aria-hidden="true">{glyph}</span>
      <span>{severity}</span>
    </span>
  );
}
