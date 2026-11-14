import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { componentRow, componentsAnswer, json, stubApi } from "./fixtures";

/** The smallest settings view that opens Settings: the page needs it before any tab. */
const SETTINGS = {
  version: "v1.2.3",
  server: [],
  kbs: [{ name: "kb-a", entries: [], yaml: "" }],
  server_yaml: "",
  clients: [{ machine: "laptop", version: "v1.2.3", received_at: new Date().toISOString(), entries: [], yaml: "" }],
};

/** The components (D1901), folded into Settings: each tab shows its component's live status, read-only. */
describe("component status in Settings", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=components");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  // An old ?panel=components link lands on Settings; the Surveyor tab holds its block.
  async function panel() {
    const settings = await screen.findByRole("region", { name: "Settings" });
    await userEvent.setup().click(await within(settings).findByRole("tab", { name: /^Surveyor/ }));
    return within(settings).findByRole("region", { name: "Surveyor status" });
  }

  it("says so when nothing is deployed or expected, and points to the docs", async () => {
    stubApi({ "/settings": () => json(SETTINGS), "/components": () => json(componentsAnswer()) });
    render(<App />);
    const page = await panel();
    await waitFor(() => expect(page).toHaveTextContent("None for this KB"));
    expect(within(page).getByRole("link", { name: "Docs" })).toHaveAttribute("href", expect.stringContaining("docs/deployment.md#"));
    expect(page).not.toHaveTextContent(/Atlas/);
  });

  it("is absent when the read fails (an older server)", async () => {
    stubApi({ "/settings": () => json(SETTINGS), "/components": () => new Response("no", { status: 404 }) });
    render(<App />);
    const settings = await screen.findByRole("region", { name: "Settings" });
    const user = userEvent.setup();
    for (const tab of ["Meridian", "Surveyor"]) {
      await user.click(await within(settings).findByRole("tab", { name: new RegExp(`^${tab}`) }));
      await new Promise((r) => setTimeout(r, 30));
      expect(within(settings).queryByRole("region", { name: `${tab} status` })).toBeNull();
    }
    // The configuration stays.
    expect(within(settings).getByRole("tabpanel")).toBeInTheDocument();
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
    stubApi({ "/settings": () => json(SETTINGS), "/components": () => json(componentsAnswer(row)) });
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
    stubApi({ "/settings": () => json(SETTINGS), "/components": () => json(componentsAnswer(row)) });
    render(<App />);
    const block = await within(await panel()).findByRole("region", { name: "team-a surveyor" });
    expect(block).toHaveTextContent("repair ok · just now");
    expect(within(block).getByRole("list", { name: "Alarms" })).toHaveTextContent("feed failing · just now");
  });

  it("drops the previous KB's rows when the KB changes", async () => {
    let call = 0;
    stubApi({
      "/settings": () => json(SETTINGS),
      "/components": () => {
        call++;
        return call === 1 ? json(componentsAnswer(componentRow({ name: "only-a" }, [{}]))) : new Promise<Response>(() => {}) as never;
      },
    });
    const { SurveyorStatus } = await import("../components/Components");
    const { rerender } = render(<SurveyorStatus kb="kb-a" />);
    expect(await screen.findByRole("region", { name: "only-a surveyor" })).toBeInTheDocument();
    rerender(<SurveyorStatus kb="kb-b" />);
    await waitFor(() => expect(screen.queryByRole("region", { name: "only-a surveyor" })).toBeNull());
  });

  it("shows a silent expected Surveyor with no replicas", async () => {
    stubApi({ "/settings": () => json(SETTINGS), "/components": () => json(componentsAnswer(componentRow({ declared: true, state: "silent" }))) });
    render(<App />);
    const block = await within(await panel()).findByRole("region", { name: "team-a surveyor" });
    expect(block).toHaveTextContent("silent");
    expect(block).toHaveTextContent("0 of 1 replica");
  });

  it("says the skew of a replica in words", async () => {
    stubApi({
      "/settings": () => json(SETTINGS),
      "/components": () =>
        json(componentsAnswer(componentRow({}, [{ id: "w-1", protocol_skew: "behind" }, { id: "w-2", protocol: 2, protocol_skew: "ahead" }]))),
    });
    render(<App />);
    const block = await within(await panel()).findByRole("region", { name: "team-a surveyor" });
    expect(block).toHaveTextContent("one behind");
    expect(block).toHaveTextContent("one ahead, the server speaks protocol 1");
  });

  it("marks an undeclared component", async () => {
    stubApi({ "/settings": () => json(SETTINGS), "/components": () => json(componentsAnswer(componentRow({ declared: false, state: "undeclared" }, [{}]))) });
    render(<App />);
    const block = await within(await panel()).findByRole("region", { name: "team-a surveyor" });
    expect(block).toHaveTextContent("undeclared");
  });

  it("shows Meridian's and Compass's status on their tabs", async () => {
    stubApi({ "/settings": () => json(SETTINGS), "/components": () => json(componentsAnswer()) });
    render(<App />);
    const settings = await screen.findByRole("region", { name: "Settings" });
    const user = userEvent.setup();
    await user.click(await within(settings).findByRole("tab", { name: /^Meridian/ }));
    expect(await within(settings).findByRole("region", { name: "Meridian status" })).toHaveTextContent(/Running .* · up /);
    await user.click(within(settings).getByRole("tab", { name: /^Compass/ }));
    expect(await within(settings).findByRole("region", { name: "Compass status" })).toBeInTheDocument();
  });

  it("has no rail entry: an old Components link opens Settings", async () => {
    stubApi({ "/settings": () => json(SETTINGS), "/components": () => json(componentsAnswer()) });
    render(<App />);
    const rail = await screen.findByRole("navigation", { name: "Atlas navigation" });
    expect(within(rail).queryByRole("button", { name: "Components" })).toBeNull();
    expect(await screen.findByRole("region", { name: "Settings" })).toBeInTheDocument();
  });
});
