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

/** The two-line summary above the timeline: figures and days, then Where and Who. */
describe("the Activity summary", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("says the figures in one line, a zero apart", async () => {
    const { unmount } = renderActivity(concepts);
    const stats = within(await summary()).getByRole("list", { name: "Changes in the window" });
    // Upkeep is counted apart: 2 edited, 1 new, 1 reorganised, 2 authors.
    expect(within(stats).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
      "2 edited",
      "1 new",
      "1 reorganised",
      "2 authors",
    ]);
    expect(stats.querySelectorAll('[data-tone="muted"]')).toHaveLength(0);
    unmount();

    // A kind with nothing in it fades.
    renderActivity([change("infra/alpha")]);
    const quiet = within(await summary()).getByRole("list", { name: "Changes in the window" });
    const zeroes = quiet.querySelectorAll('[data-tone="muted"]');
    expect([...zeroes].map((z) => z.textContent)).toEqual(["0 new", "0 reorganised"]);
  });

  it("names the most worked on concept only when it was worked on more than once", async () => {
    const onOpen = vi.fn();
    const user = userEvent.setup();
    const { unmount } = renderActivity(concepts, onOpen);
    const top = within(await summary()).getByRole("button", { name: /Most worked on/ });
    expect(top).toHaveTextContent("infra/alpha");
    expect(top).toHaveTextContent("3 changes");
    await user.click(top);
    expect(onOpen).toHaveBeenCalledWith("infra/alpha");
    unmount();

    // One content operation each: nothing stands out.
    renderActivity([change("infra/alpha"), change("infra/beta", { authors: ["bob-lee"] })]);
    const band = await summary();
    expect(within(band).queryByRole("button", { name: /Most worked on/ })).not.toBeInTheDocument();
  });

  it("filters by Map from the legend, and clears on a second press", async () => {
    const user = userEvent.setup();
    renderActivity(concepts);
    const legend = within(await summary()).getByRole("list", { name: "Filter by Map" });
    await waitFor(() => expect(entries()).toBe(4));

    const notes = within(legend).getByRole("button", { name: /Notes/ });
    expect(notes).toHaveTextContent("2");
    await user.click(notes);
    expect(notes).toHaveAttribute("aria-pressed", "true");
    expect(entries()).toBe(2);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("in Notes");

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
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("by bob-lee");

    await user.click(within(people).getByRole("button", { name: /bob-lee/ }));
    expect(entries()).toBe(4);
  });

  it("leaves Who out when one author did it all", async () => {
    renderActivity([change("infra/alpha"), change("notes/beta")]);
    const band = await summary();
    expect(within(band).queryByRole("list", { name: "Filter by author" })).not.toBeInTheDocument();
    expect(within(band).getByRole("list", { name: "Changes in the window" })).not.toHaveTextContent("authors");
  });

  it("keeps the maintenance toggle at the end, and it brings the touched concepts into the timeline", async () => {
    const user = userEvent.setup();
    renderActivity(concepts);
    const band = await summary();
    await waitFor(() => expect(entries()).toBe(4));
    const toggle = within(band).getByRole("button", { name: "+ 1 touched by automatic maintenance · show" });
    expect(toggle).toHaveAttribute("aria-pressed", "false");

    await user.click(toggle);
    expect(toggle).toHaveTextContent("+ 1 touched by automatic maintenance · hide");
    expect(toggle).toHaveAttribute("aria-pressed", "true");
    expect(entries()).toBe(5);
    // The last thing of the second line.
    expect(band.querySelector(".activity-facets")!.lastElementChild).toBe(toggle);
  });

  it("reads the days off a sparkline: today at rest, the hovered day under the pointer", async () => {
    const user = userEvent.setup();
    renderActivity(concepts);
    const band = await summary();
    const caption = band.querySelector(".activity-bars__caption")!;
    expect(caption).toHaveTextContent("today · 3 changed · 1 maintenance");

    const yesterday = within(band).getByRole("button", { name: /^.*: 1 changed$/, hidden: true });
    await user.hover(yesterday);
    expect(caption).toHaveTextContent(/^[A-Z][a-z]{2} \d{1,2} · 1 changed$/);
    expect(caption).not.toHaveTextContent("today");
    await user.unhover(yesterday);
    expect(caption).toHaveTextContent("today · 3 changed · 1 maintenance");
  });

  it("is absent when nothing changed in the window", async () => {
    renderActivity([]);
    await screen.findByText("Quiet stretch.");
    expect(screen.queryByRole("region", { name: "Summary and filters" })).not.toBeInTheDocument();
  });
});
