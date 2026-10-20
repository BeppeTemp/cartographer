import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MapContract } from "../api/types";
import { MapContractPanel, MAP_PANEL_WIDTH } from "../components/MapContractPanel";
import { json, stubApi } from "./fixtures";
import { LivingScene } from "./sceneStub";

/**
 * A Map's contract is a panel docked to the right edge of the graph area while
 * the Atlas is narrowed to the Map (D818): titled sections, each value with a
 * line saying what the server does with it. The rail no longer holds it.
 */

/** What map_list returns for a Map that sets most of the contract. */
const contract: MapContract = {
  name: "infra",
  title: "Infrastructure",
  description: "Hosts, services and how they connect.",
  kind: "map",
  ontology_mode: "strict",
  concept_types: ["Service", "Host"],
  expanded_concepts: 3,
  required_fields: { "*": ["timestamp"], Host: ["provenance"] },
  forbidden_fields: ["state"],
  templates: ["host", "service"],
  default_template: "service",
  field_values: { "*": { status: ["active", "draft"] }, Host: { role: ["edge", "core"] } },
  value_synonyms: { active: ["live", "in use"] },
  open_field: "phase",
  open_statuses: ["open", "blocked"],
  open_markers: ["TODO"],
  stale_after: 90,
  stale_after_defaulted: true,
  promote_to: "runbooks",
  procedure_headings: ["steps"],
  glossary: true,
  work_map: "tasks",
  index: "generated",
  forbidden_title_terms: ["wip"],
};

const bare: MapContract = { name: "infra", title: "Infrastructure", expanded_concepts: 0 };

