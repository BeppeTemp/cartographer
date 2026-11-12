import { render, renderHook, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { fetchExtensions } from "../api/client";
import { useExtensions } from "../components/ExtensionView";
import type { ExtensionApp, ExtensionView, Extensions } from "../api/types";
import { json, stubApi } from "./fixtures";

const view = (over: Partial<ExtensionView> = {}): ExtensionView => ({
  plugin: "acme",
  name: "sources",
  title: "Source ledger",
  description: "What was ingested",
  icon: "sources",
  scope: "kb",
  source: { route: "/api/v1/kbs/{kb}/sources", items: "sources" },
  renderer: "table",
  columns: [
    { key: "id", label: "Source", format: "text" },
    { key: "state", label: "State", format: "badge" },
  ],
  limit: 100,
  ...over,
});
const app: ExtensionApp = {
  plugin: "acme",
  name: "board",
  title: "Board",
  description: "",
  icon: "paths",
  url: "https://board.example.com/",
};
const ext = (over: Partial<Extensions> = {}): Extensions => ({ views: [], apps: [], plugins: [], ...over });

function open(search: string) {
  window.history.replaceState(null, "", `/ui/${search}`);
  localStorage.clear();
  localStorage.setItem("cartographer.panel.rail", "0");
  sessionStorage.clear();
}
const sourceCalls = (fetchMock: ReturnType<typeof vi.fn>) =>
  fetchMock.mock.calls.filter((c) => String(c[0]).startsWith("/api/v1/"));

afterEach(() => vi.unstubAllGlobals());

describe("the Extensions rail group", () => {
  it("is absent without views or apps", async () => {
    open("?kb=kb-a");
    stubApi({ "/extensions": () => json(ext()) });
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    await within(rail).findByRole("button", { name: "Settings" });
    expect(within(rail).queryByText("Extensions")).toBeNull();
  });

  it("lists two views and an app; the app is a plain link that hands over nothing", async () => {
    open("?kb=kb-a");
    stubApi({ "/extensions": () => json(ext({ views: [view(), view({ name: "two", title: "Two" })], apps: [app] })) });
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    expect(await within(rail).findByText("Extensions")).toBeInTheDocument();
    expect(within(rail).getByRole("button", { name: "Source ledger" })).toBeInTheDocument();
    expect(within(rail).getByRole("button", { name: "Two" })).toBeInTheDocument();
    const link = within(rail).getByRole("link", { name: "Board, opens board.example.com in a new tab" });
    expect(link).toHaveAttribute("href", "https://board.example.com/");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link.getAttribute("rel")).toMatch(/noopener/);
    expect(link.getAttribute("rel")).toMatch(/noreferrer/);
    expect(link.outerHTML).not.toMatch(/token|authorization|ping=/i);
  });

  it("does not draw an app whose URL is not http(s)", async () => {
    open("?kb=kb-a");
    stubApi({ "/extensions": () => json(ext({ apps: [{ ...app, url: "javascript:alert(1)" }] })) });
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    await within(rail).findByRole("button", { name: "Settings" });
    expect(within(rail).queryByRole("link", { name: /Board/ })).toBeNull();
  });
});

