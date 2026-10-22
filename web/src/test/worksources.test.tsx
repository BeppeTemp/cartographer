import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { extraWork } from "../components/Work";
import { json, stubApi } from "./fixtures";

// One open gap from work_list: the "to answer" column the searches join.
const work = {
  total: 1,
  open_concepts: 1,
  by_status: {},
  by_reason: { gap: 1 },
  by_map: { notes: 1 },
  open_items: 0,
  entries: [
    {
      id: "notes/gap-1",
      title: "How do we rotate the cluster certificate?",
      type: "Gap",
      map: "notes",
      open_phase: true,
      reason: "gap",
      stale: false,
      items: [],
    },
  ],
};

// kb_status: two searches nobody answered, one that finds something now.
const status = {
  search_misses: [
    { query: "cluster upgrade", count: 3, last_seen: "2026-10-12T08:00:00Z" },
    { query: "backup window", count: 1 },
    { query: "old term", count: 2, last_seen: "2026-09-01T00:00:00Z", resolved: true },
  ],
};

// kb_review: one proposal about one page, one about many (and a map's contract).
const review = {
  items: [
    {
      kind: "duplicate_candidate",
      concepts: ["infra/a"],
      evidence: 'titles "Alpha" and "Alfa" share 80% of their words in map infra',
      suggested_action: "merge them, or link each to the other",
      weight: 5,
    },
    {
      kind: "repeated_fact",
      concepts: ["infra/_map", "infra/b", "infra/c", "infra/d", "infra/e", "infra/f", "infra/g"],
      evidence: 'the same line in 6 concepts: "ssh to the bastion"',
      suggested_action: "keep the fact in one concept and link to it from the others",
      weight: 3,
    },
  ],
};

function stubWork(overrides: Record<string, () => Response> = {}) {
  return stubApi({
    "/work": () => json(work),
    "/status": () => json(status),
    "/maintenance/review": () => json(review),
    ...overrides,
  });
}

/** The columns of the status board, as "name count". */
function columns(panel: HTMLElement) {
  return within(within(panel).getByRole("region", { name: /^Work by/ }))
    .getAllByRole("heading", { level: 2 })
    .map((h) => h.textContent);
}

