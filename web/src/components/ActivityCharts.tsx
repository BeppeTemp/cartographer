import { useMemo, useRef, useState, type CSSProperties } from "react";

/* The Activity page's summary pieces, shared by its Changes and Surveyor tabs.
   They reuse the app's own vocabulary: the Sources tiles (`.source-tile`) for
   the figures and the activity strip (`.activity-bars`) for time, inside the
   open hero every page has. Colour comes from the Maps' hue wheel, a hue per
   kind, shown as a dot and a figure in the tile and as a share of the strip:
   the tiles are the strip's legend. */

export type Figure = {
  label: string;
  value: string | number;
  tone?: "error" | "ok";
  title?: string;
  /** The kind's hue (a `--hue-n` of the wheel): its dot, its figure and its share of the strip. */
  hue?: string;
};

/** The five figures of a tab, as the Sources tiles: a label and a count. */
export function FigureTiles({ label, items }: { label: string; items: Figure[] }) {
  return (
    <ul className="source-tiles source-tiles--row" aria-label={label} style={{ "--tiles": items.length } as CSSProperties}>
      {items.map((f) => (
        <li
          key={f.label}
          className="source-tile"
          data-tone={f.tone}
          data-zero={f.value === 0 || f.value === "0" || undefined}
          title={f.title}
          data-hue={f.hue ? "" : undefined}
          style={f.hue ? ({ "--tile-hue": f.hue } as CSSProperties) : undefined}
        >
          <span className="source-tile__label">
            {f.hue && <span className="source-tile__dot" aria-hidden="true" />}
            {f.label}
          </span>
          <strong className="source-tile__count">{f.value}</strong>
        </li>
      ))}
    </ul>
  );
}

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

export type Point = { t: number; key: string };
/** A key of the strip: how its count reads in the caption, and its hue. */
export type StripKey = { key: string; word: string; hue: string };
type Bucket = { start: number; end: number; counts: Map<string, number>; total: number };
export type Buckets = { buckets: Bucket[]; step: "hour" | "day" | "week" | "month" };

