import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DashboardView, isDue } from "../components/DashboardView";
import type { DashboardResolved } from "../api/types";

const base = (widgets: DashboardResolved["widgets"]): DashboardResolved => ({ name: "week", widgets });

describe("DashboardView", () => {
  // AC-kbdash-18: a note is sanitised and a failed widget does not stop its neighbours.
  it("draws no script from a note and an error tile beside the widgets that resolved", () => {
    const { container } = render(
      <DashboardView
        data={base([
          { id: "n", type: "note", title: "Notes", text: "hello <script>alert(1)</script> **bold**", total: 0 },
          { id: "bad", type: "stat", title: "Broken", error: "unknown field", total: 0 },
          { id: "ok", type: "stat", title: "Open", total: 7, icon: "work", accent: "ok" },
        ])}
      />,
    );
    expect(container.querySelector("script")).toBeNull();
    expect(screen.getByText("bold").tagName).toBe("STRONG");
    expect(screen.getByRole("alert")).toHaveTextContent("unknown field");
    expect(screen.getByRole("heading", { level: 2, name: "Open" })).toBeInTheDocument();
    expect(screen.getByText("7", { selector: ".dashboard__stat" })).toBeInTheDocument();
  });

  it("draws both cards of two widgets that repeat an id, with no duplicate-key warning", () => {
    const warn = vi.spyOn(console, "error").mockImplementation(() => {});
    render(
      <DashboardView
        data={base([
          { id: "dup", type: "stat", title: "First", total: 1 },
          { id: "dup", type: "stat", title: "Second", total: 2, error: "duplicate widget id" },
        ])}
      />,
    );
    expect(screen.getByRole("heading", { level: 2, name: "First" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Second" })).toBeInTheDocument();
    expect(warn.mock.calls.flat().join(" ")).not.toMatch(/same key/);
    warn.mockRestore();
  });

  it("names a type it does not draw yet and works with structural keys only", () => {
    render(<DashboardView data={base([{ id: "k", type: "gauge", total: 2 }, { id: "s", type: "stat", total: 1, groups: [{ key: "open", total: 1 }] }])} />);
    expect(screen.getByText(/gauge widget is not drawn yet/)).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "k" })).toBeInTheDocument();
    expect(screen.getByText("open")).toBeInTheDocument();
  });

  // AC-kbdash-20: a kanban draws its columns in the declared order with the resolver's totals.
  it("draws the columns of a kanban in order with the resolver totals, not the shown rows", () => {
    const { container } = render(
      <DashboardView
        data={base([
          {
            id: "board",
            type: "kanban",
            title: "Board",
            total: 7,
            groups: [
              { key: "blocked", total: 4, entries: [{ id: "a/one", title: "One", fields: { waiting_on: "someone", review_after: "2000-01-01" } }] },
              { key: "open", total: 2, entries: [{ id: "a/two" }, { id: "a/two" }] },
              { key: "", total: 1 },
            ],
          },
        ])}
      />,
    );
    const cols = Array.from(container.querySelectorAll(".dashboard__column"));
    expect(cols.map((c) => c.getAttribute("aria-label"))).toEqual(["blocked, 4", "open, 2", "(none), 1"]);
    expect(screen.getByText("Waiting on someone")).toBeInTheDocument();
    expect(screen.getByText(/Review due 2000-01-01/)).toHaveClass("dashboard__due");
    // No title, no fields: the id names the card.
    expect(screen.getAllByText("a/two")).toHaveLength(2);
  });

  it("draws a list flat or grouped, opens a row, and marks only a due review", () => {
    const onOpen = vi.fn();
    render(
      <DashboardView
        onOpen={onOpen}
        data={base([
          { id: "flat", type: "list", title: "Flat", total: 1, entries: [{ id: "p/x", title: "X", fields: { review_after: "2999-01-01" } }] },
        ])}
      />,
    );
    expect(screen.getByText(/Review after 2999-01-01/)).not.toHaveClass("dashboard__due");
    screen.getByRole("button", { name: /X/ }).click();
    expect(onOpen).toHaveBeenCalledWith("p/x");
    expect(isDue("not a date")).toBe(false);
    expect(isDue("2026-11-25", "2026-11-25")).toBe(false);
    expect(isDue("2026-11-24", "2026-11-25")).toBe(true);
    expect(isDue("2026-13-45", "2026-11-25")).toBe(false);
    expect(isDue("2026-02-30", "2026-11-25")).toBe(false);
  });

  // AC-kbdash-21: agenda draws the overdue bucket first and a due review_after carries the due style.
  it("draws an agenda with the overdue day first and marks a due review_after", () => {
    const { container } = render(
      <DashboardView
        data={base([
          {
            id: "week",
            type: "agenda",
            title: "Week",
            total: 2,
            groups: [
              { key: "overdue", total: 1, entries: [{ id: "a", title: "Late", fields: { review_after: "2000-01-01" } }] },
              { key: "2999-01-01", total: 1, entries: [{ id: "b", title: "Later", fields: { review_after: "2999-01-01" } }] },
            ],
          },
        ])}
      />,
    );
    const days = container.querySelectorAll(".dashboard__day");
    expect(days).toHaveLength(2);
    expect(days[0]).toHaveTextContent("Overdue");
    expect(days[1]).toHaveTextContent("2999-01-01");
    expect(days[0]!.querySelector(".dashboard__due")).not.toBeNull();
    expect(days[1]!.querySelector(".work__card-note.dashboard__due")).toBeNull();
    expect(screen.getByText(/Review due 2000-01-01/)).toHaveClass("dashboard__due");
  });
});

