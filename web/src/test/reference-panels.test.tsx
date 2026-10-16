import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { json, stubApi } from "./fixtures";

function open(panel: string) {
  window.history.replaceState(null, "", `/ui/?kb=kb-a&panel=${panel}`);
  localStorage.clear();
  localStorage.setItem("cartographer.panel.rail", "0");
  sessionStorage.clear();
}

describe("the reference panels", () => {
  afterEach(() => vi.unstubAllGlobals());

  describe("Paths", () => {
    beforeEach(() => open("paths"));

    it("explains itself when the KB has no paths.yaml", async () => {
      stubApi();
      render(<App />);
      expect(await screen.findByText("This KB has no paths.yaml")).toBeInTheDocument();
      expect(screen.getByText(/cartographer paths set/)).toBeInTheDocument();
    });

    it("flags undeclared, uncited and conflicting keys and links to the citing concept and artifact", async () => {
      stubApi({
        "/placeholders": () =>
          json({
            registry: true,
            keys: [
              {
                kind: "repo",
                key: "tool-a",
                description: "The tool repository",
                default: "$HOME/src/tool-a",
                remote: "example.com/org/tool-a",
                declared: true,
                concepts: ["infra/a"],
                artifacts: ["skill/review"],
                conflicts: [{ kb: "work-kb", default: "$HOME/other/tool-a" }],
              },
              { kind: "path", key: "ghost", declared: false, concepts: ["notes/c"], artifacts: [] },
              { kind: "path", key: "idle", description: "Cited nowhere", declared: true, concepts: [], artifacts: [] },
            ],
          }),
      });
      const user = userEvent.setup();
      render(<App />);
      const panel = await screen.findByRole("region", { name: "Paths" });
      expect(within(panel).getByText("cited but undeclared")).toBeInTheDocument();
      expect(within(panel).getByText("declared, cited nowhere")).toBeInTheDocument();
      expect(within(panel).getByText("default differs in another KB")).toBeInTheDocument();
      expect(within(panel).getByText("$HOME/src/tool-a")).toBeInTheDocument();
      expect(within(panel).getByText("example.com/org/tool-a")).toBeInTheDocument();
      await user.click(within(panel).getByRole("button", { name: "skill/review" }));
      await waitFor(() => expect(window.location.search).toContain("artifact=skill%2Freview"));
      expect(window.location.search).toContain("panel=artifacts");
    });
  });

  it("lists the pending sources as work to ingest, where an old Sources link lands", async () => {
    open("sources");
    stubApi({
      "/work": () => json({ total: 0, open_concepts: 0, by_status: {}, by_map: {}, open_items: 0, entries: [] }),
      "/sources": () =>
        json({
          sources: [
            { id: "sources/memo", title: "New memo", ingest_status: "pending", timestamp: "2026-09-02T00:00:00Z", cited_by: 0, cited_by_ids: [] },
          ],
        }),
    });
    render(<App />);
    const column = await screen.findByRole("region", { name: "to ingest, 1" });
    expect(within(column).getByText("New memo")).toBeInTheDocument();
  });

  it("offers the reference panels in the rail", async () => {
    open("health");
    stubApi();
    render(<App />);
    expect(await screen.findByRole("button", { name: "Paths" })).toBeInTheDocument();
    for (const name of ["Sources", "Services", "Secrets"]) {
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    }
  });

  it("shows the KB journal in Activity, rendered, and explains an empty one", async () => {
    open("activity");
    stubApi({
      "/changes": () => json({ since: "x", concepts: [], truncated: false }),
      "/journal": () => json({ text: "## 2026-10-01 closing\n\nMerged the **plan**.\n" }),
    });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Journal" })).toBeInTheDocument();
    expect(await screen.findByText("plan")).toBeInTheDocument();
  });

  it("says how the journal fills when it is empty", async () => {
    open("activity");
    stubApi({ "/changes": () => json({ since: "x", concepts: [], truncated: false }) });
    render(<App />);
    expect(await screen.findByText(/log_append/)).toBeInTheDocument();
  });
});
