import {
  categoryHue,
  checkHelp,
  checkLabel,
  filterReportByMap,
  groupByCategory,
  groupFindings,
  OTHER,
  pageCategories,
} from "./health";

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

describe("artifact_unused", () => {
  it("has a label and says what it looks for (D1043)", () => {
    expect(checkLabel("artifact_unused")).toBe("Unused skills and agents");
    expect(checkHelp("artifact_unused")).toMatch(/usage_stale_days/);
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

describe("categories", () => {
  const catalog = {
    categories: ["pages", "links"],
    checks: [
      { name: "missing_title", category: "pages", fixable: false, auto: false },
      { name: "broken_link", category: "links", fixable: false, auto: false },
    ],
  };
  const f = (concept: string, check: string) => ({ path: `${concept}.md`, concept, check, message: "m" });

  it("gives each category a fixed hue, and the neutral colour to the others", () => {
    expect(categoryHue("pages")).toBe("var(--hue-2)");
    expect(categoryHue("kb")).toBe("var(--hue-3)");
    expect(categoryHue(OTHER)).toBe("var(--text-muted)");
    expect(categoryHue("something_new")).toBe("var(--text-muted)");
    // eight categories, eight different slots, all inside the ten hues the theme names for them
    const slots = ["pages", "templates", "validity", "links", "values", "maps", "artifacts", "kb"].map(categoryHue);
    expect(new Set(slots).size).toBe(8);
    for (const s of slots) expect(Number(/--hue-(\d+)/.exec(s)![1])).toBeLessThanOrEqual(10);
  });

  it("orders groups by the catalog, the unlisted checks last, and each category's rows by pages touched", () => {
    const groups = groupFindings([
      f("a", "broken_link"),
      f("b", "broken_link"),
      f("c", "missing_title"),
      f("c", "mystery"),
    ]);
    const out = groupByCategory(groups, catalog);
    expect(out.map((g) => [g.category, g.rows.map((r) => r.check)])).toEqual([
      ["pages", ["missing_title"]],
      ["links", ["broken_link"]],
      [OTHER, ["mystery"]],
    ]);
    expect(groupByCategory(groups, null).map((g) => g.category)).toEqual([OTHER]);
  });

  it("lists a page's categories in catalog order, other last", () => {
    const out = pageCategories(
      [f("p", "mystery"), f("p", "broken_link"), f("p", "missing_title"), f("q", "broken_link")],
      catalog,
    );
    expect(out.get("p")).toEqual(["pages", "links", OTHER]);
    expect(out.get("q")).toEqual(["links"]);
    expect(pageCategories([{ path: "maps/x", check: "index_incomplete", message: "m" }], catalog).size).toBe(0);
  });
});
