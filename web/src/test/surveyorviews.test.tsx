import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { componentRow, componentsAnswer, json, stubApi } from "./fixtures";

const SETTINGS = {
  version: "v1.2.3",
  server: [{ key: "git.sync", value: true, origin: "default", description: "Sync.", default: "true" }],
  kbs: [{ name: "kb-a", entries: [], yaml: "" }],
  server_yaml: "",
};
const work = { total: 0, open_concepts: 0, by_status: {}, by_map: {}, open_items: 0, entries: [] };
const run = {
  run_id: "r1",
  replica: "w-1",
  task: "doctor",
  kb: "kb-a",
  started_at: "2026-01-01T00:00:00Z",
  duration_ms: 10,
  outcome: "ok",
  tokens_spent: 42,
  received_at: new Date().toISOString(),
};

function open(panel: string) {
  window.history.replaceState(null, "", `/ui/?kb=kb-a&panel=${panel}`);
  localStorage.clear();
  localStorage.setItem("cartographer.panel.rail", "0");
  sessionStorage.clear();
}

afterEach(() => vi.unstubAllGlobals());

async function surveyorTab() {
  const user = userEvent.setup();
  const group = await screen.findByRole("group", { name: "Activity of" });
  await user.click(within(group).getByRole("button", { name: "Surveyor" }));
  return user;
}

describe("Activity: Surveyor tab", () => {
  beforeEach(() => open("activity"));

  it("has no tabs with no run and no change of the Surveyor's", async () => {
    stubApi({ "/components": () => json(componentsAnswer(componentRow({}, [{}]))) });
    render(<App />);
    await screen.findByRole("region", { name: /activity/i });
    expect(screen.queryByRole("group", { name: "Activity of" })).not.toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Surveyor runs" })).not.toBeInTheDocument();
  });

  it("has no tabs when the components read fails", async () => {
    stubApi({ "/components": () => json({ error: { code: "not_found", message: "no" } }, 404) });
    render(<App />);
    await screen.findByRole("region", { name: /activity/i });
    expect(screen.queryByRole("group", { name: "Activity of" })).not.toBeInTheDocument();
  });

  it("shows the fetch error on the Surveyor tab, not zero tiles", async () => {
    stubApi({
      "/changes": () => json({ error: { code: "internal", message: "boom" } }, 500),
      "/components": () => json(componentsAnswer(componentRow({ runs: [run] }, [{}]))),
    });
    render(<App />);
    await surveyorTab();
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "The Surveyor in the window" })).not.toBeInTheDocument();
    expect(screen.queryByText(/changed no page/)).not.toBeInTheDocument();
  });

  it("shows the skeleton on the Surveyor tab while the changes are pending", async () => {
    stubApi({
      "/changes": () => new Promise<Response>(() => {}) as never,
      "/components": () => json(componentsAnswer(componentRow({ runs: [run] }, [{}]))),
    });
    render(<App />);
    await surveyorTab();
    expect(screen.getByText("Reading the Surveyor's work")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "The Surveyor in the window" })).not.toBeInTheDocument();
    expect(screen.queryByText(/changed no page/)).not.toBeInTheDocument();
  });

  it("has no tabs when only another KB has runs", async () => {
    stubApi({ "/components": () => json(componentsAnswer(componentRow({ runs: [{ ...run, kb: "kb-b" }] }, [{}]))) });
    render(<App />);
    await screen.findByRole("region", { name: /activity/i });
    expect(screen.queryByRole("group", { name: "Activity of" })).not.toBeInTheDocument();
  });

  it("lists only this KB's runs, on the Surveyor tab", async () => {
    stubApi({ "/changes": () => json({ concepts: [] }), "/components": () => json(componentsAnswer(componentRow({ runs: [{ ...run, started_at: new Date().toISOString() }, { ...run, run_id: "r2", kb: "kb-b", started_at: new Date().toISOString() }] }, [{}]))) });
    render(<App />);
    await surveyorTab();
    const list = await screen.findByRole("list", { name: "Surveyor runs" });
    expect(within(list).getAllByText(/\d+ tokens/)).toHaveLength(2); // the day row and the run
  });
});

