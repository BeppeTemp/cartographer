import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { ClusterGraph } from "../api/types";
import { clusterSnapshot, isClusterID } from "../lib/clusters";
import { graph, json, overview, stubApi } from "./fixtures";

// A controllable live tick: bump() is a new revision of the open KB.
const live = vi.hoisted(() => ({ bump: () => {} }));
vi.mock("../lib/live", async () => {
  const React = await import("react");
  return {
    useLiveRevision: () => {
      const [tick, setTick] = React.useState(0);
      live.bump = () => setTick((t) => t + 1);
      return tick;
    },
  };
});

const large = { ...overview, concepts: { ...overview.concepts, total: 30_000 } };

const clusters = (over: Partial<ClusterGraph> = {}): ClusterGraph => ({
  mode: "clusters",
  threshold: 2000,
  clusters: [
    { id: "c:infra/a", label: "Alpha", collection: "infra", size: 1200, slot: 1, internal_edges: 3000 },
    { id: "c:~tail", label: "Other", size: 900, slot: 0, internal_edges: 40, communities: 300 },
  ],
  nodes: graph.nodes,
  edges: [
    { source: "infra/a", target: "infra/b", weight: 1 },
    { source: "c:infra/a", target: "c:~tail", weight: 17 },
    { source: "c:~tail", target: "notes/c", weight: 2 },
  ],
  unfolded: [],
  dropped_unfolds: [],
  communities: [],
  total_nodes: 30_000,
  total_edges: 90_000,
  ...over,
});

function urls(fetchMock: ReturnType<typeof stubApi>): string[] {
  return fetchMock.mock.calls.map((c) => String(c[0]));
}
const clusterCalls = (m: ReturnType<typeof stubApi>) => urls(m).filter((u) => u.includes("/graph/clusters"));
const flatCalls = (m: ReturnType<typeof stubApi>) =>
  urls(m).filter((u) => u.includes("/graph") && !u.includes("/graph/clusters"));
const params = (u: string) => new URL(u, "http://example.com").searchParams;