describe("table and timeline widgets", () => {
  // AC-kbdash-26: a table sorts by clicking a header, a timeline groups by group_by.
  it("sorts a table by a clicked header, ascending then descending", () => {
    render(
      <DashboardView
        data={base([
          {
            id: "t",
            type: "table",
            title: "Tasks",
            total: 3,
            columns: ["title", "customer"],
            entries: [
              { id: "a", title: "Bravo", fields: { customer: "kb-b" } },
              { id: "b", title: "Alpha", fields: { customer: "kb-c" } },
              { id: "c", title: "Charlie" },
            ],
          },
        ])}
      />,
    );
    const names = () => screen.getAllByRole("row").slice(1).map((r) => within(r).getAllByRole("cell")[0]?.textContent);
    expect(names()).toEqual(["Bravo", "Alpha", "Charlie"]);
    fireEvent.click(screen.getByRole("button", { name: /^title/ }));
    expect(names()).toEqual(["Alpha", "Bravo", "Charlie"]);
    fireEvent.click(screen.getByRole("button", { name: /^title/ }));
    expect(names()).toEqual(["Charlie", "Bravo", "Alpha"]);
    // An empty cell stays last whichever way the column runs.
    fireEvent.click(screen.getByRole("button", { name: /^customer/ }));
    expect(names()).toEqual(["Bravo", "Alpha", "Charlie"]);
    fireEvent.click(screen.getByRole("button", { name: /^customer/ }));
    expect(names()).toEqual(["Alpha", "Bravo", "Charlie"]);
  });

  it("draws a table with no columns (restricted caller) with the default ones", () => {
    render(<DashboardView data={base([{ id: "t", type: "table", total: 1, entries: [{ id: "a", title: "Alpha", type: "task", status: "open" }] }])} />);
    expect(screen.getAllByRole("columnheader").map((h) => h.textContent?.trim())).toEqual(["title", "type", "status"]);
  });

  it("groups a timeline by group_by with the dates in the resolver's order", () => {
    render(
      <DashboardView
        data={base([
          {
            id: "tl",
            type: "timeline",
            title: "When",
            total: 3,
            date_field: "due",
            group_by: "status",
            groups: [
              { key: "open", total: 2, entries: [{ id: "a", title: "First", fields: { due: "2026-01-02" } }, { id: "b", title: "Second", fields: { due: "2026-03-04" } }] },
              { key: "done", total: 1, entries: [{ id: "c", title: "Third", fields: { due: "2026-02-01" } }] },
            ],
          },
        ])}
      />,
    );
    const open = screen.getByRole("region", { name: "open, 2" });
    expect(within(open).getAllByRole("listitem").map((li) => li.querySelector("time")?.textContent)).toEqual(["2026-01-02", "2026-03-04"]);
    expect(screen.getByRole("region", { name: "done, 1" })).toHaveTextContent("2026-02-01");
  });

  it("draws an ungrouped timeline as one dated list", () => {
    render(<DashboardView data={base([{ id: "tl", type: "timeline", total: 1, entries: [{ id: "a", title: "Only", fields: { review_after: "2026-05-06" } }] }])} />);
    expect(screen.getByText("2026-05-06")).toBeInTheDocument();
  });
});

describe("links widget", () => {
  // AC-kbdash-30: a resolved id is a link into the Atlas, a missing one reads "not found".
  it("draws a resolved id as a link and a missing id as not found", () => {
    const onOpen = vi.fn();
    render(
      <DashboardView
        onOpen={onOpen}
        data={base([
          {
            id: "l",
            type: "links",
            title: "Reading",
            total: 3,
            entries: [{ id: "dev/a", title: "Alpha page" }, { id: "gone", state: "missing" }, { id: "", state: "missing" }],
          },
        ])}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Alpha page" }));
    expect(onOpen).toHaveBeenCalledWith("dev/a");
    expect(screen.getAllByText("not found")).toHaveLength(2);
    expect(screen.queryByText("gone")).toBeNull();
  });
});

describe("cross-KB links widget", () => {
  // AC-kbdash-32: a resolved cross-KB id draws the KB name and a title; the neutral state draws neutral text.
  it("draws the KB name with the title, and neutral text for unresolved", () => {
    const onOpen = vi.fn();
    render(
      <DashboardView
        onOpen={onOpen}
        data={base([
          {
            id: "l",
            type: "links",
            total: 3,
            entries: [
              { id: "ops/x", kb: "kb-b", title: "Target page" },
              { id: "", state: "unresolved" },
              { id: "", state: "out_of_scope" },
            ],
          },
        ])}
      />,
    );
    expect(screen.getByText("kb-b")).toBeTruthy();
    expect(screen.getByText("Target page", { exact: false })).toBeTruthy();
    expect(screen.getByText("not available")).toBeTruthy();
    expect(screen.getByText("not reachable from this connection")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
    expect(onOpen).not.toHaveBeenCalled();
  });
});
