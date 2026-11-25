import { render, screen } from "@testing-library/react";
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
    render(<DashboardView data={base([{ id: "k", type: "agenda", total: 2 }, { id: "s", type: "stat", total: 1, groups: [{ key: "open", total: 1 }] }])} />);
    expect(screen.getByText(/agenda widget is not drawn yet/)).toBeInTheDocument();
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
});
