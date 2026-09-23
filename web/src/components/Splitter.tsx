import { useEffect, useRef, type KeyboardEvent, type PointerEvent } from "react";

const STEP = 16;
const BIG_STEP = 64;

/**
 * A vertical window splitter (WAI-ARIA separator pattern) that sizes one panel
 * in px. `edge` is the side of that panel the handle sits on: on its start
 * edge, moving the handle left widens the panel. The keys move the handle, not
 * the value: Arrow Left/Right by 16px (Shift: 64px), Home/End to the far
 * left/right; Enter or a double-click restore the default.
 *
 * `onChange` follows the drag; `onCommit` fires once the value is settled
 * (pointer up, a key, a reset) and is the only one a caller should persist.
 */
export function Splitter({
  value,
  min,
  max,
  defaultValue,
  edge,
  controls,
  label,
  onChange,
  onCommit,
}: {
  value: number;
  min: number;
  max: number;
  defaultValue: number;
  edge: "start" | "end";
  controls: string;
  label: string;
  onChange(px: number): void;
  onCommit(px: number): void;
}) {
  const drag = useRef<{ x: number; from: number; last: number } | null>(null);
  // A handle moved right grows a panel it ends, shrinks one it starts.
  const sign = edge === "start" ? -1 : 1;
  // A drag cut short by an unmount must not leave the page unselectable.
  useEffect(() => () => document.body.classList.remove("is-resizing"), []);
  const clamp = (px: number) => Math.round(Math.min(Math.max(px, min), max));

  const commit = (px: number) => {
    const next = clamp(px);
    onChange(next);
    onCommit(next);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const step = e.shiftKey ? BIG_STEP : STEP;
    // Home/End move the handle to the far left/right.
    const leftmost = edge === "start" ? max : min;
    const rightmost = edge === "start" ? min : max;
    let next: number;
    switch (e.key) {
      case "ArrowLeft":
        next = value - sign * step;
        break;
      case "ArrowRight":
        next = value + sign * step;
        break;
      case "Home":
        next = leftmost;
        break;
      case "End":
        next = rightmost;
        break;
      case "Enter":
        next = defaultValue;
        break;
      default:
        return;
    }
    e.preventDefault();
    commit(next);
  };

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.currentTarget.setPointerCapture?.(e.pointerId);
    drag.current = { x: e.clientX, from: value, last: value };
    document.body.classList.add("is-resizing");
  };

  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d) return;
    d.last = clamp(d.from + sign * (e.clientX - d.x));
    onChange(d.last);
  };

  const onPointerEnd = () => {
    const d = drag.current;
    if (!d) return;
    drag.current = null;
    document.body.classList.remove("is-resizing");
    onCommit(d.last);
  };

  return (
    <div
      className={`splitter splitter--${edge}`}
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-controls={controls}
      aria-valuenow={value}
      aria-valuemin={min}
      aria-valuemax={max}
      tabIndex={0}
      onKeyDown={onKeyDown}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerEnd}
      onPointerCancel={onPointerEnd}
      onDoubleClick={() => commit(defaultValue)}
    />
  );
}
