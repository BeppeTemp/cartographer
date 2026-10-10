import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { LintReport } from "../api/types";
import { json, lint, stubApi } from "./fixtures";

/** The rail navigates, the pages filter (#697). */

const two: LintReport = {
  ...lint,
  findings: [
    { path: "infra/a.md", concept: "infra/a", check: "broken_link", severity: "error", message: "missing target" },
    { path: "infra/b.md", concept: "infra/b", check: "broken_link", severity: "error", message: "missing target" },
    { path: "notes/c.md", concept: "notes/c", check: "orphan", severity: "warning", message: "no links" },
  ],
  count: 3,
};

/** A lint stub that answers the scope it is asked for, like the server. */
function lintByScope(): Response {
  return json(two);
}
function stubLint() {
  const fetchMock = stubApi({
    "/lint": () => lintByScope(),
    "/work": () => json({ total: 0, open_concepts: 0, by_status: {}, by_map: {}, open_items: 0, entries: [] }),
  });
  return fetchMock;
}

function open(search: string) {
  window.history.replaceState(null, "", `/ui/${search}`);
  localStorage.clear();
  localStorage.setItem("cartographer.panel.rail", "0");
  sessionStorage.clear();
}

afterEach(() => vi.unstubAllGlobals());

describe("the rail's Maps open the Atlas", () => {
  it.each(["work", "health", "activity"])("from %s, a Map click lands on the Atlas with that scope", async (panel) => {
    open(`?kb=kb-a&panel=${panel}`);
    stubLint();
    const user = userEvent.setup();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    // Off the Atlas no Map row is the current one.
    await waitFor(() => expect(within(rail).getByRole("button", { name: /Infrastructure/ })).toBeInTheDocument());
    expect(rail.querySelector('[aria-current="true"]')).toBeNull();
    await user.click(within(rail).getByRole("button", { name: /Infrastructure/ }));
    expect(window.location.search).toContain("scope=infra");
    expect(window.location.search).not.toContain("panel=");
    expect(within(rail).getByRole("button", { name: /Infrastructure/ })).toHaveAttribute("aria-current", "true");
  });
});

describe("Health filters by Map in the page", () => {
  it("narrows with the chips, keeps their counts stable and round-trips through the URL", async () => {
    open("?kb=kb-a&panel=health");
    const fetchMock = stubLint();
    const user = userEvent.setup();
    render(<App />);
    const chips = await screen.findByRole("list", { name: "Filter by Map" });
    const infra = await within(chips).findByRole("button", { name: /Infrastructure/ });
    expect(infra).toHaveTextContent("2");
    await user.click(infra);
    expect(window.location.search).toContain("hmap=infra");
    expect(infra).toHaveAttribute("aria-pressed", "true");
    // The chip filters in the page: one whole-KB fetch, none scoped.
    await waitFor(() => expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(/in Infrastructure/));
    const lintUrls = () => fetchMock.mock.calls.map(([u]) => String(u)).filter((u) => u.includes("/lint"));
    expect(lintUrls()).toHaveLength(1);
    expect(lintUrls().some((u) => u.includes("scope="))).toBe(false);
    // The counts come from the unfiltered findings, so they do not move.
    expect(within(chips).getByRole("button", { name: /Infrastructure/ })).toHaveTextContent("2");
    expect(within(chips).getByRole("button", { name: /notes/ })).toHaveTextContent("1");
    await user.click(within(chips).getByRole("button", { name: /Infrastructure/ }));
    expect(window.location.search).not.toContain("hmap");
  });

  it("reads the filter from the URL and ignores the rail's scope", async () => {
    open("?kb=kb-a&panel=health&hmap=infra&scope=notes");
    const fetchMock = stubLint();
    render(<App />);
    await waitFor(() => expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(/in Infrastructure/));
    const lintUrls = fetchMock.mock.calls.map(([u]) => String(u)).filter((u) => u.includes("/lint"));
    expect(lintUrls.some((u) => u.includes("scope="))).toBe(false);
  });
});
