import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The real scene, with a renderer that draws nothing: the camera's moves are
// what is counted. (The suite stubs the scene by default; this file wants the code.)
vi.unmock("../lib/graph3d/scene");
vi.mock("three", async (original) => {
  const three = await original<typeof import("three")>();
  class FakeRenderer {
    domElement = document.createElement("canvas");
    info = { render: { calls: 0 } };
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

import { LivingScene, type SceneNode } from "../lib/graph3d/scene";

const callbacks = { onSelect() {}, onHover() {}, onLost() {} };

function node(id: string, x: number, y: number, extra: Partial<SceneNode> = {}): SceneNode {
  return { id, weight: 0.5, x, y, z: 0, ...extra };
}

describe("the camera in a cluster transition", () => {
  const sizes = { clientWidth: 800, clientHeight: 600 };
  beforeEach(() => {
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} unobserve() {} });
    for (const [k, v] of Object.entries(sizes)) Object.defineProperty(HTMLElement.prototype, k, { configurable: true, get: () => v });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    for (const k of Object.keys(sizes)) delete (HTMLElement.prototype as unknown as Record<string, unknown>)[k];
  });

  it("follows the graph once per open, switch and fold -- one smooth follow, no tween -- and nothing refits after it", () => {
    const container = document.createElement("div");
    const scene = new LivingScene(container, callbacks, { live: false, reducedMotion: false });
    const spy = vi.spyOn(scene as unknown as { followFit(...a: unknown[]): void }, "followFit");
    const tween = vi.spyOn(scene as unknown as { animateTo(...a: unknown[]): void }, "animateTo");
    const glyphs = Array.from({ length: 6 }, (_, i) => node(`c:g${i}`, Math.cos(i) * 80, Math.sin(i) * 80, { cluster: true, weight: 0.8 }));
    const members = (tag: string, at: SceneNode) =>
      Array.from({ length: 30 }, (_, i) => node(`${tag}${i}`, (at.x ?? 0) + i, (at.y ?? 0), { weight: 0.3 }));

    scene.setData(glyphs, [], 10, false, {});
    spy.mockClear();
    tween.mockClear();

    // Open g2: its glyph leaves, its members come out of it, the simulation does the rest.
    const g2 = glyphs[2]!;
    const m2 = members("a", g2);
    const afterOpen = [...glyphs.filter((g) => g !== g2), ...m2];
    scene.setData(afterOpen, [], 10, false, { local: new Set(["a0"]), hulls: [{ id: g2.id, members: m2.map((m) => m.id) }] });
    expect(spy).toHaveBeenCalledTimes(1);

    // Switch to g4: g2 folds back, g4 opens: still one move.
    spy.mockClear();
    const g4 = glyphs[4]!;
    const m4 = members("b", g4);
    const afterSwitch = [...glyphs.filter((g) => g !== g4), ...m4];
    scene.setData(afterSwitch, [], 10, false, { local: new Set(["b0"]), hulls: [{ id: g4.id, members: m4.map((m) => m.id) }] });
    expect(spy).toHaveBeenCalledTimes(1);

    // Fold: one move back to the overview.
    spy.mockClear();
    scene.setData(glyphs, [], 10, false, { local: new Set([g4.id]), hulls: [] });
    expect(spy).toHaveBeenCalledTimes(1);

    // The camera only follows: no tween ever plays for a cluster.
    expect(tween).not.toHaveBeenCalled();
    scene.dispose();
  });
});
