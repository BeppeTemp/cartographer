import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CommandPalette } from "../components/CommandPalette";
import { NodeList } from "../components/NodeList";
import { LeftRail } from "../components/LeftRail";
import { Health } from "../components/Health";
import { json, stubApi } from "./fixtures";
import type { GraphNode, KBStatus, LintReport, MaintenanceSummary, Overview } from "../api/types";

const nodes: GraphNode[] = [
  { id: "infra/gateway", collection: "infra", type: "Service", in_degree: 4, out_degree: 1 },
  { id: "infra/runbook", collection: "infra", type: "Runbook", in_degree: 0, out_degree: 2 },
  { id: "notes/meeting", collection: "notes", type: "Note", in_degree: 1, out_degree: 0 },
];

describe("command palette", () => {
  it("is fully navigable from the keyboard", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<CommandPalette open nodes={nodes} onClose={vi.fn()} onSelect={onSelect} />);

    const input = screen.getByRole("textbox", { name: /search concepts/i });
    await user.type(input, "infra");

    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(2);
    expect(options[0]).toHaveAttribute("aria-selected", "true");

    await user.keyboard("{ArrowDown}");
    expect(screen.getAllByRole("option")[1]).toHaveAttribute("aria-selected", "true");

    await user.keyboard("{Enter}");
    expect(onSelect).toHaveBeenCalledWith("infra/runbook");
  });

  it("highlights the matched span rather than only filtering", async () => {
    const user = userEvent.setup();
    render(<CommandPalette open nodes={nodes} onClose={vi.fn()} onSelect={vi.fn()} />);
    await user.type(screen.getByRole("textbox", { name: /search concepts/i }), "gate");
    expect(screen.getByText("gate", { selector: "mark" })).toBeInTheDocument();
  });

  it("dismisses on Escape without selecting", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const onSelect = vi.fn();
    render(<CommandPalette open nodes={nodes} onClose={onClose} onSelect={onSelect} />);
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("says so when nothing matches instead of showing an empty box", async () => {
    const user = userEvent.setup();
    render(<CommandPalette open nodes={nodes} onClose={vi.fn()} onSelect={vi.fn()} />);
    await user.type(screen.getByRole("textbox", { name: /search concepts/i }), "zzz");
    expect(screen.getByText(/no concept matches/i)).toBeInTheDocument();
  });

  it("puts a secret phrase's row on top and keeps a concept named like it reachable", async () => {
    const onEgg = vi.fn();
    const onSelect = vi.fn();
    const user = userEvent.setup();
    const named: GraphNode[] = [...nodes, { id: "notes/glass", title: "Liquid glass", collection: "notes", type: "Note", in_degree: 0, out_degree: 0 }];
    const { unmount } = render(<CommandPalette open nodes={named} onClose={vi.fn()} onSelect={onSelect} onEgg={onEgg} />);
    const input = screen.getByRole("textbox", { name: /search concepts/i });
    await user.type(input, "liquid glass");
    expect(screen.getAllByRole("option")).toHaveLength(2);
    await user.keyboard("{ArrowDown}{Enter}");
    expect(onSelect).toHaveBeenCalledWith("notes/glass");
    expect(onEgg).not.toHaveBeenCalled();
    unmount();

    render(<CommandPalette open nodes={named} onClose={vi.fn()} onSelect={onSelect} onEgg={onEgg} />);
    await user.type(screen.getByRole("textbox", { name: /search concepts/i }), "liquid glass");
    expect(screen.getByRole("textbox", { name: /search concepts/i })).toHaveAttribute("aria-activedescendant", "palette-option-0");
    await user.keyboard("{Enter}");
    expect(onEgg).toHaveBeenCalledWith(["glass"]);
  });

  it("survives a full-text search that finds nothing (the server sends results: null)", async () => {
    const fetchMock = stubApi({ "/search": () => json({ query: "zzz", count: 0, results: null }) });
    const user = userEvent.setup();
    render(<CommandPalette open kb="kb-a" nodes={nodes} onClose={vi.fn()} onSelect={vi.fn()} />);
    await user.type(screen.getByRole("textbox", { name: /search concepts/i }), "zzz");
    await waitFor(() => expect(fetchMock.mock.calls.some(([u]) => String(u).includes("/search"))).toBe(true));
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.getByText(/no concept matches/i)).toBeInTheDocument();
    vi.unstubAllGlobals();
  });
});

