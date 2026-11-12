import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { ClusterGraph, GraphSnapshot } from "../api/types";
import { clusterWeight, edgeStrokes, GraphView, type ClusterView } from "../components/GraphView";
import { Legend } from "../components/Legend";
import { snapshotCommunities } from "../lib/communities";
import { beside } from "../lib/graph3d/physics";
import { graph, json, overview, stubApi } from "./fixtures";
import { sceneStub } from "./sceneStub";

const NONE = new Set<string>();
const clusters = [
  { id: "c:infra/a", label: "Alpha", collection: "infra", size: 3400, slot: 1, internal_edges: 9000 },
  { id: "c:~tail", label: "Other", size: 900, slot: 0, internal_edges: 40, communities: 300 },
];
const members: GraphSnapshot = { ...graph, nodes: graph.nodes.slice(0, 2), edges: graph.edges.slice(0, 1) };

function view(over: Partial<ClusterView> = {}): ClusterView {
  return {
    clusters,
    edges: [{ source: "c:infra/a", target: "c:~tail", weight: 40 }],
    unfolded: [],
    totalNodes: 30_000,
    onUnfold: vi.fn(),
    onFold: vi.fn(),
    ...over,
  };
}

function props(snapshot: GraphSnapshot, extra: Record<string, unknown> = {}) {
  return {
    snapshot,
    layoutKey: "kb-a\u0000",
    kbName: "kb-a",
    communities: snapshotCommunities(snapshot),
    colorBy: "community" as const,
    selected: null,
    hiddenIds: NONE,
    themeKey: "dark",
    live: false,
    occludedRight: 0,
    onSelect: () => {},
    ...extra,
  };
}

const lastData = () => sceneStub.calls.data.at(-1)!;

