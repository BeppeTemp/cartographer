import { useEffect, useRef, useState } from "react";
import { prefersReducedMotion } from "../lib/theme";

const COUNT_MS = 450;

/**
 * A number that runs from its previous value to the new one instead of
 * jumping, so a KB switch or a refresh reads as the count changing. Reduced
 * motion shows the new value at once.
 */
export function Count({ value }: { value: number }) {
  const [shown, setShown] = useState(value);
  const from = useRef(value);
  useEffect(() => {
    const start = from.current;
    from.current = value;
    if (start === value || prefersReducedMotion()) {
      setShown(value);
      return;
    }
    const t0 = performance.now();
    let frame = requestAnimationFrame(function step(now) {
      const t = Math.min(1, (now - t0) / COUNT_MS);
      const k = 1 - (1 - t) ** 3;
      setShown(Math.round(start + (value - start) * k));
      if (t < 1) frame = requestAnimationFrame(step);
    });
    return () => cancelAnimationFrame(frame);
  }, [value]);
  return <>{shown}</>;
}
