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
    window.history.replaceState(null, "", "/ui/?kb=homelab&panel=artifacts");
    const fetchMock = stubApi();
    const route = fetchMock.getMockImplementation()!;
    const requested: string[] = [];
    fetchMock.mockImplementation(async (url: string) => {
      requested.push(url);
      if (url.endsWith("/kbs")) {
        return json({ kbs: [{ name: "homelab", status: "normal", ready: true, artifacts: false }] });
      }
      return route(url);
    });
    render(<App />);
    expect(await screen.findByRole("button", { name: "Observatory" })).toBeInTheDocument();
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

    const groups = within(panel).getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(groups).toEqual(["Skills1", "Subagents1", "Templates1"]);

    await user.type(within(panel).getByRole("searchbox", { name: "Filter artifacts" }), "sorts");
    expect(within(panel).queryByRole("button", { name: /review/ })).not.toBeInTheDocument();
    expect(within(panel).getByRole("button", { name: /triage/ })).toBeInTheDocument();
    await user.clear(within(panel).getByRole("searchbox", { name: "Filter artifacts" }));

    await user.click(within(panel).getByRole("button", { name: /review/ }));
    await waitFor(() => expect(window.location.search).toContain("artifact=skill%2Freview"));
    const detail = await screen.findByRole("article", { name: "Artifact skill/review" });
    expect(within(detail).getByText("Signed")).toBeInTheDocument();
    expect(within(detail).getByText("Codex CLI")).toBeInTheDocument();
    // Frontmatter as rows, the body as Markdown.
    expect(within(detail).getByRole("rowheader", { name: "description" })).toBeInTheDocument();
    expect(within(detail).getByRole("heading", { name: "Review steps" })).toBeInTheDocument();

    await user.click(within(detail).getByRole("tab", { name: "logo.bin" }));
    expect(within(detail).getByText("Binary file — not shown.")).toBeInTheDocument();

    window.history.back();
    await waitFor(() => expect(window.location.search).not.toContain("artifact="));
    expect(screen.queryByRole("article", { name: /Artifact/ })).not.toBeInTheDocument();
  });

  it("opens the artifact a shared link names", async () => {
    window.history.replaceState(null, "", "/ui/?kb=homelab&panel=artifacts&artifact=skill%2Freview");
    stubApi();
    render(<App />);
    expect(await screen.findByRole("article", { name: "Artifact skill/review" })).toBeInTheDocument();
  });
});
