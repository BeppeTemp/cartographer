import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { json, stubApi } from "./fixtures";

/** The Map the Atlas is narrowed to: named in the context bar, its contract under its row in the rail. */

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

describe("the Map's contract", () => {
  it("shows under the selected Map's row in the rail, with the Map named in the bar", async () => {
    open("?kb=kb-a");
    const fetchMock = stub();
    const user = userEvent.setup();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    expect(within(rail).queryByLabelText(/^Contract of/)).toBeNull();
    await user.click(await within(rail).findByRole("button", { name: /Infrastructure/ }));

    const facts = await within(rail).findByLabelText("Contract of Infrastructure");
    expect(within(facts).getByText("strict")).toBeInTheDocument();
    expect(within(facts).getByText("90 days")).toBeInTheDocument();
    expect(within(facts).getByText("(default)")).toBeInTheDocument();
    expect(within(facts).getByText("3")).toBeInTheDocument();
    expect(within(facts).getByText("Service, Host")).toBeInTheDocument();
    expect(within(facts).getByText("orphan")).toBeInTheDocument();
    expect(within(facts).getByText(/active, draft/)).toBeInTheDocument();
    expect(within(facts).getByText(/edge/)).toBeInTheDocument();

    const bar = screen.getByRole("region", { name: "Graph context" });
    expect(within(bar).getByText("Infrastructure")).toBeInTheDocument();
    expect(within(bar).getByText("2 concepts")).toBeInTheDocument();
    // Nothing over the graph and nothing in the reading panel.
    expect(document.getElementById("reading-panel")).toBeNull();
    // The replay is the whole KB's story: nothing offers it inside one Map.
    expect(screen.queryByRole("button", { name: "Replay growth" })).toBeNull();
    // Fetched once, not per click.
    expect(fetchMock.mock.calls.filter(([u]) => String(u).includes("/maps"))).toHaveLength(1);
  });

  it("goes with the Map when the Atlas entry is picked", async () => {
    open("?kb=kb-a&scope=infra");
    stub();
    const user = userEvent.setup();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    await within(rail).findByLabelText("Contract of Infrastructure");
    await user.click(within(rail).getByRole("button", { name: /^Atlas/ }));
    await waitFor(() => expect(window.location.search).not.toContain("scope="));
    expect(within(rail).queryByLabelText(/^Contract of/)).toBeNull();
    expect(within(screen.getByRole("region", { name: "Graph context" })).queryByText("Infrastructure")).toBeNull();
  });

  it("leaves out the fields a Map does not declare", async () => {
    open("?kb=kb-a&scope=infra");
    stub([{ name: "infra", title: "Infrastructure", expanded_concepts: 0 }]);
    render(<App />);
    const facts = await screen.findByLabelText("Contract of Infrastructure");
    expect(within(facts).queryByText("stale after")).not.toBeInTheDocument();
    expect(within(facts).queryByText("types")).not.toBeInTheDocument();
    expect(within(facts).queryByText("lint ignore")).not.toBeInTheDocument();
    expect(within(facts).queryByText("expanded")).not.toBeInTheDocument();
  });

  it("has no page of its own: no rail entry, and ?panel=maps falls back to the Atlas", async () => {
    open("?kb=kb-a&panel=maps");
    stub();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    expect(within(rail).queryByRole("button", { name: "Maps" })).not.toBeInTheDocument();
    expect(await screen.findByTestId("graph-view")).toBeInTheDocument();
    expect(within(rail).getByRole("button", { name: /^Atlas/ })).toHaveAttribute("aria-current", "page");
  });
});
