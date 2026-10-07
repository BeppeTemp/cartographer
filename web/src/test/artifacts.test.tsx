import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { json, stubApi } from "./fixtures";

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
    await user.click(await screen.findByRole("button", { name: "Artifacts, 3" }));
    const panel = await screen.findByRole("region", { name: "Artifacts" });
    expect(window.location.search).toContain("panel=artifacts");

    const nav = within(panel).getByRole("navigation", { name: "Artifacts by kind" });
    const groups = within(nav).getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(groups).toEqual(["Skills 1", "Subagents 1", "Templates 1"]);
    // Nothing selected: the other side sums the KB's artifacts up.
    expect(within(panel).getByRole("region", { name: "Artifacts overview" })).toBeInTheDocument();

    // One kind at a time, without scrolling past the others; pressed again, all.
    const kinds = within(nav).getByRole("list", { name: "Show kind" });
    await user.click(within(kinds).getByRole("button", { name: /^Subagents/ }));
    expect(within(nav).getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(["Subagents 1"]);
    await user.click(within(kinds).getByRole("button", { name: /^Subagents/ }));
    expect(within(nav).getAllByRole("heading", { level: 2 })).toHaveLength(3);

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
    expect(within(item).getByText("warning")).toBeInTheDocument();
    expect(within(panel).getByRole("button", { name: /triage/ }).querySelector(".severity")).toBeNull();
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

  it("opens the artifact a shared link names", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=artifacts&artifact=skill%2Freview");
    stubApi();
    render(<App />);
    expect(await screen.findByRole("article", { name: "Artifact skill/review" })).toBeInTheDocument();
  });
});
