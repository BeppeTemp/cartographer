import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { GraphView } from "../components/GraphView";
import { Legend } from "../components/Legend";
import type { GraphNode, GraphSnapshot } from "../api/types";
import { snapshotCommunities } from "../lib/communities";
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

function labels() {
  return [...document.querySelectorAll(".legend__label")].map((e) => e.textContent);
}

function card(s: GraphSnapshot, grouping?: { grouped: boolean; onToggle(): void }) {
  return <Legend snapshot={s} grouping={grouping} />;
}

describe("the legend", () => {
  it("keys the Maps, by the Map's colour, and says what is drawn", () => {
    render(card(snap([node("agents/scout", "Scout Agent", 0), node("infra/lab", "Lab", 1)], [{ source: "agents/scout", target: "infra/lab" }])));
    const legend = screen.getByRole("region", { name: "Graph legend" });
    expect(within(legend).queryByText("kb-a")).toBeNull();
    expect(within(legend).getByText("2 concepts · 1 link")).toBeInTheDocument();
    expect(labels()).toEqual(["agents", "infra"]);
  });

  it("has no colour switch: colour is the Map's", () => {
    render(card(full));
    expect(screen.queryByRole("group", { name: "Colour nodes by" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Community" })).toBeNull();
  });

  it("summarises the Maps past the sixth in one closing row", () => {
    const maps = snap(["a", "b", "c", "d", "e", "f", "g", "h"].map((m) => node(`${m}/x`, m, 0)));
    render(card(maps));
    expect(labels()).toEqual(["a", "b", "c", "d", "e", "f", "2 more Maps"]);
    expect(document.querySelector(".legend__row--rest .legend__count")?.textContent).toBe("2");
  });

  it("offers the grouping switch only when given one, and reports the click", async () => {
    const onToggle = vi.fn();
    const { rerender } = render(card(full));
    expect(screen.queryByRole("button", { name: /Show all concepts|Group into clusters/ })).toBeNull();
    rerender(card(full, { grouped: true, onToggle }));
    await userEvent.click(screen.getByRole("button", { name: "Show all concepts" }));
    expect(onToggle).toHaveBeenCalledTimes(1);
    rerender(card(full, { grouped: false, onToggle }));
    expect(screen.getByRole("button", { name: "Group into clusters" })).toBeInTheDocument();
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
