import { beforeEach, describe, expect, it } from "vitest";
import {
  MEMORY_LIMIT,
  forgetLayouts,
  recallLayout,
  rememberLayout,
  sameKB,
  smooth,
  snapshotPositions,
  type LayoutMemory,
} from "./transition";

const entry = (): LayoutMemory => ({
  nodes: new Map(),
  artifacts: new Map(),
  pose: { position: { x: 0, y: 0, z: 1 }, lookAt: { x: 0, y: 0, z: 0 } },
  shift: 0,
});

describe("sameKB", () => {
  it("compares the KB part of a layout key", () => {
    expect(sameKB("kb-a\u0000", "kb-a\u0000infra")).toBe(true);
    expect(sameKB("kb-a\u0000", "kb-b\u0000")).toBe(false);
    expect(sameKB("", "kb-a\u0000")).toBe(false);
  });
});

describe("layout memory", () => {
  beforeEach(forgetLayouts);
  it("recalls what was remembered and forgets the oldest past the limit", () => {
    for (let i = 0; i <= MEMORY_LIMIT; i++) rememberLayout(`k${i}`, entry());
    expect(recallLayout("k0")).toBeNull();
    expect(recallLayout(`k${MEMORY_LIMIT}`)).not.toBeNull();
  });
  it("copies positions and skips nodes without finite ones", () => {
    const node = { x: 1, y: 2, z: 3 };
    const out = snapshotPositions([
      ["a", node],
      ["b", { x: NaN, y: 0, z: 0 }],
    ]);
    node.x = 9;
    expect([...out.keys()]).toEqual(["a"]);
    expect(out.get("a")).toEqual({ x: 1, y: 2, z: 3 });
  });
});

describe("smooth", () => {
  it("runs 0 to 1 and clamps", () => {
    expect([smooth(-1), smooth(0), smooth(0.5), smooth(1), smooth(2)]).toEqual([0, 0, 0.5, 1, 1]);
  });
});
