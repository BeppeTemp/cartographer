import { growthOrder, seedGrowth } from "./growth";

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

describe("seedGrowth", () => {
  const node = (id: string, x = 0, y = 0, z = 0) => ({ id, x, y, z });
  it("seeds a concept with no placed neighbour beside the centre of those out, never at the origin", () => {
    const a = node("a", 100, 100, 100);
    const b = node("b", 120, 100, 100);
    const loose = node("loose");
    const byId = new Map([a, b, loose].map((n) => [n.id, n]));
    seedGrowth([loose], new Set(["a", "b"]), [], byId);
    expect(Math.hypot(loose.x - 110, loose.y - 100, loose.z - 100)).toBeLessThan(40);
  });
  it("chains through a new neighbour and reads the edges once per pass", () => {
    const a = node("a", 50, 0, 0);
    const b = node("b");
    const c = node("c");
    const byId = new Map([a, b, c].map((n) => [n.id, n]));
    let reads = 0;
    const edges = [{ source: "c", target: "b" }, { source: "b", target: "a" }];
    const counted = new Proxy(edges, {
      get(t, k, r) {
        if (k === Symbol.iterator) reads++;
        return Reflect.get(t, k, r);
      },
    });
    seedGrowth([c, b], new Set(["a"]), counted, byId);
    expect(reads).toBe(1);
    expect(Math.hypot(b.x - 50, b.y, b.z)).toBeLessThan(40);
    expect(Math.hypot(c.x - b.x, c.y - b.y, c.z - b.z)).toBeLessThan(40);
  });
});
