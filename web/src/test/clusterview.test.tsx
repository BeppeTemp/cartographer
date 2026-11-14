import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { ClusterGraph, GraphSnapshot } from "../api/types";
import { clusterWeight, edgeStrokes, GraphView, type ClusterView } from "../components/GraphView";
import { Legend } from "../components/Legend";
import { snapshotCommunities } from "../lib/communities";
import { springsOf } from "../lib/graph3d/physics";
import { budgetEdges, clusterSlot, EDGE_BUDGET } from "../lib/clusters";
import { collectionHue } from "../lib/palette";
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

  it("sizes clusters on a square-root scale and draws heavier edges with more strokes", () => {
    expect(clusterWeight(20_000, 20_000)).toBe(1);
    expect(clusterWeight(1, 20_000)).toBeGreaterThan(0.35);
    expect(clusterWeight(200, 20_000)).toBeLessThan(clusterWeight(2000, 20_000));
    // Square-root scale: a quarter of the members, half of the span above the floor.
    expect(clusterWeight(5000, 20_000)).toBeCloseTo(0.35 + 0.65 * 0.5);
    expect([1, 3, 4, 31, 32, 9999].map(edgeStrokes)).toEqual([3, 3, 5, 5, 8, 8]);
  });

  it("colours every super-node by its dominant Map, so the legend, the rail and the graph agree", () => {
    expect(clusterSlot(clusters[0]!)).toBe(collectionHue("infra"));
    expect(clusterSlot(clusters[1]!)).toBe(collectionHue(""));
  });

  it("thins a hairball to the edge budget, every node keeping its strongest links", () => {
    // A hub (n0) linked to 300 leaves, each leaf also linked to the next ten: 3,300 links.
    const edges: { source: string; target: string }[] = [];
    for (let i = 1; i <= 300; i++) {
      edges.push({ source: "n0", target: `n${i}` });
      for (let d = 1; d <= 10; d++) edges.push({ source: `n${i}`, target: `n${((i + d - 1) % 300) + 1}` });
    }
    const degree = (id: string) => (id === "n0" ? 300 : 11);
    expect(budgetEdges(edges, degree, 301, 6000)).toBe(edges);
    const thin = budgetEdges(edges, degree, 301, 1000);
    expect(thin.length).toBeLessThanOrEqual(1000);
    expect(thin.length).toBeGreaterThan(300);
    // Every leaf keeps its link to the hub, the best-connected neighbour.
    expect(thin.filter((e) => e.source === "n0")).toHaveLength(300);
    expect(EDGE_BUDGET).toBe(6000);
  });

  it("makes one weighted spring of the repeated strokes of a super-edge", () => {
    const nodes = new Map([["a", { id: "a", weight: 1 }], ["b", { id: "b", weight: 1 }]]);
    const links = [
      { source: "a", target: "b" },
      { source: "b", target: "a" },
      { source: "a", target: "b" },
    ];
    const springs = springsOf(links, nodes);
    expect(springs).toHaveLength(1);
    expect(springs[0]!.weight).toBe(3);
    // The strokes drawn are given their nodes, the link force never sees them.
    expect(links.every((l) => typeof l.source === "object" && typeof l.target === "object")).toBe(true);
  });

  it("draws every cluster beside the unfolded concepts, and a weighted edge as overlaid strokes", async () => {
    render(<GraphView {...props(members, { clusterView: view() })} />);
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    expect([...lastData().positions.keys()]).toEqual(["infra/a", "infra/b", "c:infra/a", "c:~tail"]);
    // Weight 40: eight strokes of the one edge.
    expect(lastData().links.filter((l) => l === "c:infra/a>c:~tail")).toHaveLength(8);
    expect(lastData().links).toContain("infra/a>infra/b");
    // No banner: the graph says nothing about being clustered.
    expect(document.querySelector(".graph__banner")).toBeNull();
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

  it("a layout recalled after leaving the Atlas keeps its cluster glyphs flagged", async () => {
    const first = render(<GraphView {...props({ ...members, nodes: [], edges: [] }, { clusterView: view() })} />);
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    await new Promise((r) => setTimeout(r, 60));
    first.unmount();
    sceneStub.reset();
    render(<GraphView {...props({ ...members, nodes: [], edges: [] }, { clusterView: view() })} />);
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    expect(lastData().restore).toBeTruthy();
    expect(lastData().glyphs.sort()).toEqual(["c:infra/a", "c:~tail"]);
  });

  it("grows a cluster's members out of where it stood", async () => {
    const { rerender } = render(<GraphView {...props({ ...members, nodes: [], edges: [] }, { clusterView: view() })} />);
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    const at = lastData().positions.get("c:infra/a")!;
    const opened = view({
      clusters: clusters.slice(1),
      edges: [],
      unfolded: [{ id: "c:infra/a", label: "Alpha", shown: 2, total: 3400, slot: 1, collection: "infra", members: [0, 1] }],
    });
    rerender(<GraphView {...props(members, { clusterView: opened })} />);
    await waitFor(() => expect(lastData().positions.has("infra/a")).toBe(true));
    // Around the cluster, not at a seed of the layout, and settled alone.
    const m = lastData().positions.get("infra/a")!;
    expect(Math.hypot(m.x! - at.x!, m.y! - at.y!, m.z! - at.z!)).toBeLessThan(60);
    // The members settle alone with the folded clusters, which make room; the
    // camera goes to the hull, and the hull is the scene's to draw.
    expect(lastData().local?.sort()).toEqual(["c:~tail", "infra/a", "infra/b"]);
    expect(lastData().hulls).toEqual([{ id: "c:infra/a", members: ["infra/a", "infra/b"] }]);
    // Closing it again brings the cluster back, alone, where its members were.
    rerender(<GraphView {...props({ ...members, nodes: [], edges: [] }, { clusterView: view() })} />);
    await waitFor(() => expect(lastData().positions.has("c:infra/a")).toBe(true));
    expect(lastData().local?.sort()).toEqual(["c:infra/a", "c:~tail"]);
    expect(lastData().hulls).toEqual([]);
  });

  it("names a folded cluster for the keyboard and unfolds it from there", async () => {
    const v = view();
    render(<GraphView {...props(members, { clusterView: v })} />);
    const list = screen.getByRole("list", { name: "Clusters" });
    await userEvent.setup().click(within(list).getByRole("button", { name: "cluster Alpha, 3,400 concepts" }));
    expect(v.onUnfold).toHaveBeenCalledWith("c:infra/a");
  });

  it("names the open cluster in a breadcrumb that folds it: the KB and Escape, never the background", async () => {
    const unfolded = [
      { id: "c:infra/a", label: "Alpha", shown: 2, total: 3400, slot: 1, collection: "infra", members: [0, 1] },
      { id: "c:infra/b", label: "Beta", shown: 2, total: 400, slot: 2, collection: "infra", members: [0, 1] },
    ];
    const v = view({ unfolded });
    const onSelect = vi.fn();
    render(<GraphView {...props(members, { clusterView: v, onSelect, kbName: "kb-a" })} />);
    await waitFor(() => expect(sceneStub.callbacks).not.toBeNull());
    const crumbs = screen.getByRole("navigation", { name: "Open cluster" });
    // The last opened, the KB a link back to the overview. No ring, no chip.
    expect(crumbs).toHaveTextContent("kb-a›Beta · 400");
    expect(document.querySelector(".graph3d__open")).toBeNull();
    const user = userEvent.setup();
    await user.click(within(crumbs).getByRole("button", { name: "kb-a" }));
    expect(v.onFold).toHaveBeenCalledWith("c:infra/a");
    expect(v.onFold).toHaveBeenCalledWith("c:infra/b");
    // Escape folds the last opened, one at a time.
    (v.onFold as ReturnType<typeof vi.fn>).mockClear();
    await user.keyboard("{Escape}");
    expect(v.onFold).toHaveBeenCalledTimes(1);
    expect(v.onFold).toHaveBeenCalledWith("c:infra/b");
    // A click on the background only deselects: an open cluster folds by Escape, the breadcrumb or Compact.
    const cb = sceneStub.callbacks as unknown as { onSelect(id: string | null): void };
    (v.onFold as ReturnType<typeof vi.fn>).mockClear();
    act(() => cb.onSelect(null));
    expect(onSelect).toHaveBeenCalledWith(null);
    expect(v.onFold).not.toHaveBeenCalled();
  });

  it("draws no always-on names on the super-nodes: the tooltip and the open chip name them", async () => {
    render(<GraphView {...props(members, { clusterView: view() })} />);
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    expect(document.querySelector(".graph3d__label--cluster")).toBeNull();
  });

  it("has no level switch, and the replay is off in the cluster view", async () => {
    const loadBirths = () => Promise.resolve({});
    render(
      <GraphView {...props(members, { clusterView: view(), loadBirths })}>
        <Legend snapshot={members} />
      </GraphView>,
    );
    expect(screen.queryByRole("group", { name: "Graph level" })).toBeNull();
    expect(screen.getByRole("button", { name: "Replay growth" })).toBeDisabled();
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

  it("lists the clusters in the legend, largest first, then the open concepts, with a count of the rest", () => {
    const many = Array.from({ length: 9 }, (_, i) => ({
      id: `c:m/x${i}`,
      label: `Cluster ${i}`,
      collection: "m",
      size: 100 - i,
      slot: 1,
      internal_edges: 0,
    }));
    render(
      <Legend
        snapshot={{ ...members, nodes: [], edges: [], total_nodes: 4300, total_edges: 9100 }}
        clusters={many}
      />,
    );
    expect(screen.getByText(/4,300 concepts · 9,100 links/)).toBeInTheDocument();
    expect(screen.getByText("Cluster 0")).toBeInTheDocument();
    // Three clusters and a summary: with two Maps and theirs, the card has the flat view's seven rows.
    expect(screen.getByText("Cluster 2")).toBeInTheDocument();
    expect(screen.queryByText("Cluster 3")).toBeNull();
    expect(screen.getByText("6 more clusters")).toBeInTheDocument();
    // No open concepts in this snapshot: no loose rows either.
    expect(screen.queryByText(/Smaller groups|Other Maps/)).toBeNull();
    expect(screen.queryByRole("button", { name: /Unfold|Show all/ })).toBeNull();
  });

  it("holds the clustered card to three cluster rows, two Maps and their summaries", () => {
    const many = Array.from({ length: 5 }, (_, i) => ({ id: `c:m/x${i}`, label: `Cluster ${i}`, collection: "m", size: 50 - i, slot: 1, internal_edges: 0 }));
    const loose = ["m1", "m2", "m3", "m4", "m5"].map((c, i) => ({ ...members.nodes[0]!, id: `${c}/a`, collection: c, title: `T${i}` }));
    const { container } = render(
      <Legend snapshot={{ ...members, nodes: loose, edges: [], total_nodes: 400, total_edges: 10 }} clusters={many} />,
    );
    expect(screen.getByText("Cluster 2")).toBeInTheDocument();
    expect(screen.queryByText("Cluster 3")).toBeNull();
    expect(screen.getByText("2 more clusters")).toBeInTheDocument();
    expect(screen.getByText("3 more Maps")).toBeInTheDocument();
    expect(container.querySelectorAll(".legend__row")).toHaveLength(7);
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
    threshold: 1200,
    clusters,
    nodes: [],
    edges: [{ source: "c:infra/a", target: "c:~tail", weight: 5 }],
    unfolded: [],
    dropped_unfolds: [],
    communities: [],
    total_nodes: 30_000,
    total_edges: 90_000,
  };

  it("explodes and folds a super-node again and again, the URL following both ways", async () => {
    const m = stubApi({ "/overview": () => json(large) });
    const route = m.getMockImplementation()!;
    m.mockImplementation(async (url: string) => {
      if (!url.includes("/graph/clusters")) return route(url);
      const open = (new URL(url, "http://x").searchParams.get("unfold") ?? "").split(",").includes("c:infra/a");
      return json(
        open
          ? {
              ...answer,
              clusters: clusters.slice(1),
              edges: [],
              nodes: graph.nodes.slice(0, 2),
              unfolded: [{ id: "c:infra/a", label: "Alpha", shown: 2, total: 3400, slot: 1, collection: "infra", members: [0, 1] }],
            }
          : answer,
      );
    });
    const user = userEvent.setup();
    render(<App />);
    await screen.findByTestId("graph-view");
    await waitFor(() => expect(lastData().positions.has("c:infra/a")).toBe(true));
    const unfoldParam = () => new URLSearchParams(window.location.search).get("unfold");
    for (let cycle = 0; cycle < 3; cycle++) {
      act(() => sceneStub.callbacks!.onSelect("c:infra/a"));
      await waitFor(() => expect(unfoldParam()).toBe("c:infra/a"));
      await waitFor(() => expect(lastData().positions.has("infra/a")).toBe(true));
      expect(lastData().positions.has("c:infra/a")).toBe(false);
      // The way back: the breadcrumb's KB, or Escape, alternately.
      if (cycle % 2 === 0) await user.click(within(await screen.findByRole("navigation", { name: "Open cluster" })).getByRole("button"));
      else await user.keyboard("{Escape}");
      await waitFor(() => expect(unfoldParam()).toBeNull());
      await waitFor(() => expect(lastData().positions.has("c:infra/a")).toBe(true));
      expect(lastData().positions.has("infra/a")).toBe(false);
    }
  });

  it("opening another cluster folds the one open: an accordion, the URL holding one id", async () => {
    const m = stubApi({ "/overview": () => json(large) });
    const route = m.getMockImplementation()!;
    const two = [
      { id: "c:infra/a", label: "Alpha", collection: "infra", size: 3400, slot: 1, internal_edges: 9000 },
      { id: "c:infra/b", label: "Beta", collection: "infra", size: 900, slot: 2, internal_edges: 40 },
    ];
    m.mockImplementation(async (url: string) => {
      if (!url.includes("/graph/clusters")) return route(url);
      const open = new URL(url, "http://x").searchParams.get("unfold");
      const sel = two.find((c) => c.id === open);
      return json({
        ...answer,
        clusters: two.filter((c) => c !== sel),
        edges: [],
        nodes: sel ? graph.nodes.slice(0, 2) : [],
        unfolded: sel ? [{ id: sel.id, label: sel.label, shown: 2, total: sel.size, slot: sel.slot, members: [0, 1] }] : [],
      });
    });
    render(<App />);
    await screen.findByTestId("graph-view");
    await waitFor(() => expect(lastData().positions.has("c:infra/a")).toBe(true));
    const unfoldParam = () => new URLSearchParams(window.location.search).get("unfold");
    act(() => sceneStub.callbacks!.onSelect("c:infra/a"));
    await waitFor(() => expect(unfoldParam()).toBe("c:infra/a"));
    await waitFor(() => expect(lastData().positions.has("infra/a")).toBe(true));
    // Opening the other folds the first: one id in the URL, Alpha a glyph again.
    act(() => sceneStub.callbacks!.onSelect("c:infra/b"));
    await waitFor(() => expect(unfoldParam()).toBe("c:infra/b"));
    await waitFor(() => expect(lastData().positions.has("c:infra/a")).toBe(true));
    expect(lastData().hulls?.map((h) => h.id)).toEqual(["c:infra/b"]);
    expect(screen.getByRole("navigation", { name: "Open cluster" })).toHaveTextContent("Beta");
  });

  it("draws a fully folded KB and unfolds into the URL, with no switch or banner", async () => {
    stubApi({ "/overview": () => json(large), "/graph/clusters": () => json(answer) });
    render(<App />);
    await screen.findByTestId("graph-view");
    await waitFor(() => expect(sceneStub.callbacks).not.toBeNull());
    await waitFor(() => expect(lastData().positions.has("c:~tail")).toBe(true));
    expect(screen.queryByRole("group", { name: "Graph level" })).toBeNull();
    expect(screen.queryByText(/unfolded/)).toBeNull();
    act(() => sceneStub.callbacks!.onSelect("c:~tail"));
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("unfold")).toBe("c:~tail"));
  });

  it("offers all concepts and grouping on a clustered KB only, asking /graph/clusters or /graph, and opens grouped again", async () => {
    const m = stubApi({ "/overview": () => json(large), "/graph/clusters": () => json(answer) });
    const user = userEvent.setup();
    const { unmount } = render(<App />);
    await screen.findByTestId("graph-view");
    const legend = await screen.findByRole("region", { name: /graph legend/i });
    expect(within(legend).queryByRole("button", { name: "Community" })).toBeNull();
    expect(within(legend).queryByRole("group", { name: /colour nodes by/i })).toBeNull();
    const asked = (part: string) => m.mock.calls.filter(([u]) => String(u).includes(part)).length;
    await waitFor(() => expect(asked("/graph/clusters")).toBeGreaterThan(0));
    await user.click(await within(legend).findByRole("button", { name: "Show all concepts" }));
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("graph")).toBe("nodes"));
    await waitFor(() => expect(m.mock.calls.some(([u]) => /\/graph(\?|$)/.test(String(u)))).toBe(true));
    expect(await within(await screen.findByRole("region", { name: /graph legend/i })).findByRole("button", { name: "Group into clusters" })).toBeInTheDocument();
    // Not remembered: a fresh visit with a bare URL opens a large KB grouped.
    unmount();
    window.history.replaceState(null, "", "/ui/");
    m.mockClear();
    render(<App />);
    await waitFor(() => expect(asked("/graph/clusters")).toBeGreaterThan(0));
    expect(await screen.findByRole("button", { name: "Show all concepts" })).toBeInTheDocument();
  });

  it("morphs between the grouped and the whole layout: Expand and Group keep the concepts both draw", async () => {
    const withNodes: ClusterGraph = { ...answer, nodes: graph.nodes.slice(0, 2).map((n) => ({ ...n })), edges: [] };
    stubApi({ "/overview": () => json(large), "/graph/clusters": () => json(withNodes) });
    const user = userEvent.setup();
    render(<App />);
    const legend = await screen.findByRole("region", { name: /graph legend/i });
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    expect(sceneStub.calls.data[0]).toMatchObject({ replace: false, morph: false });
    const before = sceneStub.calls.data.length;
    await user.click(await within(legend).findByRole("button", { name: "Show all concepts" }));
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(before));
    // A second layout of the same KB (its key carries the mode), not a rebuild from the centre.
    expect(sceneStub.calls.data[before]).toMatchObject({ replace: true, morph: true });
    expect(sceneStub.calls.data[before]!.glyphs).toEqual([]);
  });

  it("folds an open cluster first (Compact), then expands the whole KB", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&unfold=c%3A~tail");
    const tailOpen = {
      ...answer,
      unfolded: [{ id: "c:~tail", label: "Tail", shown: 0, total: 3, slot: 1, collection: "infra", members: [] }],
    };
    let folded = false;
    stubApi({ "/overview": () => json(large), "/graph/clusters": () => json(folded ? answer : tailOpen) });
    const user = userEvent.setup();
    render(<App />);
    const legend = await screen.findByRole("region", { name: /graph legend/i });
    folded = true;
    await user.click(await within(legend).findByRole("button", { name: "Fold the open cluster" }));
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("unfold")).toBeNull());
    expect(new URLSearchParams(window.location.search).get("graph")).toBeNull();
    await user.click(await within(legend).findByRole("button", { name: "Show all concepts" }));
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("graph")).toBe("nodes"));
  });

  it("offers no grouping control on a KB that is not clustered", async () => {
    stubApi();
    render(<App />);
    const legend = await screen.findByRole("region", { name: /graph legend/i });
    expect(within(legend).queryByRole("button", { name: /Show all concepts|Group into clusters/ })).toBeNull();
  });
});
