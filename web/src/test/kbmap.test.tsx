import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { clearToken } from "../api/client";
import type { KBMap as KBMapData } from "../api/types";
import { readViewState, viewStateToSearch } from "../lib/viewstate";
import { json, overview, stubApi } from "./fixtures";
import { sceneStub } from "./sceneStub";

const kbRow = (name: string) => ({ name, status: "normal", ready: true, artifacts: true });
const twoKBs = () => json({ kbs: [kbRow("core-kb"), kbRow("kb-a")] });
const map: KBMapData = {
  nodes: [
    { name: "core-kb", ready: true, layer: 0 },
    { name: "kb-a", ready: true, layer: 1 },
  ],
  relations: [{ from: "kb-a", to: "core-kb" }],
  links: [{ from: "kb-a", to: "core-kb", count: 3 }],
  truncated: false,
};

/** The default stub, with two KBs and (optionally) a KB map answered. */
function stub(kbMap: KBMapData | null, kbs = true) {
  const fetchMock = stubApi();
  const base = fetchMock.getMockImplementation()!;
  fetchMock.mockImplementation(async (url: string) => {
    if (kbs && url.endsWith("/kbs")) return twoKBs();
    if (kbMap && url.endsWith("/kb-map")) return json(kbMap);
    return base(url);
  });
}

function open(search: string) {
  window.history.replaceState(null, "", `/ui/${search}`);
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem("cartographer.panel.rail", "0");
}

beforeEach(() => sceneStub.reset());
afterEach(() => {
  vi.unstubAllGlobals();
  sceneStub.reset();
});

// AC-atlas-kb-map-9
describe("KBMap", () => {
  it("says why a KB's relations are unavailable", async () => {
    open("?kb=core-kb&panel=kbmap");
    stub({
      ...map,
      nodes: [
        { name: "core-kb", ready: true, layer: 0, relations_unavailable: true, relations_unavailable_reason: "no_origin_head" },
        { name: "kb-a", ready: true, layer: 0, relations_unavailable: true, relations_unavailable_reason: "read_failed" },
      ],
    });
    render(<App />);
    const list = await screen.findByRole("list", { name: "Relations unavailable" });
    expect(list).toHaveTextContent("core-kb: the remote has not been fetched yet, sync first");
    expect(list).toHaveTextContent("kb-a: the relations could not be read");
  });

  it("draws a node per KB in rows by layer, the layers behind their toggles, and a click opens that KB", async () => {
    open("?kb=core-kb&panel=kbmap");
    stub(map);
    const user = userEvent.setup();
    render(<App />);
    const core = await screen.findByRole("group", { name: "Layer 0" });
    expect(within(core).getByRole("button", { name: "core-kb" })).toBeInTheDocument();
    expect(within(screen.getByRole("group", { name: "Layer 1" })).getByRole("button", { name: "kb-a" })).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Relations" })).toHaveTextContent("kb-a → core-kb");
    expect(screen.getByRole("list", { name: "Cross-KB links" })).toHaveTextContent("kb-a → core-kb (3)");

    await user.click(screen.getByRole("checkbox", { name: "Relations" }));
    expect(screen.queryByRole("list", { name: "Relations" })).toBeNull();
    expect(screen.getByRole("list", { name: "Cross-KB links" })).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Cross-KB links" }));
    expect(screen.queryByRole("list", { name: "Cross-KB links" })).toBeNull();

    // Opening a KB leaves the map for that KB's atlas: the state of the map is not kept.
    await user.click(within(screen.getByRole("group", { name: "Layer 1" })).getByRole("button", { name: "kb-a" }));
    expect(window.location.search).toContain("kb=kb-a");
    expect(window.location.search).not.toContain("panel=");
    await waitFor(() => expect(screen.queryByRole("group", { name: "Layer 0" })).toBeNull());
  });
});

