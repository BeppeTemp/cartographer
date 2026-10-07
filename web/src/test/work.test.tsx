import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { json, stubApi } from "./fixtures";

const work = {
  total: 3,
  open_concepts: 2,
  by_status: { open: 1, "in-progress": 1 },
  by_map: { infra: 2, notes: 1 },
  open_items: 2,
  entries: [
    { id: "infra/a", title: "Alpha", type: "Task", map: "infra", status: "open", open_phase: true, stale: true, age_days: 90, items: [] },
    { id: "infra/b", title: "Beta", type: "Topic", map: "infra", status: "in-progress", open_phase: true, stale: false, items: [] },
    {
      id: "notes/c",
      title: "Gamma",
      type: "Note",
      map: "notes",
      status: "done",
      open_phase: false,
      stale: false,
      items: [
        { text: "renew the certificate", section: "Next steps", line: 5 },
        { text: "rotate keys", section: "Next steps", line: 6 },
      ],
    },
  ],
};

/** The Work panel (D302): work_list as a page, read-only. */
describe("the Work panel", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=work");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("explains why a status is open work (D321)", async () => {
    stubApi({ "/work": () => json(work) });
    render(<App />);
    expect(await screen.findByText(/"active" means the page is valid, not that work is pending/)).toBeInTheDocument();
  });

  it("groups by status and by map, filters, and opens a concept", async () => {
    stubApi({ "/work": () => json(work) });
    const user = userEvent.setup();
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Open work" });
    const heading = within(panel).getByRole("heading", { level: 1 });
    await waitFor(() => expect(heading).toHaveTextContent("3 concepts carry open work, 2 unchecked items."));
    // Columns follow the work's progress, not the alphabet.
    const columns = () =>
      within(within(panel).getByRole("region", { name: /^Work by/ }))
        .getAllByRole("heading", { level: 2 })
        .map((h) => h.textContent);
    expect(columns()).toEqual(["in-progress 1", "open 1", "unchecked items 1"]);

    await user.click(within(panel).getByRole("button", { name: "By map" }));
    // A Map is named by its title, as everywhere else; the busiest comes first.
    expect(columns()).toEqual(["Infrastructure 2", "notes 1"]);

    // Items expand with their section.
    await user.click(within(panel).getByRole("button", { name: "2 open items" }));
    expect(within(panel).getByText("renew the certificate")).toBeInTheDocument();

    // Free text matches items as well as titles; stale only narrows.
    await user.type(within(panel).getByRole("searchbox", { name: "Filter work" }), "rotate");
    expect(within(panel).queryByText("Alpha")).not.toBeInTheDocument();
    expect(within(panel).getByText("Gamma")).toBeInTheDocument();
    await user.clear(within(panel).getByRole("searchbox", { name: "Filter work" }));
    await user.click(within(panel).getByRole("checkbox", { name: /^Stale only/ }));
    expect(within(panel).queryByText("Gamma")).not.toBeInTheDocument();
    await user.click(within(panel).getByRole("checkbox", { name: /^Stale only/ }));
    const byStatus = within(panel).getByRole("list", { name: "Filter by status" });
    await user.click(within(byStatus).getByRole("button", { name: /in-progress/ }));
    expect(within(panel).getByText("Beta")).toBeInTheDocument();
    expect(within(panel).queryByText("Alpha")).not.toBeInTheDocument();
    // Pressing it again clears the filter; a Map narrows the same way.
    await user.click(within(byStatus).getByRole("button", { name: /in-progress/ }));
    await user.click(within(within(panel).getByRole("list", { name: "Filter by Map" })).getByRole("button", { name: /notes/ }));
    expect(within(panel).getByText("Gamma")).toBeInTheDocument();
    expect(within(panel).queryByText("Beta")).not.toBeInTheDocument();
    await user.click(within(within(panel).getByRole("list", { name: "Filter by Map" })).getByRole("button", { name: /Infrastructure/ }));

    await user.click(within(panel).getByText("Beta"));
    expect(window.location.search).toContain("concept=infra%2Fb");
    expect(window.location.search).not.toContain("panel=work");
  });

  it("says so when nothing is open", async () => {
    stubApi({ "/work": () => json({ total: 0, open_concepts: 0, by_status: {}, by_map: {}, open_items: 0, entries: [] }) });
    render(<App />);
    expect(
      await screen.findByText("No open work: no concept in an open status and no unchecked item."),
    ).toBeInTheDocument();
  });
});
