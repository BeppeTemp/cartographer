import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { ConceptChange } from "../api/types";
import { touchedByRun } from "../lib/activity";
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

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

// Fixtures built on Date.now() and grouped by day cross midnight when the suite runs
// just after it (#2396): hold the clock at local midday. Only Date is faked, so
// waitFor and findBy keep their timers.
const MIDDAY = new Date(2026, 5, 15, 12, 0, 0).getTime();
function holdMidday() {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(MIDDAY);
}

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
    await screen.findByRole("region", { name: "Summary and filters" });
    expect(document.querySelectorAll(".sv-run")).toHaveLength(1);
    expect(tile("Runs")).toHaveTextContent("1");
  });
});

/** One figure tile of the Surveyor tab's hero. */
const tile = (label: string) => within(screen.getByRole("list", { name: "The Surveyor in the window" })).getByText(label).closest("li")!;
const runRows = () => [...document.querySelectorAll<HTMLElement>(".sv-run")];

describe("Activity: the Surveyor's report", () => {
  const iso = (ms: number) => new Date(ms).toISOString();
  let recent: typeof run;
  let failed: typeof run & { error: string };
  let changes: { since: string; commit_count: number; concepts: object[] };
  beforeEach(() => {
    open("activity");
    holdMidday();
    const at = Date.now() - 3600_000;
    recent = { ...run, run_id: "r9", task: "ingest", started_at: iso(at), duration_ms: 60_000, received_at: iso(at + 61_000), tokens_spent: 700 };
    failed = { ...run, run_id: "r8", outcome: "failed", error: "feed down", started_at: iso(at - 7200_000), received_at: iso(at - 7100_000), tokens_spent: 0 };
    changes = {
      since: "7d",
      commit_count: 3,
      concepts: [
        { id: "a/made", change: "added", last_at: iso(at + 30_000), last_edit_at: iso(at + 30_000), authors: ["surveyor/team-a/w-1"], ops: ["concept_write"] },
        { id: "a/before", change: "modified", last_at: iso(at - 600_000), last_edit_at: iso(at - 600_000), authors: ["surveyor/team-a/w-1"], ops: ["concept_write"] },
        { id: "a/human", change: "modified", last_at: iso(at + 30_000), last_edit_at: iso(at + 30_000), authors: ["bob"], ops: ["concept_write"] },
        { id: "a/shared", change: "modified", last_at: iso(at + 40_000), last_edit_at: iso(at + 40_000), authors: ["surveyor/team-a/w-1", "bob"], ops: ["concept_write"] },
      ],
    };
  });

  it("keeps the Users tab to people: Surveyor-only changes and its tag are gone", async () => {
    stubApi({ "/changes": () => json(changes), "/components": () => json(componentsAnswer(componentRow({ runs: [recent, failed] }, [{}]))) });
    render(<App />);
    const group = await screen.findByRole("group", { name: "Activity of" });
    expect(within(group).getByRole("button", { name: "Users" })).toHaveAttribute("aria-pressed", "true");
    await screen.findByRole("heading", { level: 1, name: "What users changed" });
    await waitFor(() => expect(document.querySelectorAll(".timeline__entry")).toHaveLength(2));
    const titles = [...document.querySelectorAll(".timeline__title")].map((t) => t.textContent).sort();
    expect(titles).toEqual(["a/human", "a/shared"]);
    expect(document.querySelector(".timeline__surveyor")).toBeNull();
    // The shared change keeps its human author only.
    const shared = [...document.querySelectorAll(".timeline__entry")].find((e) => e.textContent?.includes("a/shared"))!;
    expect(shared.querySelector(".timeline__authors")).toHaveAttribute("aria-label", "By bob");
    expect(screen.queryByRole("list", { name: "The Surveyor in the window" })).not.toBeInTheDocument();
  });

  it("details a run on the Surveyor tab, with tiles and the pages it wrote", async () => {
    stubApi({ "/changes": () => json(changes), "/components": () => json(componentsAnswer(componentRow({ runs: [recent, failed] }, [{}]))) });
    render(<App />);
    await surveyorTab();
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("What the Surveyor did");
    await screen.findByRole("region", { name: "Summary and filters" });
    const rows = runRows();
    expect(rows).toHaveLength(2);
    const ingest = rows.find((r) => r.textContent?.includes("Ingest run"))!;
    expect(within(ingest).getByText("ok")).toBeInTheDocument();
    expect(within(ingest).getByText("60 s")).toBeInTheDocument();
    expect(ingest.querySelector(".sv-run__tokens")).toHaveAttribute("title", "700 tokens");
    // Its pages, as Users rows beneath it: the one it wrote, not the earlier or the human's.
    const pages = [...ingest.querySelectorAll(".sv-run__pages .timeline__title")].map((t) => t.textContent);
    expect(pages).toEqual(["a/made", "a/shared"]);
    expect(ingest).toHaveTextContent("2 pages, 1 new");
    expect(screen.queryByText("a/human")).not.toBeInTheDocument();
    // The failed run: its pill, its error, no pages.
    const bad = rows.find((r) => r.textContent?.includes("Doctor run"))!;
    expect(bad).toHaveAttribute("data-tone", "error");
    expect(within(bad).getByText("failed")).toBeInTheDocument();
    expect(within(bad).getByText("feed down")).toBeInTheDocument();
    expect(bad).toHaveTextContent("nothing written");

    expect(tile("Runs")).toHaveTextContent("2");
    expect(tile("Runs")).toHaveAttribute("title", expect.stringMatching(/^1 failed/));
    // Pages come from the commits: three of the Surveyor's, one new.
    expect(tile("Pages written")).toHaveTextContent("3");
    expect(tile("Tokens")).toHaveTextContent("700");
    expect(tile("Tokens wasted")).toHaveTextContent("0");
    expect(tile("Last run")).toHaveTextContent(/ago|now|h|m/);
  });

  it("lists under a run the page that carries its run id, whatever its time", async () => {
    const farAt = iso(Date.now() - 6 * 3600_000);
    const far = { id: "a/far", change: "modified", last_at: farAt, last_edit_at: farAt, authors: ["surveyor/team-a/w-1"], ops: ["concept_write"], surveyor_runs: [recent.run_id] };
    stubApi({
      "/changes": () => json({ ...changes, concepts: [...changes.concepts, far] }),
      "/components": () => json(componentsAnswer(componentRow({ runs: [recent] }, [{}]))),
    });
    render(<App />);
    await surveyorTab();
    await screen.findByRole("region", { name: "Summary and filters" });
    const ingest = runRows().find((r) => r.textContent?.includes("Ingest run"))!;
    const pages = [...ingest.querySelectorAll(".sv-run__pages .timeline__title")].map((t) => t.textContent);
    expect(pages).toContain("a/far");
  });

  it("opens a page's diff from under its run", async () => {
    stubApi({
      "/changes": () => json(changes),
      "/components": () => json(componentsAnswer(componentRow({ runs: [recent] }, [{}]))),
      "/diff": () => json({ id: "a/made", path: "a/made.md", commits: 1, files: [{ path: "a/made.md", status: "added", before: "", after: "x\n" }] }),
    });
    render(<App />);
    const user = await surveyorTab();
    const row = await screen.findByRole("button", { name: /a\/made/ });
    await user.click(row);
    expect(await screen.findByRole("region", { name: "What changed in a/made" })).toBeInTheDocument();
  });
});

