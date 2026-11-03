import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { GraphView } from "../components/GraphView";
import { snapshotCommunities } from "../lib/communities";
import type { GraphSnapshot } from "../api/types";

// The scene chunk is gone from the server (a tab opened before an upgrade).
vi.mock("../lib/graph3d/scene", () => {
  throw new Error("Failed to fetch dynamically imported module");
});

const snapshot: GraphSnapshot = {
  nodes: [{ id: "k/a", collection: "k", type: "Note", in_degree: 0, out_degree: 0 }],
  edges: [],
  total_nodes: 1,
  total_edges: 0,
  limit: 2000,
};

describe("a graph chunk that cannot be fetched", () => {
  it("asks for a reload and does not report WebGL as lost", async () => {
    const onUnavailable = vi.fn();
    render(
      <GraphView
        snapshot={snapshot}
        layoutKey="kb-a"
        communities={snapshotCommunities(snapshot)}
        colorBy="community"
        selected={null}
        hiddenIds={new Set<string>()}
        themeKey="dark"
        live={false}
        occludedRight={0}
        onSelect={() => {}}
        onUnavailable={onUnavailable}
      />,
    );
    expect(await screen.findByText(/Reload the page/)).toBeInTheDocument();
    expect(onUnavailable).not.toHaveBeenCalled();
  });
});
