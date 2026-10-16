import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { GraphView } from "../components/GraphView";
import { Legend } from "../components/Legend";
import type { GraphNode, GraphSnapshot } from "../api/types";
import { snapshotCommunities } from "../lib/communities";
import type { ColorBy } from "../lib/palette";
import { sceneStub } from "./sceneStub";

const node = (id: string, title: string, community: number): GraphNode =>
  ({ id, title, community, collection: id.split("/")[0] }) as GraphNode;

// The server names every anchor by title, whether or not its node is in the snapshot.
const communities = [
  { rank: 0, size: 5, anchor: "agents/scout", anchor_title: "Scout Agent", slot: 1 },
  { rank: 1, size: 3, anchor: "infra/lab", anchor_title: "Lab", slot: 2 },
];

function snap(nodes: GraphNode[], edges: GraphSnapshot["edges"] = []): GraphSnapshot {
  return { nodes, edges, total_nodes: nodes.length, total_edges: edges.length, limit: 2000, communities };
}

const full = snap([node("agents/scout", "Scout Agent", 0), node("infra/lab", "Lab", 1)]);
// A Map's snapshot: the whole-KB communities, but only that Map's nodes.
const scoped = snap([node("infra/lab", "Lab", 1), node("infra/x", "X", 0)]);

function labels() {
  return [...document.querySelectorAll(".legend__label")].map((e) => e.textContent);
}

function card(s: GraphSnapshot, colorBy: ColorBy = "community", onColorBy: (v: ColorBy) => void = () => {}) {
  return <Legend snapshot={s} communities={snapshotCommunities(s)} colorBy={colorBy} onColorBy={onColorBy} />;
}

describe("the community legend", () => {
  it("names communities by the anchor's title even when a Map's snapshot lacks the anchor node", () => {
    const { rerender } = render(card(full));
    const all = labels();
    expect(all).toContain("Scout Agent");
    rerender(card(scoped));
    expect(labels()).toEqual(all);
    expect(screen.queryByText("scout")).toBeNull();
  });

  it("says what is drawn, its concepts and its links, not the KB the picker names", () => {
    render(card(snap([node("agents/scout", "Scout Agent", 0), node("infra/lab", "Lab", 1)], [{ source: "agents/scout", target: "infra/lab" }])));
    const legend = screen.getByRole("region", { name: "Graph legend" });
    expect(within(legend).queryByText("kb-a")).toBeNull();
    expect(within(legend).getByText("2 concepts · 1 link")).toBeInTheDocument();
  });

  it("summarises the communities past the sixth, and the neutral tail, in one closing row", () => {
    const many = Array.from({ length: 8 }, (_, i) => ({
      rank: i,
      size: 10 - i,
      anchor: `k/c${i}`,
      anchor_title: `Group ${i}`,
      slot: i + 1,
    }));
    const nodes = many.map((_, i) => node(`k/c${i}`, `Group ${i}`, i));
    const s: GraphSnapshot = { nodes, edges: [], total_nodes: 8, total_edges: 0, limit: 2000, communities: many };
    render(card(s));
    expect(labels()).toEqual(["Group 0", "Group 1", "Group 2", "Group 3", "Group 4", "Group 5", "Smaller groups"]);
    expect(document.querySelector(".legend__row--rest .legend__count")?.textContent).toBe("2");
  });
});

describe("the colour switch", () => {
  it("offers Community and Map, reports the choice and keys the Maps when asked", async () => {
    const onColorBy = vi.fn();
    const maps = snap(
      ["a", "b", "c", "d", "e", "f", "g", "h"].map((m) => node(`${m}/x`, m, 0)),
    );
    const { rerender } = render(card(maps, "community", onColorBy));
    const switcher = screen.getByRole("group", { name: "Colour nodes by" });
    expect(within(switcher).getByRole("button", { name: "Community" })).toHaveAttribute("aria-pressed", "true");
    expect(within(switcher).getByRole("button", { name: "Map" })).toHaveAttribute("aria-pressed", "false");
    await userEvent.click(within(switcher).getByRole("button", { name: "Map" }));
    expect(onColorBy).toHaveBeenCalledWith("map");

    rerender(card(maps, "map", onColorBy));
    // Six Maps keyed, the rest summed: the card stays a key, not a second list.
    expect(labels()).toEqual(["a", "b", "c", "d", "e", "f", "Other Maps"]);
    expect(document.querySelector(".legend__row--rest .legend__count")?.textContent).toBe("2");
  });
});

describe("the replay, from the card", () => {
  beforeEach(() => sceneStub.reset());

  const births = { "agents/scout": "2026-01-01T00:00:00Z", "infra/lab": "2026-02-01T00:00:00Z" };
  const mount = (loadBirths?: () => Promise<Record<string, string>>) =>
    render(
      <GraphView
        snapshot={full}
        layoutKey="kb-a"
        loadBirths={loadBirths}
        kbName="kb-a"
        communities={snapshotCommunities(full)}
        colorBy="community"
        selected={null}
        hiddenIds={new Set<string>()}
        themeKey="dark"
        live={false}
        occludedRight={0}
        onSelect={() => {}}
      >
        {card(full)}
      </GraphView>,
    );

  it("starts the growth replay from the legend, and not from the camera controls", async () => {
    mount(async () => births);
    const camera = screen.getByRole("group", { name: "Graph camera" });
    expect(within(camera).queryByRole("button", { name: "Replay growth" })).toBeNull();
    const legend = screen.getByRole("region", { name: "Graph legend" });
    await userEvent.click(within(legend).getByRole("button", { name: "Replay growth" }));
    expect(await screen.findByRole("group", { name: "Growth replay" })).toBeInTheDocument();
    expect(screen.getByTestId("graph-view")).toHaveAttribute("data-growing");
  });

  it("offers no replay where the view has none to give", () => {
    mount(undefined);
    expect(screen.getByRole("region", { name: "Graph legend" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Replay growth" })).toBeNull();
  });
});
