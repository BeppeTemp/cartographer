import { GROWTH_TICK_MIN_MS, growthOrder, growthPace } from "./growth";

describe("growthOrder", () => {
  it("brings concepts in by birth, and those without history last", () => {
    const order = growthOrder(
      [{ id: "c" }, { id: "a" }, { id: "none" }, { id: "b" }],
      [],
      { a: "2026-01-01T00:00:00Z", b: "2026-02-01T00:00:00Z", c: "2026-03-01T00:00:00Z" },
    );
    expect(order.map((s) => s.id)).toEqual(["a", "b", "c", "none"]);
    expect(order[3]!.born).toBeUndefined();
  });

  it("grows one commit's concepts along their links, from what is already there", () => {
    const at = "2026-02-01T00:00:00Z";
    const order = growthOrder(
      [{ id: "root" }, { id: "far", pagerank: 0.9 }, { id: "mid" }, { id: "near" }],
      [
        { source: "near", target: "root" },
        { source: "mid", target: "near" },
        { source: "far", target: "mid" },
      ],
      { root: "2026-01-01T00:00:00Z", far: at, mid: at, near: at },
    );
    // "far" ranks highest, but the batch starts where it touches the graph.
    expect(order.map((s) => s.id)).toEqual(["root", "near", "mid", "far"]);
  });

  it("starts a batch linked to nothing from its best-ranked concept", () => {
    const at = "2026-01-01T00:00:00Z";
    const order = growthOrder(
      [{ id: "x", pagerank: 0.1 }, { id: "hub", pagerank: 0.8 }, { id: "y", pagerank: 0.5 }],
      [{ source: "x", target: "hub" }],
      { x: at, hub: at, y: at },
    );
    expect(order.map((s) => s.id)).toEqual(["hub", "x", "y"]);
  });
});

describe("growthPace", () => {
  it.each([10, 221, 5_000, 50_000])("lasts 8 to 20 seconds and never beats faster than the floor (%i)", (steps) => {
    const { tickMs, perTick } = growthPace(steps);
    const total = Math.ceil(steps / perTick) * tickMs;
    expect(tickMs).toBeGreaterThanOrEqual(GROWTH_TICK_MIN_MS);
    expect(total).toBeGreaterThanOrEqual(7_900);
    expect(total).toBeLessThanOrEqual(20_500);
  });
});