describe("the cluster view", () => {
  beforeEach(() => sceneStub.reset());

  it("sizes clusters on a log scale and draws heavier edges with more strokes", () => {
    expect(clusterWeight(20_000, 20_000)).toBe(1);
    expect(clusterWeight(1, 20_000)).toBeGreaterThan(0.35);
    expect(clusterWeight(200, 20_000)).toBeLessThan(clusterWeight(2000, 20_000));
    expect([1, 3, 4, 31, 32, 9999].map(edgeStrokes)).toEqual([1, 1, 2, 2, 3, 3]);
  });

  it("draws every cluster beside the unfolded concepts, and a weighted edge as overlaid strokes", async () => {
    render(<GraphView {...props(members, { clusterView: view() })} />);
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    expect([...lastData().positions.keys()]).toEqual(["infra/a", "infra/b", "c:infra/a", "c:~tail"]);
    // Weight 40: three strokes of the one edge.
    expect(lastData().links.filter((l) => l === "c:infra/a>c:~tail")).toHaveLength(3);
    expect(lastData().links).toContain("infra/a>infra/b");
    expect(screen.getByText("2 clusters, 30,000 concepts; 0 unfolded")).toBeInTheDocument();
    // One colour per drawn thing, in the order the scene got them.
    expect(sceneStub.calls.colours.at(-1)).toHaveLength(lastData().positions.size);
  });

  it("unfolds a cluster on click, never selects it", async () => {
    const onSelect = vi.fn();
    const v = view();
    render(<GraphView {...props(members, { clusterView: v, onSelect })} />);
    await waitFor(() => expect(sceneStub.callbacks).not.toBeNull());
    act(() => sceneStub.callbacks!.onSelect("c:infra/a"));
    expect(v.onUnfold).toHaveBeenCalledWith("c:infra/a");
    expect(onSelect).not.toHaveBeenCalled();
    act(() => sceneStub.callbacks!.onSelect("infra/a"));
    expect(onSelect).toHaveBeenCalledWith("infra/a");
  });

  it("grows a cluster's members out of where it stood", async () => {
    const { rerender } = render(<GraphView {...props({ ...members, nodes: [], edges: [] }, { clusterView: view() })} />);
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    const at = lastData().positions.get("c:infra/a")!;
    const opened = view({
      clusters: clusters.slice(1),
      edges: [],
      unfolded: [{ id: "c:infra/a", label: "Alpha", shown: 2, total: 3400 }],
    });
    rerender(<GraphView {...props(members, { clusterView: opened })} />);
    await waitFor(() => expect(lastData().positions.has("infra/a")).toBe(true));
    expect(lastData().positions.get("infra/a")).toEqual(beside(at, "infra/a"));
  });

  it("says how much of an unfolded cluster is shown, and folds it from its chip or Escape", async () => {
    const v = view({ unfolded: [{ id: "c:infra/a", label: "Alpha", shown: 120, total: 3400 }] });
    render(<GraphView {...props(members, { clusterView: v })} />);
    const chips = screen.getByRole("list", { name: "Unfolded clusters" });
    expect(chips).toHaveTextContent("Showing 120 of 3,400");
    const fold = within(chips).getByRole("button", { name: "Fold Alpha" });
    await userEvent.setup().click(fold);
    expect(v.onFold).toHaveBeenCalledTimes(1);
    fold.focus();
    await userEvent.setup().keyboard("{Escape}");
    expect(v.onFold).toHaveBeenCalledTimes(2);
  });

  it("offers the level switch only when asked, and the replay is off in the cluster view", async () => {
    const onChange = vi.fn();
    const loadBirths = () => Promise.resolve({});
    const { rerender } = render(
      <GraphView {...props(members, { clusterView: view(), loadBirths, graphLevel: { mode: "clusters", onChange } })}>
        <Legend snapshot={members} communities={snapshotCommunities(members)} colorBy="community" onColorBy={() => {}} />
      </GraphView>,
    );
    const level = screen.getByRole("group", { name: "Graph level" });
    expect(within(level).getByRole("button", { name: "Clusters" })).toHaveAttribute("aria-pressed", "true");
    await userEvent.setup().click(within(level).getByRole("button", { name: "Nodes" }));
    expect(onChange).toHaveBeenCalledWith("nodes");
    expect(screen.getByRole("button", { name: "Replay growth" })).toBeDisabled();
    rerender(<GraphView {...props(members, { loadBirths })} />);
    expect(screen.queryByRole("group", { name: "Graph level" })).toBeNull();
  });

  describe("zooming in on a cluster", () => {
    const realMatchMedia = window.matchMedia;
    const width = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "clientWidth");
    const height = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "clientHeight");
    beforeEach(() => {
      Object.defineProperty(HTMLElement.prototype, "clientWidth", { configurable: true, get: () => 800 });
      Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => 600 });
      // The one in the middle, large in view; the other at a corner.
      sceneStub.view = {
        project: (id) => (id === "c:infra/a" ? { x: 400, y: 300 } : { x: 10, y: 10 }),
        radius: 1e6,
      };
    });
    afterEach(() => {
      window.matchMedia = realMatchMedia;
      if (width) Object.defineProperty(HTMLElement.prototype, "clientWidth", width);
      if (height) Object.defineProperty(HTMLElement.prototype, "clientHeight", height);
    });
    const zoom = async (v: ClusterView) => {
      render(<GraphView {...props(members, { clusterView: v })} />);
      await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
      document.querySelector(".graph3d")!.dispatchEvent(new WheelEvent("wheel", { deltaY: -50 }));
      act(() => sceneStub.frame());
      act(() => sceneStub.frame());
    };

    it("unfolds the one in the middle, once per zoom", async () => {
      const v = view();
      await zoom(v);
      expect(v.onUnfold).toHaveBeenCalledTimes(1);
      expect(v.onUnfold).toHaveBeenCalledWith("c:infra/a");
    });

    it("never under reduced motion: a click is the only way", async () => {
      window.matchMedia = ((query: string) => ({
        matches: query.includes("reduce"),
        media: query,
        onchange: null,
        addEventListener() {},
        removeEventListener() {},
        addListener() {},
        removeListener() {},
        dispatchEvent: () => false,
      })) as typeof window.matchMedia;
      const v = view();
      await zoom(v);
      expect(v.onUnfold).not.toHaveBeenCalled();
    });
  });

  it("lists the clusters in the legend, each a button that unfolds it", async () => {
    const onUnfold = vi.fn();
    render(
      <Legend
        snapshot={members}
        communities={snapshotCommunities(members)}
        colorBy="community"
        onColorBy={() => {}}
        clusters={clusters}
        onUnfold={onUnfold}
      />,
    );
    expect(screen.getByText(/2 clusters · 3 concepts/)).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "Unfold Alpha, 3,400 concepts" }));
    expect(onUnfold).toHaveBeenCalledWith("c:infra/a");
  });
});

