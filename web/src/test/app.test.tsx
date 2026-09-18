import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { GraphSnapshot, LintReport, Overview } from "../api/types";

/**
 * Boot tests for the whole shell.
 *
 * They exist because the failure they catch is the worst one this UI has: a
 * render-time throw unmounts the tree, and against a dark background that is a
 * black page with nothing in it -- no message, no console breadcrumb the
 * operator would think to look for.
 *
 * Sigma is stubbed: jsdom has no WebGL, and what is under test here is the
 * shell, the data flow and the states, not the renderer.
 *
 * The force-layout worker is deliberately NOT stubbed. jsdom has no
 * URL.createObjectURL, so the supervisor genuinely fails to spawn here -- which
 * is the same thing that happens in a browser whose CSP forbids blob workers.
 * These tests therefore prove the graph degrades to a still picture instead of
 * taking the page down with it.
 */
vi.mock("sigma", () => ({
  default: class SigmaStub {
    on() {}
    kill() {}
    refresh() {}
    setSetting() {}
    viewportToGraph() {
      return { x: 0, y: 0 };
    }
    getGraph() {
      return { hasNode: () => false };
    }
    getNodeDisplayData() {
      return { x: 0, y: 0 };
    }
    getCamera() {
      return { ratio: 1, animate: () => {}, setState: () => {} };
    }
    getMouseCaptor() {
      return { on: () => {} };
    }
  },
}));

const overview: Overview = {
  collections: [
    { name: "infra", title: "Infrastructure", kind: "map", concepts: 2, expanded_concepts: 0 },
    { name: "notes", kind: "journal", concepts: 1, expanded_concepts: 0 },
  ],
  concepts: { total: 3, by_type: { Service: 2, Note: 1 }, by_status: { active: 2 } },
  lint: { total: 4, by_severity: { error: 1, warning: 3 }, by_check: { broken_link: 1 } },
};

const graph: GraphSnapshot = {
  nodes: [
    { id: "infra/a", collection: "infra", type: "Service", status: "active", in_degree: 1, out_degree: 1 },
    { id: "infra/b", collection: "infra", type: "Service", status: "active", in_degree: 1, out_degree: 0 },
    { id: "notes/c", collection: "notes", type: "Note", in_degree: 0, out_degree: 1 },
  ],
  edges: [
    { source: "infra/a", target: "infra/b" },
    { source: "notes/c", target: "infra/a" },
  ],
  total_nodes: 3,
  total_edges: 2,
  limit: 2000,
};

const lint: LintReport = {
  findings: [
    { path: "infra/a.md", concept: "infra/a", check: "broken_link", severity: "error", message: "missing target" },
  ],
  count: 1,
  total: 4,
  by_severity: { error: 1, warning: 3 },
  by_check: { broken_link: 1 },
  severity_min: "info",
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/** Routes a fetch to the right fixture, so a missing route fails loudly here
 *  rather than as an unexplained empty panel. */
function stubApi(overrides: Record<string, () => Response> = {}) {
  const fetchMock = vi.fn(async (url: string) => {
    for (const [fragment, make] of Object.entries(overrides)) {
      if (url.includes(fragment)) return make();
    }
    if (url.includes("/kbs/") && url.includes("/overview")) return json(overview);
    if (url.includes("/kbs/") && url.includes("/graph")) return json(graph);
    if (url.includes("/kbs/") && url.includes("/lint")) return json(lint);
    if (url.includes("/kbs/") && url.includes("/concept")) {
      return json({
        id: "infra/a",
        title: "Alpha",
        collection: "infra",
        frontmatter: { type: "Service", status: "active" },
        body: "# Alpha\n\nA service.",
        body_bytes: 20,
        outline: [{ level: 1, title: "Alpha", bytes: 20 }],
        content_hash: "abc123",
        outbound: ["infra/b"],
        inbound: ["notes/c"],
        broken: [],
      });
    }
    if (url.endsWith("/kbs")) return json({ kbs: [{ name: "homelab", status: "normal", ready: true }] });
    throw new Error(`unstubbed request: ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

describe("the shell boots", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/");
    localStorage.clear();
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("renders the atlas without throwing", async () => {
    stubApi();
    render(<App />);

    // If this never appears, the tree threw during render -- the black-page
    // failure this test exists for.
    expect(await screen.findByRole("banner")).toBeInTheDocument();
    expect(await screen.findByRole("navigation", { name: /atlas navigation/i })).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("region", { name: /concepts in this view/i })).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: /infra\/a/ })).toBeInTheDocument();
  });

  it("lists the visible collections with their counts", async () => {
    stubApi();
    render(<App />);
    expect(await screen.findByRole("button", { name: /Infrastructure/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Whole atlas/ })).toBeInTheDocument();
  });

  it("shows the auth prompt on a 401 instead of a blank page", async () => {
    stubApi({ "/kbs": () => json({ error: { code: "unauthorized", message: "no" } }, 401) });
    render(<App />);
    expect(await screen.findByLabelText(/bearer token/i)).toBeInTheDocument();
  });

  it("names an unreachable server rather than rendering nothing", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.reject(new TypeError("Failed to fetch"))),
    );
    render(<App />);
    expect(await screen.findByText(/server unreachable/i)).toBeInTheDocument();
  });

  it("says a KB is empty rather than showing an empty canvas", async () => {
    stubApi({
      "/graph": () => json({ nodes: [], edges: [], total_nodes: 0, total_edges: 0, limit: 2000 }),
    });
    render(<App />);
    expect(await screen.findByText(/no concepts yet/i)).toBeInTheDocument();
  });

  it("explains a truncated graph instead of presenting it as complete", async () => {
    stubApi({
      "/graph": () => json({ ...graph, truncated: true, total_nodes: 9000 }),
    });
    render(<App />);
    expect(await screen.findByText(/truncated/i)).toBeInTheDocument();
    expect(screen.getByText(/of 9000 concepts/i)).toBeInTheDocument();
  });
});
