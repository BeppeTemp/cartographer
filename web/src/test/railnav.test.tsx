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

describe("the brand", () => {
  it("returns to the Atlas on All, nothing selected", async () => {
    open("?kb=kb-a&scope=infra&concept=infra/a");
    stubLint();
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole("button", { name: /back to the atlas/ }));
    expect(window.location.search).not.toContain("scope=");
    expect(window.location.search).not.toContain("concept=");
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    expect(within(rail).getByRole("button", { name: /^All/ })).toHaveAttribute("aria-current", "true");
  });
});

describe("Health filters by Map in the page", () => {
  it("narrows with the page field's Maps and round-trips through the URL", async () => {
    open("?kb=kb-a&panel=health");
    const fetchMock = stubLint();
    const user = userEvent.setup();
    render(<App />);
    const field = await screen.findByRole("group", { name: "Pages by Map" });
    const infra = await within(field).findByRole("button", { name: /Infrastructure/ });
    expect(infra).toHaveAttribute("title", expect.stringMatching(/^2 of \d+ pages with an issue/));
    await user.click(infra);
    expect(window.location.search).toContain("hmap=infra");
    expect(infra).toHaveAttribute("aria-pressed", "true");
    // The field filters in the page: one whole-KB fetch, none scoped.
    await waitFor(() => expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(/in Infrastructure/));
    const lintUrls = () => fetchMock.mock.calls.map(([u]) => String(u)).filter((u) => u.includes("/lint"));
    expect(lintUrls()).toHaveLength(1);
    expect(lintUrls().some((u) => u.includes("scope="))).toBe(false);
    // The field counts the unfiltered findings, so its numbers do not move.
    expect(within(field).getByRole("button", { name: /Infrastructure/ })).toHaveAttribute(
      "title",
      expect.stringMatching(/^2 of /),
    );
    await user.click(within(field).getByRole("button", { name: /Infrastructure/ }));
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