describe("a plugin view", () => {
  it("opens with one GET of the substituted public path and draws hostile cells as text", async () => {
    open("?kb=kb-a&panel=ext&ext=acme/sources");
    const fetchMock = stubApi({
      "/api/v1/": () =>
        json({ sources: [{ id: "<img src=x onerror=alert(1)>", state: "javascript:alert(1)" }, { id: "b", state: "ok" }] }),
      "/extensions": () => json(ext({ views: [view()] })),
    });
    render(<App />);
    const table = await screen.findByRole("table", { name: "Source ledger" });
    expect(within(table).getByText("<img src=x onerror=alert(1)>")).toBeInTheDocument();
    expect(within(table).getByText("javascript:alert(1)")).toBeInTheDocument();
    expect(table.querySelector("img, a")).toBeNull();
    expect(screen.getByText("Supplied by plugin acme")).toBeInTheDocument();
    expect(sourceCalls(fetchMock)).toHaveLength(1);
    expect(sourceCalls(fetchMock)[0]![0]).toBe("/api/v1/kbs/kb-a/sources");
  });

  it("shows the error state when the route answers 403", async () => {
    open("?kb=kb-a&panel=ext&ext=acme/sources");
    stubApi({
      "/api/v1/": () => json({ error: { code: "forbidden", message: "no" } }, 403),
      "/extensions": () => json(ext({ views: [view()] })),
    });
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent(/403/);
  });

  it("says so when the answer is not the declared shape", async () => {
    open("?kb=kb-a&panel=ext&ext=acme/sources");
    stubApi({ "/api/v1/": () => json({ sources: "nope" }), "/extensions": () => json(ext({ views: [view()] })) });
    render(<App />);
    expect(await screen.findByText("Unexpected shape")).toBeInTheDocument();
  });

  it("caps rows at the limit with 'N more'", async () => {
    open("?kb=kb-a&panel=ext&ext=acme/sources");
    const rows = Array.from({ length: 250 }, (_, i) => ({ id: `s${i}`, state: "ok" }));
    stubApi({
      "/api/v1/": () => json({ sources: rows }),
      "/extensions": () => json(ext({ views: [view({ limit: 200 })] })),
    });
    render(<App />);
    const table = await screen.findByRole("table", { name: "Source ledger" });
    expect(within(table).getAllByRole("row")).toHaveLength(201);
    expect(screen.getByText("50 more")).toBeInTheDocument();
  });

  it("draws stats and key-value renderers from an object", async () => {
    open("?kb=kb-a&panel=ext&ext=acme/sources");
    stubApi({
      "/api/v1/": () => json({ sum: { n: 1500 } }),
      "/extensions": () =>
        json(
          ext({
            views: [
              view({
                renderer: "stats",
                source: { route: "/api/v1/kbs/{kb}/sources", items: "sum" },
                columns: [{ key: "n", label: "Total", format: "number" }],
              }),
            ],
          }),
        ),
    });
    render(<App />);
    const list = await screen.findByRole("list", { name: "Source ledger" });
    expect(list).toHaveTextContent("1,500");
    expect(list).toHaveTextContent("Total");
  });

  it("falls back to the Atlas with a notice when the view is not approved", async () => {
    open("?kb=kb-a&panel=ext&ext=acme/gone");
    stubApi({ "/extensions": () => json(ext({ views: [view()] })) });
    render(<App />);
    await waitFor(() => expect(window.location.search).not.toContain("panel=ext"));
    await waitFor(() => expect(screen.getAllByRole("status").some((n) => /not available/.test(n.textContent ?? ""))).toBe(true));
  });

  it("opens from the rail with the view in the URL", async () => {
    open("?kb=kb-a");
    stubApi({ "/api/v1/": () => json({ sources: [] }), "/extensions": () => json(ext({ views: [view()] })) });
    const user = userEvent.setup();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    await user.click(await within(rail).findByRole("button", { name: "Source ledger" }));
    expect(window.location.search).toContain("panel=ext");
    expect(decodeURIComponent(window.location.search)).toContain("ext=acme/sources");
    expect(await screen.findByText("Nothing to show")).toBeInTheDocument();
  });

  it("refuses a route outside the public prefix without a request", async () => {
    open("?kb=kb-a&panel=ext&ext=acme/sources");
    const fetchMock = stubApi({
      "/extensions": () => json(ext({ views: [view({ source: { route: "/api/ui/v1/settings" } })] })),
    });
    render(<App />);
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(fetchMock.mock.calls.some((c) => String(c[0]).endsWith("/settings"))).toBe(false);
  });
});

describe("a failed read of the extensions", () => {
  it("keeps the last good list on a 500 and says 'none' only for a 404", async () => {
    let status = 200;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => (status === 200 ? json(ext({ views: [view()] })) : json({ error: { code: "x", message: "x" } }, status))),
    );
    const { result, rerender } = renderHook(({ live }) => useExtensions(live, true), { initialProps: { live: 0 } });
    await waitFor(() => expect(result.current.extensions.views).toHaveLength(1));
    status = 500;
    rerender({ live: 1 });
    await waitFor(() => expect((fetch as ReturnType<typeof vi.fn>).mock.calls).toHaveLength(2));
    expect(result.current.loaded).toBe(true);
    expect(result.current.extensions.views).toHaveLength(1);
    await expect(fetchExtensions()).rejects.toMatchObject({ status: 500 });
    status = 404;
    expect((await fetchExtensions()).views).toEqual([]);
  });

  it("does not leave a view's page before the first success", async () => {
    open("?kb=kb-a&panel=ext&ext=acme/sources");
    stubApi({ "/extensions": () => json({ error: { code: "x", message: "x" } }, 500) });
    render(<App />);
    await screen.findByRole("navigation", { name: "Atlas navigation" });
    expect(window.location.search).toContain("panel=ext");
  });
});
