/** One step of the growth replay: a concept and when it entered the KB. */
export interface GrowthStep {
  id: string;
  /** RFC 3339; absent for a concept with no history, which comes last. */
  born?: string;
}

interface GrowthNode {
  id: string;
  pagerank?: number;
}

interface GrowthEdge {
  source: string;
  target: string;
}

/**
 * The order the replay brings the concepts in: by birth, oldest first. The
 * concepts born in the same commit -- an import lands dozens at once -- grow
 * along their links instead of all at once: first those touching what is
 * already there, then outward from them, and a part linked to nothing yet
 * starts from its best-ranked concept.
 */
export function growthOrder(nodes: GrowthNode[], edges: GrowthEdge[], births: Record<string, string>): GrowthStep[] {
  const adjacent = new Map<string, string[]>(nodes.map((n) => [n.id, []]));
  for (const e of edges) {
    adjacent.get(e.source)?.push(e.target);
    adjacent.get(e.target)?.push(e.source);
  }
  const rank = new Map(nodes.map((n) => [n.id, n.pagerank ?? 0]));
  const byRank = (a: string, b: string) => rank.get(b)! - rank.get(a)! || a.localeCompare(b);

  // Batches by birth instant, oldest first; no history is the last batch.
  const batches = new Map<string, string[]>();
  for (const n of nodes) {
    const key = births[n.id] ?? "";
    (batches.get(key) ?? batches.set(key, []).get(key)!).push(n.id);
  }
  const keys = [...batches.keys()].sort((a, b) => (a === "" ? 1 : b === "" ? -1 : Date.parse(a) - Date.parse(b)));

  const placed = new Set<string>();
  const order: GrowthStep[] = [];
  for (const key of keys) {
    const pending = new Set(batches.get(key)!);
    const queue = [...pending].filter((id) => adjacent.get(id)!.some((m) => placed.has(m))).sort(byRank);
    while (pending.size) {
      if (!queue.length) queue.push([...pending].sort(byRank)[0]!);
      const id = queue.shift()!;
      if (!pending.delete(id)) continue;
      placed.add(id);
      order.push(key ? { id, born: key } : { id });
      for (const m of adjacent.get(id)!) if (pending.has(m)) queue.push(m);
    }
  }
  return order;
}

/** The replay never redraws the graph more often than this (about 30 a
 *  second): a stretch denser than that brings two concepts out per redraw. */
export const GROWTH_TICK_MIN_MS = 33;

/** The replay's window, in ms: live, and in the exported video. */
export const GROWTH_LIVE_MS = { lo: 12_000, hi: 40_000 };
export const GROWTH_VIDEO_MS = { lo: 12_000, hi: 30_000 };

/** One beat of the replay: the concepts born on the same day. */
export interface GrowthBeat {
  /** Index of its first step in the order, and how many steps it holds. */
  at: number;
  n: number;
  start: number;
  end: number;
  ms: number;
}

export interface GrowthSchedule {
  beats: GrowthBeat[];
  totalMs: number;
  /** When each step comes out, ms from the start; the first at 0. */
  times: number[];
}

/** Concepts come out continuously: the gap before the next is the beat's
 *  ms / n, smoothed over this many neighbours so the pace changes gradually
 *  between a bulk day and a quiet one, and kept within these bounds (the
 *  total wins where a KB is too small or too big for them to hold). */
const SMOOTH_WINDOW = 10;
const MIN_GAP_MS = 35;
const MAX_GAP_MS = 300;

function gaps(beats: GrowthBeat[], total: number): number[] {
  const raw = beats.flatMap((b) => Array<number>(b.n).fill(b.ms / b.n));
  const n = raw.length;
  const prefix = [0];
  for (const g of raw) prefix.push(prefix[prefix.length - 1]! + g);
  const half = SMOOTH_WINDOW >> 1;
  let g = raw.map((_, i) => {
    const a = Math.max(0, i - half);
    const b = Math.min(n, i + half + 1);
    return (prefix[b]! - prefix[a]!) / (b - a);
  });
  for (let pass = 0; pass < 6; pass++) {
    g = g.map((x) => Math.min(MAX_GAP_MS, Math.max(MIN_GAP_MS, x)));
    const k = total / g.reduce((a, b) => a + b, 0);
    g = g.map((x) => x * k);
  }
  return g;
}
/** Below this a beat is too short to read: the beats are grouped coarser. */
const MIN_BEAT_MS = 250;
/** A tiny KB is never slowed past this many times its natural pace. */
const MAX_SLOWDOWN = 2;

