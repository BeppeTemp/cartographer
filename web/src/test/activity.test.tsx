import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Activity } from "../components/Activity";
import type { ChangesResponse, ConceptChange } from "../api/types";
import { json, stubApi } from "./fixtures";

const NOW = Date.now();
const ago = (days: number) => new Date(NOW - days * 86_400_000).toISOString();

const change = (id: string, over: Partial<ConceptChange> = {}): ConceptChange => ({
  id,
  change: "modified",
  last_at: ago(0),
  last_edit_at: ago(0),
  authors: ["agent-a"],
  ops: ["concept_write"],
  ...over,
});

const response = (concepts: ConceptChange[]): ChangesResponse => ({ since: "7d", commit_count: concepts.length, concepts });

/** Two edited, one new, one reorganised, one touched by upkeep only; two authors, two Maps. */
const concepts: ConceptChange[] = [
  change("infra/alpha", { ops: ["concept_write", "concept_patch", "concept_patch"], authors: ["agent-a", "bob-lee"] }),
  change("infra/beta", { last_at: ago(1), last_edit_at: ago(1), authors: ["bob-lee"] }),
  change("notes/gamma", { change: "added", authors: ["agent-a"] }),
  change("notes/delta", { change: "renamed", ops: ["concept_move"], authors: ["agent-a"] }),
  change("notes/upkeep", { last_edit_at: undefined, ops: ["auto-repair"], authors: ["agent-a"] }),
];

function renderActivity(list: ConceptChange[], onOpen: (id: string) => void = () => {}) {
  stubApi({ "/changes": () => json(response(list)) });
  const collections = [
    { name: "infra", title: "Infrastructure", kind: "map", concepts: 2, expanded_concepts: 0 },
    { name: "notes", title: "Notes", kind: "map", concepts: 3, expanded_concepts: 0 },
  ];
  return render(<Activity kb="kb-a" snapshot={null} collections={collections} onOpen={onOpen} />);
}

const summary = async () => screen.findByRole("region", { name: "Summary and filters" });
const entries = () => document.querySelectorAll(".timeline__entry").length;

