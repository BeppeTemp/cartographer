import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchLint, LINT_PAGE } from "./client";

const finding = (n: number) => ({ path: `m/c${n}.md`, concept: `m/c${n}`, check: "broken_link", message: "gone", handler: "doctor" });

function stubPages(pages: Array<{ findings: unknown[]; next_cursor?: string }>) {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      calls.push(String(url));
      const page = pages[calls.length - 1];
      return new Response(JSON.stringify({ ...page, count: 3, by_check: { broken_link: 3 } }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
  return calls;
}

describe("fetchLint", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reads every page and reports the whole list", async () => {
    const calls = stubPages([
      { findings: [finding(0), finding(1)], next_cursor: "2" },
      { findings: [finding(2)] },
    ]);
    const report = await fetchLint("kb-a");
    expect(report.findings.map((f) => f.concept)).toEqual(["m/c0", "m/c1", "m/c2"]);
    expect(report.count).toBe(3);
    expect(report.next_cursor).toBeUndefined();
    expect(calls).toHaveLength(2);
    expect(calls[0]).toContain(`/kbs/kb-a/lint?limit=${LINT_PAGE}`);
    expect(calls[0]).not.toContain("cursor");
    expect(calls[1]).toContain("&cursor=2");
  });

  it("stops when the server hands back the cursor it was given", async () => {
    const calls = stubPages([
      { findings: [finding(0)], next_cursor: "1" },
      { findings: [finding(1)], next_cursor: "1" },
      { findings: [finding(2)] },
    ]);
    const report = await fetchLint("kb-a");
    expect(calls).toHaveLength(2);
    expect(report.findings).toHaveLength(2);
    expect(report.next_cursor).toBeUndefined();
  });
});
