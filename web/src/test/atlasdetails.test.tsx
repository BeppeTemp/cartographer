import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CommandPalette } from "../components/CommandPalette";
import { Health } from "../components/Health";
import { Settings } from "../components/Settings";
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

describe("Health with a clean Map chosen while another Map has issues", () => {
  it("does not switch to the all-clear state", () => {
    render(
      <Health
        report={{ findings: [], count: 0, by_check: {} }}
        allFindings={all.slice(0, 2)}
        collections={collections}
        hmap="notes"
        scopeTitle="Notes"
        loading={false}
        error={null}
        onReveal={() => {}}
        onRetry={() => {}}
      />,
    );
    const field = screen.getByRole("group", { name: "Pages by Map" });
    expect(field).not.toHaveAttribute("data-clear");
    expect(field.querySelectorAll("[data-problem]")).toHaveLength(2);
  });
});

const baseSettings = { version: "v1", server: [], kbs: [{ name: "kb-a", entries: [], yaml: "" }, { name: "kb-b", entries: [], yaml: "" }] };
const clientWith = (clients: unknown, key = "providers") => ({
  ...baseSettings,
  clients: [
    {
      machine: "laptop",
      version: "v1",
      server_url: "http://example.com/mcp",
      received_at: "2026-10-16T10:00:00Z",
      yaml: "",
      providers: [],
      entries: [{ key, value: clients, origin: "file", description: "Bindings.", default: "{}" }],
    },
  ],
});

describe("Settings", () => {
  it.each([["providers"], ["clients"]])("lists an agent under a KB unless its kbs key is present and excludes it (%s)", async (key) => {
    stubApi({
      "/settings": () => json(clientWith({ kiro: { kbs: [] }, claude: {}, codex: { kbs: ["kb-b"] } }, key)),
    });
    render(<Settings reloadKey="a" activeKB="kb-a" onRetry={() => {}} onFailure={() => false} />);
    const kbA = await screen.findByRole("list", { name: "Client settings of kb-a" });
    const value = within(kbA).getByLabelText("Value of providers.<agent>.kbs");
    expect(value).toHaveTextContent("claude");
    expect(value).not.toHaveTextContent("kiro");
    expect(value).not.toHaveTextContent("codex");
  });

  it("keeps a loaded page, with a note, when a refetch fails", async () => {
    let fail = false;
    stubApi({ "/settings": () => (fail ? json({ error: { code: "internal", message: "x" } }, 500) : json(baseSettings)) });
    const { rerender } = render(<Settings reloadKey="a" activeKB="kb-a" onRetry={() => {}} onFailure={() => false} />);
    await screen.findByRole("heading", { level: 1 });
    fail = true;
    rerender(<Settings reloadKey="b" activeKB="kb-a" onRetry={() => {}} onFailure={() => false} />);
    expect(await screen.findByText(/Could not refresh the settings/)).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1 })).toBeInTheDocument();
  });

  it("says artifacts read-only for a rule that withholds the artifact write tools", async () => {
    stubApi({
      "/settings": () => json(baseSettings),
      "/whoami": () => json({ id: "bot", admin: false, permissions: [{ kb: "kb-a", write: true, artifacts: "r" }] }),
    });
    render(<Settings reloadKey="a" activeKB="kb-a" onRetry={() => {}} onFailure={() => false} />);
    expect(await screen.findByLabelText("Who is looking")).toHaveTextContent("artifacts read-only");
  });
});
