import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CommandPalette } from "../components/CommandPalette";
import { Health } from "../components/Health";
import { Instructions } from "../components/Instructions";
import { Splitter } from "../components/Splitter";
import { checkLabel, checkHelp } from "../lib/health";
import type { ArtifactList, CollectionSummary, LintFinding, LintReport } from "../api/types";
import { json, stubApi } from "./fixtures";

const collections: CollectionSummary[] = [
  { name: "infra", title: "Infrastructure", kind: "folder", concepts: 10, expanded_concepts: 0 },
  { name: "notes", title: "Notes", kind: "folder", concepts: 4, expanded_concepts: 0 },
];
const all: LintFinding[] = [
  { path: "infra/a.md", concept: "infra/a", check: "broken_link", message: "x" },
  { path: "infra/b.md", concept: "infra/b", check: "broken_link", message: "x" },
  { path: "notes/c.md", concept: "notes/c", check: "broken_link", message: "x" },
];
const scoped: LintReport = { findings: all.slice(2), count: 1, by_check: { broken_link: 1 } };

describe("Health with a Map filter", () => {
  it("describes occurrences, touched and pages from that Map alone", () => {
    render(
      <Health
        report={scoped}
        allFindings={all}
        collections={collections}
        hmap="notes"
        scopeTitle="Notes"
        loading={false}
        error={null}
        onReveal={() => {}}
        onRetry={() => {}}
      />,
    );
    expect(document.querySelector(".health__lede")).toHaveTextContent("1 occurrence in all, on 1 of 4 pages");
  });
});

describe("check labels", () => {
  it("names the two checks the registry added", () => {
    for (const id of ["contract_legacy_key", "legacy_skill_name"]) {
      expect(checkLabel(id)).not.toBe(id.charAt(0).toUpperCase() + id.slice(1).replace(/_/g, " "));
      expect(checkHelp(id)).not.toBe("");
    }
  });
});

describe("Instructions retry", () => {
  it("reads again when Retry is clicked after a failed read", async () => {
    let calls = 0;
    stubApi({
      "/artifact?kind=instructions": () => {
        calls++;
        return calls === 1
          ? json({ error: "boom" }, 500)
          : json({ kind: "instructions", name: "main", description: "", content_hash: "a", signed: false, clients: [], files: [], content: "Be kind." });
      },
    });
    const list = {
      artifacts: [{ kind: "instructions", name: "main", description: "", content_hash: "a", signed: false, clients: [], files: [] }],
      kinds: [],
    } as unknown as ArtifactList;
    const user = userEvent.setup();
    render(<Instructions kb="kb-a" list={list} loading={false} error={null} onRetry={() => {}} onFailure={() => false} />);
    await user.click(await screen.findByRole("button", { name: /retry|try again/i }));
    await vi.waitFor(() => expect(calls).toBe(2));
  });
});

describe("the palette with no result", () => {
  it("keeps the cursor at 0 and points aria-activedescendant at nothing", async () => {
    stubApi();
    const user = userEvent.setup();
    render(<CommandPalette open kb="kb-a" nodes={[]} onClose={vi.fn()} onSelect={vi.fn()} />);
    const input = screen.getByRole("textbox", { name: /search concepts/i });
    await user.type(input, "zzzz{ArrowDown}{ArrowDown}");
    expect(input).not.toHaveAttribute("aria-activedescendant");
  });
});

describe("Splitter restore", () => {
  it("commits 0 (no preference) on Enter and on a double-click", () => {
    const onChange = vi.fn();
    const onCommit = vi.fn();
    render(
      <Splitter value={500} min={300} max={800} defaultValue={420} edge="start" controls="x" label="Resize" onChange={onChange} onCommit={onCommit} />,
    );
    const handle = screen.getByRole("separator");
    fireEvent.keyDown(handle, { key: "Enter" });
    expect(onChange).toHaveBeenLastCalledWith(420);
    expect(onCommit).toHaveBeenLastCalledWith(0);
    onCommit.mockClear();
    fireEvent.doubleClick(handle);
    expect(onCommit).toHaveBeenLastCalledWith(0);
  });
});
