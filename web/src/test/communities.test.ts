import { describe, expect, it } from "vitest";
import type { GraphSnapshot } from "../api/types";
import { OTHER_SLOT, communitySlot, snapshotCommunities } from "../lib/communities";
import { generateSnapshot } from "./fixtures";

/** Two 5-cliques joined by one bridge edge, plus an isolated node, with the
 *  communities the server reports for it (internal/graphalgo
 *  TestCommunitiesTwoCliques pins the same answer on the Go side). */
function twoCliques(): GraphSnapshot {
  const nodes: GraphSnapshot["nodes"] = [];
  const edges: GraphSnapshot["edges"] = [];
  for (const [rank, group] of ["a", "b"].entries()) {
    for (let i = 0; i < 5; i++) nodes.push({ id: `${group}/${i}`, in_degree: 4, out_degree: 4, community: rank });
    for (let i = 0; i < 5; i++)
      for (let j = i + 1; j < 5; j++) edges.push({ source: `${group}/${i}`, target: `${group}/${j}` });
  }
  edges.push({ source: "a/0", target: "b/0" });
  nodes.push({ id: "loose/x", in_degree: 0, out_degree: 0, community: 2 });
  return {
    nodes,
    edges,
    total_nodes: nodes.length,
    total_edges: edges.length,
    limit: 2000,
    communities: [
      { rank: 0, size: 5, anchor: "a/0", slot: 1 },
      { rank: 1, size: 5, anchor: "b/0", slot: 2 },
      { rank: 2, size: 1, anchor: "loose/x", slot: OTHER_SLOT },
    ],
  };
}

describe("server communities", () => {
  it("maps every node to its community", () => {
    const c = snapshotCommunities(twoCliques());
    for (let i = 0; i < 5; i++) {
      expect(c.rankOf.get(`a/${i}`)).toBe(0);
      expect(c.rankOf.get(`b/${i}`)).toBe(1);
    }
    expect(c.list.map((community) => community.anchor)).toEqual(["a/0", "b/0", "loose/x"]);
  });

  it("gives real communities their hue and singletons the neutral", () => {
    const c = snapshotCommunities(twoCliques());
    expect(communitySlot(c, "a/1")).toBe(1);
    expect(communitySlot(c, "b/1")).toBe(2);
    expect(communitySlot(c, "loose/x")).toBe(OTHER_SLOT);
  });

  it("colours a node the server did not place with the neutral", () => {
    const snapshot = twoCliques();
    snapshot.nodes.push({ id: "new/y", in_degree: 0, out_degree: 0, community: 99 });
    const c = snapshotCommunities(snapshot);
    expect(communitySlot(c, "new/y")).toBe(OTHER_SLOT);
    expect(communitySlot(c, "unknown")).toBe(OTHER_SLOT);
  });

  it("tolerates a snapshot without communities", () => {
    const c = snapshotCommunities({ nodes: [{ id: "a", in_degree: 0, out_degree: 0 }], edges: [], total_nodes: 1, total_edges: 0, limit: 2000 });
    expect(communitySlot(c, "a")).toBe(OTHER_SLOT);
    expect(c.list).toEqual([]);
  });

  it("reads the budget fixture's communities", () => {
    const c = snapshotCommunities(generateSnapshot(600));
    expect(c.list).toHaveLength(12);
    expect(communitySlot(c, generateSnapshot(600).nodes[0]!.id)).toBeGreaterThan(OTHER_SLOT);
  });
});
