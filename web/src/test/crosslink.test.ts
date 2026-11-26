import { describe, expect, it } from "vitest";
import type { ExternalEdges, GraphSnapshot } from "../api/types";
import { withExternalStubs } from "../lib/crosslink";

const snap: GraphSnapshot = {
  nodes: [{ id: "a/x", in_degree: 0, out_degree: 0 }],
  edges: [],
  total_nodes: 1,
  total_edges: 0,
  limit: 10,
};

describe("withExternalStubs", () => {
  it("draws only resolved edges of drawn pages, labelled with the id", () => {
    const ext: ExternalEdges = {
      edges: [
        { from: "a/x", direction: "out", kb: "kb-b", id: "p/q", state: "resolved" },
        { from: "a/x", direction: "out", kb: "kb-b", id: "p/hidden", state: "unresolved" },
        { from: "a/x", direction: "in", kb: "kb-c", id: "r/s" },
        { from: "a/other", direction: "out", kb: "kb-b", id: "p/z", state: "resolved" },
      ],
      limit: 10,
      truncated: false,
    };
    const out = withExternalStubs(snap, ext);
    expect(out.nodes.map((n) => n.id)).toEqual(["a/x", "kb:kb-b/p/q", "kb:kb-c/r/s"]);
    expect(out.edges).toEqual([
      { source: "a/x", target: "kb:kb-b/p/q" },
      { source: "kb:kb-c/r/s", target: "a/x" },
    ]);
    expect(out.nodes[1]!.title).toBe("kb:kb-b/p/q");
  });
  it("is the same snapshot with nothing to draw", () => {
    expect(withExternalStubs(snap, null)).toBe(snap);
    expect(withExternalStubs(snap, { edges: [], limit: 1, truncated: false })).toBe(snap);
  });
});
