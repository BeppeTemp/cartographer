import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { ArtifactList } from "../api/types";
import { artifactList, json, stubApi } from "./fixtures";

/**
 * The Artifacts panel (D238): what the KB ships to agent clients, behind
 * whole-KB visibility, with the selection in the URL.
 */
describe("the Artifacts panel", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("is not offered to a principal that cannot see the whole KB", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=artifacts");
    const fetchMock = stubApi();
    const route = fetchMock.getMockImplementation()!;
    const requested: string[] = [];
    fetchMock.mockImplementation(async (url: string) => {
      requested.push(url);
      if (url.endsWith("/kbs")) {
        return json({ kbs: [{ name: "kb-a", status: "normal", ready: true, artifacts: false }] });
      }
      return route(url);
    });
    render(<App />);
    expect(await screen.findByRole("button", { name: "Health" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Artifacts/ })).not.toBeInTheDocument();
    await waitFor(() => expect(window.location.search).not.toContain("panel=artifacts"));
    expect(requested.some((url) => url.includes("/artifact"))).toBe(false);
  });

  it("groups by kind, filters, and keeps the selection in the URL", async () => {
    stubApi();
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole("button", { name: "Artifacts" }));
    const panel = await screen.findByRole("region", { name: "Artifacts" });
    expect(window.location.search).toContain("panel=artifacts");

    const nav = within(panel).getByRole("navigation", { name: "Artifacts by kind" });
    // Each heading carries its kind's mark (aria-hidden) before the words.
    const heads = () => within(nav).getAllByRole("heading", { level: 2 }).map((h) => h.textContent?.slice(1));
    // Every kind a KB can hold, the empty ones too (D864).
    expect(heads()).toEqual([
      "Skills 1",
      "Subagents 1",
      "Hooks 0",
      "MCP servers 0",
      "Instructions 0",
      "Templates 1",
    ]);

    // One kind at a time, without scrolling past the others; pressed again, all.
    const kinds = within(panel).getByRole("list", { name: "Show kind" });
    await user.click(within(kinds).getByRole("button", { name: /Subagents/ }));
    expect(heads()).toEqual(["Subagents 1"]);
    await user.click(within(kinds).getByRole("button", { name: /Subagents/ }));
    expect(heads()).toHaveLength(6);

    await user.type(within(panel).getByRole("searchbox", { name: "Filter artifacts" }), "sorts");
    expect(within(nav).queryByRole("button", { name: /review/ })).not.toBeInTheDocument();
    expect(within(nav).getByRole("button", { name: /triage/ })).toBeInTheDocument();
    await user.clear(within(panel).getByRole("searchbox", { name: "Filter artifacts" }));

    await user.click(within(nav).getByRole("button", { name: /review/ }));
    await waitFor(() => expect(window.location.search).toContain("artifact=skill%2Freview"));
    const detail = await screen.findByRole("article", { name: "Artifact skill/review" });
    expect(within(detail).getByText("Signed")).toBeInTheDocument();
    expect(within(detail).getByText("Codex CLI")).toBeInTheDocument();
    // The body as Markdown; frontmatter the header already shows is not said twice.
    expect(within(detail).queryByRole("rowheader", { name: "description" })).not.toBeInTheDocument();
    expect(within(detail).getAllByText("Reviews a change")).toHaveLength(1);
    expect(within(detail).getByRole("heading", { name: "Review steps" })).toBeInTheDocument();

    await user.click(within(detail).getByRole("tab", { name: "logo.bin" }));
    expect(within(detail).getByText("Binary file — not shown.")).toBeInTheDocument();

    window.history.back();
    await waitFor(() => expect(window.location.search).not.toContain("artifact="));
    expect(screen.queryByRole("article", { name: /Artifact/ })).not.toBeInTheDocument();
  });

  it("shows artifact findings in the list and the detail (D316)", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=artifacts&artifact=skill%2Freview");
    stubApi();
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Artifacts" });
    expect(within(panel).getByText(/2 findings across artifacts/)).toBeInTheDocument();
    const item = within(panel).getByRole("button", { name: /review/ });
    expect(within(item).getByText("problem")).toBeInTheDocument();
    expect(within(panel).getByRole("button", { name: /triage/ }).querySelector(".finding-badge")).toBeNull();
    const detail = await screen.findByRole("article", { name: "Artifact skill/review" });
    const list = within(detail).getByRole("list", { name: "Artifact findings" });
    expect(within(list).getByText("legacy_tool_name")).toBeInTheDocument();
  });

  it("shows when each skill and agent was last used (D326)", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=artifacts&artifact=skill%2Freview");
    const fetchMock = stubApi();
    const route = fetchMock.getMockImplementation()!;
    const used = {
      last_used: "2026-10-03T08:00:00Z",
      last_used_provider: "claude",
      last_used_days_ago: 3,
    };
    fetchMock.mockImplementation(async (url: string) => {
      if (url.includes("/kbs/") && url.includes("/artifacts")) {
        const res = (await route(url)) as Response;
        const list = await res.json();
        // review: used 3 days ago; triage: never; the template has no usage.
        for (const a of list.artifacts) {
          if (a.name === "review") Object.assign(a, used);
          if (a.name === "triage") Object.assign(a, { last_used: null, last_used_provider: null, last_used_days_ago: null });
        }
        return json(list);
      }
      if (url.includes("/kbs/") && url.includes("/artifact?")) {
        const res = (await route(url)) as Response;
        return json({ ...(await res.json()), ...used });
      }
      return route(url);
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Artifacts" });
    const review = within(panel).getByRole("button", { name: /review/ });
    expect(within(review).getByText("3 days ago (claude)")).toHaveClass("artifacts__used--active");
    const triage = within(panel).getByRole("button", { name: /triage/ });
    expect(within(triage).getByText("never used")).toHaveClass("artifacts__used--never");
    // A template is not followed by the scanner: no cell at all.
    expect(within(panel).getByRole("button", { name: /runbook/ }).querySelector(".artifacts__used")).toBeNull();
    // The detail carries the exact timestamp.
    const detail = await screen.findByRole("article", { name: "Artifact skill/review" });
    expect(within(detail).getByText("Last used")).toBeInTheDocument();
    expect(within(detail).getByText("2026-10-03T08:00:00Z")).toBeInTheDocument();
  });

  /** The list a KB with nothing in it gets: every kind, none shipped. */
  const emptyList: ArtifactList = {
    ...artifactList,
    artifacts: [],
    counts: {},
    kinds: artifactList.kinds!.map((k) => ({ ...k, count: 0 })),
    finding_counts: {},
  };

  it("lists every kind of an empty KB with its path, clients and docs, and no page-level empty state (D864)", async () => {
    stubApi({ "/artifacts": () => json(emptyList) });
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole("button", { name: "Artifacts" }));
    const panel = await screen.findByRole("region", { name: "Artifacts" });

    expect(within(panel).queryByText("This KB ships no artifacts")).not.toBeInTheDocument();
    const nav = within(panel).getByRole("navigation", { name: "Artifacts by kind" });
    expect(within(nav).getAllByRole("heading", { level: 2 }).map((h) => h.textContent?.slice(1))).toEqual([
      "Skills 0",
      "Subagents 0",
      "Hooks 0",
      "MCP servers 0",
      "Instructions 0",
      "Templates 0",
    ]);

    const hooks = within(nav).getByRole("region", { name: "Hooks" });
    expect(within(hooks).getByText("hooks/<name>/hook.json").tagName).toBe("CODE");
    expect(within(hooks).getByText(/Synced to Claude Code, Codex CLI\./)).toBeInTheDocument();
    expect(within(hooks).getByText(/Commands a client runs on its own events\./)).toBeInTheDocument();
    // An empty section says where the files go.
    const mcp = within(nav).getByRole("region", { name: "MCP servers" });
    expect(within(mcp).getByText("mcp/<name>.json").tagName).toBe("CODE");
    const templates = within(nav).getByRole("region", { name: "Templates" });
    expect(within(templates).getByText(/Stays in the KB: no client receives it\./)).toBeInTheDocument();
    expect(within(templates).queryByText(/Synced to/)).not.toBeInTheDocument();

    // The path names a folder: text, never a link.
    expect(within(nav).queryByRole("link", { name: /hooks\/<name>/ })).not.toBeInTheDocument();

    // Every kind has a chip, and pressing an empty one shows only its section.
    const kinds = within(panel).getByRole("list", { name: "Show kind" });
    await user.click(within(kinds).getByRole("button", { name: /MCP servers/ }));
    expect(within(nav).getAllByRole("heading", { level: 2 }).map((h) => h.textContent?.slice(1))).toEqual([
      "MCP servers 0",
    ]);
    expect(within(nav).getByText("mcp/<name>.json")).toBeInTheDocument();
  });

  it("links the docs to the repository on main, in a new tab, without opener or referrer (D864)", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=artifacts");
    stubApi({ "/artifacts": () => json(emptyList) });
    render(<App />);
    const hooks = within(await screen.findByRole("region", { name: "Hooks" }));
    const link = hooks.getByRole("link", { name: "Docs" });
    expect(link).toHaveAttribute("href", "https://github.com/BeppeTemp/cartographer/blob/main/docs/sync.md#agents-and-hooks");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("links only a repository docs path, whatever the response says (D864)", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=artifacts");
    const hostile: ArtifactList = {
      ...emptyList,
      kinds: emptyList.kinds!.map((k, i) => ({
        ...k,
        doc: ["https://evil.example/x#y", "docs/../../other/repo/blob/main/x.md#y", "javascript:alert(1)#x", "docs/sync.md", "/docs/sync.md#a", "docs/sync.md#ok"][i]!,
      })),
    };
    stubApi({ "/artifacts": () => json(hostile) });
    render(<App />);
    const nav = within(await screen.findByRole("navigation", { name: "Artifacts by kind" }));
    // Only the last kind, whose path is one of ours, gets a link.
    const links = nav.getAllByRole("link", { name: "Docs" });
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveAttribute("href", "https://github.com/BeppeTemp/cartographer/blob/main/docs/sync.md#ok");
  });

  it("hides the empty kinds while a filter or a slice narrows the list (D864)", async () => {
    stubApi();
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole("button", { name: "Artifacts" }));
    const panel = await screen.findByRole("region", { name: "Artifacts" });
    const nav = within(panel).getByRole("navigation", { name: "Artifacts by kind" });
    const heads = () => within(nav).getAllByRole("heading", { level: 2 }).map((h) => h.textContent?.slice(1));
    expect(heads()).toHaveLength(6);

    await user.type(within(panel).getByRole("searchbox", { name: "Filter artifacts" }), "sorts");
    expect(heads()).toEqual(["Subagents 1"]);
    await user.clear(within(panel).getByRole("searchbox", { name: "Filter artifacts" }));
    expect(heads()).toHaveLength(6);

    // A filter nothing matches reads as no match, not as six empty kinds.
    await user.type(within(panel).getByRole("searchbox", { name: "Filter artifacts" }), "zzz");
    expect(within(nav).queryAllByRole("heading", { level: 2 })).toHaveLength(0);
    expect(within(nav).getByText("No artifact matches these filters.")).toBeInTheDocument();
    await user.clear(within(panel).getByRole("searchbox", { name: "Filter artifacts" }));

    // A slice narrows the list too.
    await user.click(within(panel).getByRole("button", { name: /With findings/ }));
    expect(heads()).toEqual(["Skills 1"]);
  });

  it("falls back to the kinds that have an artifact when the server sends no list (older server)", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=artifacts");
    const { kinds: _kinds, ...older } = artifactList;
    stubApi({ "/artifacts": () => json(older) });
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole("button", { name: "Artifacts" }));
    const panel = await screen.findByRole("region", { name: "Artifacts" });
    const nav = within(panel).getByRole("navigation", { name: "Artifacts by kind" });
    expect(within(nav).getAllByRole("heading", { level: 2 }).map((h) => h.textContent?.slice(1))).toEqual([
      "Skills 1",
      "Subagents 1",
      "Templates 1",
    ]);
    const chips = within(within(panel).getByRole("list", { name: "Show kind" }));
    expect(chips.queryByRole("button", { name: /Hooks/ })).not.toBeInTheDocument();
    expect(within(panel).queryByRole("link", { name: "Docs" })).not.toBeInTheDocument();
  });

  it("keeps the page-level empty state for an older server and a KB with nothing", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=artifacts");
    const { kinds: _kinds, ...older } = emptyList;
    stubApi({ "/artifacts": () => json(older) });
    render(<App />);
    expect(await screen.findByText("This KB ships no artifacts")).toBeInTheDocument();
  });

  it("renders a kind's path and name as text, never as markup (D864)", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=artifacts");
    const hostile: ArtifactList = {
      ...emptyList,
      kinds: emptyList.kinds!.map((k) => ({
        ...k,
        title: k.kind === "hook" ? "<img src=x onerror=\"window.__pwned = true\">Hooks" : k.title,
        kb_path: "<img src=x onerror=\"window.__pwned = true\">",
        clients: [{ id: "x", name: "<script>window.__pwned = true</script>" }],
      })),
    };
    stubApi({ "/artifacts": () => json(hostile) });
    render(<App />);
    const nav = await screen.findByRole("navigation", { name: "Artifacts by kind" });
    expect(nav.querySelector("script, img, [onerror]")).toBeNull();
    expect(within(nav).getAllByText(/<img src=x onerror/).length).toBeGreaterThan(0);
    expect((window as unknown as { __pwned?: boolean }).__pwned).toBeUndefined();
  });

  it("opens the artifact a shared link names", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=artifacts&artifact=skill%2Freview");
    stubApi();
    render(<App />);
    expect(await screen.findByRole("article", { name: "Artifact skill/review" })).toBeInTheDocument();
  });
});
