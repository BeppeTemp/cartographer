import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { Retired } from "../components/Retired";
import type { Overview, RetiredResponse } from "../api/types";
import { json, overview, stubApi } from "./fixtures";

const retired: RetiredResponse = {
  concepts: [
    {
      id: "archive/old-plan",
      title: "Old plan",
      status: "deprecated",
      map: "archive",
      retired_at: "2026-03-01T09:00:00Z",
      from: "notes/old-plan",
      reason: "replaced by the new plan",
    },
    {
      id: "notes/rota",
      title: "Rota",
      status: "superseded",
      map: "notes",
      retired_at: "2026-02-01T09:00:00Z",
      superseded_by: "notes/rota-v2",
      superseded_by_title: "Rota v2",
      supersede_reason: "new rota",
    },
    { id: "notes/handmade", title: "Handmade", status: "deprecated", map: "notes", timestamp: "2026-01-05" },
    { id: "runs/2019-archived", title: "Archived run", status: "archived", map: "runs" },
  ],
  maps: [
    { name: "archive", title: "Archive", kind: "map", archive: true, retired: 1, total: 1 },
    { name: "notes", title: "Notes", kind: "map", archive: false, retired: 2, total: 5 },
    { name: "runs", title: "Runs", kind: "journal", archive: false, retired: 1, total: 2 },
  ],
  journals: [
    { name: "runs", title: "Runs", harvest_after: 45, waiting: 2, archived: 1 },
    { name: "ops", harvest_off: true, waiting: 0, archived: 3 },
  ],
};

const changes = { since: "7d", commit_count: 0, concepts: [] };

