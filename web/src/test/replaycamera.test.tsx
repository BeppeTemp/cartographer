import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { GraphView, GrowthButton } from "../components/GraphView";
import { snapshotCommunities } from "../lib/communities";
import type { GraphSnapshot } from "../api/types";
import { sceneStub } from "./sceneStub";

// A chain born one day apart, then a loose concept: a jump to the end brings
// concepts whose neighbour is itself new, and one with no neighbour at all.
const ids = ["k/a", "k/b", "k/c", "k/d", "k/loose"];
const snapshot: GraphSnapshot = {
  nodes: ids.map((id) => ({ id, collection: "k", type: "Note", in_degree: 0, out_degree: 0 })),
  edges: [
    { source: "k/a", target: "k/b" },
    { source: "k/b", target: "k/c" },
    { source: "k/c", target: "k/d" },
  ],
  total_nodes: ids.length,
  total_edges: 3,
  limit: 2000,
};
const births = Object.fromEntries(ids.map((id, i) => [id, `2026-01-0${i + 1}T00:00:00Z`]));

function view(snap: GraphSnapshot, layoutKey: string, loadBirths: () => Promise<Record<string, string>>) {
  return (
    <GraphView
      snapshot={snap}
      layoutKey={layoutKey}
      loadBirths={loadBirths}
      kbName={layoutKey}
      communities={snapshotCommunities(snap)}
      colorBy="community"
      selected={null}
      hiddenIds={new Set<string>()}
      themeKey="dark"
      live={false}
      occludedRight={0}
      onSelect={() => {}}
    >
      <GrowthButton />
    </GraphView>
  );
}
function mount() {
  return render(view(snapshot, "kb-a", async () => births));
}

describe("growth replay camera", () => {
  beforeEach(() => sceneStub.reset());

  it("seeds a jump forward beside the concepts out, never stacked on one spot", async () => {
    mount();
    await userEvent.click(screen.getByRole("button", { name: "Replay growth" }));
    const slider = await screen.findByRole("slider", { name: "Replay position" });
    slider.focus();
    await userEvent.keyboard("{Home}{End}");
    const last = sceneStub.calls.data.at(-1)!;
    expect(last.positions.size).toBe(ids.length);
    const spots = [...last.positions.values()].map((p) => `${p.x?.toFixed(3)},${p.y?.toFixed(3)},${p.z?.toFixed(3)}`);
    expect(new Set(spots).size).toBe(ids.length);
  });

  it("closing the replay sends the camera back to the fit of the whole graph", async () => {
    mount();
    await userEvent.click(screen.getByRole("button", { name: "Replay growth" }));
    await userEvent.click(await screen.findByRole("button", { name: /close/i }));
    const drawn = sceneStub.calls.data.at(-1)!;
    expect(drawn.positions.size).toBe(ids.length);
    const last = sceneStub.calls.follow.at(-1)!;
    expect(last.floor).toBe(0);
    expect(last.after).toBe(sceneStub.calls.data.length);
  });

  it("a KB switch with the replay open shows no replay and does not throw (playing, paused, finished)", async () => {
    const other: GraphSnapshot = {
      ...snapshot,
      nodes: ["z/a", "z/b"].map((id) => ({ id, collection: "z", type: "Note", in_degree: 0, out_degree: 0 })),
      edges: [{ source: "z/a", target: "z/b" }],
      total_nodes: 2,
      total_edges: 1,
    };
    for (const how of ["playing", "paused", "finished"]) {
      sceneStub.reset();
      const loadBirths = async () => births;
      const { rerender, unmount } = render(view(snapshot, "kb-a", loadBirths));
      await userEvent.click(screen.getByRole("button", { name: "Replay growth" }));
      const slider = await screen.findByRole("slider", { name: "Replay position" });
      if (how !== "playing") {
        slider.focus();
        await userEvent.keyboard(how === "finished" ? "{End}" : "{Home}");
      }
      rerender(view(other, "kb-b", async () => ({})));
      expect(screen.queryByRole("slider", { name: "Replay position" })).toBeNull();
      expect(sceneStub.calls.data.at(-1)!.positions.size).toBe(2);
      unmount();
    }
  });

  it("a refresh that drops a concept already out, in the same layout, does not throw", async () => {
    const { rerender } = render(view(snapshot, "kb-a", async () => births));
    await userEvent.click(screen.getByRole("button", { name: "Replay growth" }));
    const slider = await screen.findByRole("slider", { name: "Replay position" });
    slider.focus();
    await userEvent.keyboard("{End}");
    const next: GraphSnapshot = {
      ...snapshot,
      nodes: [...snapshot.nodes.filter((n) => n.id !== "k/b"), { id: "k/new", collection: "k", type: "Note", in_degree: 0, out_degree: 0 }],
      edges: [],
    };
    rerender(view(next, "kb-a", async () => births));
    expect(sceneStub.calls.data.at(-1)!.positions.size).toBe(next.nodes.length);
  });

  it("births that arrive after a KB switch open no replay", async () => {
    let resolve!: (b: Record<string, string>) => void;
    const late = new Promise<Record<string, string>>((r) => (resolve = r));
    const { rerender } = render(view(snapshot, "kb-a", () => late));
    await userEvent.click(screen.getByRole("button", { name: "Replay growth" }));
    rerender(view(snapshot, "kb-b", async () => births));
    await act(async () => resolve(births));
    expect(screen.queryByRole("slider", { name: "Replay position" })).toBeNull();
  });
});