const dayOf = (born: string): string => {
  const t = Date.parse(born);
  return Number.isNaN(t) ? born.slice(0, 10) : new Date(t).toISOString().slice(0, 10);
};
/** ISO week key (the week's Thursday decides the year), from a YYYY-MM-DD day. */
const weekOf = (day: string): string => {
  const d = new Date(`${day}T00:00:00Z`);
  const dow = d.getUTCDay() || 7;
  d.setUTCDate(d.getUTCDate() + 4 - dow);
  const jan1 = Date.UTC(d.getUTCFullYear(), 0, 1);
  const week = Math.ceil(((d.getTime() - jan1) / 86_400_000 + 1) / 7);
  return `${d.getUTCFullYear()}-W${week}`;
};
const GROUPINGS: ((day: string) => string)[] = [(day) => day, weekOf, (day) => day.slice(0, 7)];

/** Runs of consecutive steps with the same key; steps with no birth are one final run. */
function runs(order: GrowthStep[], key: (day: string) => string): number[] {
  const sizes: number[] = [];
  let last: string | null = null;
  for (const step of order) {
    const k = step.born ? key(dayOf(step.born)) : "";
    if (k === last) sizes[sizes.length - 1]!++;
    else sizes.push(1);
    last = k;
  }
  return sizes;
}

/**
 * The replay's timeline: one beat per birth day, so a bulk import is one beat
 * and a quiet week is several. A beat of n concepts lasts 300 + 120 sqrt(n) ms
 * (at most 1500); the whole is scaled into `[lo, hi]` ms (a KB too small to
 * reach `lo` is slowed at most twice). If that leaves a beat under 250 ms the
 * days are grouped by ISO week, and if still, by month. The beats only fix
 * the total and the pace of each stretch: the concepts themselves come out one
 * by one with no hold, at a pace smoothed across beat boundaries. Live and
 * the exported video both read this, with their own window.
 */
export function growthSchedule(order: GrowthStep[], window: { lo: number; hi: number } = GROWTH_LIVE_MS): GrowthSchedule {
  let sizes: number[] = [];
  let scale = 1;
  for (const [i, key] of GROUPINGS.entries()) {
    sizes = runs(order, key);
    const natural = sizes.map((n) => Math.min(1500, 300 + 120 * Math.sqrt(n)));
    const total = natural.reduce((a, b) => a + b, 0);
    scale = total > window.hi ? window.hi / total : total < window.lo ? Math.min(window.lo / total, MAX_SLOWDOWN) : 1;
    if (!natural.length || Math.min(...natural) * scale >= MIN_BEAT_MS || i === GROUPINGS.length - 1) {
      let start = 0;
      let at = 0;
      const beats = sizes.map((n, j) => {
        const ms = natural[j]! * scale;
        const beat = { at, n, start, end: start + ms, ms };
        start += ms;
        at += n;
        return beat;
      });
      const times: number[] = [];
      let t = 0;
      for (const g of gaps(beats, start)) {
        times.push(t);
        t += g;
      }
      return { beats, totalMs: start, times };
    }
  }
  return { beats: [], totalMs: 0, times: [] };
}

/** The time, in ms, at which step `index` (0-based) comes out. */
export function stepTime(schedule: GrowthSchedule, index: number): number {
  return schedule.times[Math.min(Math.max(0, index), schedule.times.length - 1)] ?? 0;
}

/** How many steps are out `tMs` into the replay: the first is out at 0. */
export function shownAt(schedule: GrowthSchedule, tMs: number): number {
  const { times } = schedule;
  if (tMs < 0) return 0;
  let lo = 0;
  let hi = times.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (times[mid]! <= tMs + 1e-9) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

/** The closest the replay's camera comes, as a share of the whole graph's
 *  fit: low enough to follow a KB as it grows, high enough that a handful of
 *  first concepts is not blown up. */
export const GROWTH_MIN_ZOOM = 0.6;
