import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { componentRow, componentsAnswer, json, stubApi } from "./fixtures";

/** The Components view (D1901): what is deployed for the KB, read-only. */
describe("the Components panel", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=components");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  async function panel() {
    return screen.findByRole("region", { name: "Components" });
  }

  it("says so when nothing is deployed or expected, and points to the docs", async () => {
    stubApi({ "/components": () => json(componentsAnswer()) });
    render(<App />);
    const page = await panel();
    await waitFor(() => expect(page).toHaveTextContent("No Surveyor is deployed or expected for this KB."));
    expect(within(page).getByRole("link", { name: "Docs" })).toHaveAttribute("href", expect.stringContaining("docs/deployment.md#"));
    // The server row is there, the Atlas is not a row.
    expect(within(page).getByRole("heading", { name: "Meridian" })).toBeInTheDocument();
    expect(page).not.toHaveTextContent(/Atlas/);
  });

  it("shows the empty sentence when the read fails", async () => {
    stubApi({ "/components": () => new Response("no", { status: 404 }) });
    render(<App />);
    const page = await panel();
    await waitFor(() => expect(page).toHaveTextContent("No Surveyor is deployed or expected for this KB."));
  });

  it("lists a live Surveyor with its replica, budgets, runs and an alarm without a KB", async () => {
    const row = componentRow(
      { declared: true, min_replicas: 2 },
      [
        {
          id: "w-1",
          version: "0.29.0",
          tasks: [{ kind: "repair", interval_seconds: 60 }],
          budgets: [{ profile: "small", spent: 3, limit: 10 }],
          next_window_at: "2026-02-03T00:00:00Z",
        },
      ],
    );
    row.runs = [
      { run_id: "r1", replica: "w-1", task: "repair", kb: "kb-a", started_at: new Date().toISOString(), duration_ms: 4000, outcome: "ok", tokens_spent: 1, received_at: new Date().toISOString() },
    ] as never;
    row.alarms = [{ code: "model_unreachable", source: "", detail: "", origin: "reported", received_at: new Date().toISOString() }] as never;
    stubApi({ "/components": () => json(componentsAnswer(row)) });
    render(<App />);
    const page = await panel();
    const block = await within(page).findByRole("region", { name: "team-a surveyor" });
    expect(block).toHaveTextContent("1 of 2 replicas");
    expect(block).toHaveTextContent("expected");
    expect(block).toHaveTextContent("small: 3 / 10");
    expect(block).toHaveTextContent("tasks repair");
    expect(block).toHaveTextContent("repair ok");
    // An alarm with no source and no detail renders its code alone: no stray separator.
    const alarm = within(block).getByRole("list", { name: "Alarms" }).querySelector("li")!;
    expect(alarm.textContent).toMatch(/^model unreachable · /);
    expect(alarm.textContent).not.toContain(": ");
  });

  it("ages runs and alarms from the server's stamp, not the Surveyor's clock", async () => {
    const row = componentRow({}, [{}]);
    const old = "2020-01-01T00:00:00Z";
    row.runs = [
      { run_id: "r1", replica: "w-1", task: "repair", kb: "kb-a", started_at: old, duration_ms: 1000, outcome: "ok", tokens_spent: 1, received_at: new Date().toISOString() },
    ] as never;
    row.alarms = [{ code: "feed_failing", origin: "reported", since: old, received_at: new Date().toISOString() }] as never;
    stubApi({ "/components": () => json(componentsAnswer(row)) });
    render(<App />);
    const block = await within(await panel()).findByRole("region", { name: "team-a surveyor" });
    expect(block).toHaveTextContent("repair ok · just now");
    expect(within(block).getByRole("list", { name: "Alarms" })).toHaveTextContent("feed failing · just now");
  });

  it("drops the previous KB's rows when the KB changes", async () => {
    let call = 0;
    stubApi({
      "/components": () => {
        call++;
        return call === 1 ? json(componentsAnswer(componentRow({ name: "only-a" }, [{}]))) : new Promise<Response>(() => {}) as never;
      },
    });
    const { Components } = await import("../components/Components");
    const { rerender } = render(<Components kb="kb-a" />);
    expect(await screen.findByRole("region", { name: "only-a surveyor" })).toBeInTheDocument();
    rerender(<Components kb="kb-b" />);
    await waitFor(() => expect(screen.queryByRole("region", { name: "only-a surveyor" })).toBeNull());
    expect(screen.getByRole("region", { name: "Components" })).toHaveAttribute("aria-busy", "true");
  });

  it("shows a silent expected Surveyor with no replicas", async () => {
    stubApi({ "/components": () => json(componentsAnswer(componentRow({ declared: true, state: "silent" }))) });
    render(<App />);
    const block = await within(await panel()).findByRole("region", { name: "team-a surveyor" });
    expect(block).toHaveTextContent("silent");
    expect(block).toHaveTextContent("0 of 1 replica");
  });

  it("says the skew of a replica in words", async () => {
    stubApi({
      "/components": () =>
        json(componentsAnswer(componentRow({}, [{ id: "w-1", protocol_skew: "behind" }, { id: "w-2", protocol: 2, protocol_skew: "ahead" }]))),
    });
    render(<App />);
    const block = await within(await panel()).findByRole("region", { name: "team-a surveyor" });
    expect(block).toHaveTextContent("one behind");
    expect(block).toHaveTextContent("one ahead, the server speaks protocol 1");
  });

  it("marks an undeclared component", async () => {
    stubApi({ "/components": () => json(componentsAnswer(componentRow({ declared: false, state: "undeclared" }, [{}]))) });
    render(<App />);
    const block = await within(await panel()).findByRole("region", { name: "team-a surveyor" });
    expect(block).toHaveTextContent("undeclared");
  });

  it("is reachable from the rail by keyboard", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=health");
    stubApi({ "/components": () => json(componentsAnswer()) });
    const user = userEvent.setup();
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    const entry = await within(rail).findByRole("button", { name: "Components" });
    entry.focus();
    await user.keyboard("{Enter}");
    expect(window.location.search).toContain("panel=components");
    expect(await panel()).toBeInTheDocument();
  });
});
