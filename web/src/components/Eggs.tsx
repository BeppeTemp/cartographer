import { useEffect, useRef, useState, type CSSProperties } from "react";
import { TAKO_CONFETTI, TAKO_LINES, TAKO_TICKER, TAKO_TIPS, type Egg } from "../lib/eggs";

/**
 * What the easter eggs add to the page besides their stylesheet: a card for
 * each that says it is on, with a way off; in glass mode the disco lights and
 * the mirror ball; in tako mode the octopus mascot, the confetti, the ticker
 * and the button every vibe-coded app ships with.
 */
export function Eggs({ on, onOff }: { on: ReadonlySet<Egg>; onOff(egg: Egg): void }) {
  useEffect(() => {
    for (const egg of ["glass", "tako"] as const) document.body.classList.toggle(`egg-${egg}`, on.has(egg));
  }, [on]);
  useEffect(
    () => () => {
      document.body.classList.remove("egg-glass", "egg-tako");
    },
    [],
  );
  if (on.size === 0) return null;
  return (
    <>
      {on.has("glass") && (
        <>
          <div className="egg-lights" aria-hidden="true" />
          <div className="egg-disco" aria-hidden="true">
            <span className="egg-disco__ball" />
          </div>
        </>
      )}
      {on.has("tako") && <TakoParty />}
      <div className="eggs" role="status">
        {on.has("tako") && <TakoBanner onOff={() => onOff("tako")} />}
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
    </>
  );
}

function TakoBanner({ onOff }: { onOff(): void }) {
  const [line, setLine] = useState(0);
  const [dodge, setDodge] = useState({ x: 0, y: 0 });
  const dodges = useRef(0);
  useEffect(() => {
    const timer = window.setInterval(() => setLine((l) => (l + 1) % TAKO_LINES.length), 2600);
    return () => window.clearInterval(timer);
  }, []);
  // The button runs from the pointer; after a few tries it gives up and
  // lets itself be pressed, which turns the mode off.
  const flee = () => {
    if (++dodges.current > 6) return;
    setDodge({ x: Math.round((Math.random() - 0.5) * 260), y: Math.round((Math.random() - 0.5) * 80) });
  };
  return (
    <div className="eggs__card eggs__card--tako">
      <span className="eggs__tako-title">✨ UI perfetta, generata in 3 minuti ✨</span>
      <span className="eggs__tako-line">{TAKO_LINES[line]}</span>
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

/** The octopus mascot with its advice, the confetti and the ticker. */
function TakoParty() {
  const [tip, setTip] = useState(0);
  useEffect(() => {
    const timer = window.setInterval(() => setTip((t) => (t + 1) % TAKO_TIPS.length), 3200);
    return () => window.clearInterval(timer);
  }, []);
  return (
    <>
      <div className="egg-confetti" aria-hidden="true">
        {TAKO_CONFETTI.map((emoji, i) => (
          <span
            key={i}
            style={
              {
                "--x": `${(i * 73) % 100}%`,
                "--delay": `${(i * 0.53) % 4}s`,
                "--dur": `${4 + ((i * 7) % 5)}s`,
              } as CSSProperties
            }
          >
            {emoji}
          </span>
        ))}
      </div>
      <div className="tako-mascot" aria-hidden="true">
        <span className="tako-mascot__bubble" key={tip}>
          {TAKO_TIPS[tip]}
        </span>
        <span className="tako-mascot__octopus">
          🐙<span className="tako-mascot__shades">😎</span>
        </span>
      </div>
      <div className="egg-ticker" aria-hidden="true">
        <span>
          {TAKO_TICKER}
          {TAKO_TICKER}
        </span>
      </div>
    </>
  );
}
