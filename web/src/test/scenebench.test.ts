import { afterAll, beforeAll, describe, it, vi } from "vitest";

/**
 * What the graph costs per frame and to settle, by size and shape: the numbers
 * the cluster threshold and the render budget are chosen from (docs/testing.md
 * §Atlas UI budgets). The real scene, a renderer that draws nothing: the
 * simulation tick, the buffer preparation and the draw calls are what is
 * counted -- the GPU's share needs a browser. Runs alone, like perf.test.tsx
 * (`SCENE_BENCH=1 npx vitest run src/test/scenebench.test.ts`, skipped otherwise); it asserts nothing, it prints.
 */
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

const callbacks = { onSelect() {}, onHover() {}, onLost() {} };

/** A deterministic random graph: preferential attachment, `per` edges per node. */
function graph(n: number, per: number, cluster = 0): { nodes: SceneNode[]; links: SceneLink[] } {
  let seed = 7;
  const rnd = () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 2 ** 32);
  const degree = new Array<number>(n).fill(0);
  const ends: number[] = [0];
  const links: SceneLink[] = [];
  for (let i = 1; i < n; i++) {
    const k = Math.min(i, Math.max(1, Math.round(per + (rnd() - 0.5))));
    const seen = new Set<number>();
    for (let j = 0; j < k; j++) {
      const t = ends[Math.floor(rnd() * ends.length)]!;
      if (t === i || seen.has(t)) continue;
      seen.add(t);
      links.push({ source: `n${i}`, target: `n${t}` } as SceneLink);
      degree[i]!++;
      degree[t]!++;
      ends.push(i, t);
    }
  }
  const max = Math.max(...degree, 1);
  const nodes = degree.map((d, i): SceneNode => ({
    id: `n${i}`,
    weight: d / max,
    x: (rnd() - 0.5) * 200,
    y: (rnd() - 0.5) * 200,
    z: (rnd() - 0.5) * 200,
    ...(i < cluster ? { cluster: true, weight: 0.3 + 0.7 * rnd() } : {}),
  }));
  return { nodes, links };
}

const sizes = { clientWidth: 1200, clientHeight: 800 };
beforeAll(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} unobserve() {} });
  for (const [k, v] of Object.entries(sizes)) Object.defineProperty(HTMLElement.prototype, k, { configurable: true, get: () => v });
});
afterAll(() => {
  vi.unstubAllGlobals();
  for (const k of Object.keys(sizes)) delete (HTMLElement.prototype as unknown as Record<string, unknown>)[k];
});

type Internals = { sim: { alpha(): number; tick(): void }; step(now: number, ticks?: number, force?: boolean): void; sync(now: number): void; scene: import("three").Scene };

function drawCalls(scene: LivingScene): number {
  let calls = 0;
  (scene as unknown as Internals).scene.traverse((o) => {
    const m = o as import("three").Mesh & { count?: number };
    if (!o.visible) return;
    if ((m.isMesh || (o as { isLineSegments?: boolean }).isLineSegments || (o as { isPoints?: boolean }).isPoints) && (m.count ?? 1) > 0) calls++;
  });
  return calls;
}

const mean = (f: () => void, runs: number) => {
  const t0 = performance.now();
  for (let i = 0; i < runs; i++) f();
  return (performance.now() - t0) / runs;
};

function measure(label: string, data: { nodes: SceneNode[]; links: SceneLink[] }, extra: Parameters<LivingScene["setData"]>[4] = {}) {
  const container = document.createElement("div");
  const scene = new LivingScene(container, callbacks, { live: false, reducedMotion: false });
  const inner = scene as unknown as Internals;
  scene.setColours(data.nodes.map(() => "#6699cc"), { background: "#101014", edge: "#888888", edgeActive: "#ffffff", signal: "#ffffff", ring: "#ffffff" } as never);
  const t0 = performance.now();
  scene.setData(data.nodes, data.links, 0, true, extra);
  const setup = performance.now() - t0;
  // Settle: ticks (and CPU) until the cooling simulation rests, drawn as it would be.
  let ticks = 0;
  const s0 = performance.now();
  let now = s0;
  while (inner.sim.alpha() > 0.002 && ticks < 2000) {
    now += 1000 / 60;
    inner.step(now, 1, true);
    ticks++;
  }
  const settleCpu = performance.now() - s0;
  // Live drift: what a frame costs once it only breathes.
  scene.setLive(true);
  const tick = mean(() => inner.sim.tick(), 30);
  const frame = mean(() => inner.step((now += 1000 / 60), 1, true), 60);
  const prep = mean(() => inner.sync((now += 1000 / 60)), 60);
  console.info(
    `bench ${label.padEnd(26)} nodes=${String(data.nodes.length).padStart(5)} links=${String(data.links.length).padStart(5)}` +
      ` | setup ${setup.toFixed(0).padStart(4)}ms | settle ${ticks} ticks ${settleCpu.toFixed(0).padStart(5)}ms CPU (${(ticks / 60).toFixed(1)}s at 60Hz)` +
      ` | tick ${tick.toFixed(2)}ms prep ${prep.toFixed(2)}ms frame ${frame.toFixed(2)}ms | draw calls ${drawCalls(scene)}`,
  );
  scene.dispose();
}

describe.skipIf(!process.env.SCENE_BENCH)("scene cost by size", () => {
  it("flat graphs", () => {
    for (const n of [300, 500, 800, 1200, 1500, 2000, 3000]) measure(`flat ${n} (4/concept)`, graph(n, 4));
    for (const n of [500, 1200, 2000]) measure(`flat ${n} (dense 12)`, graph(n, 12));
  }, 600_000);

  it("cluster overview and one open", () => {
    for (const total of [100, 200, 300, 400]) {
      const glyphs = Math.round(total / 5);
      measure(`overview ${total} el.`, graph(total, 2, glyphs));
    }
    // One open: a glyph gives way to its members (80), the rest stay folded.
    const base = graph(200, 2, 40);
    const glyph = base.nodes[0]!;
    const members: SceneNode[] = Array.from({ length: 80 }, (_, i) => ({ id: `m${i}`, weight: 0.2, x: (glyph.x ?? 0) + i, y: glyph.y, z: glyph.z }));
    const links = [...base.links.filter((l) => l.source !== glyph.id && l.target !== glyph.id), ...members.map((m, i) => ({ source: m.id, target: members[(i * 7 + 1) % 80]!.id }) as SceneLink)];
    measure("one open (80 members)", { nodes: [...base.nodes.slice(1), ...members], links }, { hulls: [{ id: glyph.id, members: members.map((m) => m.id) }] });
  }, 600_000);
});