describe("node list", () => {
  it("exposes the same selection action the canvas does", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<NodeList nodes={nodes} selected={null} onSelect={onSelect} />);

    await user.click(screen.getByRole("button", { name: /notes\/meeting/ }));
    expect(onSelect).toHaveBeenCalledWith("notes/meeting");
  });

  it("marks the selected concept for assistive technology", () => {
    render(<NodeList nodes={nodes} selected="infra/gateway" onSelect={vi.fn()} />);
    expect(screen.getByRole("button", { name: /infra\/gateway/ })).toHaveAttribute(
      "aria-current",
      "true",
    );
  });

  it("re-sorts without losing any node", async () => {
    const user = userEvent.setup();
    render(<NodeList nodes={nodes} selected={null} onSelect={vi.fn()} />);
    await user.selectOptions(screen.getByRole("combobox", { name: /sort concepts by/i }), "degree");
    const items = screen.getAllByRole("button");
    expect(items).toHaveLength(nodes.length);
    expect(items[0]).toHaveAccessibleName(/infra\/gateway/);
  });
});

const report: LintReport = {
  findings: [
    { path: "infra/gateway.md", concept: "infra/gateway", check: "broken_link", message: "target missing", handler: "doctor" },
    { path: "maps/infra", check: "index_incomplete", message: "index is stale", handler: "doctor" },
    { path: "infra/a.md", concept: "infra/a", check: "nonstandard_field", message: "field aggiornato", handler: "auto" },
    { path: "infra/b.md", concept: "infra/b", check: "nonstandard_field", message: "field aggiornato", handler: "auto" },
    { path: "infra/c.md", concept: "infra/c", check: "title_quality", message: "title is long", handler: "doctor" },
  ],
  count: 5,
  by_check: { broken_link: 1, index_incomplete: 1, nonstandard_field: 2, title_quality: 1 },
};

const upkeep: MaintenanceSummary = {
  auto_repair: { enabled: true, default: true, checks: ["nonstandard_field"], interval_days: 1 },
  last_auto_repair: null,
  doctor_interval_days: 1,
  repairs: [],
};

const catalog = {
  categories: ["pages", "links"],
  checks: [
    { name: "nonstandard_field", category: "pages", fixable: true, auto: true },
    { name: "machine_path", category: "pages", fixable: false, auto: false },
    { name: "broken_link", category: "links", fixable: true, auto: false },
    { name: "orphan", category: "links", fixable: false, auto: false },
  ],
};

/** Health with neutral defaults: the tests only change what they are about. */
function health(
  props: Partial<{
    report: LintReport;
    scopeTitle: string | null;
    status: KBStatus | null;
    summary: MaintenanceSummary | null;
    checks: typeof catalog | null;
    collections: { name: string; title: string; kind: string; concepts: number; expanded_concepts: number }[];
    onReveal: (concept: string | null, message: string) => void;
  }> = {},
) {
  return (
    <Health
      report={props.report ?? report}
      status={props.status ?? null}
      summary={props.summary ?? null}
      checks={"checks" in props ? props.checks : catalog}
      collections={props.collections}
      allFindings={(props.report ?? report).findings}
      scopeTitle={props.scopeTitle ?? null}
      loading={false}
      error={null}
      onReveal={props.onReveal ?? vi.fn()}
      onRetry={vi.fn()}
    />
  );
}

const rowLabels = (group: string) =>
  within(screen.getByRole("group", { name: group }))
    .getAllByRole("listitem")
    .filter((li) => li.classList.contains("arow-item"))
    .map((li) => li.querySelector(".arow__label")?.textContent);

