import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Legend } from "../components/Legend";
import type { GraphNode, GraphSnapshot } from "../api/types";
import { snapshotCommunities } from "../lib/communities";

const node = (id: string, title: string, community: number): GraphNode =>
  ({ id, title, community, collection: id.split("/")[0] }) as GraphNode;

// The server names every anchor by title, whether or not its node is in the snapshot.
const communities = [
  { rank: 0, size: 5, anchor: "agents/scout", anchor_title: "Scout Agent", slot: 1 },
  { rank: 1, size: 3, anchor: "infra/lab", anchor_title: "Lab", slot: 2 },
];

function snap(nodes: GraphNode[]): GraphSnapshot {
  return { nodes, edges: [], total_nodes: nodes.length, total_edges: 0, limit: 2000, communities };
}

const full = snap([node("agents/scout", "Scout Agent", 0), node("infra/lab", "Lab", 1)]);
// A Map's snapshot: the whole-KB communities, but only that Map's nodes.
const scoped = snap([node("infra/lab", "Lab", 1), node("infra/x", "X", 0)]);

function labels() {
  return [...document.querySelectorAll(".legend__label")].map((e) => e.textContent);
}

describe("the community legend", () => {
  it("names communities by the anchor's title even when a Map's snapshot lacks the anchor node", () => {
    const view = (s: GraphSnapshot) => (
      <Legend snapshot={s} communities={snapshotCommunities(s)} colorBy="community" onColorBy={() => {}} />
    );
    const { rerender } = render(view(full));
    const all = labels();
    expect(all).toContain("Scout Agent");
    rerender(view(scoped));
    expect(labels()).toEqual(all);
    expect(screen.queryByText("scout")).toBeNull();
  });
});