/** The hero above the timeline: figure tiles, the When strip, then Where and Who. */
describe("the Activity summary", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  const tiles = (band: HTMLElement) =>
    within(within(band).getByRole("list", { name: "Changes in the window" }))
      .getAllByRole("listitem")
      .map((li) => li.textContent);

  it("says the figures as tiles, a zero faded", async () => {
    const { unmount } = renderActivity(concepts);
    const band = await summary();
    // Upkeep is counted apart: 2 edited, 1 new, 1 reorganised, 4 distinct concepts touched.
    expect(tiles(band)).toEqual(["New1", "Edited2", "Reorganised1", "Retired0", "Touched4"]);
    unmount();

    renderActivity([change("infra/alpha")]);
    const quiet = await summary();
    const zeroes = [...within(quiet).getByRole("list", { name: "Changes in the window" }).querySelectorAll("[data-zero]")];
    expect(zeroes.map((z) => z.textContent)).toEqual(["New0", "Reorganised0", "Retired0"]);
  });

  it("draws the When strip, and a tooltip card with its total on hover", async () => {
    const user = userEvent.setup();
    renderActivity(concepts);
    const band = await summary();
    const strip = band.querySelector(".activity-strip__plot")!;
    // The strip names every bucket for a screen reader.
    expect(within(band).getByRole("list", { name: "changes per day" })).toBeInTheDocument();
    expect(document.querySelector(".activity-strip__tip")).toBeNull();
    await user.hover(strip);
    await user.pointer({ target: strip, coords: { clientX: 1, clientY: 1 } });
    const tip = document.querySelector(".activity-strip__tip");
    expect(tip).not.toBeNull();
    expect(tip).toHaveTextContent("Total");
  });

  it("filters by Map from the chips, and clears on a second press", async () => {
    const user = userEvent.setup();
    renderActivity(concepts);
    const legend = within(await summary()).getByRole("list", { name: "Filter by Map" });
    await waitFor(() => expect(entries()).toBe(4));

    const notes = within(legend).getByRole("button", { name: /Notes/ });
    expect(notes).toHaveTextContent("2");
    await user.click(notes);
    expect(notes).toHaveAttribute("aria-pressed", "true");
    expect(entries()).toBe(2);

    await user.click(notes);
    expect(notes).toHaveAttribute("aria-pressed", "false");
    expect(entries()).toBe(4);
  });

  it("filters by author from the avatars, each named for a screen reader", async () => {
    const user = userEvent.setup();
    renderActivity(concepts);
    const people = within(await summary()).getByRole("list", { name: "Filter by author" });
    await waitFor(() => expect(entries()).toBe(4));

    // The busiest first, each with its count.
    const buttons = within(people).getAllByRole("button");
    expect(buttons.map((b) => b.getAttribute("aria-label"))).toEqual(["agent-a: 3 changes", "bob-lee: 2 changes"]);

    await user.click(within(people).getByRole("button", { name: /bob-lee/ }));
    expect(within(people).getByRole("button", { name: /bob-lee/ })).toHaveAttribute("aria-pressed", "true");
    expect(entries()).toBe(2);

    await user.click(within(people).getByRole("button", { name: /bob-lee/ }));
    expect(entries()).toBe(4);
  });

  it("leaves Who out when one author did it all", async () => {
    renderActivity([change("infra/alpha"), change("notes/beta")]);
    const band = await summary();
    expect(within(band).queryByRole("list", { name: "Filter by author" })).not.toBeInTheDocument();
  });

  it("keeps the maintenance toggle in Who, and it brings the touched concepts into the timeline", async () => {
    const user = userEvent.setup();
    renderActivity(concepts);
    const band = await summary();
    await waitFor(() => expect(entries()).toBe(4));
    const toggle = within(band).getByRole("checkbox", { name: /Maintenance/ });
    expect(toggle).not.toBeChecked();
    expect(toggle.closest("label")).toHaveTextContent("1");

    await user.click(toggle);
    expect(toggle).toBeChecked();
    expect(entries()).toBe(5);
    await user.click(toggle);
    expect(entries()).toBe(4);
  });

  it("is the people's tab: a change only the Surveyor made is not on it, a shared one shows its people", async () => {
    renderActivity([
      change("infra/alpha", { authors: ["surveyor/team-a/w-1"] }),
      change("infra/beta", { authors: ["surveyor/team-a/w-1", "bob-lee"] }),
      change("notes/gamma", { authors: ["agent-a"] }),
    ]);
    await waitFor(() => expect(entries()).toBe(2));
    const rows = [...document.querySelectorAll(".timeline__entry")];
    expect(rows.map((r) => r.querySelector(".timeline__title")!.textContent).sort()).toEqual(["infra/beta", "notes/gamma"]);
    // The shared change carries bob-lee's avatar only, and no "Surveyor" tag.
    const beta = rows.find((r) => r.textContent?.includes("infra/beta"))!;
    expect(beta.querySelector(".timeline__authors")).toHaveAttribute("aria-label", "By bob-lee");
    expect(document.querySelector(".timeline__surveyor")).toBeNull();
    // Who counts people only.
    const people = within(await summary()).getByRole("list", { name: "Filter by author" });
    expect(within(people).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual([
      "agent-a: 1 change",
      "bob-lee: 1 change",
    ]);
  });

  it("opens a row's diff in place instead of the page", async () => {
    const onOpen = vi.fn();
    const user = userEvent.setup();
    stubApi({
      "/changes": () => json(response(concepts)),
      "/diff": () =>
        json({ id: "infra/alpha", path: "infra/alpha.md", commits: 1, files: [{ path: "infra/alpha.md", status: "modified", before: "a\n", after: "b\n" }] }),
    });
    render(<Activity kb="kb-a" snapshot={null} collections={[]} onOpen={onOpen} />);
    await waitFor(() => expect(entries()).toBe(4));
    const row = screen.getByRole("button", { name: /infra\/alpha/ });
    expect(row).toHaveAttribute("aria-expanded", "false");
    await user.click(row);
    expect(row).toHaveAttribute("aria-expanded", "true");
    expect(await screen.findByRole("region", { name: "What changed in infra/alpha" })).toBeInTheDocument();
    expect(onOpen).not.toHaveBeenCalled();
    await user.click(row);
    expect(screen.queryByRole("region", { name: "What changed in infra/alpha" })).toBeNull();
  });

  it("opens the concept page when the server has no diff route (404), as the static demo does", async () => {
    const onOpen = vi.fn();
    const user = userEvent.setup();
    stubApi({
      "/changes": () => json(response(concepts)),
      "/diff": () => json({ error: { code: "not_found", message: "not found" } }, 404),
    });
    render(<Activity kb="kb-a" snapshot={null} collections={[]} onOpen={onOpen} />);
    await waitFor(() => expect(entries()).toBe(4));
    const row = screen.getByRole("button", { name: /infra\/beta/ });
    await user.click(row);
    await waitFor(() => expect(onOpen).toHaveBeenCalledWith("infra/beta"));
    expect(row).toHaveAttribute("aria-expanded", "false");
  });

  it("is absent when nothing changed in the window", async () => {
    renderActivity([]);
    await screen.findByText("Quiet stretch.");
    expect(screen.queryByRole("region", { name: "Summary and filters" })).not.toBeInTheDocument();
  });
});

