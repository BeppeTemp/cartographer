import { render, screen } from "@testing-library/react";
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

function mount() {
  return render(
    <GraphView
      snapshot={snapshot}
      layoutKey="kb-a"
      loadBirths={async () => births}
      kbName="kb-a"
      communities={snapshotCommunities(snapshot)}
      colorBy="community"
      selected={null}
      hiddenIds={new Set<string>()}
      themeKey="dark"
      live={false}
      occludedRight={0}
      onSelect={() => {}}
    >
      <GrowthButton />
    </GraphView>,
  );
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
});
