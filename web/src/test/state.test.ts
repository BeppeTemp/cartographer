import { beforeEach, describe, expect, it } from "vitest";
import {
  applyLayout,
  buildGraph,
  nodeSize,
  readCachedPositions,
  snapshotFingerprint,
  writeCachedPositions,
} from "../lib/layout";
import { collectionHue } from "../lib/palette";
import { readViewState, viewStateToSearch, type ViewState } from "../lib/viewstate";
import type { GraphSnapshot } from "../api/types";

const snapshot: GraphSnapshot = {
  nodes: [
    { id: "infra/a", collection: "infra", in_degree: 1, out_degree: 2 },
    { id: "infra/b", collection: "infra", in_degree: 2, out_degree: 0 },
    { id: "notes/c", collection: "notes", in_degree: 0, out_degree: 1 },
  ],
  edges: [
    { source: "infra/a", target: "infra/b" },
    { source: "infra/a", target: "notes/c" },
    { source: "notes/c", target: "infra/b" },
  ],
  total_nodes: 3,
  total_edges: 3,
  limit: 2000,
};

describe("view state round-trips through the URL", () => {
  it("restores everything it writes", () => {
    const view: ViewState = {
      kb: "homelab",
      scope: "infra",
      concept: "infra/gateway",
      panel: "observatory",
    };
    expect(readViewState(viewStateToSearch(view))).toEqual(view);
  });

  it("omits empty values instead of writing blanks", () => {
    expect(viewStateToSearch({ kb: null, scope: null, concept: null, panel: "atlas" })).toBe("");
    expect(viewStateToSearch({ kb: "kb", scope: null, concept: null, panel: "atlas" })).toBe("?kb=kb");
  });

  it("falls back to the atlas for an unknown panel", () => {
    expect(readViewState("?panel=nonsense").panel).toBe("atlas");
  });

  it("survives a concept id containing a slash", () => {
    const view: ViewState = { kb: "k", scope: null, concept: "a/b/c", panel: "atlas" };
    expect(readViewState(viewStateToSearch(view)).concept).toBe("a/b/c");
  });
});

describe("graph layout is deterministic", () => {
  it("produces identical coordinates for identical input", () => {
    const first = applyLayout(buildGraph(snapshot));
    const second = applyLayout(buildGraph(snapshot));
    expect(first).toEqual(second);
  });

  it("reuses cached coordinates rather than re-seeding", () => {
    const positions = applyLayout(buildGraph(snapshot));
    const restored = buildGraph(snapshot, positions);
    restored.forEachNode((id, attrs) => {
      expect(attrs.x).toBeCloseTo(positions[id]!.x, 10);
      expect(attrs.y).toBeCloseTo(positions[id]!.y, 10);
    });
  });

  it("fingerprints a node set, so a different graph misses the cache", () => {
    const a = snapshotFingerprint("kb", "infra", snapshot);
    expect(snapshotFingerprint("kb", "infra", snapshot)).toBe(a);
    expect(snapshotFingerprint("kb", null, snapshot)).not.toBe(a);
    expect(snapshotFingerprint("other", "infra", snapshot)).not.toBe(a);
    expect(
      snapshotFingerprint("kb", "infra", { ...snapshot, nodes: snapshot.nodes.slice(1) }),
    ).not.toBe(a);
  });

  it("drops an edge whose endpoint is not in the node set", () => {
    const graph = buildGraph({
      ...snapshot,
      edges: [...snapshot.edges, { source: "infra/a", target: "ghost" }],
    });
    expect(graph.size).toBe(3);
  });

  it("bounds node size so a hub cannot swallow the canvas", () => {
    expect(nodeSize(0)).toBeLessThan(nodeSize(10));
    expect(nodeSize(10)).toBeLessThan(nodeSize(100));
    expect(nodeSize(100_000)).toBeLessThanOrEqual(18);
  });
});

describe("layout cache", () => {
  beforeEach(() => localStorage.clear());

  it("stores coordinates and nothing else", () => {
    writeCachedPositions("fp", { "infra/a": { x: 1, y: 2 } });
    expect(readCachedPositions("fp")).toEqual({ "infra/a": { x: 1, y: 2 } });
    expect(JSON.stringify(localStorage)).not.toMatch(/token|body/i);
  });

  it("returns null for a miss rather than throwing", () => {
    expect(readCachedPositions("absent")).toBeNull();
  });
});

describe("collection colour", () => {
  it("is stable for a name and inside the wheel", () => {
    for (const name of ["infra", "notes", "incidents", ""]) {
      const hue = collectionHue(name);
      expect(hue).toBe(collectionHue(name));
      expect(hue).toBeGreaterThanOrEqual(1);
      expect(hue).toBeLessThanOrEqual(12);
    }
  });
});
