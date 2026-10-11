import { useEffect, useRef, useState } from "react";
import { VIBE_LINES, type Egg } from "../lib/eggs";

/**
 * What the easter eggs add to the page besides their stylesheet: a card for
 * each that says it is on, with a way off, and, in vibe mode, the button
 * every vibe-coded app ships with.
 */
export function Eggs({ on, onOff }: { on: ReadonlySet<Egg>; onOff(egg: Egg): void }) {
  useEffect(() => {
    for (const egg of ["glass", "vibe"] as const) document.body.classList.toggle(`egg-${egg}`, on.has(egg));
  }, [on]);
  useEffect(
    () => () => {
      document.body.classList.remove("egg-glass", "egg-vibe");
    },
    [],
  );
  if (on.size === 0) return null;
  return (
    <div className="eggs" role="status">
      {on.has("vibe") && <VibeBanner onOff={() => onOff("vibe")} />}
      {on.has("glass") && (
        <div className="eggs__card eggs__card--glass">
          <span>
            <strong>Glass mode.</strong> Approved by Ippo.
          </span>
          <button type="button" className="eggs__off" onClick={() => onOff("glass")} aria-label="Turn glass mode off">
            ✕
          </button>
        </div>
      )}
    </div>
  );
}

function VibeBanner({ onOff }: { onOff(): void }) {
  const [line, setLine] = useState(0);
  const [dodge, setDodge] = useState({ x: 0, y: 0 });
  const dodges = useRef(0);
  useEffect(() => {
    const timer = window.setInterval(() => setLine((l) => (l + 1) % VIBE_LINES.length), 2600);
    return () => window.clearInterval(timer);
  }, []);
  // The button runs from the pointer; after a few tries it gives up and
  // lets itself be pressed, which turns the mode off.
  const flee = () => {
    if (++dodges.current > 6) return;
    setDodge({ x: Math.round((Math.random() - 0.5) * 260), y: Math.round((Math.random() - 0.5) * 80) });
  };
  return (
    <div className="eggs__card eggs__card--vibe">
      <span className="eggs__vibe-title">✨ UI perfetta, generata in 3 minuti ✨</span>
      <span className="eggs__vibe-line">{VIBE_LINES[line]}</span>
      <button
        type="button"
        className="eggs__ship"
        style={{ transform: `translate(${dodge.x}px, ${dodge.y}px)` }}
        onMouseEnter={flee}
        onClick={onOff}
      >
        Ship it 🚀
      </button>
    </div>
  );
}
