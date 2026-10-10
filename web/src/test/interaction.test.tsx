import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CommandPalette } from "../components/CommandPalette";
import { NodeList } from "../components/NodeList";
import { LeftRail } from "../components/LeftRail";
import { Health } from "../components/Health";
import type { GraphNode, KBStatus, LintReport, MaintenanceQuestions, MaintenanceSummary, Overview } from "../api/types";

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
    { path: "infra/gateway.md", concept: "infra/gateway", check: "broken_link", severity: "error", message: "target missing", handler: "doctor" },
    { path: "maps/infra", check: "index_incomplete", severity: "warning", message: "index is stale", handler: "doctor" },
    { path: "infra/a.md", concept: "infra/a", check: "nonstandard_field", severity: "warning", message: "field aggiornato", handler: "auto" },
    { path: "infra/b.md", concept: "infra/b", check: "nonstandard_field", severity: "warning", message: "field aggiornato", handler: "auto" },
    { path: "infra/c.md", concept: "infra/c", check: "title_quality", severity: "info", message: "title is long", handler: "doctor" },
  ],
  count: 5,
  total: 5,
  by_severity: { error: 1, warning: 3, info: 1 },
  by_check: { broken_link: 1, index_incomplete: 1, nonstandard_field: 2, title_quality: 1 },
  severity_min: "info",
};

const upkeep: MaintenanceSummary = {
  auto_repair: { enabled: true, default: true, checks: ["nonstandard_field"], interval_days: 1 },
  last_auto_repair: null,
  doctor_interval_days: 1,
  doctor_mode: "unattended",
  repairs: [],
};

/** Health with neutral defaults: the findings tests only change what they are about. */
function health(
  props: Partial<{
    report: LintReport;
    scopeTitle: string | null;
    status: KBStatus | null;
    summary: MaintenanceSummary | null;
    questions: MaintenanceQuestions | null;
    onReveal: (concept: string | null, message: string) => void;
  }> = {},
) {
  return (
    <Health
      report={props.report ?? report}
      status={props.status ?? null}
      summary={props.summary ?? null}
      questions={props.questions ?? null}
      scopeTitle={props.scopeTitle ?? null}
      loading={false}
      error={null}
      onReveal={props.onReveal ?? vi.fn()}
      onOpen={vi.fn()}
      onRetry={vi.fn()}
    />
  );
}

