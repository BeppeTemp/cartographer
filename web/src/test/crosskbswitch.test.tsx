import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { App } from "../App";
import type { Concept, GraphSnapshot } from "../api/types";
import { json, stubApi } from "./fixtures";
import { sceneStub } from "./sceneStub";

const kbs = { kbs: [{ name: "kb-a", status: "normal", ready: true, artifacts: false }, { name: "kb-b", status: "normal", ready: true, artifacts: false }] };
const snap = (ids: string[]): GraphSnapshot => ({
  nodes: ids.map((id) => ({ id, collection: id.split("/")[0]!, type: "Note", in_degree: 1, out_degree: 1 })),
  edges: [],
  total_nodes: ids.length,
  total_edges: 0,
  limit: 2000,
});
const concept = (body: string): Concept => ({
  id: "index",
  title: "Index",
  collection: "",
  frontmatter: {},
  body,
  body_bytes: body.length,
  outline: [],
  content_hash: "h",
  outbound: [],
  inbound: [],
  broken: [],
  cross: [],
});
const goTo = (search: string) =>
  act(() => {
    window.history.pushState(null, "", `/ui/${search}`);
    window.dispatchEvent(new PopStateEvent("popstate"));
  });

describe("changing KB with cross-KB data", () => {
  beforeEach(() => {
    localStorage.clear();
    sceneStub.reset();
  });
  afterEach(() => sceneStub.reset());

  it("a page with the same id in the other KB never shows the old KB's body while it loads", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&concept=index");
    const m = stubApi();
    const route = m.getMockImplementation()!;
    m.mockImplementation(async (url: string) => {
      if (url.endsWith("/kbs")) return json(kbs);
      if (url.includes("/kbs/kb-a/concept")) return json(concept("Body of the first KB"));
      if (url.includes("/kbs/kb-b/concept")) {
        await new Promise((r) => setTimeout(r, 400));
        return json(concept("Body of the second KB"));
      }
      return route(url);
    });
    render(<App />);
    expect(await screen.findByText("Body of the first KB")).toBeInTheDocument();
    goTo("?kb=kb-b&concept=index");
    await waitFor(() => expect(screen.queryByText("Body of the first KB")).toBeNull());
    expect(await screen.findByText("Body of the second KB")).toBeInTheDocument();
  });

  it("the stubs of one KB's external edges are never drawn under the other KB", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a");
    const m = stubApi();
    const route = m.getMockImplementation()!;
    m.mockImplementation(async (url: string) => {
      if (url.endsWith("/kbs")) return json(kbs);
      if (url.includes("/kbs/kb-a/graph/external"))
        return json({ edges: [{ from: "a/1", direction: "out", kb: "kb-c", id: "p/q", state: "resolved" }], limit: 2000, truncated: false });
      if (url.includes("/kbs/kb-b/graph/external")) {
        await new Promise((r) => setTimeout(r, 200));
        return json({ edges: [], limit: 2000, truncated: false });
      }
      if (url.includes("/kbs/kb-a/graph")) return json(snap(["a/1", "a/2"]));
      if (url.includes("/kbs/kb-b/graph")) return json(snap(["b/1", "b/2"]));
      return route(url);
    });
    render(<App />);
    await waitFor(() => expect(sceneStub.calls.data.some((c) => c.positions.has("kb:kb-c/p/q"))).toBe(true));
    sceneStub.calls.data.length = 0;
    goTo("?kb=kb-b");
    await waitFor(() => expect(sceneStub.calls.data.some((c) => c.positions.has("b/1"))).toBe(true));
    await act(async () => {
      await new Promise((r) => setTimeout(r, 400));
    });
    expect(sceneStub.calls.data.some((c) => c.positions.has("kb:kb-c/p/q"))).toBe(false);
  });
});