// AC-atlas-kb-map-10
describe("the KB map entry and its URL", () => {
  it("round-trips panel=kbmap and keeps kb", () => {
    const view = { ...readViewState("?kb=kb-a&panel=kbmap") };
    expect(view.panel).toBe("kbmap");
    expect(view.kb).toBe("kb-a");
    expect(viewStateToSearch(view)).toBe("?kb=kb-a&panel=kbmap");
  });

  it("is a button with two KBs, absent with one, and hidden by a 404 from an older server", async () => {
    open("?kb=core-kb");
    stub(map);
    const user = userEvent.setup();
    const first = render(<App />);
    const entry = await screen.findByRole("button", { name: "KB map" });
    await user.click(entry);
    expect(window.location.search).toContain("panel=kbmap");
    first.unmount();

    open("?kb=kb-a");
    stubApi();
    const single = render(<App />);
    await screen.findByRole("button", { name: /Search concepts/ });
    expect(screen.queryByRole("button", { name: "KB map" })).toBeNull();
    single.unmount();

    open("?kb=core-kb");
    stub(null); // /kb-map answers 404 by default
    render(<App />);
    await screen.findByRole("button", { name: /Search concepts/ });
    await waitFor(() => expect(screen.queryByRole("button", { name: "KB map" })).toBeNull());
  });
});

describe("a link to the KB map where there is nothing to map", () => {
  it("falls back to the atlas with one KB and on a server without the route", async () => {
    open("?kb=kb-a&panel=kbmap");
    stubApi();
    const single = render(<App />);
    await waitFor(() => expect(window.location.search).not.toContain("panel=kbmap"));
    single.unmount();

    open("?kb=core-kb&panel=kbmap");
    stub(null); // two KBs, /kb-map answers 404
    render(<App />);
    await waitFor(() => expect(window.location.search).not.toContain("panel=kbmap"));
    expect(screen.queryByRole("group", { name: "Layer 0" })).toBeNull();
  });

  it("asks for the map once when the app's probe is fresh", async () => {
    open("?kb=core-kb&panel=kbmap");
    const fetchMock = stubApi();
    const base = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (url: string) => {
      if (url.endsWith("/kbs")) return twoKBs();
      if (url.endsWith("/kb-map")) return json(map);
      return base(url);
    });
    render(<App />);
    await screen.findByRole("group", { name: "Layer 0" });
    expect(fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/kb-map"))).toHaveLength(1);
  });
});

describe("the KB map after a token change", () => {
  afterEach(() => clearToken());

  it("never shows the previous caller's map and asks again", async () => {
    open("?kb=core-kb");
    const fetchMock = stubApi();
    const base = fetchMock.getMockImplementation()!;
    const mapOf = (names: string[]): KBMapData => ({
      nodes: names.map((name, layer) => ({ name, ready: true, layer })),
      relations: [],
      links: [],
      truncated: false,
    });
    let revoked = false;
    // Alice's /kb-map answer is held back: it lands after bob has signed in.
    let releaseAlice: () => void = () => {};
    const aliceGate = new Promise<void>((resolve) => (releaseAlice = resolve));
    const as = (init: unknown) => ((init as { headers?: Record<string, string> } | undefined)?.headers?.Authorization ?? "");
    fetchMock.mockImplementation(async (url: string, init?: unknown) => {
      const who = as(init);
      const unauthorized = () => json({ error: { code: "unauthorized", message: "no" } }, 401);
      if (url.endsWith("/kbs")) return who ? twoKBs() : unauthorized();
      if (url.endsWith("/kb-map") && who.includes("alice")) {
        await aliceGate;
        return json(mapOf(["core-kb", "alice-only"]));
      }
      if (url.endsWith("/kb-map")) return json(mapOf(["core-kb", "kb-a"]));
      if (revoked && url.includes("/kbs/kb-a/graph")) return unauthorized();
      return base(url);
    });
    const user = userEvent.setup();
    render(<App />);
    const signIn = async (who: string) => {
      await user.type(await screen.findByLabelText(/bearer token/i), who);
      await user.click(screen.getByRole("button", { name: /open the atlas/i }));
    };
    const mapCalls = (who: string) =>
      fetchMock.mock.calls.filter((call) => String(call[0]).endsWith("/kb-map") && as((call as unknown[])[1]).includes(who)).length;

    await signIn("alice");
    await waitFor(() => expect(mapCalls("alice")).toBe(1));

    // Alice's token is revoked while she reads another KB: the prompt returns.
    revoked = true;
    window.history.pushState(null, "", "/ui/?kb=kb-a");
    window.dispatchEvent(new PopStateEvent("popstate"));
    await screen.findByLabelText(/bearer token/i);
    revoked = false;
    await signIn("bob");

    // Alice's in-flight answer lands now, for a caller who has gone.
    releaseAlice();
    await new Promise((resolve) => setTimeout(resolve, 20));
    await user.click(await screen.findByRole("button", { name: "KB map" }));
    await screen.findByRole("group", { name: "Layer 1" });
    expect(screen.queryByText("alice-only")).toBeNull();
    expect(mapCalls("bob")).toBeGreaterThanOrEqual(1);
  });
});