describe("the clustered graph's data layer", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/");
    localStorage.clear();
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("keeps only concept-to-concept links in the snapshot the rest of the Atlas reads", () => {
    const snap = clusterSnapshot(clusters());
    expect(snap.nodes).toEqual(graph.nodes);
    expect(snap.edges).toEqual([{ source: "infra/a", target: "infra/b" }]);
    expect(snap.total_nodes).toBe(30_000);
    expect(isClusterID("c:~tail")).toBe(true);
    expect(isClusterID("infra/a")).toBe(false);
  });

  it("asks the flat route below the threshold", async () => {
    const m = stubApi();
    render(<App />);
    await screen.findByTestId("graph-view");
    await waitFor(() => expect(flatCalls(m).length).toBeGreaterThan(0));
    expect(clusterCalls(m)).toEqual([]);
  });

  it("asks the clusters route above it", async () => {
    const m = stubApi({ "/overview": () => json(large), "/graph/clusters": () => json(clusters()) });
    render(<App />);
    await waitFor(() => expect(clusterCalls(m).length).toBe(1));
    expect(params(clusterCalls(m)[0]!).get("unfold")).toBeNull();
  });

  it("asks the flat route when the URL says graph=nodes", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&graph=nodes");
    const m = stubApi({ "/overview": () => json(large), "/graph/clusters": () => json(clusters()) });
    render(<App />);
    await screen.findByTestId("graph-view");
    await waitFor(() => expect(flatCalls(m).length).toBeGreaterThan(0));
    await act(async () => {});
    expect(clusterCalls(m)).toEqual([]);
  });

  it("sends the Map, and lets the server answer a small one flat", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&scope=infra");
    const m = stubApi({
      "/overview": () => json(large),
      "/graph/clusters": () => json(clusters({ mode: "nodes", clusters: [], edges: [], total_nodes: 3 })),
    });
    render(<App />);
    await waitFor(() => expect(clusterCalls(m).length).toBe(1));
    expect(params(clusterCalls(m)[0]!).get("scope")).toBe("infra");
  });

  it("keeps the unfold set across a live refresh and drops what the server could not unfold", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&unfold=c%3Ainfra%2Fa%2Cc%3Agone");
    const m = stubApi({
      "/overview": () => json(large),
      "/graph/clusters": () => json(clusters({ dropped_unfolds: ["c:gone"] })),
    });
    render(<App />);
    await waitFor(() => expect(clusterCalls(m).length).toBeGreaterThan(0));
    expect(params(clusterCalls(m)[0]!).get("unfold")).toBe("c:infra/a,c:gone");
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("unfold")).toBe("c:infra/a"));
    await waitFor(() => expect(params(clusterCalls(m).at(-1)!).get("unfold")).toBe("c:infra/a"));
    const before = clusterCalls(m).length;
    act(() => live.bump());
    await waitFor(() => expect(clusterCalls(m).length).toBe(before + 1));
    expect(params(clusterCalls(m).at(-1)!).get("unfold")).toBe("c:infra/a");
  });

  it("starts another KB folded", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&unfold=c%3Ainfra%2Fa");
    const m = stubApi({
      "/overview": () => json(large),
      "/graph/clusters": () => json(clusters()),
    });
    const route = m.getMockImplementation()!;
    m.mockImplementation(async (url: string) =>
      url.endsWith("/kbs")
        ? json({
            kbs: [
              { name: "kb-a", status: "normal", ready: true, artifacts: true },
              { name: "kb-b", status: "normal", ready: true, artifacts: true },
            ],
          })
        : route(url),
    );
    render(<App />);
    await waitFor(() => expect(clusterCalls(m).length).toBeGreaterThan(0));
    const user = userEvent.setup();
    await user.click(await screen.findByRole("combobox", { name: "Knowledge Base" }));
    await user.click(await screen.findByRole("option", { name: /kb-b/ }));
    await waitFor(() => expect(clusterCalls(m).some((u) => u.includes("/kbs/kb-b/"))).toBe(true));
    const kbB = clusterCalls(m).filter((u) => u.includes("/kbs/kb-b/"));
    expect(params(kbB[0]!).get("unfold")).toBeNull();
    expect(new URLSearchParams(window.location.search).get("unfold")).toBeNull();
  });

  it("sends the selected concept as focus, and asks again for one the graph does not draw", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&concept=infra%2Fdeep");
    const m = stubApi({ "/overview": () => json(large), "/graph/clusters": () => json(clusters()) });
    render(<App />);
    await waitFor(() => expect(clusterCalls(m).length).toBeGreaterThan(0));
    expect(params(clusterCalls(m)[0]!).get("focus")).toBe("infra/deep");
    // The answer still does not draw it (an id the server ignored): one
    // request with focus per concept, not a loop.
    await act(async () => {});
    const settled = clusterCalls(m).length;
    await act(async () => {});
    expect(clusterCalls(m).length).toBe(settled);
    expect(clusterCalls(m).every((u) => params(u).get("focus") === "infra/deep")).toBe(true);
  });

  it("asks with focus when a concept the graph does not draw is opened later", async () => {
    const m = stubApi({ "/overview": () => json(large), "/graph/clusters": () => json(clusters()) });
    render(<App />);
    await waitFor(() => expect(clusterCalls(m).length).toBeGreaterThan(0));
    const before = clusterCalls(m).length;
    // A drawn concept costs no request.
    act(() => {
      window.history.pushState(null, "", "/ui/?kb=kb-a&concept=infra%2Fa");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await act(async () => {});
    expect(clusterCalls(m).length).toBe(before);
    act(() => {
      window.history.pushState(null, "", "/ui/?kb=kb-a&concept=notes%2Ffolded");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await waitFor(() => expect(clusterCalls(m).length).toBe(before + 1));
    expect(params(clusterCalls(m).at(-1)!).get("focus")).toBe("notes/folded");
  });

  it("sends focus for a palette pick of a folded concept", async () => {
    const m = stubApi({
      "/overview": () => json(large),
      "/graph/clusters": () => json(clusters()),
      "/search": () => json({ query: "folded", count: 1, results: [{ id: "notes/folded", title: "Folded", score: 1 }] }),
    });
    const user = userEvent.setup();
    render(<App />);
    await waitFor(() => expect(clusterCalls(m).length).toBeGreaterThan(0));
    const before = clusterCalls(m).length;
    await user.keyboard("{Control>}k{/Control}");
    await user.type(await screen.findByRole("textbox", { name: /search concepts/i }), "folded");
    await user.click(await screen.findByRole("option", { name: /Folded/ }));
    await waitFor(() => expect(clusterCalls(m).length).toBeGreaterThan(before));
    expect(params(clusterCalls(m).at(-1)!).get("focus")).toBe("notes/folded");
  });

  it("asks again for a concept that was drawn and folded away since", async () => {
    // The first answer does not draw it (asked once with focus), the second does.
    let answers = 0;
    let drawFolded = () => answers > 1;
    const folded = { id: "notes/folded", collection: "notes", in_degree: 0, out_degree: 0 };
    const m = stubApi({
      "/overview": () => json(large),
      "/graph/clusters": () => {
        answers++;
        return json(clusters(drawFolded() ? { nodes: [...graph.nodes, folded] } : {}));
      },
    });
    window.history.replaceState(null, "", "/ui/?kb=kb-a&concept=notes%2Ffolded");
    render(<App />);
    await waitFor(() => expect(answers).toBe(2));
    await act(async () => {});
    // Deselected, then a refresh folds it; selecting it again asks again.
    drawFolded = () => false;
    act(() => {
      window.history.pushState(null, "", "/ui/?kb=kb-a");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    act(() => live.bump());
    await waitFor(() => expect(params(clusterCalls(m).at(-1)!).get("focus")).toBeNull());
    const before = clusterCalls(m).length;
    act(() => {
      window.history.pushState(null, "", "/ui/?kb=kb-a&concept=notes%2Ffolded");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await waitFor(() => expect(clusterCalls(m).length).toBe(before + 1));
    expect(params(clusterCalls(m).at(-1)!).get("focus")).toBe("notes/folded");
  });
});
