import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { optional } from "../api/client";
import { Work } from "../components/Work";
import { json, stubApi } from "./fixtures";

const abort = () => new DOMException("aborted", "AbortError");
const entry = (id: string, map: string, status: string) => ({
  id,
  title: id,
  type: "Task",
  map,
  status,
  open_phase: true,
  stale: false,
  items: [],
});
const board = (entries: ReturnType<typeof entry>[]) => ({
  total: entries.length,
  open_concepts: entries.length,
  by_status: {},
  by_map: {},
  open_items: 0,
  entries,
});

describe("optional", () => {
  it("turns a failure into null and rethrows an abort", async () => {
    await expect(optional(Promise.reject(new Error("404")))).resolves.toBeNull();
    await expect(optional(Promise.reject(abort()))).rejects.toMatchObject({ name: "AbortError" });
    await expect(optional(Promise.resolve(3))).resolves.toBe(3);
  });
});

describe("Work", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("never shows the old KB's board after a switch, whichever leg is slowest", async () => {
    const base = stubApi();
    let release!: () => void;
    const slow = new Promise<void>((r) => (release = r));
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        if (url.includes("/kbs/kb-a/work")) return json(board([entry("a/old", "a", "open")]));
        if (url.includes("/kbs/kb-b/work")) return json(board([entry("b/new", "b", "open")]));
        if (url.includes("/kbs/kb-a/sources")) {
          await new Promise<void>((resolve, reject) => {
            init?.signal?.addEventListener("abort", () => reject(abort()));
            void slow.then(resolve);
          });
          return json({ sources: [] });
        }
        return base(url);
      }),
    );
    const { rerender } = render(<Work kb="kb-a" onOpen={() => {}} />);
    rerender(<Work kb="kb-b" onOpen={() => {}} />);
    release();
    expect(await screen.findByText("b/new")).toBeInTheDocument();
    expect(screen.queryByText("a/old")).toBeNull();
  });

  it("clears a Map filter whose chip left the data", async () => {
    let entries = [entry("a/x", "a", "open"), entry("b/y", "b", "open")];
    const base = stubApi();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) =>
        url.includes("/work") ? json(board(entries)) : base(url),
      ),
    );
    const user = userEvent.setup();
    const { rerender } = render(<Work kb="kb-a" live={0} onOpen={() => {}} />);
    const chip = await within(await screen.findByRole("list", { name: "Filter by Map" })).findByRole("button", { name: /b/ });
    await user.click(chip);
    expect(screen.queryByText("a/x")).toBeNull();
    entries = [entry("a/x", "a", "open")];
    rerender(<Work kb="kb-a" live={1} onOpen={() => {}} />);
    expect(await screen.findByText("a/x")).toBeInTheDocument();
  });
});

describe("App fetches", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&scope=infra");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("a Map click refetches the graph only, not the overview or the artifacts", async () => {
    const fetchMock = stubApi();
    const user = userEvent.setup();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    await waitFor(() => expect(fetchMock.mock.calls.filter(([u]) => String(u).includes("/graph"))).toHaveLength(1));
    await user.click(within(rail).getByRole("button", { name: /^Atlas/ }));
    await waitFor(() => expect(window.location.search).not.toContain("scope="));
    await waitFor(() => expect(fetchMock.mock.calls.filter(([u]) => String(u).includes("/graph"))).toHaveLength(2));
    const count = (part: string) => fetchMock.mock.calls.filter(([u]) => String(u).includes(part)).length;
    expect(count("/overview")).toBe(1);
    expect(count("/artifacts")).toBe(1);
  });

  it("a failed overview is fetched again on the next Map click", async () => {
    const base = stubApi();
    let overviews = 0;
    const fetchMock = vi.fn(async (url: string) => {
      if (url.includes("/overview") && overviews++ === 0) return json({ error: { code: "error", message: "boom" } }, 500);
      return base(url);
    });
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    await waitFor(() => expect(overviews).toBe(1));
    await user.click(within(rail).getByRole("button", { name: /^Atlas/ }));
    await waitFor(() => expect(overviews).toBe(2));
  });
});
