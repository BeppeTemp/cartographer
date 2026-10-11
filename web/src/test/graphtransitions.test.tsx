import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { GraphView } from "../components/GraphView";
import { snapshotCommunities } from "../lib/communities";
import { forgetLayouts } from "../lib/graph3d/transition";
import type { GraphSnapshot } from "../api/types";
import { graph } from "./fixtures";
import { sceneStub } from "./sceneStub";

/**
 * The three moments the graph must not restart or jump: coming back to the
 * Atlas, changing Map, and closing the inspector.
 */
const scoped: GraphSnapshot = {
  ...graph,
  nodes: graph.nodes.filter((n) => n.collection === "infra"),
  edges: [{ source: "infra/a", target: "infra/b" }],
};

const NONE = new Set<string>();
function props(snapshot: GraphSnapshot, layoutKey: string, extra: Record<string, unknown> = {}) {
  return {
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
    ...extra,
  };
}

const drawn = () => sceneStub.calls.data.length;
/** Waits until the scene has been given data and the view has stopped re-sending it. */
async function settled(): Promise<number> {
  await waitFor(() => expect(drawn()).toBeGreaterThan(0));
  let n = -1;
  while (n !== drawn()) {
    n = drawn();
    await new Promise((r) => setTimeout(r, 60));
  }
  return n;
}

describe("graph transitions", () => {
  beforeEach(() => {
    sceneStub.reset();
    forgetLayouts();
  });

  it("opens a KB seen for the first time with its birth, nothing restored", async () => {
    render(<GraphView {...props(graph, "kb-a\u0000")} />);
    await settled();
    expect(sceneStub.calls.data[0]).toMatchObject({ replace: false, morph: false, restore: undefined });
  });

  it("brings the Atlas back as it was left: same positions, the camera restored, no birth", async () => {
    const first = render(<GraphView {...props(graph, "kb-a\u0000")} />);
    const n = await settled();
    const placed = sceneStub.calls.data[n - 1]!.positions;
    first.unmount();

    render(<GraphView {...props(graph, "kb-a\u0000")} />);
    await waitFor(() => expect(drawn()).toBeGreaterThan(n));
    const back = sceneStub.calls.data[n]!;
    expect(back.restore).toMatchObject({ pose: { position: { x: 1, y: 2, z: 3 } }, shift: 0 });
    expect(back.replace).toBe(false);
    for (const [id, p] of placed) expect(back.positions.get(id)).toEqual(p);
  });

  it("does not restore across layouts", async () => {
    const first = render(<GraphView {...props(graph, "kb-a\u0000")} />);
    const n = await settled();
    first.unmount();
    render(<GraphView {...props(graph, "kb-b\u0000")} />);
    await waitFor(() => expect(drawn()).toBeGreaterThan(n));
    expect(sceneStub.calls.data[n]!.restore).toBeUndefined();
  });

  it("morphs to another Map of the same KB, the nodes both draw keeping their place", async () => {
    const view = render(<GraphView {...props(graph, "kb-a\u0000")} />);
    const n = await settled();
    const before = sceneStub.calls.data[n - 1]!.positions;

    view.rerender(<GraphView {...props(scoped, "kb-a\u0000infra")} />);
    await waitFor(() => expect(drawn()).toBeGreaterThan(n));
    const after = sceneStub.calls.data[n]!;
    expect(after).toMatchObject({ replace: true, morph: true });
    expect([...after.positions.keys()].sort()).toEqual(["infra/a", "infra/b"]);
    for (const id of ["infra/a", "infra/b"]) expect(after.positions.get(id)).toEqual(before.get(id));
  });

  it("places a node new to the view beside one already placed, not at the origin", async () => {
    const view = render(<GraphView {...props(scoped, "kb-a\u0000infra")} />);
    const n = await settled();
    const before = sceneStub.calls.data[n - 1]!.positions;

    view.rerender(<GraphView {...props(graph, "kb-a\u0000")} />);
    await waitFor(() => expect(drawn()).toBeGreaterThan(n));
    const c = sceneStub.calls.data[n]!.positions.get("notes/c")!;
    const a = before.get("infra/a")!;
    expect(c).not.toEqual({ x: 0, y: 0, z: 0 });
    // notes/c links to infra/a: it starts within a link's reach of it.
    expect(Math.hypot(c.x! - a.x!, c.y! - a.y!, c.z! - a.z!)).toBeLessThan(60);
  });

  it("opens another KB from its centre: nothing in common to morph from", async () => {
    const view = render(<GraphView {...props(graph, "kb-a\u0000")} />);
    const n = await settled();
    view.rerender(<GraphView {...props(graph, "kb-b\u0000")} />);
    await waitFor(() => expect(drawn()).toBeGreaterThan(n));
    expect(sceneStub.calls.data[n]).toMatchObject({ replace: true, morph: false });
  });

  it("closing the inspector eases back to the pose it left, with no cut and no refit", async () => {
    const view = render(<GraphView {...props(graph, "kb-a\u0000", { selected: "infra/a", occludedRight: 420 })} />);
    await waitFor(() => expect(sceneStub.calls.focus).toContain("infra/a"));
    const refits = sceneStub.calls.fit.length;

    view.rerender(<GraphView {...props(graph, "kb-a\u0000", { selected: null, occludedRight: 0 })} />);
    await waitFor(() => expect(sceneStub.calls.unfocus).toBe(1));
    // One unfocus, handed the pose saved when the selection opened; the
    // occlusion change alone never refocuses or refits the graph.
    expect(sceneStub.calls.unfocusArgs[0]).toMatchObject({ position: { x: 1, y: 2, z: 3 } });
    await new Promise((r) => setTimeout(r, 250));
    expect(sceneStub.calls.focus).toEqual(["infra/a"]);
    expect(sceneStub.calls.fit.length).toBe(refits);
  });
});
