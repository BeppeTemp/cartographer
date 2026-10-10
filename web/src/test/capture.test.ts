import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// A WebGLRenderer without WebGL: it keeps the size and pixel ratio it is
// given, which is all the capture mode touches.
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
    render() {}
    dispose() {}
  }
  return { ...actual, WebGLRenderer: FakeRenderer };
});

describe("LivingScene capture mode (D677)", () => {
  beforeEach(() => {
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
    vi.stubGlobal("requestAnimationFrame", () => 1);
    vi.stubGlobal("cancelAnimationFrame", () => {});
  });
  afterEach(() => vi.unstubAllGlobals());

  it("sizes the frame for the export and restores the view exactly afterwards", async () => {
    const { LivingScene } = await vi.importActual<typeof import("../lib/graph3d/scene")>("../lib/graph3d/scene");
    const container = document.createElement("div");
    Object.defineProperty(container, "clientWidth", { value: 800 });
    Object.defineProperty(container, "clientHeight", { value: 400 });
    const scene = new LivingScene(container, { onSelect() {}, onExpand() {}, onLost() {}, onHover() {} } as never, {
      live: false,
      reducedMotion: true,
    });
    const renderer = (scene as unknown as { renderer: { getSize(v: { x: number; y: number }): unknown; getPixelRatio(): number } }).renderer;
    const size = () => {
      const v = { x: 0, y: 0, set(x: number, y: number) { this.x = x; this.y = y; return this; } };
      renderer.getSize(v);
      return [v.x, v.y, renderer.getPixelRatio(), scene.camera.aspect];
    };
    expect(size()).toEqual([800, 400, 1, 2]);

    scene.beginCapture(1080, 1920);
    expect(size()).toEqual([1080, 1920, 1, 1080 / 1920]);
    expect(scene.canvas.style.pointerEvents).toBe("none");
    expect(scene.canvas.style.objectFit).toBe("contain");
    // A container resize mid-export does not take the frame back.
    (scene as unknown as { resize(): void }).resize();
    expect(size()).toEqual([1080, 1920, 1, 1080 / 1920]);

    scene.endCapture();
    expect(size()).toEqual([800, 400, 1, 2]);
    expect(scene.canvas.style.pointerEvents).toBe("");
    expect(scene.canvas.style.objectFit).toBe("");
    scene.dispose();
  });

  it("fits the graph to the export's frame, not the container's", async () => {
    const { LivingScene } = await vi.importActual<typeof import("../lib/graph3d/scene")>("../lib/graph3d/scene");
    const container = document.createElement("div");
    Object.defineProperty(container, "clientWidth", { value: 800 });
    Object.defineProperty(container, "clientHeight", { value: 400 });
    const scene = new LivingScene(container, { onSelect() {}, onExpand() {}, onLost() {}, onHover() {} } as never, {
      live: false,
      reducedMotion: true,
    });
    // A wide graph: a tall frame must stand further back than a wide one.
    const nodes = Array.from({ length: 12 }, (_, i) => ({ id: `n${i}`, weight: 0.5, x: (i - 6) * 80, y: (i % 2) * 20, z: 0 }));
    scene.setData(nodes, [], 0);
    scene.beginCapture(1920, 1080);
    const wide = scene.fitDistance(0);
    scene.endCapture();
    scene.beginCapture(1080, 1920);
    const tall = scene.fitDistance(0);
    scene.endCapture();
    expect(wide).toBeGreaterThan(0);
    expect(tall).toBeGreaterThan(wide);
    scene.dispose();
  });
});