describe("Activity: the Surveyor's runs, honestly", () => {
  beforeEach(() => {
    open("activity");
    holdMidday();
  });
  const DAY = 86_400_000;
  const iso = (ms: number) => new Date(ms).toISOString();
  const mk = (id: string, ago: number, extra: object = {}) => ({
    ...run,
    run_id: id,
    started_at: iso(Date.now() - ago),
    received_at: iso(Date.now() - ago + 1000),
    ...extra,
  });
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
  const serve = (runs: object[], ch: object = changes(0)) =>
    stubApi({ "/changes": () => json(ch), "/components": () => json(componentsAnswer(componentRow({ runs }, [{}]))) });

  it("words the Runs tile since the oldest kept run when the window predates it", async () => {
    serve([mk("r1", 2 * DAY), mk("r2", 3600_000)]);
    render(<App />);
    await surveyorTab();
    await screen.findByRole("region", { name: "Summary and filters" });
    const since = new Date(Date.now() - 2 * DAY).toLocaleDateString("en", { month: "short", day: "numeric" });
    // The default window (7 days) starts before the oldest kept run.
    expect(tile("Runs")).toHaveTextContent("2");
    expect(tile("Runs")).toHaveAttribute("title", `since ${since}`);
    expect(tile("Pages written")).not.toHaveAttribute("title", expect.stringContaining("since"));
  });

  it("keeps the plain wording within the kept range", async () => {
    serve([mk("r1", 3 * DAY), mk("r2", 3600_000)]);
    render(<App />);
    const user = await surveyorTab();
    await user.click(screen.getByRole("button", { name: "1d" }));
    await waitFor(() => expect(tile("Runs")).toHaveTextContent("1"));
    expect(tile("Runs")).toHaveAttribute("title", "all ok");
  });

  it("counts pages from the commits, whatever the runs", async () => {
    serve([], changes(3));
    render(<App />);
    await surveyorTab();
    await screen.findByRole("region", { name: "Summary and filters" });
    expect(tile("Pages written")).toHaveTextContent("3");
    expect(tile("Pages written")).toHaveAttribute("title", "2 new");
    expect(tile("Runs")).toHaveTextContent("0");
    expect(screen.getByText("No Surveyor run in this window.")).toBeInTheDocument();
  });

  it("groups runs by day, every day open, newest first", async () => {
    serve([mk("t1", 1000), mk("t2", 2000), mk("y1", DAY + 3_600_000, { tokens_spent: 1500 })]);
    render(<App />);
    await surveyorTab();
    await screen.findByRole("region", { name: "Summary and filters" });
    const heads = [...document.querySelectorAll(".timeline__date")].map((h) => h.textContent);
    expect(heads).toEqual(["Today2", "Yesterday1"]);
    expect(runRows()).toHaveLength(3);
    // The token bar's label is short, its title exact.
    const big = runRows().find((r) => r.querySelector(".sv-run__tokens")?.getAttribute("title") === "1500 tokens")!;
    expect(big.querySelector(".sv-run__tk")).toHaveTextContent("1.5k");
  });

  it("shows a failed run with its error, nothing collapsed", async () => {
    serve([mk("y1", DAY + 3_600_000), mk("y2", DAY + 3_700_000, { outcome: "failed", error: "feed down" })]);
    render(<App />);
    await surveyorTab();
    await screen.findByRole("region", { name: "Summary and filters" });
    expect(runRows()).toHaveLength(2);
    expect(screen.getByText("feed down")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show more" })).not.toBeInTheDocument();
  });

  it("lists every run of a long window, with no Show more", async () => {
    serve(Array.from({ length: 9 }, (_, i) => mk(`d${i}`, (i + 1) * DAY + 3_600_000)));
    render(<App />);
    const user = await surveyorTab();
    await user.click(screen.getByRole("button", { name: "30d" }));
    await waitFor(() => expect(runRows()).toHaveLength(9));
    expect(screen.queryByRole("button", { name: "Show more" })).not.toBeInTheDocument();
  });

  it("filters by task from What, and by Map from Where", async () => {
    const t0 = Date.now() - 3600_000;
    const wrote = (id: string, replica: string, task: string, at: number) => ({
      c: { id, change: "added", added: true, last_at: iso(at + 1000), last_edit_at: iso(at + 1000), authors: [`surveyor/team-a/${replica}`], ops: ["concept_write"] },
      r: mk(`r-${replica}`, Date.now() - at, { replica, task, duration_ms: 5000 }),
    });
    const x = wrote("alpha/one", "w-1", "ingest", t0);
    const y = wrote("beta/two", "w-2", "doctor", t0 + 600_000);
    serve([x.r, y.r], { since: "7d", commit_count: 2, concepts: [x.c, y.c] });
    render(<App />);
    const user = await surveyorTab();
    await screen.findByRole("region", { name: "Summary and filters" });
    expect(runRows()).toHaveLength(2);

    const what = screen.getByRole("list", { name: "Filter by task" });
    await user.click(within(what).getByRole("button", { name: /doctor/ }));
    expect(runRows()).toHaveLength(1);
    expect(runRows()[0]).toHaveTextContent("Doctor run");
    await user.click(within(what).getByRole("button", { name: /doctor/ }));
    expect(runRows()).toHaveLength(2);

    const where = screen.getByRole("list", { name: "Filter by Map" });
    await user.click(within(where).getByRole("button", { name: /alpha/ }));
    expect(runRows()).toHaveLength(1);
    expect(runRows()[0]).toHaveTextContent("Ingest run");
    expect(runRows()[0]).toHaveTextContent("alpha/one");
  });

  it("lists a page under one run only, the newest that claims it", async () => {
    const t0 = Date.now() - 3600_000;
    const page = { id: "a/shared", change: "modified", last_at: iso(t0 + 10_000), last_edit_at: iso(t0 + 10_000), authors: ["surveyor/team-a/w-1"], ops: ["concept_write"] };
    serve(
      [mk("old", Date.now() - t0, { duration_ms: 60_000 }), mk("new", Date.now() - t0 - 1000, { duration_ms: 60_000 })],
      { since: "7d", commit_count: 1, concepts: [page] },
    );
    render(<App />);
    await surveyorTab();
    await screen.findByRole("region", { name: "Summary and filters" });
    expect(runRows()).toHaveLength(2);
    expect(document.querySelectorAll(".sv-run__pages")).toHaveLength(1);
    expect(screen.getAllByText("a/shared")).toHaveLength(1);
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

describe("touchedByRun: the run id", () => {
  const R = { run_id: "w-1-1700000000-3", replica: "w-1", started_at: "2026-01-01T10:00:00Z", duration_ms: 60_000 };
  const change = (id: string, extra: Partial<ConceptChange> = {}): ConceptChange => ({
    id,
    change: "modified",
    last_at: "2026-01-01T10:00:30Z",
    authors: ["surveyor/team-a/w-1"],
    ...extra,
  });
  const ids = (cs: ConceptChange[]) => touchedByRun(R, cs).map((c) => c.id);

  // AC-surveyor-run-commit-8
  it("touchedByRun lists a change that carries the run id whatever its time", () => {
    const far = change("a/far", { last_at: "2026-03-05T00:00:00Z", surveyor_runs: [R.run_id, "w-1-1-1"] });
    expect(ids([far])).toEqual(["a/far"]);
  });

  // AC-surveyor-run-commit-9
  it("touchedByRun ignores a change with other run ids and falls back for a change with none", () => {
    const other = change("a/other", { surveyor_runs: ["w-1-1700000999-4"] });
    const none = change("a/none");
    const late = change("a/late", { last_at: "2026-01-02T00:00:00Z" });
    expect(ids([other, none, late])).toEqual(["a/none"]);
  });

  it("does not match a forged id on a change no Surveyor of the replica wrote", () => {
    const person = change("a/person", { authors: ["curator"], surveyor_runs: [R.run_id] });
    const otherReplica = change("a/w10", { authors: ["surveyor/team-a/w-10"], surveyor_runs: [R.run_id] });
    expect(ids([person, otherReplica])).toEqual([]);
  });

  it("lists a change a person edited afterwards when a Surveyor of the replica is among its authors", () => {
    const edited = change("a/edited", { authors: ["curator", "surveyor/team-a/w-1"], last_at: "2026-02-01T00:00:00Z", surveyor_runs: [R.run_id] });
    expect(ids([edited])).toEqual(["a/edited"]);
  });
});