describe("health findings", () => {
  it("groups the findings by cause and says who acts on each", () => {
    render(health());
    const problems = screen.getByRole("list", { name: "Findings by cause" });
    // One row per check, the worst first; the same message on two pages is one line.
    const rows = within(problems).getAllByRole("listitem").filter((li) => li.classList.contains("health__check"));
    expect(rows.map((r) => r.querySelector("code")?.textContent)).toEqual([
      "broken_link",
      "nonstandard_field",
      "index_incomplete",
      "title_quality",
    ]);
    expect(rows[1]).toHaveTextContent("Automatic");
    expect(rows[0]).toHaveTextContent("Doctor");
    expect(within(rows[1]!).getByText(/× 2/)).toBeInTheDocument();
  });

  it("counts an info finding as an improvement in the doctor's queue, not a pile of its own", () => {
    render(health({ report: { ...report, findings: report.findings.filter((f) => f.severity === "info") }, summary: upkeep }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("1 improvement waits for the doctor.");
    expect(screen.getByText(/\+ 1 improvement/)).toBeInTheDocument();
  });

  it("reveals the concept behind a finding", async () => {
    const user = userEvent.setup();
    const onReveal = vi.fn();
    render(health({ onReveal }));
    await user.click(screen.getByText("Broken links"));
    await user.click(screen.getByRole("button", { name: /infra\/gateway/ }));
    expect(onReveal).toHaveBeenCalledWith("infra/gateway", "");
  });

  it("explains that a finding with no concept has no node to reveal", async () => {
    const user = userEvent.setup();
    const onReveal = vi.fn();
    render(health({ onReveal }));
    await user.click(screen.getByText("Indexes missing pages"));
    await user.click(screen.getByRole("button", { name: /maps\/infra/ }));
    expect(onReveal).toHaveBeenCalledWith(null, expect.stringContaining("no node to reveal"));
  });

  it("carries severity as text, not colour alone", () => {
    render(health());
    const page = screen.getByRole("region", { name: "Health" });
    expect(within(page).getAllByText("error").length).toBeGreaterThan(0);
    expect(within(page).getAllByText("warning").length).toBeGreaterThan(0);
  });

  it("leaves out what a narrowed principal cannot read, and keeps the findings", () => {
    render(health());
    expect(screen.queryByRole("heading", { name: /^Done by Cartographer/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Knowledge" })).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /^Findings/ })).toBeInTheDocument();
  });
});

describe("health trend", () => {
  const day = (daysAgo: number, total: number) => ({
    at: new Date(Date.now() - daysAgo * 86_400_000).toISOString(),
    total,
    by_severity: {},
    by_handler: {},
  });
  it("shows the week's movement beside the upkeep log, and nothing with one sample", () => {
    const { unmount } = render(health({ summary: { ...upkeep, lint_history: [day(0, 40)] } }));
    expect(screen.queryByRole("heading", { name: /^Trend/ })).not.toBeInTheDocument();
    unmount();
    render(health({ summary: { ...upkeep, lint_history: [day(6, 188), day(0, 40)] } }));
    expect(screen.getByRole("heading", { name: /^Trend/ })).toBeInTheDocument();
    expect(screen.getByText("188 → 40 this week")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Findings over the last 2 samples/ })).toBeInTheDocument();
  });
});

describe("health coverage", () => {
  it("lists every check by category on a tab of its own, a zero too, and jumps to a check's findings", async () => {
    const user = userEvent.setup();
    render(
      <Health
        report={report}
        status={null}
        summary={null}
        checks={{
          categories: ["pages", "links"],
          checks: [
            { name: "nonstandard_field", category: "pages", severity: "warning", fixable: true, auto: true },
            { name: "machine_path", category: "pages", severity: "warning", fixable: false, auto: false },
            { name: "broken_link", category: "links", severity: "warning", fixable: true, auto: false },
            { name: "orphan", category: "links", severity: "warning", fixable: false, auto: false },
          ],
        }}
        questions={null}
        scopeTitle={null}
        loading={false}
        error={null}
        onReveal={vi.fn()}
        onOpen={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    expect(screen.queryByText("Paths of one machine in the text")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Checks · 4" }));
    const machine = screen.getByText("Paths of one machine in the text").closest("li")!;
    expect(machine).toHaveTextContent("0");
    expect(machine).not.toHaveAttribute("data-found");
    expect(screen.getByText("Non-standard field names", { selector: ".coverage__name" }).closest("li")).toHaveTextContent("automatic");
    expect(screen.getByText(/2 checks clean · 2 checks with findings · 1 fixed/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Broken links" }));
    expect(screen.getByRole("list", { name: "Findings by cause" })).toBeInTheDocument();
  });
});

describe("health coverage of a check that cannot run", () => {
  it("greys it out with its reason instead of showing a zero that would read as clean (D371)", async () => {
    const user = userEvent.setup();
    render(
      <Health
        report={report}
        status={null}
        summary={null}
        checks={{
          categories: ["pages", "links"],
          checks: [
            { name: "machine_path", category: "pages", severity: "warning", fixable: false, auto: false, active: true },
            { name: "forbidden_term", category: "links", severity: "warning", fixable: false, auto: false, active: false, reason: "glossary.yaml declares no forbidden term" },
            { name: "orphan", category: "links", severity: "warning", fixable: false, auto: false, active: true },
          ],
        }}
        questions={null}
        scopeTitle={null}
        loading={false}
        error={null}
        onReveal={vi.fn()}
        onOpen={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Checks · 3" }));
    const idle = screen.getByText("Terms the map forbids", { selector: ".coverage__name" }).closest("li")!;
    expect(idle).toHaveAttribute("data-inactive");
    expect(idle).toHaveAttribute("title", "glossary.yaml declares no forbidden term");
    expect(idle).toHaveTextContent("–");
    expect(idle).not.toHaveTextContent(/\b0\b/);
    expect(screen.getByText(/1 not checked here/)).toBeInTheDocument();
    expect(screen.getByText("all 1 checked clean")).toBeInTheDocument();
  });
});

describe("health verdict", () => {
  const warnings: LintReport = { ...report, findings: report.findings.filter((f) => f.severity !== "error") };

  it("says the problems wait for a doctor that never ran", () => {
    render(health({ report: warnings, summary: upkeep }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("1 problem waits for the doctor.");
    expect(screen.getByText("Starts when an agent next connects.")).toBeInTheDocument();
  });

  it("names the scheduled doctor session when a client declared one (D369)", () => {
    const next = new Date(Date.now() + 5 * 3600_000).toISOString();
    render(health({ report: warnings, summary: { ...upkeep, doctor_schedule: { client: "claude", next_run: next } } }));
    expect(screen.getByText(/^Next doctor session: /)).toBeInTheDocument();
    expect(screen.queryByText("Starts when an agent next connects.")).not.toBeInTheDocument();
  });

  it("says nothing needs the reader while the doctor keeps up", () => {
    const today = new Date().toISOString().slice(0, 10);
    render(health({ report: warnings, summary: { ...upkeep, last_doctor: today, next_doctor: today } }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Nothing needs you: Cartographer is on it.");
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

  it("says so in one line when the KB knows what it is asked", () => {
    render(health({ status: { open_gaps: { total: 0 }, search_misses: [], stale_count: 0 } }));
    expect(
      screen.getByText("Nothing missing: no open gap, no unanswered search, nothing past its review date."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /^Knowledge \d/ })).not.toBeInTheDocument();
  });
});

describe("health scope", () => {
  it("names the Map it is scoped to, in the headline and the summary", () => {
    render(health({ scopeTitle: "Infrastructure" }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("1 thing is broken in Infrastructure.");
    expect(screen.getByText("Over Infrastructure only.")).toBeInTheDocument();
  });

  it("does not let a clean Map read as a clean KB", () => {
    const clean: LintReport = { ...report, findings: [], count: 0, total: 0, by_severity: {}, by_check: {} };
    render(health({ report: clean, scopeTitle: "Infrastructure" }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("All clear in Infrastructure.");
    expect(screen.queryByText(/This KB passes/)).not.toBeInTheDocument();
    expect(screen.getByText(/Infrastructure passes every deterministic lint check/)).toBeInTheDocument();
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
    expect(screen.getByRole("button", { name: /Service/ })).toHaveAttribute("aria-pressed", "true");
  });
});