describe("Activity: the Surveyor's report", () => {
  beforeEach(() => open("activity"));
  const at = Date.now() - 3600_000;
  const iso = (ms: number) => new Date(ms).toISOString();
  const recent = { ...run, run_id: "r9", task: "ingest", started_at: iso(at), duration_ms: 60_000, received_at: iso(at + 61_000), tokens_spent: 700 };
  const failed = { ...run, run_id: "r8", outcome: "failed", error: "feed down", started_at: iso(at - 7200_000), received_at: iso(at - 7100_000), tokens_spent: 0 };
  const changes = {
    since: "7d",
    commit_count: 3,
    concepts: [
      { id: "a/made", change: "added", last_at: iso(at + 30_000), last_edit_at: iso(at + 30_000), authors: ["surveyor/team-a/w-1"], ops: ["concept_write"] },
      { id: "a/before", change: "modified", last_at: iso(at - 600_000), last_edit_at: iso(at - 600_000), authors: ["surveyor/team-a/w-1"], ops: ["concept_write"] },
      { id: "a/human", change: "modified", last_at: iso(at + 30_000), last_edit_at: iso(at + 30_000), authors: ["bob"], ops: ["concept_write"] },
    ],
  };

  it("keeps the Changes tab as before: the tag stays, the Surveyor's stats and switch are gone", async () => {
    stubApi({ "/changes": () => json(changes), "/components": () => json(componentsAnswer(componentRow({ runs: [recent, failed] }, [{}]))) });
    render(<App />);
    await screen.findByRole("group", { name: "Activity of" });
    expect(await screen.findAllByText("Surveyor", { selector: ".timeline__surveyor" })).toHaveLength(2);
    expect(screen.queryByRole("list", { name: "The Surveyor in the window" })).not.toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Changes made by" })).not.toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Surveyor runs" })).not.toBeInTheDocument();
  });

  it("details a run on the Surveyor tab, with tiles and its own changes only", async () => {
    stubApi({ "/changes": () => json(changes), "/components": () => json(componentsAnswer(componentRow({ runs: [recent, failed] }, [{}]))) });
    render(<App />);
    await surveyorTab();
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(/ran 2 times/);
    const list = await screen.findByRole("list", { name: "Surveyor runs" });
    const row = within(list).getAllByRole("listitem").find((li) => li.classList.contains("surveyors__row"))!;
    expect(within(row).getByText("700 tokens")).toBeInTheDocument();
    expect(within(row).getByText("60 s")).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: /a\/made/ })).toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: /a\/before/ })).not.toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: /a\/human/ })).not.toBeInTheDocument();

    const tiles = screen.getByRole("list", { name: "The Surveyor in the window" });
    expect(within(tiles).getByText("sources ingested").closest("li")).toHaveTextContent("1");
    expect(within(tiles).getByText("failed").closest("li")).toHaveTextContent("1");
    expect(within(tiles).getByText("pages created").closest("li")).toHaveTextContent("1");
    expect(within(tiles).getByText("tokens spent").closest("li")).toHaveTextContent("700");
    // The feed holds the Surveyor's changes, not bob's.
    expect(screen.queryByText("a/human")).not.toBeInTheDocument();
    expect(screen.getAllByText("a/before").length).toBeGreaterThan(0);
  });
});

