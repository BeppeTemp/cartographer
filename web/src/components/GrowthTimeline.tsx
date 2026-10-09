import { useMemo, useRef, type KeyboardEvent, type PointerEvent } from "react";
import type { GrowthStep } from "../lib/graph3d/growth";
import { Icon } from "./Icon";

interface Props {
  order: GrowthStep[];
  /** How many steps are out. */
  shown: number;
  playing: boolean;
  /** Bring out exactly this many steps (scrubbing pauses the replay). */
  onSeek(shown: number): void;
  onTogglePlay(): void;
  onClose(): void;
}

const DAY_MS = 86_400_000;
const formatDay = (ms: number) =>
  new Date(ms).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });

/**
 * The growth replay's timeline: the KB's life from its first concept to its
 * last, a bar per day that saw concepts born (taller for more), the part
 * already replayed filled in, and the day being replayed under the playhead.
 * Placed by real time, so a quiet month passes in a blink and a busy day
 * stands out. The track is a slider: drag or click it to move through the
 * KB's history, the arrows step through it, Space plays and pauses.
 */
export function GrowthTimeline({ order, shown, playing, onSeek, onTogglePlay, onClose }: Props) {
  const trackRef = useRef<HTMLDivElement>(null);
  const { start, span, days, peak, times } = useMemo(() => {
    // Birth instants in replay order (non-decreasing; no history is last).
    const times = order.map((step) => (step.born ? Date.parse(step.born) : Number.POSITIVE_INFINITY));
    const counts = new Map<number, number>();
    for (const t of times) {
      if (!Number.isFinite(t)) continue;
      const day = Math.floor(t / DAY_MS) * DAY_MS;
      counts.set(day, (counts.get(day) ?? 0) + 1);
    }
    const keys = [...counts.keys()].sort((a, b) => a - b);
    const first = keys[0] ?? 0;
    const last = keys[keys.length - 1] ?? first;
    return {
      start: first,
      span: Math.max(DAY_MS, last + DAY_MS - first),
      days: keys.map((day) => ({ day, count: counts.get(day)! })),
      peak: Math.max(1, ...counts.values()),
      times,
    };
  }, [order]);

  if (days.length === 0) return null;
  const total = order.length;
  const out = Math.min(shown, total);
  const current = order[out - 1];
  // A concept with no history yet (not committed) is shown at the last day
  // the KB has a record of: it is the newest there is.
  const end = start + span - DAY_MS;
  const now = current?.born ? Date.parse(current.born) : end;
  const at = (ms: number) => `${(((ms - start) / span) * 100).toFixed(3)}%`;
  const progress = Math.min(1, Math.max(0, (current?.born ? now - start : span) / span));
  const label = formatDay(now);

  // A point on the track is a day: everything born by its end is out.
  const seekTo = (clientX: number) => {
    const rect = trackRef.current?.getBoundingClientRect();
    if (!rect || rect.width === 0) return;
    const ratio = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
    if (ratio >= 0.999) return onSeek(total);
    const until = Math.floor((start + ratio * span) / DAY_MS) * DAY_MS + DAY_MS;
    let count = 0;
    while (count < total && times[count]! < until) count++;
    onSeek(Math.max(1, count));
  };
  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    event.currentTarget.setPointerCapture(event.pointerId);
    seekTo(event.clientX);
  };
  const onPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) seekTo(event.clientX);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? 10 : 1;
    const to: Record<string, number> = {
      ArrowLeft: out - step,
      ArrowDown: out - step,
      ArrowRight: out + step,
      ArrowUp: out + step,
      Home: 1,
      End: total,
    };
    if (event.key === " ") {
      event.preventDefault();
      onTogglePlay();
    } else if (event.key in to) {
      event.preventDefault();
      onSeek(Math.min(total, Math.max(1, to[event.key]!)));
    }
  };

  return (
    <div className="growth-timeline" aria-label="Growth replay" role="group">
      <button
        type="button"
        className="button button--icon"
        onClick={onTogglePlay}
        aria-label={playing ? "Pause the replay" : "Play the replay"}
      >
        <Icon name={playing ? "pause" : "motion"} size={16} />
      </button>
      <div className="growth-timeline__body">
        <div
          ref={trackRef}
          className="growth-timeline__track"
          role="slider"
          tabIndex={0}
          aria-label="Replay position"
          aria-valuemin={1}
          aria-valuemax={total}
          aria-valuenow={out}
          aria-valuetext={`${label}, ${out} of ${total} concepts`}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onKeyDown={onKeyDown}
        >
          {days.map(({ day, count }) => (
            <span
              key={day}
              className="growth-timeline__bar"
              data-past={day <= now || undefined}
              style={{ left: at(day + DAY_MS / 2), height: `${Math.max(12, Math.sqrt(count / peak) * 100)}%` }}
            />
          ))}
          <span className="growth-timeline__fill" style={{ width: `${progress * 100}%` }} />
          <span className="growth-timeline__head" style={{ left: `${progress * 100}%` }} />
        </div>
        <div className="growth-timeline__scale">
          <span>{formatDay(start)}</span>
          <span className="growth-timeline__now">
            {label}
            <span className="growth-timeline__count">
              {out} / {total}
            </span>
          </span>
          <span>{formatDay(end)}</span>
        </div>
      </div>
      <button type="button" className="button button--icon" onClick={onClose} aria-label="Close the replay">
        <Icon name="close" size={16} />
      </button>
    </div>
  );
}
