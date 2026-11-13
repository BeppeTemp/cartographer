import { render, renderHook, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { CANDIDATES_REFRESH, useReportCandidates } from "../components/Candidates";
import { retry } from "../components/Surveyors";
import { componentRow, componentsAnswer, json, stubApi } from "./fixtures";

/** Report candidates (D1925) in Activity and Work, and the "Draft report" action: read-only, nothing is sent. */

const work = { total: 0, open_concepts: 0, by_status: {}, by_map: {}, open_items: 0, entries: [] };
const NOW = "2026-01-01T03:00:00Z";
const cand = (over: object = {}) => ({
  id: "c1",
  kind: "tool_error",
  surveyor: "w-1",
  kb: "kb-a",
  tool: "concept_read",
  code: "internal_error",
  count: 3,
  first_at: "2026-01-01T00:00:00Z",
  last_at: "2026-01-01T00:00:00Z",
  ...over,
});
const list = (...candidates: object[]) => () => json({ candidates, destination: "github" });
const draft = (over: object = {}) => ({
  title: "concept_read failed with internal_error",
  body: "**Version**\n\n- server version: v1.2.3\n\nNot reproduced.",
  kind: "bug",
  url: "https://github.com/example/repo/issues/new?template=bug.md&title=t",
  search_url: "https://github.com/example/repo/issues?q=is%3Aissue+t",
  body_in_url: true,
  destination: "github",
  ...over,
});
// The draft route is listed first: a fragment matches the first route that contains it.
const DRAFT = "/feedback/candidates/draft";
const LIST = "/feedback/candidates";

const methodOf = (call: unknown[]) => (call[1] as RequestInit | undefined)?.method ?? "GET";

function open(panel: string) {
  window.history.replaceState(null, "", `/ui/?kb=kb-a&panel=${panel}`);
  localStorage.clear();
  localStorage.setItem("cartographer.panel.rail", "0");
  sessionStorage.clear();
}

afterEach(() => vi.unstubAllGlobals());

describe("Activity: Report candidates", () => {
  beforeEach(() => open("activity"));

  it("lists this KB's candidates per Surveyor, in words, and none of another KB's", async () => {
    stubApi({
      [LIST]: list(cand(), cand({ id: "c2", kind: "contract_mismatch", surveyor: "w-2", tool: "kb_status", code: "unknown" }), cand({ id: "c3", kb: "kb-b" }), cand({ id: "c4", kb: undefined, kind: "protocol_mismatch", tool: undefined, code: "protocol", count: 1 })),
    });
    render(<App />);
    const one = await screen.findByRole("list", { name: "Report candidates of w-1" });
    expect(within(one).getAllByRole("listitem")).toHaveLength(2);
    expect(within(one).getByText(/tool error · concept_read · internal error/)).toBeInTheDocument();
    expect(within(one).getByText(/seen 3 times/)).toBeInTheDocument();
    const two = screen.getByRole("list", { name: "Report candidates of w-2" });
    expect(within(two).getByText(/contract mismatch · kb_status/)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /Report candidates/ })).toHaveTextContent("3");
  });

  it("words the age against the server's clock, not the browser's", async () => {
    stubApi({
      [LIST]: list(cand()),
      "/components": () => json({ ...componentsAnswer(componentRow({}, [{}])), server: { version: "test", started_at: "2026-01-01T00:00:00Z", now: NOW } }),
    });
    render(<App />);
    const one = await screen.findByRole("list", { name: "Report candidates of w-1" });
    await waitFor(() => expect(within(one).getByText(/last seen 3 h ago/)).toBeInTheDocument());
  });

  it("is absent with no candidate for the KB", async () => {
    stubApi({ [LIST]: list(cand({ kb: "kb-b" })) });
    render(<App />);
    await screen.findByRole("region", { name: /activity/i });
    await waitFor(() => expect((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.some(([u]) => String(u).includes(LIST))).toBe(true));
    expect(screen.queryByRole("heading", { name: /Report candidates/ })).not.toBeInTheDocument();
  });

  it("is absent when the operator switched feedback off (404 feedback_off)", async () => {
    const fetchMock = stubApi();
    render(<App />);
    await screen.findByRole("region", { name: /activity/i });
    await waitFor(() => expect(fetchMock.mock.calls.some(([u]) => String(u).includes(LIST))).toBe(true));
    expect(screen.queryByRole("heading", { name: /Report candidates/ })).not.toBeInTheDocument();
  });

  it("drafts a report with a GET and shows it read-only, the duplicate search first, then the form link", async () => {
    const openSpy = vi.spyOn(window, "open").mockImplementation(() => null);
    const fetchMock = stubApi({ [DRAFT]: () => json(draft()), [LIST]: list(cand()) });
    const user = userEvent.setup();
    render(<App />);
    const row = (await screen.findByRole("list", { name: "Report candidates of w-1" })).querySelector("li")!;
    expect(screen.queryByRole("group", { name: "Draft report" })).not.toBeInTheDocument();
    await user.click(within(row).getByRole("button", { name: "Draft report" }));
    const group = await screen.findByRole("group", { name: "Draft report" });

    const calls = fetchMock.mock.calls.filter(([u]) => String(u).includes(DRAFT));
    expect(calls).toHaveLength(1);
    expect(String(calls[0]![0])).toMatch(/\/feedback\/candidates\/draft\?id=c1$/);
    expect(methodOf(calls[0]!)).toBe("GET");

    expect(within(group).getByRole("textbox", { name: "Report title" })).toHaveValue("concept_read failed with internal_error");
    const body = within(group).getByRole("textbox", { name: "Report body" });
    expect(body).toHaveValue(draft().body);
    expect(body).toHaveAttribute("readonly");

    const search = within(group).getByRole("link", { name: "search the existing issues" });
    const form = within(group).getByRole("link", { name: "Open the report form" });
    expect(search.compareDocumentPosition(form) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    for (const a of [search, form]) {
      expect(a).toHaveAttribute("target", "_blank");
      expect(a).toHaveAttribute("rel", "noopener noreferrer");
    }
    expect(form).toHaveAttribute("href", draft().url);
    expect(within(group).getByText("Nothing has been sent: review the text, then submit it yourself.")).toBeInTheDocument();
    expect(within(group).queryByRole("button", { name: "Copy the text" })).not.toBeInTheDocument();

    // No navigation, no new window, and every request of the page is a read.
    expect(openSpy).not.toHaveBeenCalled();
    for (const call of fetchMock.mock.calls) expect(methodOf(call)).toBe("GET");
    openSpy.mockRestore();

    await user.click(within(row).getByRole("button", { name: "Hide the draft" }));
    expect(screen.queryByRole("group", { name: "Draft report" })).not.toBeInTheDocument();
  });

  it("offers a copy button when the link carries the title only, and no duplicate search for a custom destination", async () => {
    stubApi({ [DRAFT]: () => json(draft({ body_in_url: false, search_url: "", destination: "custom" })), [LIST]: list(cand()) });
    const user = userEvent.setup();
    render(<App />);
    const row = (await screen.findByRole("list", { name: "Report candidates of w-1" })).querySelector("li")!;
    await user.click(within(row).getByRole("button", { name: "Draft report" }));
    const group = await screen.findByRole("group", { name: "Draft report" });
    expect(within(group).queryByRole("link", { name: "search the existing issues" })).not.toBeInTheDocument();
    await user.click(within(group).getByRole("button", { name: "Copy the text" }));
    expect(await within(group).findByText("Copied.")).toBeInTheDocument();
    expect(await navigator.clipboard.readText()).toBe(draft().body);
  });

  it("never follows a link that is not a web address", async () => {
    stubApi({ [DRAFT]: () => json(draft({ url: "javascript:alert(1)", search_url: "data:text/html,x" })), [LIST]: list(cand()) });
    const user = userEvent.setup();
    render(<App />);
    const row = (await screen.findByRole("list", { name: "Report candidates of w-1" })).querySelector("li")!;
    await user.click(within(row).getByRole("button", { name: "Draft report" }));
    const group = await screen.findByRole("group", { name: "Draft report" });
    expect(within(group).queryAllByRole("link")).toHaveLength(0);
  });

  it("says a vanished candidate is gone, and lets the person try again", async () => {
    let calls = 0;
    stubApi({
      [DRAFT]: () => (++calls === 1 ? json({ error: { code: "not_found", message: "not found" } }, 404) : json(draft())),
      [LIST]: list(cand()),
    });
    const user = userEvent.setup();
    render(<App />);
    const row = (await screen.findByRole("list", { name: "Report candidates of w-1" })).querySelector("li")!;
    await user.click(within(row).getByRole("button", { name: "Draft report" }));
    expect(await within(row).findByRole("alert")).toHaveTextContent(/no longer held/);
    await user.click(within(row).getByRole("button", { name: "Draft report" }));
    expect(await screen.findByRole("group", { name: "Draft report" })).toBeInTheDocument();
    expect(within(row).queryByRole("alert")).not.toBeInTheDocument();
  });

  it("keeps the list through a transient failure and recovers", async () => {
    retry.baseMs = 10;
    let calls = 0;
    stubApi({ [LIST]: () => (++calls === 1 ? json({ error: { code: "internal", message: "boom" } }, 500) : json({ candidates: [cand()], destination: "github" })) });
    render(<App />);
    expect(await screen.findByRole("list", { name: "Report candidates of w-1" })).toBeInTheDocument();
    expect(calls).toBeGreaterThan(1);
    retry.baseMs = 5000;
  });

  it("retries a 404 that is not feedback_off (a restart) instead of switching off for good", async () => {
    retry.baseMs = 10;
    let calls = 0;
    stubApi({ [LIST]: () => (++calls === 1 ? json({ error: { code: "not_found", message: "not found" } }, 404) : json({ candidates: [cand()], destination: "github" })) });
    render(<App />);
    expect(await screen.findByRole("list", { name: "Report candidates of w-1" })).toBeInTheDocument();
    retry.baseMs = 5000;
  });
});

describe("Work: report candidates badge", () => {
  beforeEach(() => open("work"));

  it("counts this KB's candidates, once", async () => {
    stubApi({ "/work": () => json(work), [LIST]: list(cand(), cand({ id: "c2" }), cand({ id: "c3", kb: "kb-b" })) });
    render(<App />);
    expect(await screen.findByText(/2 report candidates from the Surveyors/)).toBeInTheDocument();
  });

  it("is absent with none, and when feedback is off", async () => {
    stubApi({ "/work": () => json(work) });
    render(<App />);
    await screen.findByRole("region", { name: "Open work" });
    expect(screen.queryByText(/report candidate/)).not.toBeInTheDocument();
  });
});

describe("useReportCandidates", () => {
  afterEach(() => {
    CANDIDATES_REFRESH.ms = 60_000;
    retry.baseMs = 5000;
  });

  it("is read again on its timer, so a candidate shows without a commit", async () => {
    CANDIDATES_REFRESH.ms = 20;
    let calls = 0;
    stubApi({ [LIST]: () => json({ candidates: ++calls === 1 ? [] : [cand()], destination: "github" }) });
    const { result } = renderHook(() => useReportCandidates("kb-a", 0));
    await waitFor(() => expect(result.current.map((c) => c.id)).toEqual(["c1"]));
  });

  it("clears a list already shown when feedback is switched off", async () => {
    CANDIDATES_REFRESH.ms = 20;
    retry.baseMs = 5;
    let off = false;
    stubApi({
      [LIST]: () => (off ? json({ error: { code: "feedback_off", message: "off" } }, 404) : json({ candidates: [cand()], destination: "github" })),
    });
    const { result } = renderHook(() => useReportCandidates("kb-a", 0));
    await waitFor(() => expect(result.current).toHaveLength(1));
    off = true;
    await waitFor(() => expect(result.current).toHaveLength(0));
  });

  it("does not read again while the tab is hidden", async () => {
    CANDIDATES_REFRESH.ms = 20;
    let calls = 0;
    stubApi({ [LIST]: () => (++calls, json({ candidates: [], destination: "github" })) });
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    try {
      renderHook(() => useReportCandidates("kb-a", 0));
      await waitFor(() => expect(calls).toBe(1));
      await new Promise((r) => setTimeout(r, 80));
      expect(calls).toBe(1);
    } finally {
      Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
    }
  });

  it("keeps a list already shown through a transient failure", async () => {
    CANDIDATES_REFRESH.ms = 20;
    retry.baseMs = 10_000; // no recovery inside the test: the list must survive the failure itself
    let calls = 0;
    stubApi({
      [LIST]: () => (++calls === 1 ? json({ candidates: [cand()], destination: "github" }) : json({ error: { code: "internal", message: "boom" } }, 500)),
    });
    const { result } = renderHook(() => useReportCandidates("kb-a", 0));
    await waitFor(() => expect(result.current).toHaveLength(1));
    await waitFor(() => expect(calls).toBeGreaterThan(1));
    expect(result.current.map((c) => c.id)).toEqual(["c1"]);
  });

  it("shows nothing of the previous KB when the KB changes", async () => {
    stubApi({ [LIST]: list(cand(), cand({ id: "c2", kb: "kb-b" })) });
    const { result, rerender } = renderHook(({ kb }) => useReportCandidates(kb, 0), { initialProps: { kb: "kb-a" } });
    await waitFor(() => expect(result.current.map((c) => c.id)).toEqual(["c1"]));
    rerender({ kb: "kb-b" });
    expect(result.current.map((c) => c.id)).not.toContain("c1");
    await waitFor(() => expect(result.current.map((c) => c.id)).toEqual(["c2"]));
  });
});