describe("health issues", () => {
  // What was found, one row per kind (D704): the headline counts kinds, the line under it occurrences.
  it("counts kinds in the headline and every occurrence under it", () => {
    render(health({ collections: [{ name: "infra", title: "Infrastructure", kind: "map", concepts: 5, expanded_concepts: 0 }] }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Cartographer found 4 kinds of issues");
    expect(screen.getByText(/^5 occurrences in all/)).toBeInTheDocument();
  });

  // A lint finding is a problem (D947): one band, the heaviest row first, and no
  // row ranks above another for being "broken" or "a suggestion".
  it("lists every finding as a problem, the heaviest first, in one band", () => {
    render(health());
    expect(rowLabels("Problems").map((l) => l?.replace(/\s+/g, " ").trim())).toEqual([
      "Non-standard field names fixes itself",
      "Broken links",
      "Indexes missing pages",
      "Titles to tighten",
    ]);
    expect(screen.queryByRole("group", { name: "Suggestions" })).not.toBeInTheDocument();
    expect(screen.queryByText("broken")).not.toBeInTheDocument();
  });

  it("opens a row to what it is, how to fix it, and the page", async () => {
    const user = userEvent.setup();
    const onReveal = vi.fn();
    render(health({ onReveal }));
    await user.click(screen.getByText("Broken links"));
    const row = document.getElementById("check-broken_link")!;
    expect(within(row).getByText("What it is")).toBeInTheDocument();
    expect(within(row).getByText("To fix")).toBeInTheDocument();
    await user.click(within(row).getByRole("button", { name: "gateway" }));
    expect(onReveal).toHaveBeenCalledWith("infra/gateway", "");
  });

  it("names a file that is no page instead of offering to open it", async () => {
    const user = userEvent.setup();
    render(health());
    await user.click(screen.getByText("Indexes missing pages"));
    expect(screen.getByText("maps/infra")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /maps\/infra/ })).not.toBeInTheDocument();
  });

  it("says nothing needs doing for what Cartographer fixes by itself", async () => {
    const user = userEvent.setup();
    render(health());
    await user.click(screen.getByText("Non-standard field names"));
    expect(screen.getByText(/Nothing to do: Cartographer fixes it by itself/)).toBeInTheDocument();
  });

  // The doctor's proposals (kb_review) and the observations are decisions, not
  // problems: Health shows lint findings only (D947).
  it("shows only lint problems: nothing of the doctor's list", () => {
    render(health());
    expect(screen.queryByRole("group", { name: "Suggestions" })).not.toBeInTheDocument();
    expect(screen.queryByText("Templates to adopt")).not.toBeInTheDocument();
    expect(screen.queryByText("Terms nobody defines")).not.toBeInTheDocument();
  });

  it("leaves out what a narrowed principal cannot read, and keeps the issues", () => {
    render(health());
    expect(screen.queryByRole("heading", { name: /^What the KB doesn.t know/ })).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Problems" })).toBeInTheDocument();
  });
});

