import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
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

  it("lists the KB's placeholders in Settings, where an old Paths link lands", async () => {
    open("paths");
    stubApi({
      "/settings": () =>
        json({ version: "dev", server: [], kbs: [{ name: "kb-a", entries: [], yaml: "" }] }),
      "/placeholders": () =>
        json({
          registry: true,
          keys: [
            {
              kind: "repo",
              key: "tool-a",
              description: "The tool repository",
              default: "$HOME/src/tool-a",
              declared: true,
              concepts: ["infra/a"],
              artifacts: ["skill/review"],
              conflicts: [{ kb: "work-kb", default: "$HOME/other/tool-a" }],
            },
            { kind: "path", key: "ghost", declared: false, concepts: ["notes/c"], artifacts: [] },
          ],
        }),
    });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: /Knowledge Base/ }));
    const list = await screen.findByRole("list", { name: "Placeholders of kb-a" });
    expect(within(list).getByText("{{repo:tool-a}}")).toBeInTheDocument();
    expect(within(list).getByText(/cited 2 times · work-kb says \$HOME\/other\/tool-a/)).toBeInTheDocument();
    expect(within(list).getByText("not declared")).toBeInTheDocument();
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
    expect(await screen.findByRole("button", { name: "Settings" })).toBeInTheDocument();
    for (const name of ["Sources", "Services", "Secrets", "Paths"]) {
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    }
  });

  it("shows the KB journal in Activity, as rows, and explains an empty one", async () => {
    open("activity");
    stubApi({
      "/changes": () => json({ since: "x", concepts: [], truncated: false }),
      "/journal": () =>
        json({
          text:
            "## 2026-10-01T09:00:00Z\n\nMerged the **plan**.\nSecond line.\n\n## 2026-10-02T10:30:00Z\n\nOne line.\n",
        }),
    });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Journal" })).toBeInTheDocument();
    // One row per entry on the timeline's rail; the note is plain text, its first line the lead.
    await waitFor(() => expect(document.querySelectorAll(".journal__entry")).toHaveLength(2));
    const rows = [...document.querySelectorAll(".journal__entry")];
    expect(rows[0]!.textContent).toContain("Merged the **plan**.");
    expect(rows[0]!.textContent).toContain("Second line.");
    expect(rows[1]!.textContent).toContain("One line.");
    expect(rows[0]!.querySelector("time")).toHaveAttribute("datetime", "2026-10-01T09:00:00Z");
  });

  it("says how the journal fills when it is empty", async () => {
    open("activity");
    stubApi({ "/changes": () => json({ since: "x", concepts: [], truncated: false }) });
    render(<App />);
    expect(await screen.findByText(/log_append/)).toBeInTheDocument();
  });
});
