import { describe, expect, it } from "vitest";
import type { GraphSnapshot } from "../api/types";
import {
  COMMUNITY_SLOTS,
  OTHER_SLOT,
  communitySlot,
  detectCommunities,
} from "../lib/communities";
import { applyLayout, buildGraph } from "../lib/layout";
import { generateSnapshot } from "./fixtures";

/** Two 5-cliques joined by one bridge edge, plus an isolated node. */
function twoCliques(): GraphSnapshot {
  const nodes: GraphSnapshot["nodes"] = [];
  const edges: GraphSnapshot["edges"] = [];
  for (const group of ["a", "b"]) {
    for (let i = 0; i < 5; i++) nodes.push({ id: `${group}/${i}`, in_degree: 4, out_degree: 4 });
    for (let i = 0; i < 5; i++)
      for (let j = i + 1; j < 5; j++) edges.push({ source: `${group}/${i}`, target: `${group}/${j}` });
  }
  edges.push({ source: "a/0", target: "b/0" });
  nodes.push({ id: "loose/x", in_degree: 0, out_degree: 0 });
  return { nodes, edges, total_nodes: nodes.length, total_edges: edges.length, limit: 2000 };
}

describe("community detection", () => {
  it("finds the clusters the links draw", () => {
    const c = detectCommunities(twoCliques());
    const a = c.rankOf.get("a/1");
    const b = c.rankOf.get("b/1");
    expect(a).not.toBe(b);
    for (let i = 0; i < 5; i++) {
      expect(c.rankOf.get(`a/${i}`)).toBe(a);
      expect(c.rankOf.get(`b/${i}`)).toBe(b);
    }
  });

  it("gives real communities a hue and singletons the neutral", () => {
    const snapshot = twoCliques();
    const c = detectCommunities(snapshot);
    expect(communitySlot(c, "a/1")).toBeGreaterThan(OTHER_SLOT);
    expect(communitySlot(c, "b/1")).toBeGreaterThan(OTHER_SLOT);
    expect(communitySlot(c, "a/1")).not.toBe(communitySlot(c, "b/1"));
    expect(communitySlot(c, "loose/x")).toBe(OTHER_SLOT);
  });

  it("ranks by size, then by smallest member, so equal inputs give equal colours", () => {
    const c = detectCommunities(twoCliques());
    // Equal sizes: "a/..." sorts before "b/...".
    expect(c.list[0]!.anchor.startsWith("a/")).toBe(true);
    expect(c.list[0]!.slot).toBe(1);
    expect(c.list[1]!.slot).toBe(2);
  });

  it("is deterministic: the same KB state gives the same communities", () => {
    const snapshot = generateSnapshot(600);
    const first = detectCommunities(snapshot);
    const second = detectCommunities(snapshot);
    expect([...second.rankOf.entries()]).toEqual([...first.rankOf.entries()]);
    expect(second.list).toEqual(first.list);
  });

  it("never colours more than the hue wheel holds", () => {
    const c = detectCommunities(generateSnapshot(2000, 40));
    const slots = new Set(c.list.map((community) => community.slot));
    for (const slot of slots) expect(slot).toBeLessThanOrEqual(COMMUNITY_SLOTS);
    expect(c.list.filter((community) => community.slot !== OTHER_SLOT).length).toBeLessThanOrEqual(
      COMMUNITY_SLOTS,
    );
  });

  it("handles a graph without edges", () => {
    const c = detectCommunities({
      nodes: [{ id: "a", in_degree: 0, out_degree: 0 }],
      edges: [],
      total_nodes: 1,
      total_edges: 0,
      limit: 2000,
    });
    expect(communitySlot(c, "a")).toBe(OTHER_SLOT);
  });

  it("keeps the community-seeded layout deterministic", () => {
    const snapshot = generateSnapshot(300);
    const c = detectCommunities(snapshot);
    const first = applyLayout(buildGraph(snapshot, undefined, c));
    const second = applyLayout(buildGraph(snapshot, undefined, detectCommunities(snapshot)));
    expect(second).toEqual(first);
  });
});
