import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const renders = vi.hoisted(() => ({ count: 0 }));
vi.unmock("../lib/graph3d/scene");
vi.mock("three", async (original) => {
  const three = await original<typeof import("three")>();
  class FakeRenderer {
    domElement = document.createElement("canvas");
    setPixelRatio() {}
    setSize() {}
    setClearColor() {}
    render() {
      renders.count++;
    }
    dispose() {}
    getContext() {
      return {};
    }
  }
  return { ...three, WebGLRenderer: FakeRenderer };
});

import { LivingScene, type SceneNode } from "../lib/graph3d/scene";
import { radiusOf } from "../lib/graph3d/clusterlayout";

const callbacks = { onSelect() {}, onHover() {}, onLost() {} };
type Internals = {
  tween: unknown;
  fitFollow: unknown;
  controls: { enableDamping: boolean };
  step(now: number, ticks?: number, force?: boolean): void;
};
const node = (id: string, x: number, extra: Partial<SceneNode> = {}): SceneNode => ({ id, weight: 0.4, x, y: 0, z: 0, ...extra });

describe("graph motion", () => {
  const sizes = { clientWidth: 800, clientHeight: 600 };
  beforeEach(() => {
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} unobserve() {} });
    for (const [k, v] of Object.entries(sizes)) Object.defineProperty(HTMLElement.prototype, k, { configurable: true, get: () => v });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    for (const k of Object.keys(sizes)) delete (HTMLElement.prototype as unknown as Record<string, unknown>)[k];
  });

  const make = (live: boolean, reducedMotion: boolean) => {
    const scene = new LivingScene(document.createElement("div"), callbacks, { live, reducedMotion });
    scene.setData([node("a", 0, { cluster: true }), node("b", 60), node("c", -60)], [], 5, false, {});
    return { scene, inner: scene as unknown as Internals };
  };

  it("keeps one camera animation: a move replaces the one in flight and ends a follow", () => {
    const { scene, inner } = make(true, false);
    scene.followFit(0, 0, Infinity);
    expect(inner.fitFollow).not.toBeNull();
    scene.zoomBy(0.5);
    expect(inner.fitFollow).toBeNull();
    const first = inner.tween;
    expect(first).not.toBeNull();
    scene.zoomBy(2);
    expect(inner.tween).not.toBe(first);
    expect(inner.tween).not.toBeNull();
    scene.dispose();
  });

  it("under reduced motion the camera jumps, nothing turns and inertia is off", () => {
    const { scene, inner } = make(true, true);
    scene.zoomBy(0.5);
    expect(inner.tween).toBeNull();
    scene.fitEverything(0);
    expect(inner.tween).toBeNull();
    expect(inner.controls.enableDamping).toBe(false);
    inner.step(1000, 1, true);
    inner.step(3000, 1, true);
        scene.dispose();
  });

  it("keeps glyphs still: no rotation, whatever the time and the motion", () => {
    const { scene, inner } = make(true, false);
    // Only the diagonal (scale) and the translation may be non-zero in a glyph's matrix: no rotation.
    const rotation = () => {
      const m = (scene as unknown as { haloMesh: { instanceMatrix: { array: Float32Array } } }).haloMesh.instanceMatrix.array;
      return [1, 2, 4, 6, 8, 9].map((i) => m[i]);
    };
    for (const now of [1000, 3000, 9000]) {
      inner.step(now, 1, true);
      expect(rotation()).toEqual([0, 0, 0, 0, 0, 0]);
    }
    scene.dispose();
  });

  it("draws a glyph clearly larger than a concept, never dominating", () => {
    const concept = radiusOf({ id: "c", weight: 0.3 });
    // Weights start at .35: a glyph is 1.6x an average concept at the least, 2x at the most.
    expect(radiusOf({ id: "g", weight: 0.35, cluster: true })).toBeGreaterThanOrEqual(1.55 * concept);
    expect(radiusOf({ id: "g", weight: 1, cluster: true })).toBeLessThanOrEqual(2.1 * concept);
  });

  describe("a cluster opens and folds by the simulation", () => {
    type Open = {
      sim: { alpha(): number; alphaDecay(): number };
      tween: unknown;
      fitFollow: unknown;
      morph: unknown;
      hulls: { born: number; dying: number | null; ms: number }[];
    };
    const glyphs = Array.from({ length: 6 }, (_, i) => node(`c:g${i}`, Math.cos(i) * 80, { cluster: true, weight: 0.8 }));
    const members = (tag: string, at: SceneNode) => Array.from({ length: 30 }, (_, i) => node(`${tag}${i}`, (at.x ?? 0) + i * 2, { weight: 0.3 }));
    const parts = (scene: LivingScene) => scene as unknown as Open;
    const open = (scene: LivingScene, g: SceneNode, tag: string) => {
      const m = members(tag, g);
      scene.setData([...glyphs.filter((x) => x !== g), ...m], [], 10, false, { local: new Set([`${tag}0`]), hulls: [{ id: g.id, members: m.map((x) => x.id) }] });
      return m;
    };

    it("puts the members at the glyph and reheats the simulation: no precomputed move, one camera follow", () => {
      const scene = new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: false });
      scene.setData(glyphs, [], 10, false, {});
      const g2 = glyphs[2]!;
      const at = { x: g2.x!, y: g2.y!, z: g2.z! };
      const m2 = open(scene, g2, "a");
      // Spread over the glyph they replace (its radius 1.6 times, each axis), never beyond it.
      const glyph = (scene as unknown as { hulls: { glyph: number }[] }).hulls[0]!.glyph;
      for (const m of m2) expect(Math.hypot(m.x! - at.x, m.y! - at.y, m.z! - at.z)).toBeLessThan(glyph * 1.6);
      const p = parts(scene);
      expect(p.sim.alpha()).toBeGreaterThan(0.2);
      expect(p.sim.alpha()).toBeLessThanOrEqual(0.3);
      expect(p.tween).toBeNull();
      expect(p.fitFollow).not.toBeNull();
      // The hull and the members' growth share one length: the opening beat.
      expect(p.hulls[0]!.ms).toBe(520);
      scene.dispose();
    });

    it("lets the rest of the graph stay where it is at the first frame, and folds on the shorter beat", () => {
      const scene = new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: false });
      scene.setData(glyphs, [], 10, false, {});
      const others = glyphs.filter((g) => g !== glyphs[2]).map((g) => [g.id, g.x, g.y, g.z] as const);
      const g2 = glyphs[2]!;
      const m2 = open(scene, g2, "a");
      const nodes = (scene as unknown as { nodes: SceneNode[] }).nodes;
      for (const [id, x, y, z] of others) {
        const n = nodes.find((v) => v.id === id)!;
        expect(Math.hypot(n.x! - x!, n.y! - y!, n.z! - z!)).toBeLessThan(1e-6);
      }
      expect(m2.length).toBe(30);
      scene.setData(glyphs, [], 10, false, { local: new Set([g2.id]), hulls: [] });
      expect(parts(scene).hulls.find((h) => h.dying !== null)!.ms).toBe(360);
      scene.dispose();
    });

    it("is instant under reduced motion: laid out at once, nothing left to animate", () => {
      const scene = new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: true });
      scene.setData(glyphs, [], 10, false, {});
      open(scene, glyphs[2]!, "a");
      const p = parts(scene);
      expect(p.morph).toBeNull();
      expect(p.tween).toBeNull();
      expect(p.sim.alpha()).toBe(0);
      expect(p.hulls.every((h) => h.dying === null)).toBe(true);
      scene.dispose();
    });

    it("cools back to the normal decay once the opening has settled", () => {
      const scene = new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: false });
      scene.setData(glyphs, [], 10, false, {});
      open(scene, glyphs[2]!, "a");
      const inner = scene as unknown as Internals & Open;
      let now = performance.now();
      for (let i = 0; i < 200; i++) inner.step((now += 16), 1, true);
      expect(inner.sim.alpha()).toBeLessThan(0.01);
      expect(inner.sim.alphaDecay()).toBeCloseTo(1 - 0.001 ** (1 / 300), 4);
      scene.dispose();
    });
  });

  it("breathes when idle like a flat KB: the same drift, the same frames", () => {
    const frames = (clustered: boolean) => {
      const scene = new LivingScene(document.createElement("div"), callbacks, { live: true, reducedMotion: false });
      const nodes = Array.from({ length: 4 }, (_, i) => node(`c:g${i}`, Math.cos(i) * 80, clustered ? { cluster: true, weight: 0.8 } : { weight: 0.8 }));
      scene.setData(nodes, [{ source: "c:g0", target: "c:g1" }], 30, true, {});
      const inner = scene as unknown as Internals;
      let now = performance.now();
      for (let t = 0; t < 900; t++) inner.step((now += 1000 / 60), 1);
      const before = renders.count;
      for (let t = 0; t < 300; t++) inner.step((now += 1000 / 60), 1);
      const drawn = renders.count - before;
      scene.dispose();
      return drawn;
    };
    const flat = frames(false);
    const clustered = frames(true);
    expect(flat).toBeGreaterThan(100);
    expect(clustered).toBeGreaterThan(100);
    expect(clustered).toBeLessThanOrEqual(flat * 1.1);
  });

  it("enters a clustered KB without holding the simulation warm", () => {
    const flatScene = new LivingScene(document.createElement("div"), callbacks, { live: true, reducedMotion: false });
    flatScene.setData([node("a", 0), node("b", 50), node("c", -50)], [], 20, true, {});
    const flat = flatScene as unknown as { sim: { alphaTarget(): number } };
    const clustered = new LivingScene(document.createElement("div"), callbacks, { live: true, reducedMotion: false });
    clustered.setData(Array.from({ length: 4 }, (_, i) => node(`c:g${i}`, i * 60, { cluster: true })), [], 20, true, {});
    // Not held warm for the burst (flat KBs are); only the gentle drift's own target.
    expect((clustered as unknown as { sim: { alphaTarget(): number } }).sim.alphaTarget()).toBeLessThan(0.05);
    expect(flat.sim.alphaTarget()).toBeGreaterThan(0.05);
    clustered.dispose();
    flatScene.dispose();
  });

  it("traces the frames and the calls in a dev build, for analysing the entrance in a browser", () => {
    const scene = new LivingScene(document.createElement("div"), callbacks, { live: true, reducedMotion: false });
    const trace = (window as unknown as { __atlasTrace: { start(): void; stop(): { t: number; meanMove: number; maxMove: number }[]; events: { label: string }[] } }).__atlasTrace;
    trace.start();
    scene.setData([node("a", 0), node("b", 50)], [], 5, true, {});
    const inner = scene as unknown as Internals;
    let now = performance.now();
    for (let i = 0; i < 20; i++) inner.step((now += 16), 1, true);
    const frames = trace.stop();
    expect(frames.length).toBe(20);
    expect(frames[5]).toEqual(expect.objectContaining({ t: expect.any(Number), meanMove: expect.any(Number), maxMove: expect.any(Number) }));
    expect(trace.events.map((e) => e.label)).toEqual(expect.arrayContaining(["setData", "bloom"]));
    scene.dispose();
  });
});
