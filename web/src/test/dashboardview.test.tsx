import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DashboardView } from "../components/DashboardView";
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
    render(<DashboardView data={base([{ id: "k", type: "kanban", total: 2 }, { id: "s", type: "stat", total: 1, groups: [{ key: "open", total: 1 }] }])} />);
    expect(screen.getByText(/kanban widget is not drawn yet/)).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "k" })).toBeInTheDocument();
    expect(screen.getByText("open")).toBeInTheDocument();
  });
});