const realMatchMedia = window.matchMedia;
function viewport(narrow: boolean) {
  window.matchMedia = ((query: string) => ({
    matches: narrow && query.includes("max-width: 1023px"),
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
}

function stub(maps: unknown[] = [contract]) {
  return stubApi({ "/maps": () => json({ maps }) });
}

function open(search: string) {
  window.history.replaceState(null, "", `/ui/${search}`);
  localStorage.clear();
  localStorage.setItem("cartographer.panel.rail", "0");
  sessionStorage.clear();
}

afterEach(() => {
  window.matchMedia = realMatchMedia;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("the Map's contract panel", () => {
  it("docks beside the graph when a Map is picked, without the legend, the replay or the camera controls", async () => {
    open("?kb=kb-a");
    const fetchMock = stub();
    const user = userEvent.setup();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    // Whole KB: the legend card is the colour key and the replay's home; no contract.
    expect(await screen.findByRole("region", { name: "Graph legend" })).toBeInTheDocument();
    expect(screen.queryByLabelText(/^Contract of/)).toBeNull();
    expect(screen.getByRole("group", { name: "Graph camera" })).toBeInTheDocument();
    // The rail holds no colour key of its own any more.
    expect(within(rail).queryByRole("heading", { name: "Communities" })).toBeNull();

    await user.click(await within(rail).findByRole("button", { name: /Infrastructure/ }));

    const panel = await screen.findByRole("region", { name: "Contract of Infrastructure" });
    // In the layout beside the graph (CSS order puts it on the right), not in the rail and not over the canvas.
    expect(screen.getByTestId("graph-view").contains(panel)).toBe(false);
    expect(panel.nextElementSibling).toBe(screen.getByTestId("graph-view"));
    expect(panel.parentElement).toHaveClass("mapsplit");
    expect(within(rail).queryByLabelText(/^Contract of/)).toBeNull();
    expect(screen.queryByRole("region", { name: "Graph context" })).toBeNull();
    // The header names the file and how it changes; what the Map is, is the first section.
    expect(within(panel).getByRole("heading", { level: 2, name: "Map contract" })).toBeInTheDocument();
    expect(within(panel).getByText("infra/_map.md")).toBeInTheDocument();
    expect(within(panel).getByText(/Agents change it with map_update/)).toBeInTheDocument();
    const about = within(within(panel).getByRole("heading", { level: 3, name: "About" }).parentElement!);
    expect(about.getByText("Infrastructure")).toBeInTheDocument();
    expect(about.getByText("map")).toBeInTheDocument();
    expect(about.getByText("2")).toBeInTheDocument();
    expect(about.getByText("Hosts, services and how they connect.")).toBeInTheDocument();
    // The legend card and the camera controls give way to the contract; the replay is the whole KB's story.
    expect(screen.queryByRole("region", { name: "Graph legend" })).toBeNull();
    expect(screen.queryByRole("group", { name: "Graph camera" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Replay growth" })).toBeNull();
    // Fetched once, not per click.
    expect(fetchMock.mock.calls.filter(([u]) => String(u).includes("/maps"))).toHaveLength(1);
  });

  it("goes with the Map when the Atlas entry is picked, and the legend comes back", async () => {
    open("?kb=kb-a&scope=infra");
    stub();
    const user = userEvent.setup();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    await screen.findByRole("region", { name: "Contract of Infrastructure" });
    await user.click(within(rail).getByRole("button", { name: /^Atlas/ }));
    await waitFor(() => expect(window.location.search).not.toContain("scope="));
    // It slides out, hidden from assistive tech, then is gone.
    expect(screen.queryByRole("region", { name: /^Contract of/ })).toBeNull();
    await waitFor(() => expect(screen.queryByLabelText(/^Contract of/)).toBeNull());
    expect(await screen.findByRole("region", { name: "Graph legend" })).toBeInTheDocument();
  });

  it("leaves the graph the space that is left: the canvas is not told to frame around it a second time", async () => {
    open("?kb=kb-a&scope=infra&concept=infra%2Fa");
    stub();
    const focus = vi.spyOn(LivingScene.prototype, "focus");
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Contract of Infrastructure" });
    expect(panel).toHaveStyle({ width: `${MAP_PANEL_WIDTH}px` });
    await waitFor(() => expect(focus).toHaveBeenCalled());
    // The canvas already starts after the panel: a left occlusion would shift the framing twice.
    expect(focus.mock.calls.at(-1)![1]).toMatchObject({ left: 0 });
  });

  it("is a folded block above the graph on a narrow screen, never over it", async () => {
    viewport(true);
    open("?kb=kb-a&scope=infra");
    stub();
    const user = userEvent.setup();
    render(<App />);
    const panel = await screen.findByLabelText("Contract of Infrastructure");
    expect(panel.tagName).toBe("DETAILS");
    expect(panel).not.toHaveAttribute("open");
    expect(screen.getByTestId("graph-view").contains(panel)).toBe(false);
    expect(panel.parentElement).toHaveClass("mapsplit--stacked");
    expect(panel.nextElementSibling).toBe(screen.getByTestId("graph-view"));
    await user.click(within(panel).getByText("Infrastructure", { selector: ".mapcontract__summary-title" }));
    expect(panel).toHaveAttribute("open");
    expect(within(panel).getByText("Types & fields")).toBeInTheDocument();
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

describe("what the panel says", () => {
  const panel = (map: MapContract, concepts = 7) => {
    render(<MapContractPanel map={map} concepts={concepts} />);
    return screen.getByRole("region", { name: `Contract of ${map.title || map.name}` });
  };
  const section = (p: HTMLElement, name: string) => within(p).getByRole("heading", { level: 3, name }).parentElement!;

  it("lays a full contract out in titled sections: a label, the _map.md key, the value and a note where it needs one", () => {
    const p = panel(contract);
    expect(within(p).getAllByRole("heading", { level: 3 }).map((h) => h.textContent)).toEqual([
      "About",
      "Types & fields",
      "Templates",
      "Allowed values",
      "Lifecycle",
      "Review & work",
      "Index",
      "Titles",
    ]);

    const concepts = within(section(p, "Types & fields"));
    expect(concepts.getByText("Service")).toBeInTheDocument();
    expect(concepts.getByText("strict")).toBeInTheDocument();
    expect(concepts.getByText("only these types are accepted")).toBeInTheDocument();
    // Each section says once what it governs; each row names the key as written in _map.md.
    expect(concepts.getByText(/What a page in this Map may be/)).toBeVisible();
    expect(concepts.getByText("Ontology")).toBeInTheDocument();
    // The ontology mode is derived from the types (D907): no _map.md key to name.
    expect(concepts.queryByText("ontology_mode")).toBeNull();
    // One shape keyed by type, `*` first (D907).
    expect(concepts.getAllByText(/^required_fields\./).map((e) => e.textContent)).toEqual([
      "required_fields.*",
      "required_fields.Host",
    ]);
    // The longer explanation is the row's tooltip.
    expect(concepts.getByText("Ontology").closest(".mapcontract__row")).toHaveAttribute(
      "title",
      expect.stringContaining("Derived from the types"),
    );
    expect(concepts.getByText("timestamp")).toBeInTheDocument();
    expect(concepts.getByText("provenance")).toBeInTheDocument();
    expect(concepts.getByText("state")).toBeInTheDocument();
    expect(concepts.getByText("3 concepts")).toBeInTheDocument();

    const templates = within(section(p, "Templates"));
    expect(templates.getByText("host")).toBeInTheDocument();
    expect(templates.getByText("service", { selector: "code" })).toBeInTheDocument();
    // Declared templates bind every page (D371, D907): no switch to show.
    expect(templates.getByText("every page follows one, with its required sections")).toBeVisible();

    // Allowed values are chips beside their field; the key says map-wide or by type.
    const values = within(section(p, "Allowed values"));
    expect(values.getByText("status")).toBeInTheDocument();
    expect(values.getByText("draft")).toBeInTheDocument();
    expect(values.getByText("field_values.*.status")).toBeInTheDocument();
    expect(values.getByText("field_values.Host.role")).toBeInTheDocument();
    expect(values.getByText("on Host pages only")).toBeInTheDocument();
    expect(values.getByText("edge")).toBeInTheDocument();
    expect(values.getByText("live")).toBeInTheDocument();

    const lifecycle = within(section(p, "Lifecycle"));
    expect(lifecycle.getByText("phase")).toBeInTheDocument();
    expect(lifecycle.getByText("blocked")).toBeInTheDocument();
    expect(lifecycle.getByText("TODO")).toBeInTheDocument();
    expect(lifecycle.getByText("90 days")).toBeInTheDocument();
    expect(lifecycle.getByText("(default)")).toBeInTheDocument();

    const review = within(section(p, "Review & work"));
    expect(review.getByText("runbooks")).toBeInTheDocument();
    expect(review.getByText("steps")).toBeInTheDocument();
    expect(review.getByText("tasks")).toBeInTheDocument();
    expect(review.getByText("defines the KB's terms")).toBeInTheDocument();

    const index = within(section(p, "Index"));
    expect(index.getByText("generated")).toBeInTheDocument();
    expect(index.getByText("the server rewrites it on every write")).toBeInTheDocument();

    const titles = within(section(p, "Titles"));
    expect(titles.getByText("wip")).toBeInTheDocument();
    expect(titles.getByText("forbidden_title_terms")).toBeInTheDocument();
  });

  it("leaves out what the Map does not declare: no empty section, no invented value", () => {
    const p = panel(bare, 1);
    // Only what every Map has: its title, kind and size.
    expect(within(p).getAllByRole("heading", { level: 3 }).map((h) => h.textContent)).toEqual(["About"]);
    expect(within(p).queryByRole("alert")).toBeNull();
    expect(within(p).getByText("Infrastructure")).toBeInTheDocument();
    expect(within(p).getByText("map")).toBeInTheDocument();
    expect(within(p).getByText("1")).toBeInTheDocument();
    expect(within(p).queryByText("description")).toBeNull();
  });

  it("shows only the sections that have something", () => {
    const p = panel({ ...bare, forbidden_title_terms: ["wip"], index: "generated" });
    expect(within(p).getAllByRole("heading", { level: 3 }).map((h) => h.textContent)).toEqual(["About", "Index", "Titles"]);
  });

  it("marks a journal, and a harvest that is defaulted", () => {
    render(
      <MapContractPanel
        map={{ ...bare, name: "notes", title: "Notes", kind: "journal", harvest_after: 45, harvest_after_defaulted: true }}
        concepts={9}
      />,
    );
    const p = screen.getByRole("region", { name: "Contract of Notes" });
    expect(within(p).getByText("journal")).toBeInTheDocument();
    expect(within(p).getByText("45 days")).toBeInTheDocument();
    expect(within(p).getByText("(default)")).toBeInTheDocument();
  });

  it("warns when _map.md still carries keys D907 removed, and says the repair drops them", () => {
    const p = panel({ ...bare, legacy_keys: ["lint_ignore", "require_template"] });
    const alert = within(p).getByRole("alert");
    expect(within(alert).getByText("lint_ignore")).toBeInTheDocument();
    expect(within(alert).getByText("require_template")).toBeInTheDocument();
    expect(within(alert).getByText(/kb_repair rewrites the descriptor without them/)).toBeInTheDocument();
  });

  it("opens with a warning when entries of _map.md could not be read", () => {
    const p = panel({ ...bare, malformed: [{ key: "stale_after", reason: "not a number" }] });
    const alert = within(p).getByRole("alert");
    expect(within(alert).getByText("stale_after")).toBeInTheDocument();
    expect(within(alert).getByText("not a number")).toBeInTheDocument();
    // Before any section: the first thing the reader meets in the scroll.
    expect(alert.compareDocumentPosition(p.querySelector(".mapcontract__section")!)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
  });
});
