import { checkLabel, filterReportByMap, groupFindings } from "./health";

describe("groupFindings", () => {
  it("folds one cause on many pages into one group, the largest first", () => {
    const groups = groupFindings([
      { path: "a.md", check: "unknown_type", message: 'type "Entity"', handler: "doctor" },
      { path: "b.md", check: "unknown_type", message: 'type "Entity"', handler: "doctor" },
      { path: "c.md", check: "unknown_type", message: 'type "Topic"', handler: "doctor" },
      { path: "d.md", check: "missing_type", message: "no type", handler: "auto" },
      { path: "e.md", check: "nonstandard_field", message: "x", handler: "auto" },
      { path: "f.md", check: "nonstandard_field", message: "y", handler: "doctor" },
    ]);
    // No group outranks another (D947): the count orders them, then the name.
    expect(groups.map((g) => g.check)).toEqual(["unknown_type", "nonstandard_field", "missing_type"]);
    expect(groups[0]!.messages.map((m) => [m.message, m.findings.length])).toEqual([
      ['type "Entity"', 2],
      ['type "Topic"', 1],
    ]);
    expect(groups.map((g) => g.handler)).toEqual(["doctor", "mixed", "auto"]);
  });
});

describe("checkLabel", () => {
  it("says what a check finds, and falls back to its name", () => {
    expect(checkLabel("orphan")).toBe("Pages nothing links to");
    expect(checkLabel("some_new_check")).toBe("Some new check");
  });
});

describe("filterReportByMap", () => {
  const report = {
    findings: [
      { path: "infra/a.md", check: "broken_link", message: "m" },
      { path: "infra-old/b.md", check: "orphan", message: "m" },
    ],
    count: 2,
    by_check: { broken_link: 1, orphan: 1 },
  };
  it("keeps the Map's folder and beneath, not a sibling sharing the prefix, and recounts", () => {
    const out = filterReportByMap(report, "infra")!;
    expect(out.findings.map((f) => f.path)).toEqual(["infra/a.md"]);
    expect(out.count).toBe(1);
    expect(out.by_check).toEqual({ broken_link: 1 });
    expect(filterReportByMap(report, null)).toBe(report);
  });
});