/** The Retired tab of Activity (D820): what the KB retired, read from its log. */
describe("the Retired panel", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=retired");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("tells each story, and says so when there is none", async () => {
    stubApi({ "/retired": () => json(retired) });
    const user = userEvent.setup();
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Retired concepts" });
    await waitFor(() =>
      expect(within(panel).getByRole("heading", { level: 1 })).toHaveTextContent("4 retired concepts."),
    );
    expect(within(panel).getByText("1 archived · 2 deprecated · 1 superseded")).toBeInTheDocument();

    const rows = within(within(panel).getByRole("list", { name: "Retired concepts" })).getAllByRole("listitem");
    expect(rows).toHaveLength(4);
    // Archived with a reason: where it came from, when, why.
    const archived = rows[0]!;
    expect(within(archived).getByText("archive/old-plan")).toBeInTheDocument();
    expect(within(archived).getByText("notes/old-plan")).toBeInTheDocument();
    expect(within(archived).getByText("2026-03-01")).toBeInTheDocument();
    expect(within(archived).getByText("replaced by the new plan")).toBeInTheDocument();
    // Superseded: the successor by its title, and the reason from the page.
    const superseded = rows[1]!;
    expect(within(superseded).getByText("new rota")).toBeInTheDocument();
    // Set by hand: no log entry, so only what the page itself says.
    const handmade = rows[2]!;
    expect(within(handmade).getByText("no retirement recorded · updated 2026-01-05")).toBeInTheDocument();
    expect(within(handmade).queryByText(/^from /)).not.toBeInTheDocument();

    await user.click(within(superseded).getByRole("button", { name: "Rota v2" }));
    expect(window.location.search).toContain("concept=notes%2Frota-v2");
    expect(window.location.search).not.toContain("panel=retired");
  });

  it("filters by status and by text", async () => {
    stubApi({ "/retired": () => json(retired) });
    const user = userEvent.setup();
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Retired concepts" });
    const list = async () =>
      (await within(panel).findByRole("list", { name: "Retired concepts" })).querySelectorAll("li").length;
    expect(await list()).toBe(4);

    const chips = within(panel).getByRole("list", { name: "Filter by status" });
    await user.click(within(chips).getByRole("button", { name: /Deprecated/ }));
    expect(await list()).toBe(2);
    await user.click(within(chips).getByRole("button", { name: /Deprecated/ }));
    expect(await list()).toBe(4);

    await user.type(within(panel).getByRole("searchbox", { name: "Filter retired concepts" }), "new plan");
    expect(await list()).toBe(1);
    expect(within(panel).getByText("Old plan")).toBeInTheDocument();
    await user.type(within(panel).getByRole("searchbox", { name: "Filter retired concepts" }), " zzz");
    expect(await within(panel).findByText("Nothing matches these filters.")).toBeInTheDocument();
  });

  it("drops a status filter the data no longer has, and a new KB starts unfiltered", async () => {
    let answer = retired;
    stubApi({ "/retired": () => json(answer) });
    const user = userEvent.setup();
    const { rerender } = render(<Retired kb="kb-a" onOpen={() => {}} />);
    const list = async () =>
      (await screen.findByRole("list", { name: "Retired concepts" })).querySelectorAll("li").length;
    expect(await list()).toBe(4);
    await user.click(within(screen.getByRole("list", { name: "Filter by status" })).getByRole("button", { name: /Archived/ }));
    expect(await list()).toBe(1);

    // A live refetch without any archived page: the filter goes with its chip.
    answer = { ...retired, concepts: retired.concepts.filter((c) => c.status !== "archived") };
    rerender(<Retired kb="kb-a" live={1} onOpen={() => {}} />);
    await waitFor(async () => expect(await list()).toBe(3));

    await user.type(screen.getByRole("searchbox", { name: "Filter retired concepts" }), "rota");
    expect(await list()).toBe(1);
    rerender(<Retired kb="kb-b" live={1} onOpen={() => {}} />);
    await waitFor(async () => expect(await list()).toBe(3));
    expect(screen.getByRole("searchbox", { name: "Filter retired concepts" })).toHaveValue("");
  });

  it("says each journal's harvest state", async () => {
    stubApi({ "/retired": () => json(retired) });
    render(<App />);
    expect(
      await screen.findByText("2 closed entries waiting to be harvested (after 45 days) · 1 archived"),
    ).toBeInTheDocument();
    expect(screen.getByText("harvest off · 3 archived")).toBeInTheDocument();
  });

  it("explains how a concept gets retired when nothing is", async () => {
    stubApi({ "/retired": () => json({ concepts: [], maps: [], journals: [] }) });
    render(<App />);
    expect(await screen.findByText("Nothing retired yet")).toBeInTheDocument();
    expect(screen.getByText(/concept_archive files a page in the archive map/)).toBeInTheDocument();
  });

  it("shows an unavailable state when the server has no such route", async () => {
    stubApi({ "/retired": () => json({ error: { code: "not_found", message: "not found" } }, 404) });
    render(<App />);
    expect(await screen.findByText("The retired view is not available")).toBeInTheDocument();
  });

  it("is a tab of Activity that Back undoes, and the rail keeps Activity current", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=activity");
    stubApi({ "/retired": () => json(retired), "/changes": () => json(changes) });
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole("region", { name: "Recent activity" });
    const rail = screen.getByRole("navigation", { name: "Atlas navigation" });
    expect(within(rail).getByRole("button", { name: "Activity" })).toHaveAttribute("aria-current", "page");

    await user.click(screen.getByRole("tab", { name: "Retired" }));
    await screen.findByRole("region", { name: "Retired concepts" });
    expect(window.location.search).toContain("panel=retired");
    expect(screen.getByRole("tab", { name: "Retired" })).toHaveAttribute("aria-selected", "true");
    expect(within(rail).getByRole("button", { name: "Activity" })).toHaveAttribute("aria-current", "page");

    window.history.back();
    await screen.findByRole("region", { name: "Recent activity" });
    expect(window.location.search).toContain("panel=activity");
    expect(screen.getByRole("tab", { name: "Activity" })).toHaveAttribute("aria-selected", "true");
  });

  it("marks an archive map in the rail and lists it after the live ones", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a");
    const withArchive: Overview = {
      ...overview,
      collections: [
        { name: "archive", title: "Archive", kind: "map", concepts: 2, expanded_concepts: 0, archive: true },
        ...overview.collections.map((c) => ({ ...c, archive: false })),
      ],
    };
    stubApi({ "/overview": () => json(withArchive) });
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    const maps = await waitFor(() => {
      const names = within(rail).getAllByRole("button", { name: /Archive|Infrastructure/ });
      expect(names).toHaveLength(2);
      return names;
    });
    expect(maps[0]).toHaveTextContent("Infrastructure");
    expect(maps[1]).toHaveTextContent("Archive");
    expect(within(maps[1]!).getByText("Archive", { selector: ".rail__archive" })).toBeInTheDocument();
    expect(within(maps[0]!).queryByText("Archive", { selector: ".rail__archive" })).not.toBeInTheDocument();
  });
});
