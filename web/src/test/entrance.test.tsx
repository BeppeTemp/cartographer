import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { GraphView, type ClusterView } from "../components/GraphView";
import { snapshotCommunities } from "../lib/communities";
import type { GraphSnapshot } from "../api/types";
import { graph } from "./fixtures";
import { sceneStub } from "./sceneStub";

const NONE = new Set<string>();
const clusters = [
  { id: "c:infra/a", label: "Alpha", collection: "infra", size: 3400, slot: 1, internal_edges: 9000 },
  { id: "c:~tail", label: "Other", size: 900, slot: 0, internal_edges: 40, communities: 300 },
];
const view = (): ClusterView => ({
  clusters,
  edges: [{ source: "c:infra/a", target: "c:~tail", weight: 40 }],
  unfolded: [],
  totalNodes: 30_000,
  onUnfold: vi.fn(),
  onFold: vi.fn(),
});
const props = (snapshot: GraphSnapshot, layoutKey: string, clusterView?: ClusterView) => ({
  snapshot,
  layoutKey,
  kbName: "kb-a",
  communities: snapshotCommunities(snapshot),
  colorBy: "community" as const,
  selected: null,
  hiddenIds: NONE,
  themeKey: "dark",
  live: false,
  occludedRight: 0,
  onSelect: () => {},
  clusterView,
});

describe("changing to a clustered KB", () => {
  beforeEach(() => sceneStub.reset());

  it("hands the scene one new layout: a single setData that replaces, so a single entrance", async () => {
    const flat = { ...graph, nodes: graph.nodes.slice(0, 2), edges: graph.edges.slice(0, 1) };
    const { rerender } = render(<GraphView {...props(flat, "kb-a\u0000")} />);
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    sceneStub.calls.data.length = 0;
    rerender(<GraphView {...props(graph, "kb-b\u0000", view())} />);
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    const replaced = sceneStub.calls.data.filter((c) => c.replace);
    expect(replaced).toHaveLength(1);
    expect(sceneStub.calls.data).toHaveLength(1);
  });
});
