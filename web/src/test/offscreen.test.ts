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
});
