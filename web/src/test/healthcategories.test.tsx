import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Health } from "../components/Health";
import type { CheckCatalog, CollectionSummary, LintReport } from "../api/types";

/** Health groups its rows by the category of their check, and paints each page with the categories of its problems. */

const collections: CollectionSummary[] = [
  { name: "infra", title: "Infrastructure", kind: "folder", concepts: 6, expanded_concepts: 0 },
];
const checks: CheckCatalog = {
  categories: ["pages", "templates", "links", "maps"],
  checks: [
    { name: "missing_title", category: "pages", fixable: false, auto: false },
    { name: "stale_claim", category: "pages", fixable: false, auto: false },
    { name: "template_missing", category: "templates", fixable: false, auto: false },
    { name: "broken_link", category: "links", fixable: false, auto: false },
    { name: "index_incomplete", category: "maps", fixable: false, auto: false },
  ],
};

const f = (concept: string, check: string, message = "m") => ({
  path: `${concept}.md`,
  concept,
  check,
  message,
  handler: "doctor" as const,
});

const report: LintReport = {
  findings: [
    // links: the largest row of the report, but its category comes after "pages" in the catalog
    f("infra/a", "broken_link"),
    f("infra/b", "broken_link"),
    f("infra/c", "broken_link"),
    // pages: two rows, "stale_claim" on 2 pages, "missing_title" on 1
    f("infra/a", "missing_title"),
    f("infra/d", "stale_claim"),
    f("infra/e", "stale_claim"),
    // a check the catalog does not list
    f("infra/e", "brand_new_check"),
  ],
  count: 7,
  by_check: { broken_link: 3, missing_title: 1, stale_claim: 2, brand_new_check: 1 },
};

function page(props: { report?: LintReport; checks?: CheckCatalog | null } = {}) {
  const r = props.report ?? report;
  return (
    <Health
      report={r}
      allFindings={r.findings}
      checks={props.checks === undefined ? checks : props.checks}
      collections={collections}
      scopeTitle={null}
      loading={false}
      error={null}
      onReveal={() => {}}
      onRetry={() => {}}
    />
  );
}

const dot = (container: HTMLElement, id: string) => container.querySelector<HTMLElement>(`.pfield__dot[title="${id}"]`)!;

describe("Health's categories", () => {
  it("has no Problems band: one heading per category with findings, in catalog order, other last", () => {
    render(page());
    expect(screen.queryByRole("heading", { name: "Problems" })).toBeNull();
    const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(headings).toEqual(["Pages", "Links", "Other"]);
    // each heading carries its count of kinds
    const pages = screen.getByRole("group", { name: "Pages" });
    expect(pages.querySelector(".agenda__n")).toHaveTextContent("2");
    expect(screen.getByRole("group", { name: "Links" }).querySelector(".agenda__n")).toHaveTextContent("1");
    expect(screen.getByRole("group", { name: "Other" }).querySelector(".agenda__n")).toHaveTextContent("1");
    // only the first category opens by default; the others fold under their heading
    expect(pages).toHaveAttribute("open");
    expect(screen.getByRole("group", { name: "Links" })).not.toHaveAttribute("open");
    expect(screen.getByRole("group", { name: "Other" })).not.toHaveAttribute("open");
  });

  it("lists a category's rows largest first, and colours their bars with the category", () => {
    render(page());
    const labels = (group: string) =>
      within(screen.getByRole("group", { name: group }))
        .getAllByRole("listitem")
        .filter((li) => li.classList.contains("arow-item"))
        .map((li) => li.querySelector(".arow__label")?.textContent);
    expect(labels("Pages")).toEqual(["Claims past their date", "Pages without a title"]);
    expect(labels("Links")).toEqual(["Broken links"]);
    expect(labels("Other")).toEqual(["Brand new check"]);
    expect(screen.getByRole("group", { name: "Pages" })).toHaveStyle({ "--tone": "var(--hue-2)" });
    expect(screen.getByRole("group", { name: "Links" })).toHaveStyle({ "--tone": "var(--hue-4)" });
    expect(screen.getByRole("group", { name: "Other" })).toHaveStyle({ "--tone": "var(--text-muted)" });
  });

  it("puts every finding in Other when the catalog is missing", () => {
    render(page({ checks: null }));
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(["Other"]);
    const rows = within(screen.getByRole("group", { name: "Other" })).getAllByRole("listitem");
    expect(rows.filter((li) => li.classList.contains("arow-item"))).toHaveLength(4);
  });

  it("paints a page with the colours of the categories of its problems", () => {
    const { container } = render(page());
    // infra/a: links + pages -> two halves, the catalog's order (pages first)
    const a = dot(container, "infra/a");
    expect(a).toHaveAttribute("data-n", "2");
    expect(a.style.getPropertyValue("--c1")).toBe("var(--hue-2)");
    expect(a.style.getPropertyValue("--c2")).toBe("var(--hue-4)");
    // infra/b: links only -> solid
    const b = dot(container, "infra/b");
    expect(b).toHaveAttribute("data-n", "1");
    expect(b.style.getPropertyValue("--c1")).toBe("var(--hue-4)");
    expect(b.style.getPropertyValue("--c2")).toBe("");
    // infra/e: pages + other -> two, "other" last and muted
    const e = dot(container, "infra/e");
    expect(e).toHaveAttribute("data-n", "2");
    expect(e.style.getPropertyValue("--c2")).toBe("var(--text-muted)");
    // a page with no problem stays plain
    expect(container.querySelectorAll(".pfield__dot:not([data-page])")).toHaveLength(1);
  });

  it("caps a square at four colours, the first four in catalog order", () => {
    const many: LintReport = {
      findings: [
        f("infra/a", "index_incomplete"),
        f("infra/a", "broken_link"),
        f("infra/a", "template_missing"),
        f("infra/a", "missing_title"),
        f("infra/a", "brand_new_check"),
      ],
      count: 5,
      by_check: {},
    };
    const { container } = render(page({ report: many }));
    const a = dot(container, "infra/a");
    expect(a).toHaveAttribute("data-n", "4");
    expect(["--c1", "--c2", "--c3", "--c4"].map((p) => a.style.getPropertyValue(p))).toEqual([
      "var(--hue-2)",
      "var(--hue-8)",
      "var(--hue-4)",
      "var(--hue-7)",
    ]);
    expect(a.style.getPropertyValue("--c5")).toBe("");
  });

  it("names the categories present in the legend, then fine", () => {
    const { container } = render(page());
    const legend = container.querySelector(".pfield__legend")!;
    const items = [...legend.querySelectorAll(".pfield__item")].map((i) => i.textContent?.trim());
    expect(items).toEqual(["Pages", "Links", "Other", "fine"]);
    expect(legend).toHaveTextContent("one square per page");
    expect(legend).not.toHaveTextContent("has problems");
  });

  it("lights a row's pages in the row's category colour only", async () => {
    const user = userEvent.setup();
    const { container } = render(page());
    await user.hover(screen.getByText("Broken links"));
    expect(container.querySelector(".pfield")).toHaveAttribute("data-focus", "problem");
    const a = dot(container, "infra/a");
    expect(a).toHaveAttribute("data-lit");
    expect(a).toHaveAttribute("data-n", "1");
    expect(a.style.getPropertyValue("--c1")).toBe("var(--hue-4)");
    expect(dot(container, "infra/d")).not.toHaveAttribute("data-lit");
    await user.unhover(screen.getByText("Broken links"));
    expect(dot(container, "infra/a")).toHaveAttribute("data-n", "2");
  });
});
