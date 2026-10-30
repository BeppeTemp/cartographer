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

  // Health shows lint problems only (D947): the doctor's proposals and the
  // observations are decisions, listed elsewhere, and Health does not even ask.
  it("does not list the review's proposals beside the lint problems", async () => {
    stubApi({
      "/maintenance/review": () =>
        json({
          items: [{ kind: "duplicate_candidate", concepts: ["infra/a", "infra/b"], evidence: "two titles", suggested_action: "", weight: 1 }],
        }),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    await within(panel).findByRole("heading", { level: 1 });
    expect(within(panel).queryByText("Possible duplicates")).not.toBeInTheDocument();
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    expect(fetchMock.mock.calls.map((c) => String(c[0])).filter((u) => u.includes("/maintenance/review"))).toEqual([]);
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

  const worker = (over: object = {}) => ({
    worker: "w-1",
    perimeter: "team-a",
    state: "idle",
    last_beat_at: new Date().toISOString(),
    interval_seconds: 60,
    budgets: [],
    ...over,
  });

  it("says which worker maintains the KB, with its doctor dates, only when a live worker names it", async () => {
    stubApi({
      "/workers": () => json({ workers: [worker(), worker({ worker: "w-2", state: "running" })] }),
      "/maintenance/summary": () => json({ last_doctor: "2026-01-01", next_doctor: "2026-01-15" }),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    const line = await within(panel).findByText(/Maintained by/);
    expect(line).toHaveTextContent("Maintained by team-a (2 replicas, running) · last doctor 2026-01-01 · next due 2026-01-15");
  });

  it("shows no maintainer line without a worker, or when the route fails", async () => {
    stubApi();
    const { unmount } = render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    await within(panel).findByRole("heading", { level: 1 });
    expect(within(panel).queryByText(/Maintained by/)).not.toBeInTheDocument();
    unmount();
    stubApi({ "/workers": () => json({ error: { code: "not_found", message: "not found" } }, 404) });
    render(<App />);
    const again = await screen.findByRole("region", { name: "Health" });
    await within(again).findByRole("heading", { level: 1 });
    expect(within(again).queryByText(/Maintained by/)).not.toBeInTheDocument();
  });
});
