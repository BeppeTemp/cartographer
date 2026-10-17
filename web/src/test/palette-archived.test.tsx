import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CommandPalette } from "../components/CommandPalette";
import type { GraphNode } from "../api/types";
import { json, stubApi } from "./fixtures";

/** The palette keeps archived pages out of a search unless asked (D820). */
describe("the command palette and archived pages", () => {
  afterEach(() => vi.unstubAllGlobals());

  const live = { id: "notes/quokka-live", title: "Quokka live", score: 2 };
  const old = { id: "archive/quokka", title: "Quokka rota", score: 1, archived: true };

  it("re-runs the query with include_archived and badges archived hits", async () => {
    const fetchMock = stubApi();
    const route = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (url: string) => {
      if (!url.includes("/search?")) return route(url);
      const archived = url.includes("include_archived=1");
      return json({ query: "quokka", count: archived ? 2 : 1, results: archived ? [live, old] : [live] });
    });
    const user = userEvent.setup();
    render(<CommandPalette open kb="kb-a" nodes={[]} onClose={vi.fn()} onSelect={vi.fn()} />);
    await user.type(screen.getByRole("textbox", { name: /search concepts/i }), "quokka");
    await screen.findByRole("option", { name: /Quokka live/ });
    const urls = () => fetchMock.mock.calls.map((c) => String(c[0]));
    expect(urls().some((u) => u.includes("/search?"))).toBe(true);
    expect(urls().some((u) => u.includes("include_archived"))).toBe(false);
    expect(screen.queryByRole("option", { name: /Quokka rota/ })).not.toBeInTheDocument();

    const toggle = screen.getByRole("button", { name: "Include archived" });
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-pressed", "true");
    const archivedRow = await screen.findByRole("option", { name: /Quokka rota/ });
    expect(within(archivedRow).getByText("Archived")).toBeInTheDocument();
    expect(
      within(await screen.findByRole("option", { name: /Quokka live/ })).queryByText("Archived"),
    ).not.toBeInTheDocument();
    expect(urls().some((u) => u.includes("include_archived=1"))).toBe(true);
  });

  it("says nothing live matched when the server falls back to archived pages", async () => {
    stubApi({
      "/search": () => json({ query: "quokka", count: 1, results: [old], archived_fallback: true }),
    });
    const user = userEvent.setup();
    render(<CommandPalette open kb="kb-a" nodes={[]} onClose={vi.fn()} onSelect={vi.fn()} />);
    await user.type(screen.getByRole("textbox", { name: /search concepts/i }), "quokka");
    expect(await screen.findByText("Nothing live matched: these are archived pages.")).toBeInTheDocument();
    expect(screen.getByText("Archived")).toBeInTheDocument();
  });

  it("starts each opening without the toggle pressed", async () => {
    stubApi({ "/search": () => json({ query: "quokka", count: 0, results: null }) });
    const user = userEvent.setup();
    const { rerender } = render(<CommandPalette open kb="kb-a" nodes={[]} onClose={vi.fn()} onSelect={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: "Include archived" }));
    expect(screen.getByRole("button", { name: "Include archived" })).toHaveAttribute("aria-pressed", "true");
    rerender(<CommandPalette open={false} kb="kb-a" nodes={[]} onClose={vi.fn()} onSelect={vi.fn()} />);
    rerender(<CommandPalette open kb="kb-a" nodes={[]} onClose={vi.fn()} onSelect={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Include archived" })).toHaveAttribute("aria-pressed", "false");
  });

  // The snapshot lists archived pages too; a title match must not bring them
  // back while a search leaves them out.
  it("keeps archived pages out of the title matches until the toggle is on", async () => {
    stubApi({ "/search": () => json({ query: "quokka", count: 0, results: null }) });
    const nodes: GraphNode[] = [
      { id: "notes/quokka-live", title: "Quokka live", status: "active", collection: "notes", in_degree: 0, out_degree: 0 },
      { id: "archive/quokka", title: "Quokka rota", status: "archived", collection: "archive", in_degree: 0, out_degree: 0 },
    ];
    const user = userEvent.setup();
    render(<CommandPalette open kb="kb-a" nodes={nodes} onClose={vi.fn()} onSelect={vi.fn()} />);
    await user.type(screen.getByRole("textbox", { name: /search concepts/i }), "quokka");
    expect(await screen.findByRole("option", { name: /Quokka live/ })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /Quokka rota/ })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Include archived" }));
    const archived = await screen.findByRole("option", { name: /Quokka rota/ });
    expect(within(archived).getByText("Archived")).toBeInTheDocument();
  });
});
