import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CommandPalette } from "../components/CommandPalette";
import { NodeList } from "../components/NodeList";
import { Observatory } from "../components/Observatory";
import type { GraphNode, LintReport } from "../api/types";

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
    { path: "infra/gateway.md", concept: "infra/gateway", check: "broken_link", severity: "error", message: "target missing" },
    { path: "maps/infra", check: "index_incomplete", severity: "warning", message: "index is stale" },
  ],
  count: 2,
  total: 9,
  by_severity: { error: 1, warning: 1, info: 7 },
  by_check: { broken_link: 1, index_incomplete: 1 },
  severity_min: "info",
};

describe("observatory", () => {
  it("reveals the concept behind a finding", async () => {
    const user = userEvent.setup();
    const onReveal = vi.fn();
    render(
      <Observatory
        report={report}
        loading={false}
        error={null}
        severityMin="info"
        onSeverityChange={vi.fn()}
        onReveal={onReveal}
        onRetry={vi.fn()}
      />,
    );
    await user.click(screen.getByRole("button", { name: /broken_link/ }));
    expect(onReveal).toHaveBeenCalledWith("infra/gateway", "");
  });

  it("explains that a finding with no concept has no node to reveal", async () => {
    const user = userEvent.setup();
    const onReveal = vi.fn();
    render(
      <Observatory
        report={report}
        loading={false}
        error={null}
        severityMin="info"
        onSeverityChange={vi.fn()}
        onReveal={onReveal}
        onRetry={vi.fn()}
      />,
    );
    await user.click(screen.getByRole("button", { name: /index_incomplete/ }));
    expect(onReveal).toHaveBeenCalledWith(null, expect.stringContaining("no node to reveal"));
  });

  it("says how many findings it is not showing", () => {
    render(
      <Observatory
        report={report}
        loading={false}
        error={null}
        severityMin="warning"
        onSeverityChange={vi.fn()}
        onReveal={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    expect(screen.getByText(/showing 2 of 9 findings/i)).toBeInTheDocument();
  });

  it("carries severity as text, not colour alone", () => {
    render(
      <Observatory
        report={report}
        loading={false}
        error={null}
        severityMin="info"
        onSeverityChange={vi.fn()}
        onReveal={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const totals = screen.getByRole("region", { name: "Observatory" });
    expect(within(totals).getAllByText("error").length).toBeGreaterThan(0);
    expect(within(totals).getAllByText("warning").length).toBeGreaterThan(0);
  });
});