function startOfDay(t: number): number {
  const d = new Date(t);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

/** Hours over a day, days up to a month and a half, weeks up to a year and a bit, then months. */
function makeBuckets(from: number, to: number): Buckets {
  const out: Bucket[] = [];
  const push = (start: number, end: number) => out.push({ start, end, counts: new Map(), total: 0 });
  if (to - from <= DAY + 60_000) {
    for (let t = Math.floor(to / HOUR) * HOUR - 23 * HOUR; t <= to; t += HOUR) push(t, t + HOUR);
    return { buckets: out, step: "hour" };
  }
  const days = Math.max(1, Math.round((startOfDay(to) - startOfDay(from)) / DAY) + 1);
  const step = days <= 45 ? 1 : days <= 400 ? 7 : 30;
  // Calendar arithmetic, the last bucket ending today.
  const today = startOfDay(to);
  for (let end = days; end > 0; end -= step) {
    const s = new Date(today);
    s.setDate(s.getDate() - (end - 1));
    const e = new Date(s);
    e.setDate(e.getDate() + Math.min(step, end));
    push(s.getTime(), e.getTime());
  }
  return { buckets: out, step: step === 1 ? "day" : step === 7 ? "week" : "month" };
}

/** The window cut into columns, the points counted in them. */
export function useBuckets(points: Point[], from: number): Buckets {
  return useMemo(() => {
    const now = Date.now();
    const oldest = points.length ? Math.min(...points.map((p) => p.t)) : now - 7 * DAY;
    // A window longer than a quarter starts at the oldest point in it: a year of
    // empty columns before a KB existed says nothing.
    const start =
      from > 0 ? (now - from > 92 * DAY ? Math.max(from, Math.min(oldest, now - 14 * DAY)) : from) : Math.min(oldest, now - 7 * DAY);
    const r = makeBuckets(start, now);
    const last = r.buckets[r.buckets.length - 1]!;
    for (const p of points) {
      const b = r.buckets.find((x) => p.t >= x.start && p.t < x.end) ?? (p.t >= last.start ? last : undefined);
      if (!b) continue;
      b.counts.set(p.key, (b.counts.get(p.key) ?? 0) + 1);
      b.total++;
    }
    return r;
  }, [points, from]);
}

function bucketLabel(b: Bucket, step: Buckets["step"]): string {
  const day = (t: number) => new Date(t).toLocaleDateString("en", { month: "short", day: "numeric" });
  if (step === "hour") return new Date(b.start).toLocaleTimeString("en", { hour: "2-digit", minute: "2-digit" });
  if (step === "day") return day(b.start);
  return `${day(b.start)} – ${day(b.end - DAY)}`;
}

/** The tooltip card's width, px: where it would overflow, it flips. */
const TIP_W = 168;

/** The height at x of a path drawn left to right: a binary search on its length. */
function yOn(path: SVGPathElement | undefined, x: number): number | null {
  if (!path || typeof path.getTotalLength !== "function") return null;
  let lo = 0;
  let hi = path.getTotalLength();
  for (let i = 0; i < 24; i++) {
    const mid = (lo + hi) / 2;
    if (path.getPointAtLength(mid).x < x) lo = mid;
    else hi = mid;
  }
  return path.getPointAtLength(hi).y;
}

/** A smooth path through points (monotone cubic, Fritsch–Carlson): no overshoot below zero. */
function smooth(pts: [number, number][]): string {
  const n = pts.length;
  if (n === 0) return "";
  if (n === 1) return `M${pts[0]![0]},${pts[0]![1]}`;
  const dx = (i: number) => pts[i + 1]![0] - pts[i]![0];
  const sl = pts.slice(0, -1).map((p, i) => (pts[i + 1]![1] - p[1]) / dx(i));
  const m = pts.map((_, i) => (i === 0 ? sl[0]! : i === n - 1 ? sl[n - 2]! : sl[i - 1]! * sl[i]! <= 0 ? 0 : (sl[i - 1]! + sl[i]!) / 2));
  for (let i = 0; i < n - 1; i++) {
    if (sl[i] === 0) {
      m[i] = 0;
      m[i + 1] = 0;
      continue;
    }
    const a = m[i]! / sl[i]!;
    const b = m[i + 1]! / sl[i]!;
    const h = a * a + b * b;
    if (h > 9) {
      const t = 3 / Math.sqrt(h);
      m[i] = t * a * sl[i]!;
      m[i + 1] = t * b * sl[i]!;
    }
  }
  let d = `M${pts[0]![0]},${pts[0]![1]}`;
  for (let i = 0; i < n - 1; i++) {
    const [x0, y0] = pts[i]!;
    const [x1, y1] = pts[i + 1]!;
    const h = (x1 - x0) / 3;
    d += `C${x0 + h},${y0 + m[i]! * h} ${x1 - h},${y1 - m[i + 1]! * h} ${x1},${y1}`;
  }
  return d;
}

/** The label a column carries on the axis: the hour, or the day ("Thu 13" when a week fits). */
function tickLabel(b: Bucket, step: Buckets["step"], n: number): string {
  const d = new Date(b.start);
  if (step === "hour") return d.toLocaleTimeString("en", { hour: "2-digit", minute: "2-digit" });
  if (step === "day" && n <= 10) return d.toLocaleDateString("en", { weekday: "short", day: "numeric" });
  return d.toLocaleDateString("en", { month: "short", day: "numeric" });
}

/**
 * Time as a soft wave: one smooth area per key, stacked, in the key's hue (the
 * same hue as its tile or chip above, which is the legend). Under it, every
 * column's date when they fit, else an even subset. The pointer draws a rule,
 * a dot on each line where the rule crosses it, and a label beside it with the
 * column it is over; the column changes where one ends (midnight, for days).
 */
export function WhenStrip({
  data,
  unit,
  keys,
}: {
  data: Buckets;
  /** The counted thing: ["run", "runs"]. */
  unit: [string, string];
  /** The keys in stacking order, bottom first. */
  keys: StripKey[];
}) {
  const [hover, setHover] = useState<number | null>(null);
  // The pointer's place on the plot, 0..1: the mark follows it smoothly, and
  // the column read out changes where one column ends (midnight, for days).
  const [at, setAt] = useState<number | null>(null);
  // The pointer in plot pixels, for the tooltip card that rides beside it.
  const [px, setPx] = useState<{ x: number; y: number; w: number } | null>(null);
  // Each line's path, to put a dot where the pointer crosses it.
  const lines = useRef(new Map<string, SVGPathElement>());
  const { buckets, step } = data;
  const n = buckets.length;
  const peak = Math.max(1, ...buckets.map((b) => b.total));
  const plural = (c: number) => (c === 1 ? unit[0] : unit[1]);
  const hb = hover === null ? null : buckets[hover]!;
  // The plot is 100 wide, 40 high; a column's point sits at its centre, and the
  // wave runs from edge to edge by repeating the first and last points.
  const W = 100;
  const H = 40;
  const x = (i: number) => ((i + 0.5) / n) * W;
  const y = (v: number) => H - (v / peak) * (H - 3);
  const areas: { key: StripKey; d: string; line: string }[] = [];
  const base = buckets.map(() => 0);
  for (const k of keys) {
    const top = buckets.map((b, i) => base[i]! + (b.counts.get(k.key) ?? 0));
    if (top.every((v, i) => v === base[i])) continue;
    const upper: [number, number][] = top.map((v, i) => [x(i), y(v)]);
    const lower: [number, number][] = base.map((v, i) => [x(i), y(v)]);
    const ext = (p: [number, number][]): [number, number][] => [[0, p[0]![1]], ...p, [W, p[p.length - 1]![1]]];
    const up = smooth(ext(upper));
    const lowRev = smooth(ext(lower).reverse());
    areas.push({ key: k, d: `${up}L${lowRev.slice(1)}Z`, line: up });
    top.forEach((v, i) => (base[i] = v));
  }
  const every = Math.max(1, Math.ceil(n / (step === "hour" ? 8 : 12)));
  return (
    <figure
      className="activity-strip"
      onMouseLeave={() => {
        setHover(null);
        setAt(null);
        setPx(null);
      }}
    >
      <div
        className="activity-strip__plot"
        onMouseMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          const f = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
          setAt(f);
          setPx({ x: e.clientX - r.left, y: e.clientY - r.top, w: r.width });
          setHover(Math.min(n - 1, Math.floor(f * n)));
        }}
      >
        <svg className="activity-strip__wave" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" aria-hidden="true">
          {areas.map((a) => (
            <g key={a.key.key} style={{ "--seg": a.key.hue } as CSSProperties}>
              <path className="activity-strip__area" d={a.d} />
              <path
                className="activity-strip__line"
                d={a.line}
                vectorEffect="non-scaling-stroke"
                ref={(el) => {
                  if (el) lines.current.set(a.key.key, el);
                  else lines.current.delete(a.key.key);
                }}
              />
            </g>
          ))}
        </svg>
        {at !== null && <span className="activity-strip__mark" style={{ left: `${at * 100}%` }} aria-hidden="true" />}
        {at !== null &&
          areas.map((a) => {
            const y = yOn(lines.current.get(a.key.key), at * W);
            return y === null ? null : (
              <span
                key={a.key.key}
                className="activity-strip__dot"
                aria-hidden="true"
                style={{ left: `${at * 100}%`, top: `${(y / H) * 100}%`, "--seg": a.key.hue } as CSSProperties}
              />
            );
          })}
        {hb && px && (
          // A dashboard's tooltip: a card beside the pointer, the time on top,
          // one row per series with its swatch, name and value, the total last.
          // It sits right of the pointer and goes left only where it would run
          // off the plot.
          <div
            className="activity-strip__tip"
            data-flip={px.x + TIP_W + 16 > px.w || undefined}
            style={{ left: px.x, top: px.y }}
            role="status"
          >
            <div className="activity-strip__tip-when">{bucketLabel(hb, step)}</div>
            {keys
              .filter((k) => (hb.counts.get(k.key) ?? 0) > 0)
              .map((k) => (
                <div key={k.key} className="activity-strip__tip-row" style={{ "--seg": k.hue } as CSSProperties}>
                  <span className="activity-strip__tip-swatch" aria-hidden="true" />
                  <span className="activity-strip__tip-name">{k.word}</span>
                  <span className="activity-strip__tip-value">{hb.counts.get(k.key)}</span>
                </div>
              ))}
            <div className="activity-strip__tip-row activity-strip__tip-total">
              <span className="activity-strip__tip-swatch" aria-hidden="true" />
              <span className="activity-strip__tip-name">Total</span>
              <span className="activity-strip__tip-value">{hb.total}</span>
            </div>
          </div>
        )}
        <ol className="sr-only" aria-label={`${unit[1]} per ${step}`}>
          {buckets.map((b) => (
            <li key={b.start}>{`${bucketLabel(b, step)}: ${b.total} ${plural(b.total)}`}</li>
          ))}
        </ol>
      </div>
      <ol className="activity-strip__ticks" aria-hidden="true">
        {buckets.map((b, i) => (
          <li key={b.start} data-on={hover === i || undefined}>
            {(n - 1 - i) % every === 0 ? tickLabel(b, step, n) : ""}
          </li>
        ))}
      </ol>
    </figure>
  );
}
