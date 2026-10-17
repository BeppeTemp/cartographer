import { render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { json, stubApi } from "./fixtures";

/** Health (D338, D704) through the App: what was found, what is checked, and nothing that writes. */
describe("the Health panel", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=health");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("lists the review's proposals beside the lint issues", async () => {
    stubApi({
      "/maintenance/review": () =>
        json({
          items: [{ kind: "duplicate_candidate", concepts: ["infra/a", "infra/b"], evidence: "two titles", suggested_action: "", weight: 1 }],
        }),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    expect(await within(panel).findByText("Possible duplicates")).toBeInTheDocument();
  });

  it("lands the old Maintenance and Observatory links on Health", async () => {
    stubApi();
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=maintenance");
    render(<App />);
    expect(await screen.findByRole("region", { name: "Health" })).toBeInTheDocument();
  });

  it("only ever reads", async () => {
    stubApi();
    render(<App />);
    await screen.findByRole("region", { name: "Health" });
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    for (const call of fetchMock.mock.calls) {
      const init = call[1] as RequestInit | undefined;
      expect(init?.method ?? "GET").toBe("GET");
    }
  });

  it("lists the open contradictions and says they block commit_gate", async () => {
    stubApi({
      "/contradictions": () =>
        json({
          contradictions: [
            {
              id: "notes/clash",
              title: "Ports disagree",
              involves: ["infra/a", "infra/b"],
              contradiction_kind: "factual",
              resolution_status: "open",
            },
          ],
        }),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    expect(await within(panel).findByText("Ports disagree")).toBeInTheDocument();
    expect(within(panel).getByRole("button", { name: "infra/b" })).toBeInTheDocument();
    expect(within(panel).getByText(/blocks/)).toBeInTheDocument();
  });

  it("says in one line what contradictions are when none are open", async () => {
    stubApi();
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    expect(await within(panel).findByText(/No open contradictions/)).toBeInTheDocument();
  });
});
