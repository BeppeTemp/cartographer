import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { json, stubApi } from "./fixtures";

/** The Map's contract is read in the reading panel while the Atlas is filtered to it. */

const contract = {
  name: "infra",
  title: "Infrastructure",
  kind: "map",
  ontology_mode: "strict",
  concept_types: ["Service", "Host"],
  expanded_concepts: 3,
  stale_after: 90,
  stale_after_defaulted: true,
  lint_ignore: ["orphan"],
  field_values: { status: ["active", "draft"] },
  field_values_by_type: { Host: { role: ["edge"] } },
};

function stub(maps: unknown[] = [contract]) {
  return stubApi({ "/maps": () => json({ maps }) });
}

function open(search: string) {
  window.history.replaceState(null, "", `/ui/${search}`);
  localStorage.clear();
  localStorage.setItem("cartographer.panel.rail", "0");
  sessionStorage.clear();
}

afterEach(() => vi.unstubAllGlobals());

describe("the Map card", () => {
  it("shows the contract of the selected Map in a card over the graph, in place of the legend", async () => {
    open("?kb=kb-a");
    const fetchMock = stub();
    const user = userEvent.setup();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    expect(screen.queryByRole("complementary", { name: /^Map /i })).not.toBeInTheDocument();
    await user.click(await within(rail).findByRole("button", { name: /Infrastructure/ }));

    const panel = await screen.findByRole("complementary", { name: "Map Infrastructure" });
    // A card over the graph, not the reading panel: the graph keeps the canvas.
    expect(document.getElementById("reading-panel")).toBeNull();
    expect(panel).toHaveClass("map-card");
    expect(within(panel).getByText("infra")).toBeInTheDocument();
    // The rail's count, with the expanded ones named on top.
    expect(within(panel).getByText("2")).toBeInTheDocument();
    expect(within(panel).getByText("(3 expanded)")).toBeInTheDocument();
    expect(within(panel).getByText("90 days")).toBeInTheDocument();
    expect(within(panel).getByText("(default)")).toBeInTheDocument();
    expect(within(panel).getByText(/^Service/)).toBeInTheDocument();
    expect(within(panel).getByText(/orphan/)).toBeInTheDocument();
    expect(within(panel).getByText(/active, draft/)).toBeInTheDocument();
    expect(within(panel).getByText(/edge/)).toBeInTheDocument();
    // No close button: picking "All" in the rail is the way back.
    expect(within(panel).queryByRole("button")).toBeNull();
    // The legend's counts are the whole KB's: with a Map picked, the card is the key.
    expect(screen.queryByRole("region", { name: /graph legend/i })).not.toBeInTheDocument();
    // Fetched once, not per click.
    expect(fetchMock.mock.calls.filter(([u]) => String(u).includes("/maps"))).toHaveLength(1);
  });

  it("steps aside for the concept inspector, like the legend, and comes back when the concept closes", async () => {
    open("?kb=kb-a&scope=infra");
    stub();
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole("complementary", { name: "Map Infrastructure" });

    await user.keyboard("{Control>}k{/Control}");
    await user.keyboard("infra/a{Enter}");
    expect(await screen.findByRole("complementary", { name: /inspector for infra\/a/i })).toBeInTheDocument();
    expect(screen.queryByRole("complementary", { name: "Map Infrastructure" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /close inspector/i }));
    expect(await screen.findByRole("complementary", { name: "Map Infrastructure" })).toBeInTheDocument();
  });

  it("goes when All is picked in the rail, and the legend comes back", async () => {
    open("?kb=kb-a&scope=infra");
    stub();
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole("complementary", { name: "Map Infrastructure" });
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    await user.click(within(rail).getByRole("button", { name: /^All/ }));
    await waitFor(() => expect(window.location.search).not.toContain("scope="));
    expect(screen.queryByRole("complementary", { name: "Map Infrastructure" })).not.toBeInTheDocument();
    expect(await screen.findByRole("region", { name: /graph legend/i })).toBeInTheDocument();
  });

  it("leaves out the fields a Map does not declare", async () => {
    open("?kb=kb-a&scope=infra");
    stub([{ name: "infra", title: "Infrastructure", expanded_concepts: 0 }]);
    render(<App />);
    const panel = await screen.findByRole("complementary", { name: "Map Infrastructure" });
    expect(within(panel).queryByText("stale after")).not.toBeInTheDocument();
    expect(within(panel).queryByText("types")).not.toBeInTheDocument();
    expect(within(panel).queryByText("lint ignore")).not.toBeInTheDocument();
    expect(within(panel).queryByText(/expanded/)).not.toBeInTheDocument();
  });

  it("has no page of its own: no rail entry, and ?panel=maps falls back to the Atlas", async () => {
    open("?kb=kb-a&panel=maps");
    stub();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    expect(within(rail).queryByRole("button", { name: "Maps" })).not.toBeInTheDocument();
    expect(await screen.findByTestId("graph-view")).toBeInTheDocument();
    expect(await within(rail).findByRole("button", { name: /^All/ })).toHaveAttribute("aria-current", "true");
  });
});