describe("health page field", () => {
  const collections = [{ name: "infra", title: "Infrastructure", kind: "map", concepts: 5, expanded_concepts: 0 }];
  const both: LintReport = {
    ...report,
    findings: [
      ...report.findings,
      { path: "infra/gateway.md", concept: "infra/gateway", check: "title_quality", message: "long", handler: "doctor" },
    ],
  };

  it("marks a page that has a problem in one colour, and leaves the clean ones plain", () => {
    const { container } = render(health({ report: both, collections }));
    expect(container.querySelector('.pfield__dot[title="infra/gateway"]')).toHaveAttribute("data-problem");
    expect(container.querySelector('.pfield__dot[title="infra/c"]')).toHaveAttribute("data-problem");
    expect(container.querySelector("[data-both]")).toBeNull();
    expect(container.querySelectorAll(".pfield__dot:not([data-page])")).toHaveLength(1);
  });

  it("names its two states: has problems, fine", () => {
    const { container } = render(health({ report: both, collections }));
    expect(container.querySelector(".pfield__legend")).toHaveTextContent("has problems");
    expect(container.querySelector(".pfield__legend")).toHaveTextContent("fine");
    expect(container.querySelector(".pfield__legend")).not.toHaveTextContent(/suggestion|both/);
  });

  it("lights the pages of the row under the pointer", async () => {
    const user = userEvent.setup();
    const { container } = render(health({ report: both, collections }));
    await user.hover(screen.getByText("Broken links"));
    expect(container.querySelector(".pfield")).toHaveAttribute("data-focus", "problem");
    expect(container.querySelector('.pfield__dot[title="infra/gateway"]')).toHaveAttribute("data-lit");
    expect(container.querySelector('.pfield__dot[title="infra/c"]')).not.toHaveAttribute("data-lit");
    await user.hover(screen.getByText("Titles to tighten"));
    expect(container.querySelector('.pfield__dot[title="infra/c"]')).toHaveAttribute("data-lit");
    expect(container.querySelector('.pfield__dot[title="infra/gateway"]')).toHaveAttribute("data-lit");
  });
});

