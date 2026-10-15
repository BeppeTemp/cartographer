import { describe, expect, it } from "vitest";
import { GROWTH_LIVE_MS, GROWTH_VIDEO_MS, growthSchedule, shownAt, stepTime, type GrowthStep } from "../lib/graph3d/growth";

const DAY = 86_400_000;
const start = Date.UTC(2025, 0, 1);
/** `sizes[i]` concepts born on day `days[i]` (default: consecutive days). */
function steps(sizes: number[], days: number[] = sizes.map((_, i) => i)): GrowthStep[] {
  return sizes.flatMap((n, i) =>
    Array.from({ length: n }, (_, k) => ({ id: `c${i}-${k}`, born: new Date(start + days[i]! * DAY + k * 1000).toISOString() })),
  );
}
const w = (n: number) => Math.min(1500, 300 + 120 * Math.sqrt(n));

describe("growthSchedule", () => {
  it("makes one beat per birth day, and the unborn one final beat", () => {
    const order = [...steps([2, 3], [0, 5]), { id: "x" }, { id: "y" }];
    const { beats } = growthSchedule(order);
    expect(beats.map((b) => [b.at, b.n])).toEqual([[0, 2], [2, 3], [5, 2]]);
  });

  it("gives a beat w(n) = min(1500, 300 + 120 sqrt n) ms when the total is inside the window", () => {
    const sizes = [1, 4, 9, 400, 25, 16, ...Array(20).fill(1)];
    const natural = sizes.map(w);
    expect(natural[3]).toBe(1500);
    const sum = natural.reduce((a, b) => a + b, 0);
    expect(sum).toBeGreaterThan(GROWTH_LIVE_MS.lo);
    expect(sum).toBeLessThan(GROWTH_LIVE_MS.hi);
    const s = growthSchedule(steps(sizes), GROWTH_LIVE_MS);
    s.beats.forEach((b, i) => expect(b.ms).toBeCloseTo(natural[i]!));
    expect(s.totalMs).toBeCloseTo(sum);
  });

  it("scales a long replay down to the window's top, live and video", () => {
    const order = steps(Array(80).fill(3));
    expect(growthSchedule(order, GROWTH_LIVE_MS).totalMs).toBeCloseTo(40_000);
    expect(growthSchedule(order, GROWTH_VIDEO_MS).totalMs).toBeCloseTo(30_000);
  });

  it("scales a short replay up to the window's bottom, but never past twice its pace", () => {
    const mid = steps(Array(20).fill(1)); // 20 x 420 = 8.4 s -> 12 s
    expect(growthSchedule(mid, GROWTH_LIVE_MS).totalMs).toBeCloseTo(12_000);
    const tiny = steps([1, 1, 1]);
    expect(growthSchedule(tiny, GROWTH_LIVE_MS).totalMs).toBeCloseTo(2 * 3 * w(1));
  });

  it("groups by week, then by month, when days would be too short to read", () => {
    const order = steps(Array(400).fill(7));
    const live = growthSchedule(order, GROWTH_LIVE_MS);
    expect(live.totalMs).toBeLessThanOrEqual(40_000 + 1e-6);
    expect(Math.min(...live.beats.map((b) => b.ms))).toBeGreaterThanOrEqual(250);
    expect(live.beats.length).toBeLessThan(400);
    const video = growthSchedule(order, GROWTH_VIDEO_MS);
    expect(video.totalMs).toBeLessThanOrEqual(30_000 + 1e-6);
    expect(Math.min(...video.beats.map((b) => b.ms))).toBeGreaterThanOrEqual(250);
  });

  it("copes with 3000 concepts over 400 days", () => {
    const order = steps(Array.from({ length: 400 }, (_, i) => (i % 2 ? 8 : 7)));
    for (const [win, max] of [[GROWTH_LIVE_MS, 40_000], [GROWTH_VIDEO_MS, 30_000]] as const) {
      const s = growthSchedule(order, win);
      expect(s.totalMs).toBeLessThanOrEqual(max + 1e-6);
      expect(Math.min(...s.beats.map((b) => b.ms))).toBeGreaterThanOrEqual(250);
    }
  });

  it("falls back to months and stops there", () => {
    const order = steps(Array(3000).fill(1), Array.from({ length: 3000 }, (_, i) => i));
    const s = growthSchedule(order, GROWTH_VIDEO_MS);
    expect(s.beats.length).toBeLessThanOrEqual(100);
    expect(s.totalMs).toBeLessThanOrEqual(30_000 + 1e-6);
  });

  it("brings a real-ish KB (~250 concepts, 54 days, two bulk days) to about 20 s live", () => {
    const sizes = Array(54).fill(3);
    sizes[10] = 50;
    sizes[30] = 51;
    sizes[0] = 1;
        const order = steps(sizes);
    const s = growthSchedule(order, GROWTH_LIVE_MS);
    expect(s.totalMs).toBeGreaterThanOrEqual(12_000);
    expect(s.totalMs).toBeLessThanOrEqual(40_000);
    // a bulk day gets the long beat, a normal day a short one
    expect(s.beats[10]!.ms).toBeGreaterThan(2 * s.beats[11]!.ms);
    expect(s.times).toHaveLength(order.length);
  });

  it("reveals continuously: no pause past the clamp, neighbouring gaps change gradually, total kept", () => {
    // quiet days around two bulk days: the raw pace jumps ~25x at a boundary
    const sizes = Array(54).fill(3);
    sizes[10] = 50;
    sizes[30] = 51;
    const order = steps(sizes);
    const s = growthSchedule(order, GROWTH_LIVE_MS);
    const g = s.times.map((t, i) => (s.times[i + 1] ?? s.totalMs) - t);
    expect(s.times[0]).toBe(0);
    expect(g.reduce((a, b) => a + b, 0)).toBeCloseTo(s.totalMs);
    expect(Math.max(...g)).toBeLessThanOrEqual(300 + 1e-6);
    expect(Math.min(...g)).toBeGreaterThanOrEqual(34.9);
    for (let i = 1; i < g.length; i++) {
      const r = g[i]! / g[i - 1]!;
      expect(r).toBeLessThan(1.5);
      expect(r).toBeGreaterThan(1 / 1.5);
    }
  });

  it("shownAt is monotonic, starts with one concept and reaches every step at totalMs", () => {
    const order = steps([3, 1, 40, 2, 7], [0, 1, 2, 9, 10]);
    const s = growthSchedule(order);
    expect(shownAt(s, 0)).toBe(1);
    let prev = 0;
    for (let t = 0; t <= s.totalMs; t += 50) {
      const n = shownAt(s, t);
      expect(n).toBeGreaterThanOrEqual(prev);
      prev = n;
    }
    expect(shownAt(s, s.totalMs)).toBe(order.length);
    expect(shownAt(s, s.totalMs + 5_000)).toBe(order.length);
  });

  it("stepTime inverts shownAt: step i is out at its own time and not before", () => {
    const order = steps([3, 1, 40, 2], [0, 1, 2, 9]);
    const s = growthSchedule(order);
    for (let i = 0; i < order.length; i++) {
      expect(shownAt(s, stepTime(s, i))).toBe(i + 1);
      if (i > 0) expect(shownAt(s, stepTime(s, i) - 1e-3)).toBe(i);
    }
  });

  it("is empty for an empty order", () => {
    expect(growthSchedule([])).toEqual({ beats: [], totalMs: 0, times: [] });
    expect(shownAt(growthSchedule([]), 100)).toBe(0);
  });
});