/** Work also holds what is not a concept: the searches nobody answered and the doctor's proposals. */
describe("the Work panel's other sources", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=work");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("lists a search miss under 'to answer' and a proposal under 'to decide', and counts them", async () => {
    stubWork();
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Open work" });
    const heading = within(panel).getByRole("heading", { level: 1 });
    await waitFor(() =>
      expect(heading).toHaveTextContent(
        "1 concept carries open work, 0 unchecked items, 2 searches to answer, 2 proposals to decide.",
      ),
    );
    // The open gap and the two searches share a column; the proposals have their own.
    expect(columns(panel)).toEqual(["to answer 3", "to decide 2"]);

    const answer = within(panel).getByRole("region", { name: "to answer, 3" });
    expect(within(answer).getByText("“cluster upgrade”")).toBeInTheDocument();
    expect(within(answer).getByText("searched 3 times, last on 2026-10-12")).toBeInTheDocument();
    expect(within(answer).getByText("“backup window”")).toBeInTheDocument();
    expect(within(answer).getByText("searched once")).toBeInTheDocument();
    // A search that finds something now is no work: only named.
    expect(within(panel).queryByText("“old term”", { selector: ".work__card-title" })).toBeNull();
    const answered = within(panel).getByRole("region", { name: "Searches answered since" });
    expect(answered).toHaveTextContent("Answered since: 1 search that found nothing before now finds a page.");
    expect(within(answered).getByText("“old term”")).toBeInTheDocument();

    const decide = within(panel).getByRole("region", { name: "to decide, 2" });
    expect(within(decide).getByText(/titles "Alpha" and "Alfa" share 80%/)).toBeInTheDocument();
    expect(within(decide).getByText("merge them, or link each to the other")).toBeInTheDocument();
    expect(within(decide).getByText("Possible duplicates")).toBeInTheDocument();

    // The filters count them too, and narrow to them.
    const byStatus = within(panel).getByRole("list", { name: "Filter by status" });
    expect(within(byStatus).getByRole("button", { name: /to answer/ })).toHaveTextContent("3");
    await userEvent.setup().click(within(byStatus).getByRole("button", { name: /to decide/ }));
    expect(columns(panel)).toEqual(["to decide 2"]);
  });

  it("opens the page a proposal names, and lists the pages of one that names several", async () => {
    stubWork();
    const user = userEvent.setup();
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Open work" });
    const decide = await within(panel).findByRole("region", { name: "to decide, 2" });

    // Several pages (the map's contract is no page): a list of them, four shown and the rest counted.
    const refs = within(decide).getByRole("list", { name: "Pages concerned" });
    expect(within(refs).getAllByRole("button").map((b) => b.textContent)).toEqual([
      "infra/b",
      "infra/c",
      "infra/d",
      "infra/e",
    ]);
    expect(within(refs).getByText("+2 more")).toBeInTheDocument();
    await user.click(within(refs).getByRole("button", { name: "infra/c" }));
    expect(window.location.search).toContain("concept=infra%2Fc");
  });

  it("opens the single page a proposal names from its card", async () => {
    stubWork();
    const user = userEvent.setup();
    render(<App />);
    const decide = await screen.findByRole("region", { name: "to decide, 2" });
    await user.click(within(decide).getByText(/titles "Alpha" and "Alfa"/));
    expect(window.location.search).toContain("concept=infra%2Fa");
    expect(window.location.search).not.toContain("panel=work");
  });

  it("gives a search miss no page to open", async () => {
    stubWork();
    const user = userEvent.setup();
    render(<App />);
    const answer = await screen.findByRole("region", { name: "to answer, 3" });
    expect(within(answer).queryByRole("button", { name: /cluster upgrade/ })).toBeNull();
    await user.click(within(answer).getByText("“cluster upgrade”"));
    expect(window.location.search).toContain("panel=work");
    expect(window.location.search).not.toContain("concept=");
  });

  it("is work on its own: a KB with no concept open still shows its searches and proposals", async () => {
    stubWork({
      "/work": () => json({ total: 0, open_concepts: 0, by_status: {}, by_map: {}, open_items: 0, entries: [] }),
      "/maintenance/review": () => json({ items: [] }),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Open work" });
    await waitFor(() =>
      expect(within(panel).getByRole("heading", { level: 1 })).toHaveTextContent("2 searches to answer."),
    );
    expect(columns(panel)).toEqual(["to answer 2"]);
    expect(screen.queryByText(/No open work/)).toBeNull();
  });

  it("falls back to the work list alone when the status and the review cannot be read", async () => {
    stubWork({
      "/status": () => json({ error: { code: "not_found", message: "not found" } }, 404),
      "/maintenance/review": () => json({ error: { code: "internal", message: "boom" } }, 500),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Open work" });
    await waitFor(() =>
      expect(within(panel).getByRole("heading", { level: 1 })).toHaveTextContent(
        "1 concept carries open work, 0 unchecked items.",
      ),
    );
    expect(columns(panel)).toEqual(["to answer 1"]);
  });

  it("is gone from Health: no searched-for section, and no read of the status", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=health");
    const fetchMock = stubWork();
    render(<App />);
    await waitFor(() => expect(screen.getByText(/Cartographer found/)).toBeInTheDocument());
    expect(screen.queryByText("Searched for, not found")).toBeNull();
    expect(screen.queryByText("cluster upgrade")).toBeNull();
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/status"))).toBe(false);
  });
});

describe("extraWork", () => {
  it("makes a gap of each open search, a decision of each proposal, and keeps the closed searches apart", () => {
    const out = extraWork(status.search_misses, review.items);
    expect(out.entries.map((e) => [e.id.split(":")[0], e.reason ?? e.status, e.map])).toEqual([
      ["search", "gap", ""],
      ["search", "gap", ""],
      ["review", "to decide", "infra"],
      ["review", "to decide", "infra"],
    ]);
    expect(new Set(out.entries.map((e) => e.id)).size).toBe(4);
    expect(out.found).toEqual(["old term"]);
  });

  it("is empty without either source", () => {
    expect(extraWork([], [])).toEqual({ entries: [], found: [] });
  });
});