describe("the cluster view in the Atlas", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/");
    localStorage.clear();
    sessionStorage.clear();
    sceneStub.reset();
  });
  afterEach(() => vi.unstubAllGlobals());

  const large = { ...overview, concepts: { ...overview.concepts, total: 30_000 } };
  const answer: ClusterGraph = {
    mode: "clusters",
    threshold: 2000,
    clusters,
    nodes: [],
    edges: [{ source: "c:infra/a", target: "c:~tail", weight: 5 }],
    unfolded: [],
    dropped_unfolds: [],
    communities: [],
    total_nodes: 30_000,
    total_edges: 90_000,
  };

  it("draws a fully folded KB, unfolds into the URL and keeps the Nodes choice there", async () => {
    stubApi({ "/overview": () => json(large), "/graph/clusters": () => json(answer) });
    render(<App />);
    await screen.findByTestId("graph-view");
    await waitFor(() => expect(sceneStub.callbacks).not.toBeNull());
    await waitFor(() => expect(lastData().positions.has("c:~tail")).toBe(true));
    act(() => sceneStub.callbacks!.onSelect("c:~tail"));
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("unfold")).toBe("c:~tail"));
    const level = await screen.findByRole("group", { name: "Graph level" });
    await userEvent.setup().click(within(level).getByRole("button", { name: "Nodes" }));
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("graph")).toBe("nodes"));
  });

  it("keeps the switch, and its focus, while the clusters answer is on its way", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&graph=nodes");
    let release!: () => void;
    const slow = new Promise<void>((r) => (release = r));
    const m = stubApi({ "/overview": () => json(large) });
    const route = m.getMockImplementation()!;
    m.mockImplementation(async (url: string) => {
      if (!url.includes("/graph/clusters")) return route(url);
      await slow;
      return json(answer);
    });
    const user = userEvent.setup();
    render(<App />);
    const level = await screen.findByRole("group", { name: "Graph level" });
    const clustersButton = within(level).getByRole("button", { name: "Clusters" });
    await user.click(clustersButton);
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("graph")).toBeNull());
    expect(screen.getByRole("group", { name: "Graph level" })).toBeInTheDocument();
    expect(document.activeElement).toBe(clustersButton);
    release();
  });

  it("offers no level switch over a Map the server answers flat", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&scope=infra");
    stubApi({
      "/overview": () => json(large),
      "/graph/clusters": () => json({ ...answer, mode: "nodes", clusters: [], edges: [], nodes: graph.nodes }),
    });
    render(<App />);
    await screen.findByTestId("graph-view");
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    expect(screen.queryByRole("group", { name: "Graph level" })).toBeNull();
  });
});

describe("the legend's cluster list", () => {
  it("lists every cluster on request: the keyboard's way to each one", async () => {
    const many = Array.from({ length: 9 }, (_, i) => ({
      id: `c:m/x${i}`,
      label: `Cluster ${i}`,
      size: 100 - i,
      slot: 1,
      internal_edges: 0,
    }));
    render(
      <Legend
        snapshot={members}
        communities={snapshotCommunities(members)}
        colorBy="community"
        onColorBy={() => {}}
        clusters={many}
        onUnfold={() => {}}
      />,
    );
    expect(screen.getAllByRole("button", { name: /^Unfold / })).toHaveLength(6);
    expect(screen.getByText("Other clusters")).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "Show all 9 clusters" }));
    expect(screen.getAllByRole("button", { name: /^Unfold / })).toHaveLength(9);
    expect(screen.queryByText("Other clusters")).toBeNull();
  });
});
