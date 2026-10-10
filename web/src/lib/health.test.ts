import { checkLabel, filterReportByMap, groupFindings, trendOf } from "./health";

describe("groupFindings", () => {
  it("folds one cause on many pages into one group, worst and largest first", () => {
    const groups = groupFindings([
      { path: "a.md", check: "unknown_type", severity: "warning", message: 'type "Entity"', handler: "doctor" },
      { path: "b.md", check: "unknown_type", severity: "warning", message: 'type "Entity"', handler: "doctor" },
      { path: "c.md", check: "unknown_type", severity: "warning", message: 'type "Topic"', handler: "doctor" },
      { path: "d.md", check: "missing_type", severity: "error", message: "no type", handler: "auto" },
      { path: "e.md", check: "nonstandard_field", severity: "warning", message: "x", handler: "auto" },
      { path: "f.md", check: "nonstandard_field", severity: "warning", message: "y", handler: "doctor" },
    ]);
    expect(groups.map((g) => g.check)).toEqual(["missing_type", "unknown_type", "nonstandard_field"]);
    expect(groups[1]!.messages.map((m) => [m.message, m.findings.length])).toEqual([
      ['type "Entity"', 2],
      ['type "Topic"', 1],
    ]);
    expect(groups.map((g) => g.handler)).toEqual(["auto", "doctor", "mixed"]);
  });
});

describe("checkLabel", () => {
  it("says what a check finds, and falls back to its name", () => {
    expect(checkLabel("orphan")).toBe("Pages nothing links to");
    expect(checkLabel("some_new_check")).toBe("Some new check");
  });
});

describe("trendOf", () => {
  const now = Date.parse("2026-10-10T12:00:00Z");
  const at = (daysAgo: number, total: number) => ({ at: new Date(now - daysAgo * 86_400_000).toISOString(), total });
  it("says nothing with fewer than two samples in the week", () => {
    expect(trendOf([], now)).toBeNull();
    expect(trendOf([at(2, 9)], now)).toBeNull();
    expect(trendOf([at(30, 99), at(2, 9)], now)).toBeNull();
  });
  it("compares the first and last sample of the last 7 days", () => {
    expect(trendOf([at(30, 300), at(6, 188), at(3, 90), at(0, 40)], now)).toEqual({
      from: 188,
      to: 40,
      span: "this week",
      points: [188, 90, 40],
    });
    expect(trendOf([at(2, 9), at(0, 4)], now)?.span).toBe("over 2 days");
  });
});

describe("filterReportByMap", () => {
  const report = {
    findings: [
      { path: "infra/a.md", check: "broken_link", severity: "error", message: "m" },
      { path: "infra-old/b.md", check: "orphan", severity: "warning", message: "m" },
    ],
    count: 2,
    total: 2,
    by_severity: { error: 1, warning: 1 },
    by_check: { broken_link: 1, orphan: 1 },
    severity_min: "info",
  };
  it("keeps the Map's folder and beneath, not a sibling sharing the prefix, and recounts", () => {
    const out = filterReportByMap(report, "infra")!;
    expect(out.findings.map((f) => f.path)).toEqual(["infra/a.md"]);
    expect(out.total).toBe(1);
    expect(out.by_severity).toEqual({ error: 1 });
    expect(filterReportByMap(report, null)).toBe(report);
  });
});
