import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Health } from "../components/Health";
import type { CheckCatalog, CollectionSummary, LintReport } from "../api/types";

/** The all-clear state of Health: a KB with no finding and no proposal still shows its page field and what was read. */

const clean: LintReport = {
  findings: [],
  count: 0,
  by_check: {},
};
const collections: CollectionSummary[] = [
  { name: "infra", title: "Infrastructure", kind: "folder", concepts: 3, expanded_concepts: 0 },
  { name: "notes", title: "Notes", kind: "folder", concepts: 2, expanded_concepts: 0 },
];
// Three families; one rule of "links" cannot run here (D371), so it is read by 4 of the 5 rules.
const checks: CheckCatalog = {
  categories: ["pages", "links", "maps"],
  checks: [
    { name: "missing_title", category: "pages", fixable: true, auto: false },
    { name: "empty_body", category: "pages", fixable: false, auto: false },
    { name: "broken_link", category: "links", fixable: false, auto: false },
    { name: "orphan", category: "links", fixable: false, auto: false, active: false, reason: "no graph" },
    { name: "map_without_index", category: "maps", fixable: true, auto: true },
  ],
};

function page(extra: { checks?: CheckCatalog | null; hmap?: string | null } = {}) {
  const hmap = extra.hmap ?? null;
  return (
    <Health
      report={clean}
      allFindings={[]}
      checks={extra.checks === undefined ? checks : extra.checks}
      contradictions={[]}
      collections={collections}
      hmap={hmap}
      scopeTitle={hmap ? "Notes" : null}
      loading={false}
      error={null}
      onReveal={() => {}}
      onRetry={() => {}}
    />
  );
}

describe("Health with nothing to fix", () => {
  it("says what was read: the headline, and the page and rule counts under it", () => {
    render(page());
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Every page passes every check");
    expect(screen.queryByText(/No issues found/)).toBeNull();
    // 5 pages, and 4 rules: the one that cannot run here is no pass.
    expect(document.querySelector(".health__lede")).toHaveTextContent(
      "5 pages read against 4 rules, and none of them found anything.",
    );
  });

  it("keeps the page field: one square per page, every map, and the legend down to the fine key", () => {
    const { container } = render(page());
    const field = screen.getByRole("group", { name: "Pages by Map" });
    expect(field).toHaveAttribute("data-clear");
    expect(within(field).getByRole("button", { name: "Infrastructure" })).toBeInTheDocument();
    expect(within(field).getByRole("button", { name: "Notes" })).toBeInTheDocument();
    expect(field.querySelectorAll(".pfield__dot")).toHaveLength(5);
    // all of them "fine": none carries a problem or a suggestion
    expect(field.querySelectorAll(".pfield__dot[data-tone]")).toHaveLength(0);
    const legend = container.querySelector(".pfield__legend")!;
    expect(legend).toHaveTextContent("fine");
    expect(legend).not.toHaveTextContent("a problem");
    expect(legend).not.toHaveTextContent("a suggestion");
    expect(legend.querySelectorAll(".pfield__key")).toHaveLength(1);
  });

  it("lists one row per rule family with its count of rules, instead of one line", () => {
    render(page());
    expect(screen.queryByText("This KB passes every check.")).toBeNull();
    const rows = within(screen.getByRole("list", { name: "Rule families" })).getAllByRole("listitem");
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent("Pages");
    expect(rows[0]).toHaveTextContent("2 rules, all clear");
    // the rule that cannot run here is not counted
    expect(rows[1]).toHaveTextContent("Links");
    expect(rows[1]).toHaveTextContent("1 rule, all clear");
    expect(rows[2]).toHaveTextContent("Maps");
    expect(rows[2]).toHaveTextContent("1 rule, all clear");
  });

  it("opens What it checks from a family row", () => {
    render(page());
    fireEvent.click(screen.getByRole("button", { name: /Links/ }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("What Cartographer checks");
    expect(document.getElementById("health-coverage")).not.toBeNull();
  });

  it("keeps the sections below as direct children of the page, where the spacing rule reaches them", () => {
    const { container } = render(page());
    const section = container.querySelector(".health > .page-section");
    expect(section).not.toBeNull();
    expect(within(section as HTMLElement).getByRole("heading", { name: /Contradictions/ })).toBeInTheDocument();
  });

  it("counts the pages of the chosen map, and says which", () => {
    render(page({ hmap: "notes" }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Every page passes every check in Notes");
    expect(document.querySelector(".health__lede")).toHaveTextContent("2 pages read against 4 rules");
  });

  it("falls back to the one line when the rules did not arrive, and still keeps the field", () => {
    render(page({ checks: null }));
    expect(screen.getByText("This KB passes every check.")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Rule families" })).toBeNull();
    expect(screen.getByRole("group", { name: "Pages by Map" }).querySelectorAll(".pfield__dot")).toHaveLength(5);
    expect(document.querySelector(".health__lede")).toHaveTextContent("5 pages read against fixed rules");
  });
});
