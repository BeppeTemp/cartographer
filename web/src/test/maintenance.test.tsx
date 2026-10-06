import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { MaintenanceQuestions, MaintenanceSummary } from "../api/types";
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

const questions: MaintenanceQuestions = {
  questions: [
    { id: "ops/q1", title: "Which port does the proxy listen on?", involves: ["infra/a"], contradiction_kind: "open_question", resolution_status: "open" },
  ],
};

/** The Maintenance panel (D323): what the doctor did and what it asks. Read-only. */
describe("the Maintenance panel", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=maintenance");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("shows the summary, the questions and the repairs with their revert command", async () => {
    stubApi({
      "/maintenance/summary": () => json(summary),
      "/maintenance/questions": () => json(questions),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Maintenance" });
    expect(await within(panel).findByText(/on, every day: nonstandard_field, duplicate_link \(the default list\)/)).toBeInTheDocument();
    expect(within(panel).getByText(/2026-10-05 08:00 UTC: 3 concepts repaired/)).toBeInTheDocument();
    expect(within(panel).getByText("2026-09-20")).toBeInTheDocument();
    expect(within(panel).getByText("Which port does the proxy listen on?")).toBeInTheDocument();
    expect(within(panel).getByText("kb_repair: nonstandard_field (3 concepts)")).toBeInTheDocument();
    expect(within(panel).getByText(`cartographer kb repair kb-a --revert ${SHA.slice(0, 7)}`)).toBeInTheDocument();
  });

  it("copies an ID and a revert command, and has no button that writes", async () => {
    stubApi({
      "/maintenance/summary": () => json(summary),
      "/maintenance/questions": () => json(questions),
    });
    // setup() installs the clipboard stub the spy wraps.
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText");
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Maintenance" });
    await user.click(await within(panel).findByRole("button", { name: /^Copy ID of/ }));
    expect(writeText).toHaveBeenLastCalledWith("ops/q1");
    await user.click(within(panel).getByRole("button", { name: `Copy revert command for ${SHA.slice(0, 7)}` }));
    expect(writeText).toHaveBeenLastCalledWith(`cartographer kb repair kb-a --revert ${SHA.slice(0, 7)}`);
    expect(await within(panel).findByText("Copied to the clipboard.")).toBeInTheDocument();
    // Every request the panel made was a read.
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    for (const call of fetchMock.mock.calls) {
      const init = call[1] as RequestInit | undefined;
      expect(init?.method ?? "GET").toBe("GET");
    }
  });

  it("opens the question's concept on the atlas", async () => {
    stubApi({
      "/maintenance/summary": () => json(summary),
      "/maintenance/questions": () => json(questions),
    });
    const user = userEvent.setup();
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Maintenance" });
    await user.click(await within(panel).findByText("Which port does the proxy listen on?"));
    expect(window.location.search).toContain("concept=ops%2Fq1");
    expect(window.location.search).not.toContain("panel=maintenance");
  });

  it("says plainly when nothing waits and nothing was repaired", async () => {
    stubApi({
      "/maintenance/summary": () =>
        json({ ...summary, auto_repair: { enabled: false, default: false, checks: [], interval_days: 1 }, last_auto_repair: null, last_doctor: undefined, next_doctor: undefined, repairs: [] }),
      "/maintenance/questions": () => json({ questions: [] }),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Maintenance" });
    expect(await within(panel).findByText("No open question: the doctor has nothing waiting on you.")).toBeInTheDocument();
    expect(within(panel).getByText("No repair commit in the last 30 days.")).toBeInTheDocument();
    expect(within(panel).getByText("off: auto_repair is explicitly empty")).toBeInTheDocument();
    expect(within(panel).getByText("never")).toBeInTheDocument();
  });

  it("reports a summary the principal may not read without hiding the questions", async () => {
    stubApi({
      "/maintenance/summary": () => json({ error: { code: "not_found", message: "not found" } }, 404),
      "/maintenance/questions": () => json(questions),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Maintenance" });
    expect(await within(panel).findByText(/Could not read the maintenance summary/)).toBeInTheDocument();
    expect(await within(panel).findByText("Which port does the proxy listen on?")).toBeInTheDocument();
  });
});
