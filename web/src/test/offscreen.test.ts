import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// A WebGLRenderer without WebGL: it keeps the size and pixel ratio it is
// given and counts what is done to it.
const renderer = vi.hoisted(() => ({ renders: 0, contextLost: 0, disposed: 0 }));
vi.mock("three", async (importActual) => {
  const actual = await importActual<typeof import("three")>();
  class FakeRenderer {
    domElement = document.createElement("canvas");
    private w = 0;
    private h = 0;
    private ratio = 1;
    setPixelRatio(r: number) {
      this.ratio = r;
    }
    getPixelRatio() {
      return this.ratio;
    }
    setSize(w: number, h: number) {
      this.w = w;
      this.h = h;
    }
    getSize(v: { set(x: number, y: number): unknown }) {
      return v.set(this.w, this.h);
    }
    setClearColor() {}
    render() {
      renderer.renders++;
    }
    dispose() {
      renderer.disposed++;
    }
    forceContextLoss() {
      renderer.contextLost++;
    }
  }
  return { ...actual, WebGLRenderer: FakeRenderer };
});

const callbacks = { onSelect() {}, onExpand() {}, onLost() {}, onHover() {} } as never;
const load = async () => (await vi.importActual<typeof import("../lib/graph3d/scene")>("../lib/graph3d/scene")).LivingScene;
const offscreen = { width: 1080, height: 1080, pixelRatio: 2 };
const one = [{ id: "a", weight: 0.5, x: 10, y: 0, z: 0 }];