/** An empty window and retirements, on the one timeline (D1146). */
describe("the Activity timeline's empty window and retirements", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  const collections = [
    { name: "infra", title: "Infrastructure", kind: "map", concepts: 2, expanded_concepts: 0 },
    { name: "notes", title: "Notes", kind: "map", concepts: 3, expanded_concepts: 0 },
  ];

  it("shows a quiet notice, then the newest earlier changes under Earlier", async () => {
    stubApi({
      // The whole-history read asks for 36500 days; the window itself is empty.
      "since=36500d": () =>
        json(response([change("infra/old", { last_at: ago(40), last_edit_at: ago(40) }), change("notes/older", { last_at: ago(60), last_edit_at: ago(60) })])),
      "/changes": () => json(response([])),
    });
    render(<Activity kb="kb-a" snapshot={null} collections={collections} onOpen={() => {}} />);
    await screen.findByText("Quiet stretch.");
    expect(screen.queryByRole("region", { name: "Summary and filters" })).not.toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "Earlier" })).toBeInTheDocument();
    expect(entries()).toBe(2);
    expect(document.body).toHaveTextContent("old");
  });

  it("marks a retired concept's row with its status and where it went", async () => {
    stubApi({
      "/changes": () => json(response([change("infra/alpha"), change("notes/gamma")])),
      "/retired": () =>
        json({
          concepts: [
            {
              id: "infra/alpha",
              status: "superseded",
              superseded_by: "infra/beta",
              superseded_by_title: "Beta",
              supersede_reason: "split in two",
            },
          ],
        }),
    });
    render(<Activity kb="kb-a" snapshot={null} collections={collections} onOpen={() => {}} />);
    // The first reason is on the row, the rest in its tooltip.
    const reason = await screen.findByText(/^Replaced by Beta/);
    expect(reason).toHaveTextContent("Replaced by Beta +1");
    expect(reason).toHaveAttribute("title", "Replaced by Beta\nsplit in two");
    expect(document.querySelector(".timeline__retired--superseded")).toHaveTextContent("superseded");
    expect(document.querySelectorAll(".timeline__retired")).toHaveLength(1);
    const retiredTile = within(await summary()).getByText("Retired").closest("li")!;
    expect(retiredTile).toHaveTextContent("1");
  });
});

describe("the Activity empty state with filters", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
  });

  it("names the Map and clears both filters", async () => {
    const user = userEvent.setup();
    renderActivity(concepts);
    const band = await summary();
    await waitFor(() => expect(entries()).toBe(4));
    await user.click(within(within(band).getByRole("list", { name: "Filter by Map" })).getByRole("button", { name: /Notes/ }));
    await user.click(within(within(band).getByRole("list", { name: "Filter by author" })).getByRole("button", { name: /bob-lee/ }));
    await screen.findByText("Quiet stretch.");
    expect(screen.getByText(/No concept edited in Notes in the last 7 days by bob-lee/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    await waitFor(() => expect(entries()).toBe(4));
  });
});