describe("the cached views after a token change", () => {
  afterEach(() => clearToken());

  it("never show the previous caller's overview for the next", async () => {
    open("?kb=core-kb");
    const fetchMock = stubApi();
    const base = fetchMock.getMockImplementation()!;
    let revoked = false;
    const as = (init: unknown) => ((init as { headers?: Record<string, string> } | undefined)?.headers?.Authorization ?? "");
    const hang = () => new Promise<Response>(() => {});
    fetchMock.mockImplementation(async (url: string, init?: unknown) => {
      const who = as(init);
      if (url.endsWith("/kbs")) return who ? twoKBs() : json({ error: { code: "unauthorized", message: "no" } }, 401);
      if (url.includes("/kbs/core-kb/graph") && url.includes("scope=") && revoked && who.includes("alice")) {
        return json({ error: { code: "unauthorized", message: "no" } }, 401);
      }
      if (url.includes("/kbs/core-kb/overview")) {
        if (who.includes("bob")) return hang();
        return json({
          ...overview,
          collections: [{ name: "infra", title: "Alice Private", kind: "map", concepts: 2, expanded_concepts: 0 }],
        });
      }
      if (who.includes("bob") && url.includes("/kbs/core-kb/graph")) return hang();
      return base(url);
    });
    const user = userEvent.setup();
    render(<App />);
    await user.type(await screen.findByLabelText(/bearer token/i), "alice");
    await user.click(screen.getByRole("button", { name: /open the atlas/i }));
    expect((await screen.findAllByText("Alice Private")).length).toBeGreaterThan(0);

    revoked = true;
    window.history.pushState(null, "", "/ui/?kb=core-kb&scope=infra");
    window.dispatchEvent(new PopStateEvent("popstate"));
    await screen.findByLabelText(/bearer token/i);
    revoked = false;
    await user.type(screen.getByLabelText(/bearer token/i), "bob");
    await user.click(screen.getByRole("button", { name: /open the atlas/i }));
    await screen.findByRole("button", { name: /Search concepts/ });
    // Back to the view alice had open: the cached branch must be empty for bob.
    window.history.pushState(null, "", "/ui/?kb=core-kb");
    window.dispatchEvent(new PopStateEvent("popstate"));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(screen.queryByText("Alice Private")).toBeNull();
  });
});

// AC-atlas-kb-map-11
describe("external stub click", () => {
  it("a resolved stub opens the target page of its KB, an unresolved edge is not drawn at all", async () => {
    open("?kb=core-kb");
    const fetchMock = stubApi();
    const base = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (url: string) => {
      if (url.endsWith("/kbs")) return twoKBs();
      if (url.includes("/kbs/core-kb/graph/external")) {
        return json({
          edges: [
            { from: "infra/a", direction: "out", kb: "kb-a", id: "ops/runbook", state: "resolved" },
            { from: "infra/a", direction: "out", kb: "kb-a", id: "ops/gone", state: "missing" },
          ],
          limit: 2000,
          truncated: false,
        });
      }
      return base(url);
    });
    render(<App />);
    await waitFor(() => expect(sceneStub.calls.data.some((c) => c.positions.has("kb:kb-a/ops/runbook"))).toBe(true));
    expect(sceneStub.calls.data.some((c) => c.positions.has("kb:kb-a/ops/gone"))).toBe(false);

    act(() => sceneStub.callbacks!.onSelect("kb:kb-a/ops/runbook"));
    expect(window.location.search).toContain("kb=kb-a");
    expect(window.location.search).toContain("concept=ops%2Frunbook");
    expect(window.location.search).not.toContain("panel=");
  });
});