describe("LivingScene offscreen (D691)", () => {
  const raf = vi.fn(() => 1);
  const observers = vi.fn();
  beforeEach(() => {
    renderer.renders = renderer.contextLost = renderer.disposed = 0;
    raf.mockClear();
    observers.mockClear();
    vi.stubGlobal("requestAnimationFrame", raf);
    vi.stubGlobal("cancelAnimationFrame", () => {});
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor() {
          observers();
        }
        observe() {}
        disconnect() {}
      },
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it("registers no frame loop, observer or page listener, and keeps the size it was made with", async () => {
    const LivingScene = await load();
    const listen = vi.spyOn(document, "addEventListener");
    const scene = new LivingScene(document.createElement("div"), callbacks, { live: true, reducedMotion: false, offscreen });
    expect(raf).not.toHaveBeenCalled();
    expect(observers).not.toHaveBeenCalled();
    expect(listen.mock.calls.filter(([type]) => type === "visibilitychange")).toEqual([]);
    expect(scene.camera.aspect).toBe(1);
    const r = (scene as unknown as { renderer: { getSize(v: unknown): { x: number; y: number }; getPixelRatio(): number } }).renderer;
    const v = { x: 0, y: 0, set(x: number, y: number) { this.x = x; this.y = y; return this; } };
    r.getSize(v);
    expect([v.x, v.y, r.getPixelRatio()]).toEqual([1080, 1080, 2]);
    // A render never starts the loop either. (d3-timer asks for a frame of
    // its own when a simulation is built, and the scene stops it at once.)
    scene.setData(structuredClone(one), [], 0);
    scene.renderFrame(0);
    expect((raf.mock.calls as unknown as [Function][]).map(([f]) => f.name)).not.toContain("tick");
    scene.dispose();
    listen.mockRestore();
  });

  it("gets no input: a wheel on its canvas does not move the camera", async () => {
    const LivingScene = await load();
    const scene = new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: false, offscreen });
    scene.setData(structuredClone(one), [], 0);
    scene.renderFrame(0);
    const before = scene.camera.position.clone();
    scene.canvas.dispatchEvent(new WheelEvent("wheel", { deltaY: 500 }));
    scene.renderFrame(16);
    expect(scene.camera.position.distanceTo(before)).toBeLessThan(1e-6);
    scene.dispose();
  });

  it("runs on the clock it is given: the same virtual time gives the same camera, however it was reached", async () => {
    const LivingScene = await load();
    const at = (times: number[]) => {
      const scene = new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: false, offscreen });
      scene.setData(structuredClone(one), [], 0);
      scene.renderFrame(0);
      // A 1 s eased move, started at virtual time 0.
      scene.frameAll(1000);
      scene.fitEverything(0, 1000, 5000);
      const seen = times.map((t) => {
        scene.renderFrame(t);
        return scene.camera.position.toArray();
      });
      scene.dispose();
      return seen;
    };
    const slow = at([100, 250, 500]);
    const fast = at([500]);
    expect(slow[2]).toEqual(fast[0]);
    // it is moving at half time and has arrived at the end
    const [mid] = at([500]);
    const [end] = at([1000]);
    const [later] = at([5000]);
    expect(mid).not.toEqual(end);
    expect(end).toEqual(later);
  });

  it("reads the virtual clock, not the wall clock", async () => {
    const LivingScene = await load();
    const scene = new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: false, offscreen });
    scene.renderFrame(1234);
    expect(scene.clock()).toBe(1234);
    const live = new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: false });
    const wall = performance.now();
    expect(Math.abs(live.clock() - wall)).toBeLessThan(1000);
    expect(() => live.renderFrame(0)).toThrow(/offscreen/);
    scene.dispose();
    live.dispose();
  });

  it("starts from the seeded layout", async () => {
    const LivingScene = await load();
    const scene = new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: false, offscreen });
    const nodes = [{ id: "a", weight: 0.5 }, { id: "b", weight: 0.5 }];
    scene.setData(nodes, [], 0);
    scene.seedPositions(new Map([["a", { x: 1, y: 2, z: 3 }]]));
    expect(scene.nodeById("a")).toMatchObject({ x: 1, y: 2, z: 3, vx: 0 });
    scene.dispose();
  });

  it("releases its WebGL context when disposed, and a visible scene's is left to the page", async () => {
    const LivingScene = await load();
    new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: false, offscreen }).dispose();
    expect(renderer.contextLost).toBe(1);
    new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: false }).dispose();
    expect(renderer.contextLost).toBe(1);
  });

  it("moves the idle camera by elapsed time, not by frame count (D736)", async () => {
    const LivingScene = await load();
    const angle = async (dt: number) => {
      const scene = new LivingScene(document.createElement("div"), callbacks, { live: true, reducedMotion: false, offscreen });
      scene.setData(structuredClone(one), [], 0);
      scene.renderFrame(0);
      scene.camera.position.set(0, 0, 300);
      const start = 20_000; // past IDLE_RESUME_MS: the panorama turns
      for (let t = start; t <= start + 2000 + 1e-6; t += dt) scene.renderFrame(t);
      const p = scene.camera.position;
      scene.dispose();
      return Math.atan2(p.x, p.z);
    };
    const a = await angle(1000 / 120);
    const b = await angle(1000 / 60);
    expect(Math.abs(a)).toBeGreaterThan(0);
    expect(a).toBeCloseTo(b, 2);
  });

  it("lets the idle refit run again once a fit follow has expired", async () => {
    const LivingScene = await load();
    const scene = new LivingScene(document.createElement("div"), callbacks, { live: true, reducedMotion: false, offscreen });
    scene.setData(structuredClone(one), [], 0);
    scene.renderFrame(0);
    scene.followFit(0, 0, 2500);
    const s = scene as unknown as { fitFollow: unknown };
    scene.renderFrame(2000);
    expect(s.fitFollow).not.toBeNull();
    scene.renderFrame(2500);
    expect(s.fitFollow).toBeNull();
    scene.dispose();
  });

  it("keeps the same edge attribute across selections", async () => {
    const LivingScene = await load();
    const scene = new LivingScene(document.createElement("div"), callbacks, { live: true, reducedMotion: false, offscreen });
    const nodes = [
      { id: "a", weight: 0.5, x: 0, y: 0, z: 0 },
      { id: "b", weight: 0.5, x: 10, y: 0, z: 0 },
      { id: "c", weight: 0.5, x: 0, y: 10, z: 0 },
    ];
    scene.setData(structuredClone(nodes), [{ source: "a", target: "b" }, { source: "a", target: "c" }] as never, 0);
    const geo = (scene as unknown as { activeGeometry: { getAttribute(n: string): unknown; drawRange: { count: number } } }).activeGeometry;
    scene.setSelection("a");
    const first = geo.getAttribute("position");
    expect(geo.drawRange.count).toBe(4);
    scene.setSelection("b");
    expect(geo.getAttribute("position")).toBe(first);
    expect(geo.drawRange.count).toBe(2);
    scene.setSelection("a");
    expect(geo.getAttribute("position")).toBe(first);
    scene.dispose();
  });

  it("moves the idle camera towards the fit once a fit follow has expired", async () => {
    const LivingScene = await load();
    const scene = new LivingScene(document.createElement("div"), callbacks, { live: true, reducedMotion: false, offscreen });
    scene.setData(structuredClone(one), [], 0);
    scene.renderFrame(20_000);
    const internals = scene as unknown as {
      controls: { target: { x: number; distanceTo(v: unknown): number } };
      fitPoseFor(b: number): { lookAt: { x: number; y: number; z: number } } | null;
    };
    scene.followFit(0, 0, 2500);
    internals.controls.target.x = 500;
    let t = 20_000;
    while (t < 22_500) scene.renderFrame((t += 1000 / 60));
    const fit = internals.fitPoseFor(0)!.lookAt;
    const gap = () => Math.hypot(internals.controls.target.x - fit.x, (internals.controls as never as { target: { y: number } }).target.y - fit.y);
    // Knock the target away again once the follow is over: only the idle refit can bring it back.
    internals.controls.target.x = fit.x + 80;
    const before = gap();
    for (let k = 0; k < 120; k++) scene.renderFrame((t += 1000 / 60));
    expect(gap()).toBeLessThan(before * 0.9);
    scene.dispose();
  });

  it("puts the pivot on the node's final position when a focus tween ends on a moving node", async () => {
    const LivingScene = await load();
    const scene = new LivingScene(document.createElement("div"), callbacks, { live: false, reducedMotion: false, offscreen });
    scene.setData(structuredClone(one), [], 0);
    scene.renderFrame(0);
    scene.setSelection("a");
    scene.focus("a", { left: 0, right: 0 }, () => {});
    const internals = scene as unknown as {
      nodes: { id: string; x: number; y: number; z: number }[];
      follow: { x: number; y: number; z: number } | null;
      controls: { target: { x: number; y: number; z: number } };
    };
    const node = internals.nodes.find((n) => n.id === "a")!;
    // The node keeps moving while the 520 ms tween runs.
    let t = 0;
    while (t < 300) {
      node.x += 0.5;
      scene.renderFrame((t += 1000 / 60));
    }
    for (let k = 0; k < 120; k++) scene.renderFrame((t += 1000 / 60));
    expect(internals.follow!.x).toBeCloseTo(node.x, 6);
    expect(internals.controls.target.x).toBeCloseTo(node.x, 3);
    scene.dispose();
  });

  it("keeps the export's virtual step at the 60 Hz reference, so its eases are unchanged", async () => {
    const { FRAME_MS } = await import("../lib/graph3d/record");
    const { REF_FRAME_MS, frameEase } = await import("../lib/graph3d/motion");
    expect(FRAME_MS).toBeCloseTo(REF_FRAME_MS, 12);
    for (const e of [0.06, 0.01, 0.08]) expect(Math.abs(frameEase(e, FRAME_MS) - e)).toBeLessThan(1e-12);
  });
});
