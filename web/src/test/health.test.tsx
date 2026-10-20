import { render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MaintenanceSummary } from "../api/types";
import { json, stubApi } from "./fixtures";

const SHA = "8baf4f51d431b9183afb238a0075d139257cadb7";

const summary: MaintenanceSummary = {
  auto_repair: {
    enabled: true,
    default: true,
    checks: ["nonstandard_field", "duplicate_link"],
    interval_days: 1,
  },
  last_auto_repair: {
    at: "2026-10-05T08:00:00Z",
    checks: [
      { check: "nonstandard_field", applied: 3, commit: SHA },
      { check: "duplicate_link", applied: 0 },
    ],
  },
  last_doctor: "2026-09-20",
  next_doctor: "2026-10-04",
  doctor_interval_days: 14,
  repairs: [
    {
      sha: SHA,
      at: "2026-10-05T08:00:03Z",
      subject: "kb_repair: nonstandard_field (3 concepts)",
      reason: "auto-repair (background)",
      files: 3,
      background: true,
      revert: `cartographer kb repair kb-a --revert ${SHA.slice(0, 7)}`,
    },
  ],
};

/** Health (D338, D704) through the App: what was found, what is checked, and nothing that writes. */
describe("the Health panel", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=health");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  // Health shows lint problems only (D947): the doctor's proposals and the
  // observations are decisions, listed elsewhere, and Health does not even ask.
  it("does not list the review's proposals beside the lint problems", async () => {
    stubApi({
      "/maintenance/summary": () => json(summary),
      "/maintenance/review": () =>
        json({
          items: [{ kind: "duplicate_candidate", concepts: ["infra/a", "infra/b"], evidence: "two titles", suggested_action: "", weight: 1 }],
        }),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    expect(await within(panel).findByText(/No open contradictions/)).toBeInTheDocument();
    expect(within(panel).queryByText("Possible duplicates")).not.toBeInTheDocument();
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    expect(fetchMock.mock.calls.map((c) => String(c[0])).filter((u) => u.includes("/maintenance/review"))).toEqual([]);
  });

  it("lands the old Maintenance and Observatory links on Health", async () => {
    stubApi({ "/maintenance/summary": () => json(summary) });
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=maintenance");
    render(<App />);
    expect(await screen.findByRole("region", { name: "Health" })).toBeInTheDocument();
  });

  it("only ever reads", async () => {
    stubApi({ "/maintenance/summary": () => json(summary) });
    render(<App />);
    await screen.findByRole("region", { name: "Health" });
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    for (const call of fetchMock.mock.calls) {
      const init = call[1] as RequestInit | undefined;
      expect(init?.method ?? "GET").toBe("GET");
    }
  });

  it("shows the page when the principal may not read the summary", async () => {
    stubApi({ "/maintenance/summary": () => json({ error: { code: "not_found", message: "not found" } }, 404) });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    expect(within(panel).getByRole("heading", { level: 1 })).toBeInTheDocument();
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