describe("Activity: the Surveyor's runs, honestly", () => {
  beforeEach(() => open("activity"));
  const DAY = 86_400_000;
  const iso = (ms: number) => new Date(ms).toISOString();
  const mk = (id: string, ago: number, extra: object = {}) => ({
    ...run,
    run_id: id,
    started_at: iso(Date.now() - ago),
    received_at: iso(Date.now() - ago + 1000),
    ...extra,
  });
  const days = (n: number) => Array.from({ length: n }, (_, i) => mk(`d${i}`, (i + 1) * DAY + 3_600_000));
  const changes = (n: number) => ({
    since: "7d",
    commit_count: n,
    concepts: Array.from({ length: n }, (_, i) => ({
      id: `a/p${i}`,
      change: i === 0 ? "modified" : "added",
      added: i !== 0,
      last_at: iso(Date.now() - 3600_000),
      last_edit_at: iso(Date.now() - 3600_000),
      authors: ["surveyor/team-a/w-9"],
      ops: ["concept_write"],
    })),
  });
  const serve = (runs: object[], ch = changes(0)) =>
    stubApi({ "/changes": () => json(ch), "/components": () => json(componentsAnswer(componentRow({ runs }, [{}]))) });

  it("words the headline and tiles since the oldest kept run when the window predates it", async () => {
    serve([mk("r1", 2 * DAY), mk("r2", 3600_000)]);
    render(<App />);
    await surveyorTab();
    const since = new Date(Date.now() - 2 * DAY).toLocaleDateString("en", { month: "short", day: "numeric" });
    // The default window (7 days) starts before the oldest kept run.
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(`ran 2 times since ${since}.`);
    const tiles = screen.getByRole("list", { name: "The Surveyor in the window" });
    expect(within(tiles).getByText("runs ok").closest("li")).toHaveTextContent(`since ${since}`);
    expect(within(tiles).getByText("pages created").closest("li")).not.toHaveTextContent("since");
    expect(screen.getByText(/Pages are matched to runs by author and time/)).toBeInTheDocument();
  });

  it("keeps the plain wording within the kept range", async () => {
    serve([mk("r1", 3 * DAY), mk("r2", 3600_000)]);
    render(<App />);
    await surveyorTab();
    await userEvent.setup().click(screen.getByRole("button", { name: "1d" }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(/ran 1 time today\./);
  });

  it("counts pages from the commits, whatever the runs", async () => {
    serve([], changes(3));
    render(<App />);
    await surveyorTab();
    const tiles = screen.getByRole("list", { name: "The Surveyor in the window" });
    expect(within(tiles).getByText("pages created").closest("li")).toHaveTextContent("2");
    expect(within(tiles).getByText("pages changed").closest("li")).toHaveTextContent("1");
    expect(within(tiles).getByText("runs ok").closest("li")).not.toHaveTextContent("since");
  });

  it("groups runs by day, today expanded, and expands a day on demand", async () => {
    serve([mk("t1", 1000), mk("t2", 2000), mk("y1", DAY + 3_600_000, { tokens_spent: 1500 })]);
    render(<App />);
    await surveyorTab();
    const list = await screen.findByRole("list", { name: "Surveyor runs" });
    const buttons = within(list).getAllByRole("button", { expanded: undefined }).filter((b) => b.hasAttribute("aria-expanded"));
    expect(buttons).toHaveLength(2);
    expect(buttons[0]).toHaveAttribute("aria-expanded", "true");
    expect(buttons[0]).toHaveTextContent("2 runs · 2 ok · 84 tokens");
    expect(buttons[1]).toHaveAttribute("aria-expanded", "false");
    expect(buttons[1]).toHaveTextContent("1 run · 1 ok · 1.5k tokens");
    expect(within(buttons[1]!).getByTitle("1500 tokens")).toBeInTheDocument();
    expect(within(list).queryByText("1500 tokens")).not.toBeInTheDocument();
    await userEvent.setup().click(buttons[1]!);
    expect(buttons[1]).toHaveAttribute("aria-expanded", "true");
    expect(within(list).getByText("1500 tokens")).toBeInTheDocument();
  });

  it("shows a failed run under its collapsed day", async () => {
    serve([mk("y1", DAY + 3_600_000), mk("y2", DAY + 3_700_000, { outcome: "failed", error: "feed down" })]);
    render(<App />);
    await surveyorTab();
    const list = await screen.findByRole("list", { name: "Surveyor runs" });
    const btn = within(list).getAllByRole("button").find((b) => b.hasAttribute("aria-expanded") && /1 failed/.test(b.textContent ?? ""))!;
    expect(btn).toHaveAttribute("aria-expanded", "false");
    expect(within(list).getByText("feed down")).toBeInTheDocument();
    expect(within(list).getAllByText(/tokens$/).filter((e) => e.classList.contains("surveyors__meta"))).toHaveLength(1);
  });

  it("clamps to seven days behind Show more", async () => {
    serve(days(9));
    render(<App />);
    await surveyorTab();
    await userEvent.setup().click(screen.getByRole("button", { name: "30d" }));
    const list = await screen.findByRole("list", { name: "Surveyor runs" });
    const count = () => within(list).getAllByRole("button").filter((b) => b.hasAttribute("aria-expanded")).length;
    expect(count()).toBe(7);
    await userEvent.setup().click(screen.getByRole("button", { name: "Show more" }));
    expect(count()).toBe(9);
    expect(screen.queryByRole("button", { name: "Show more" })).not.toBeInTheDocument();
  });
});

describe("Work: no Surveyor strip", () => {
  beforeEach(() => open("work"));

  it("shows neither alarms nor budgets, whatever the Surveyor reports", async () => {
    stubApi({
      "/work": () => json(work),
      "/components": () => json(componentsAnswer(componentRow({ declared: true, alarms: [{ code: "silent", origin: "server" }] }, [{ state: "budget_exhausted" }]))),
    });
    render(<App />);
    await screen.findByRole("region", { name: "Open work" });
    expect(screen.queryByRole("region", { name: "Surveyors and feeds" })).not.toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Surveyor alarms" })).not.toBeInTheDocument();
  });
});

describe("Settings: Sources", () => {
  beforeEach(() => open("settings"));

  it("counts the KB's Sources by status and lists a feed in error", async () => {
    const src = (id: string, s: string) => ({ id, ingest_status: s, cited_by: 0, cited_by_ids: [] });
    stubApi({
      "/settings": () => json(SETTINGS),
      "/sources": () => json({ sources: [src("a", "pending"), src("b", "pending"), src("c", "ingested"), src("d", "skipped")] }),
      "/feeds": () => json({ feeds: [{ id: "feeds/news", title: "News feed", feed_error: "HTTP 500" }], total: 1 }),
    });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: /^Surveyor\b/ }));
    const counts = await screen.findByRole("list", { name: "Sources by status" });
    expect(within(counts).getByText("pending").nextSibling).toHaveTextContent("2");
    expect(within(counts).getByText("ingested").nextSibling).toHaveTextContent("1");
    expect(await screen.findByText("HTTP 500")).toBeInTheDocument();
  });

  it("keeps the Sources block through a transient failure and recovers", async () => {
    const { retry } = await import("../components/Surveyors");
    retry.baseMs = 10;
    const src = (id: string, s: string) => ({ id, ingest_status: s, cited_by: 0, cited_by_ids: [] });
    let calls = 0;
    stubApi({
      "/settings": () => json(SETTINGS),
      "/sources": () => (++calls === 1 ? json({ error: { code: "internal", message: "boom" } }, 500) : json({ sources: [src("a", "pending")] })),
    });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: /^Surveyor\b/ }));
    const counts = await screen.findByRole("list", { name: "Sources by status" });
    expect(within(counts).getByText("pending").nextSibling).toHaveTextContent("1");
    expect(calls).toBeGreaterThan(1);
    retry.baseMs = 5000;
  });

  it("never shows 0+ at the list cap", async () => {
    const many = Array.from({ length: 500 }, (_, i) => ({ id: `s${i}`, ingest_status: "pending", cited_by: 0, cited_by_ids: [] }));
    stubApi({ "/settings": () => json(SETTINGS), "/sources": () => json({ sources: many }) });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: /^Surveyor\b/ }));
    const counts = await screen.findByRole("list", { name: "Sources by status" });
    expect(within(counts).getByText("pending").nextSibling).toHaveTextContent("500+");
    expect(within(counts).getByText("ingested").nextSibling).toHaveTextContent("—");
  });

  it("is absent when the read fails", async () => {
    stubApi({ "/settings": () => json(SETTINGS), "/sources": () => json({ error: { code: "not_found", message: "no" } }, 404) });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: /^Surveyor\b/ }));
    await waitFor(() => expect(screen.getByText(/No Surveyor has reported/)).toBeInTheDocument());
    expect(screen.queryByRole("list", { name: "Sources by status" })).not.toBeInTheDocument();
  });
});