describe("health: what it checks", () => {
  const day = (daysAgo: number, total: number) => ({
    at: new Date(Date.now() - daysAgo * 86_400_000).toISOString(),
    total,
    by_handler: {},
  });

  it("explains how checking works, folds every category, and lists each rule with its sentence", async () => {
    const user = userEvent.setup();
    render(health({ checks: catalog }));
    await user.click(screen.getByRole("button", { name: "What it checks" }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("What Cartographer checks");
    expect(screen.getByRole("list", { name: "How checking works" })).toBeInTheDocument();
    expect(screen.getByText(/In this KB:/)).toHaveTextContent("2 rules pass · 2 found something");
    const groups = document.querySelectorAll("details.rules__group");
    expect([...groups].every((d) => !(d as HTMLDetailsElement).open)).toBe(true);
    const machine = screen.getByText("Paths of one machine in the text").closest("li")!;
    expect(machine).toHaveTextContent("0");
    expect(machine).not.toHaveAttribute("data-found");
    expect(screen.getByText("Non-standard field names", { selector: ".rule__name" }).closest("li")).toHaveTextContent(
      "automatic",
    );
  });

  it("greys a rule with nothing to read here, with its reason, instead of a zero that reads as clean (D371)", async () => {
    const user = userEvent.setup();
    render(
      health({
        checks: {
          categories: ["links"],
          checks: [
            { name: "forbidden_term", category: "links", fixable: false, auto: false, active: false, reason: "glossary.yaml declares no forbidden term" } as (typeof catalog)["checks"][number],
          ],
        },
      }),
    );
    await user.click(screen.getByRole("button", { name: "What it checks" }));
    const idle = screen.getByText("Terms the map forbids", { selector: ".rule__name" }).closest("li")!;
    expect(idle).toHaveAttribute("data-inactive");
    expect(idle).toHaveAttribute("title", "glossary.yaml declares no forbidden term");
    expect(idle).not.toHaveTextContent(/\b0\b/);
    expect(screen.getByText(/In this KB:/)).toHaveTextContent("1 have nothing to read here");
  });

  it("shows the week's movement, and nothing with one sample", async () => {
    const user = userEvent.setup();
    const { unmount } = render(health({ checks: catalog, summary: { ...upkeep, lint_history: [day(0, 40)] } }));
    await user.click(screen.getByRole("button", { name: "What it checks" }));
    expect(screen.queryByRole("heading", { name: /^Trend/ })).not.toBeInTheDocument();
    unmount();
    render(health({ checks: catalog, summary: { ...upkeep, lint_history: [day(6, 188), day(0, 40)] } }));
    await user.click(screen.getByRole("button", { name: "What it checks" }));
    expect(screen.getByText("188 → 40 this week")).toBeInTheDocument();
  });
});

describe("health knowledge", () => {
  it("marks a miss whose search now finds something as resolved", () => {
    render(
      health({
        status: {
          search_misses: [
            { query: "zephyr", count: 1 },
            { query: "kafka", count: 3, resolved: true },
          ],
        },
      }),
    );
    const resolved = screen.getByText("kafka").closest("li");
    expect(resolved).toHaveClass("chip--resolved");
    expect(resolved).toHaveTextContent("(now found)");
    expect(screen.getByText("zephyr").closest("li")).not.toHaveClass("chip--resolved");
  });

  it("leaves the section out when the KB knows what it is asked", () => {
    render(health({ status: { open_gaps: { total: 0 }, search_misses: [], stale_count: 0 } }));
    expect(screen.queryByRole("heading", { name: /^What the KB doesn.t know/ })).not.toBeInTheDocument();
  });

  // Open questions are what the KB does not know, whoever asked them: listed
  // apart from the issues, never as a step of upkeep (D704).
  it("lists an open question apart from the issues and opens its concept", async () => {
    const user = userEvent.setup();
    const onReveal = vi.fn();
    render(
      health({
        onReveal,
        status: { open_gaps: { total: 1, recent: [{ id: "ops/q1", title: "Which port does the proxy listen on?", kind: "open_question" }] } },
      }),
    );
    expect(screen.getByRole("heading", { name: /^What the KB doesn.t know/ })).toBeInTheDocument();
    await user.click(screen.getByText("Which port does the proxy listen on?"));
    expect(onReveal).toHaveBeenCalledWith("ops/q1", "");
  });
});

describe("health scope", () => {
  it("names the Map it is scoped to in the headline", () => {
    render(health({ scopeTitle: "Infrastructure" }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Cartographer found 4 kinds of issues in Infrastructure");
  });

  it("does not let a clean Map read as a clean KB", () => {
    const clean: LintReport = { ...report, findings: [], count: 0, by_check: {} };
    render(health({ report: clean, scopeTitle: "Infrastructure" }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("No issues found in Infrastructure");
    expect(screen.queryByText(/This KB passes/)).not.toBeInTheDocument();
    expect(screen.getByText("Infrastructure passes every check.")).toBeInTheDocument();
  });
});

describe("left rail outside the Atlas", () => {
  const overview = {
    concepts: { total: 3, by_type: { Service: 2, Note: 1 }, by_status: { draft: 1 } },
    collections: [{ name: "infra", title: "Infrastructure", kind: "map", concepts: 2 }],
    lint: { total: 0 },
  } as unknown as Overview;
  const rail = (panel: "atlas" | "health" | "artifacts") => (
    <LeftRail
      overview={overview}
      scope={null}
      panel={panel}
      artifactsTotal={null}
      collapsed={false}
      typeFilter={new Set(["Service"])}
      statusFilter={new Set()}
      onScope={vi.fn()}
      onPanel={vi.fn()}
      onToggleCollapsed={vi.fn()}
      onToggleType={vi.fn()}
      onToggleStatus={vi.fn()}
      onClearFilters={vi.fn()}
    />
  );

  it("keeps the Maps, which scope the findings, and hides the node filters", () => {
    render(rail("health"));
    expect(screen.getByRole("button", { name: /Infrastructure/ })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Type" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Status" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Clear 1 filter/ })).not.toBeInTheDocument();
  });

  // Artifacts lists files: nothing there reads the concept filters (#436).
  it("hides the node filters on the Artifacts panel", () => {
    render(rail("artifacts"));
    expect(screen.queryByRole("heading", { name: "Type" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Status" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Clear 1 filter/ })).not.toBeInTheDocument();
  });

  it("shows the node filters, selection intact, back in the Atlas", () => {
    render(rail("atlas"));
    expect(screen.getByRole("heading", { name: "Type" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Service(?!s)/ })).toHaveAttribute("aria-pressed", "true");
  });
});
