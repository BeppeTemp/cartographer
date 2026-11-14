import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.unmock("../lib/graph3d/scene");
vi.mock("three", async (original) => {
  const three = await original<typeof import("three")>();
  class FakeRenderer {
    domElement = document.createElement("canvas");
    setPixelRatio() {}
    setSize() {}
    setClearColor() {}
    render() {}
    dispose() {}
    getContext() {
      return {};
    }
  }
  return { ...three, WebGLRenderer: FakeRenderer };
});

import { LivingScene, type SceneLink, type SceneNode } from "../lib/graph3d/scene";

// Each test steps a full simulation hundreds of times: about 1.5 s on a laptop, over 5 s (vitest's default) on a loaded
// train gate, which rejected the change once for a timeout alone.
const SIM_TIMEOUT = 30_000;

const callbacks = { onSelect() {}, onHover() {}, onLost() {} };
type Internals = { step(now: number, ticks?: number, force?: boolean): void; sim: { alpha(): number }; bloom: unknown; nodes: SceneNode[] };

/** A deterministic KB: `glyphs` folded clusters, `loose` concepts each tied to one, super-edges between glyphs. */
function clustered(glyphs: number, loose: number): { nodes: SceneNode[]; links: SceneLink[] } {
  let seed = 11;
  const rnd = () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 2 ** 32);
  const nodes: SceneNode[] = [];
  const links: SceneLink[] = [];
  for (let i = 0; i < glyphs; i++) nodes.push({ id: `c:g${i}`, weight: 0.2 + 0.8 * rnd(), cluster: true, x: (rnd() - 0.5) * 100, y: (rnd() - 0.5) * 100, z: (rnd() - 0.5) * 100 });
  for (let i = 0; i < glyphs; i++) for (let k = 0; k < 2; k++) links.push({ source: `c:g${i}`, target: `c:g${Math.floor(rnd() * glyphs)}` });
  for (let i = 0; i < loose; i++) nodes.push({ id: `l${i}`, weight: 0.1, anchor: `c:g${i % glyphs}`, x: (rnd() - 0.5) * 100, y: (rnd() - 0.5) * 100, z: (rnd() - 0.5) * 100 });
  return { nodes, links: links.filter((l) => l.source !== l.target) };
}
function flat(n: number): { nodes: SceneNode[]; links: SceneLink[] } {
  let seed = 5;
  const rnd = () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 2 ** 32);
  const nodes = Array.from({ length: n }, (_, i): SceneNode => ({ id: `n${i}`, weight: rnd() * 0.5, x: (rnd() - 0.5) * 100, y: (rnd() - 0.5) * 100, z: (rnd() - 0.5) * 100 }));
  const links: SceneLink[] = [];
  for (let i = 1; i < n; i++) for (let k = 0; k < 3; k++) links.push({ source: `n${i}`, target: `n${Math.floor(rnd() * i)}` });
  return { nodes, links };
}

describe("a clustered entrance is as calm as a flat one", () => {
  const sizes = { clientWidth: 800, clientHeight: 600 };
  beforeEach(() => {
    // The warm-up stops on a wall-clock budget: under a loaded machine it would run fewer ticks and the entrance
    // would differ from run to run. A frozen clock runs it whole; the ticks below carry their own `now`.
    vi.spyOn(performance, "now").mockReturnValue(1000);
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} unobserve() {} });
    for (const [k, v] of Object.entries(sizes)) Object.defineProperty(HTMLElement.prototype, k, { configurable: true, get: () => v });
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    for (const k of Object.keys(sizes)) delete (HTMLElement.prototype as unknown as Record<string, unknown>)[k];
  });

  /** Mean per-tick displacement of the nodes, over `ticks` ticks of the entrance. */
  function profile(data: { nodes: SceneNode[]; links: SceneLink[] }, ticks: number, live: boolean): { d: number[]; calls: number } {
    const scene = new LivingScene(document.createElement("div"), callbacks, { live, reducedMotion: false });
    const inner = scene as unknown as Internals;
    let calls = 0;
    const set = scene.setData.bind(scene);
    scene.setData = (...a: Parameters<typeof set>) => {
      calls++;
      return set(...a);
    };
    scene.setData(data.nodes, data.links, 60, true, {});
    const prev = inner.nodes.map((n) => [n.x ?? 0, n.y ?? 0, n.z ?? 0]);
    const d: number[] = [];
    let now = performance.now();
    for (let t = 0; t < ticks; t++) {
      now += 1000 / 60;
      inner.step(now, 1, true);
      let sum = 0;
      inner.nodes.forEach((n, i) => {
        sum += Math.hypot((n.x ?? 0) - prev[i]![0]!, (n.y ?? 0) - prev[i]![1]!, (n.z ?? 0) - prev[i]![2]!);
        prev[i] = [n.x ?? 0, n.y ?? 0, n.z ?? 0];
      });
      d.push(sum / inner.nodes.length);
    }
    scene.dispose();
    return { d, calls };
  }

  /** The overview as the user sees it: a handful of big glyphs and the concepts tied to them. */
  function settled(glyphs: number, loose: number) {
    const data = clustered(glyphs, loose);
    const scene = new LivingScene(document.createElement("div"), callbacks, { live: true, reducedMotion: false });
    const inner = scene as unknown as Internals;
    scene.setData(data.nodes, data.links, 150, true, {});
    let now = performance.now();
    for (let t = 0; t < 600; t++) inner.step((now += 1000 / 60), 1, true);
    const nodes = inner.nodes;
    const d = (a: SceneNode, b: SceneNode) => Math.hypot(a.x! - b.x!, a.y! - b.y!, a.z! - b.z!);
    const gl = nodes.filter((n) => n.cluster);
    let gap = Infinity;
    for (const a of gl) for (const b of gl) if (a !== b) gap = Math.min(gap, d(a, b) - scene.radiusOf(a) - scene.radiusOf(b));
    let covered = 0;
    for (const n of nodes) if (!n.cluster) for (const g of gl) if (d(n, g) < scene.radiusOf(g)) covered++;
    scene.dispose();
    return { gap, covered };
  }

  it("keeps the glyphs apart and the concepts off them", () => {
    for (const [g, l] of [[8, 100], [40, 160]] as const) {
      const { gap, covered } = settled(g, l);
      console.info(`overview ${g} glyphs, ${l} loose: min glyph gap ${gap.toFixed(1)}, concepts covering a glyph ${covered}`);
      expect(gap).toBeGreaterThan(10);
      expect(covered).toBe(0);
    }
  }, SIM_TIMEOUT);

  it("enters as calmly as a flat KB: one setData, displacement falling after the first ticks", () => {
    for (const data of [flat(200), clustered(40, 160)]) {
      const { d, calls } = profile(data, 240, true);
      expect(calls).toBe(1);
      // Mean displacement per tick: after the first 30 ticks it never rises by more than 15% (or a hair, at rest) from one tick to the next.
      for (let i = 31; i < d.length; i++) expect(d[i]!).toBeLessThanOrEqual(d[i - 1]! * 1.15 + 0.005);
      expect(d[239]!).toBeLessThanOrEqual(d[30]!);
    }
  }, SIM_TIMEOUT);
});
