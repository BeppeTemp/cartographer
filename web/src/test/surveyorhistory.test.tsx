import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { Activity } from "../components/Activity";
import { componentRow, componentsAnswer, json, stubApi } from "./fixtures";

const DAY = 86_400_000;
const iso = (ms: number) => new Date(ms).toISOString();
const dayOf = (ago: number) => iso(Date.now() - ago).slice(0, 10);

const keptRun = (id: string, ago: number) => ({
  run_id: id,
  replica: "w-1",
  task: "doctor",
  kb: "kb-a",
  started_at: iso(Date.now() - ago),
  duration_ms: 10,
  outcome: "ok",
  tokens_spent: 42,
  received_at: iso(Date.now() - ago + 1000),
});
const row = (day: string, over: object = {}) => ({
  day,
  perimeter: "team-a",
  task: "poll",
  outcome: "ok",
  runs: 1,
  tokens: 10,
  duration_ms: 5,
  ...over,
});
const answer = (days: object[], over: object = {}) => ({
  retention_days: 90,
  persisted: true,
  first_day: dayOf(20 * DAY),
  now: iso(Date.now()),
  days,
  ...over,
});

function open() {
  window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=activity");
  localStorage.clear();
  localStorage.setItem("cartographer.panel.rail", "0");
  sessionStorage.clear();
}
async function surveyorTab() {
  const user = userEvent.setup();
  const group = await screen.findByRole("group", { name: "Activity of" });
  await user.click(within(group).getByRole("button", { name: "Surveyor" }));
  return user;
}
const kept = [keptRun("r1", 2 * DAY), keptRun("r2", 3600_000)];
/** One figure tile of the Surveyor tab's hero. */
const tile = (label: string) => within(screen.getByRole("list", { name: "The Surveyor in the window" })).getByText(label).closest("li")!;
const runs = () => tile("Runs");

const serve = (history: () => Response) =>
  stubApi({
    "/changes": () => json({ since: "7d", commit_count: 0, concepts: [] }),
    "/components": () => json(componentsAnswer(componentRow({ runs: kept }, [{}]))),
    "/surveyor-history": history,
  });

afterEach(() => vi.unstubAllGlobals());

describe("Activity: the persisted run trend", () => {
  beforeEach(open);

  // AC-surveyor-run-history-11
  it("figures come from the counters when they cover the window", async () => {
    // 30 runs over three days, far more than the 2 kept: the tiles show 30.
    serve(() =>
      json(
        answer([
          row(dayOf(DAY), { runs: 20, tokens: 2000 }),
          row(dayOf(2 * DAY), { runs: 8, tokens: 800 }),
          row(dayOf(3 * DAY), { runs: 2, outcome: "failed", tokens: 20 }),
        ]),
      ),
    );
    render(<App />);
    await surveyorTab();
    await waitFor(() => expect(runs()).toHaveTextContent("30"));
    expect(runs()).toHaveAttribute("title", "2 failed");
    const trend = screen.getByRole("region", { name: "Runs per day" });
    expect(within(trend).getByText("Runs per day as a table")).toBeInTheDocument();
    expect(within(trend).getAllByRole("row")).toHaveLength(4);
    expect(within(trend).queryByText(/memory only/)).not.toBeInTheDocument();
  });

  // AC-surveyor-run-history-12
  it("falls back to the kept runs on a 404", async () => {
    serve(() => json({ error: { code: "not_found", message: "no" } }, 404));
    render(<App />);
    await surveyorTab();
    const since = new Date(Date.now() - 2 * DAY).toLocaleDateString("en", { month: "short", day: "numeric" });
    expect(runs()).toHaveTextContent("2");
    expect(runs()).toHaveAttribute("title", `since ${since}`);
    expect(screen.queryByRole("region", { name: "Runs per day" })).not.toBeInTheDocument();
  });

  it("words the note since the history's first day when the counters do not reach the window", async () => {
    const first = dayOf(DAY);
    serve(() => json(answer([row(first, { runs: 3 })], { first_day: first })));
    render(<App />);
    await surveyorTab();
    const [y, m, d] = first.split("-").map(Number);
    const label = new Date(y!, m! - 1, d!, 12).toLocaleDateString("en", { month: "short", day: "numeric" });
    expect(await screen.findByRole("region", { name: "Runs per day" })).toBeInTheDocument();
    expect(runs()).toHaveAttribute("title", `since ${label}`);
  });

  it("says a memory-only history is forgotten by a restart", async () => {
    serve(() => json(answer([row(dayOf(DAY))], { persisted: false })));
    render(<App />);
    await surveyorTab();
    expect(await screen.findByText("Kept in memory only: a restart forgets it.")).toBeInTheDocument();
  });

  it("does not let the answer of a shorter window stand in for a longer one", async () => {
    // 30d is asked and never answers: the 7d counters must not describe it.
    const fetchMock = serve(() => json(answer([row(dayOf(DAY), { runs: 5 })], { first_day: dayOf(6 * DAY) })));
    const inner = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (url: string) =>
      url.includes("/surveyor-history?days=30") ? new Promise<Response>(() => undefined) : inner(url),
    );
    render(<App />);
    const user = await surveyorTab();
    await waitFor(() => expect(runs()).toHaveTextContent("5"));
    await user.click(screen.getByRole("button", { name: "30d" }));
    const since = new Date(Date.now() - 2 * DAY).toLocaleDateString("en", { month: "short", day: "numeric" });
    await waitFor(() => expect(runs()).toHaveAttribute("title", `since ${since}`));
    expect(runs()).toHaveTextContent("2");
  });

  it("keeps the kept runs for a window the counters do not reach (All)", async () => {
    serve(() => json(answer([row(dayOf(DAY), { runs: 9 })])));
    render(<App />);
    const user = await surveyorTab();
    await waitFor(() => expect(runs()).toHaveTextContent("9"));
    await user.click(screen.getByRole("button", { name: "All" }));
    // The counters keep 90 days: they never stand for "All", the kept runs do.
    await waitFor(() => expect(runs()).toHaveTextContent("2"));
    expect(runs()).not.toHaveTextContent("9");
    expect(screen.getByRole("region", { name: "Runs per day" })).toBeInTheDocument();
  });

  it("forgets the counters of the previous KB when the KB changes", async () => {
    const fetchMock = stubApi({
      "/changes": () => json({ since: "7d", commit_count: 0, concepts: [] }),
      "/kbs/kb-a/surveyor-history": () => json(answer([row(dayOf(DAY), { runs: 30 })])),
    });
    const inner = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (url: string) =>
      url.includes("/kbs/kb-b/surveyor-history") ? new Promise<Response>(() => undefined) : inner(url),
    );
    const comp = (kb: string) => [componentRow({ runs: [{ ...keptRun("r1", 3600_000), kb }], kbs: [kb] }, [{}])] as never;
    const props = { snapshot: null, onOpen: () => undefined };
    const { rerender } = render(<Activity kb="kb-a" components={comp("kb-a")} {...props} />);
    const user = userEvent.setup();
    await user.click(within(await screen.findByRole("group", { name: "Activity of" })).getByRole("button", { name: "Surveyor" }));
    await waitFor(() => expect(runs()).toHaveTextContent("30"));
    rerender(<Activity kb="kb-b" components={comp("kb-b")} {...props} />);
    await waitFor(() => expect(runs()).not.toHaveTextContent("30"));
    expect(screen.queryByRole("region", { name: "Runs per day" })).not.toBeInTheDocument();
  });
});
