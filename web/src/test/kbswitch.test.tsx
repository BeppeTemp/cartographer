import { act, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { App } from "../App";
import type { ArtifactList, GraphSnapshot } from "../api/types";
import { json, stubApi } from "./fixtures";
import { sceneStub } from "./sceneStub";

const snap = (ids: string[]): GraphSnapshot => ({
  nodes: ids.map((id) => ({ id, collection: id.split("/")[0]!, type: "Note", in_degree: 1, out_degree: 1 })),
  edges: ids.slice(1).map((id) => ({ source: ids[0]!, target: id })),
  total_nodes: ids.length,
  total_edges: ids.length - 1,
  limit: 2000,
});
const skills = (concept: string): ArtifactList => ({
  artifacts: [
    { kind: "skill", name: "review", description: "", content_hash: "a".repeat(64), signed: false, clients: [], files: [], concepts: [concept] },
  ],
  counts: { skill: 1 },
  issues: [],
});

describe("changing KB", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a");
    localStorage.clear();
    sceneStub.reset();
  });
  afterEach(() => sceneStub.reset());

  it("pushes the scene one new picture -- the new KB's graph with its artifacts -- and nothing of the old KB in between", async () => {
    const m = stubApi();
    const route = m.getMockImplementation()!;
    m.mockImplementation(async (url: string) => {
      if (url.endsWith("/kbs")) return json({ kbs: [{ name: "kb-a", status: "normal", ready: true, artifacts: true }, { name: "kb-b", status: "normal", ready: true, artifacts: true }] });
      if (url.includes("/kbs/kb-a/graph")) return json(snap(["a/1", "a/2", "a/3"]));
      if (url.includes("/kbs/kb-a/artifacts")) return json(skills("a/1"));
      if (url.includes("/kbs/kb-b/graph")) {
        await new Promise((r) => setTimeout(r, 30));
        return json(snap(["b/1", "b/2"]));
      }
      if (url.includes("/kbs/kb-b/artifacts")) return json(skills("b/1"));
      return route(url);
    });
    render(<App />);
    await waitFor(() => expect(sceneStub.calls.data.some((c) => c.positions.has("a/1"))).toBe(true));
    // The artifacts of the first KB are in its picture.
    await waitFor(() => expect([...sceneStub.calls.data.at(-1)!.positions.keys()].some((id) => id.startsWith("artifact:"))).toBe(true));
    sceneStub.calls.data.length = 0;

    act(() => {
      window.history.pushState(null, "", "/ui/?kb=kb-b");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await waitFor(() => expect(sceneStub.calls.data.some((c) => c.replace)).toBe(true));
    // Let any second push of the new graph (late artifacts) show itself.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 300));
    });
    const calls = sceneStub.calls.data;
    expect(calls.filter((c) => c.replace)).toHaveLength(1);
    expect(calls).toHaveLength(1);
    const ids = [...calls[0]!.positions.keys()];
    expect(ids.some((id) => id.startsWith("a/"))).toBe(false);
    expect(ids).toEqual(expect.arrayContaining(["b/1", "b/2"]));
    expect(ids.some((id) => id.startsWith("artifact:"))).toBe(true);
  });
});
