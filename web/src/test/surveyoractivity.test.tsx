import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ConceptChange } from "../api/types";
import { SurveyorActivity } from "../components/SurveyorActivity";
import { componentRow, componentsAnswer } from "./fixtures";

const iso = (ms: number) => new Date(ms).toISOString();
const at = Date.now() - 3600_000;
const run = (id: string, over: object = {}) => ({
  run_id: id,
  replica: "w-1",
  task: "ingest",
  kb: "kb-a",
  started_at: iso(at),
  duration_ms: 60_000,
  outcome: "ok",
  tokens_spent: 700,
  received_at: iso(at + 61_000),
  ...over,
});
const change = (id: string, over: Partial<ConceptChange> = {}): ConceptChange =>
  ({ id, change: "modified", last_at: iso(at + 30_000), last_edit_at: iso(at + 30_000), authors: ["surveyor/team-a/w-1"], ops: ["concept_write"], ...over }) as ConceptChange;

function view(runs: object[], changes: ConceptChange[], extra: object = {}) {
  const components = componentsAnswer(componentRow({ runs }, [{}])).components as never;
  return render(
    <SurveyorActivity
      kb="kb-a"
      components={components}
      from={0}
      changes={changes}
      hue={() => undefined}
      renderPage={(c) => <li key={c.id}>{c.id}</li>}
      {...extra}
    />,
  );
}

describe("SurveyorActivity", () => {
  it("lists a run's pages under it, one run per page, and words a run that wrote nothing", () => {
    view(
      [run("r1"), run("r2", { task: "doctor", started_at: iso(at - 7200_000) })],
      [change("a/made", { added: true } as never), change("a/human", { authors: ["bob"] })],
    );
    const rows = [...document.querySelectorAll<HTMLElement>(".sv-run")];
    expect(rows).toHaveLength(2);
    const ingest = rows.find((r) => r.textContent?.includes("Ingest run"))!;
    expect([...ingest.querySelectorAll(".sv-run__pages li")].map((l) => l.textContent)).toEqual(["a/made"]);
    expect(rows.find((r) => r.textContent?.includes("Doctor run"))).toHaveTextContent("nothing written");
  });

  it("matches a page to the run whose id it carries, whatever its time", () => {
    view([run("r1")], [change("a/far", { last_at: iso(at - 6 * 3600_000), surveyor_runs: ["r1"] })]);
    expect(screen.getByText("a/far")).toBeInTheDocument();
  });

  it("marks a failed run and keeps only the filtered task", () => {
    view([run("r1", { outcome: "failed", error: "feed down" }), run("r2", { task: "doctor" })], [], { task: "ingest" });
    const rows = [...document.querySelectorAll<HTMLElement>(".sv-run")];
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveAttribute("data-tone", "error");
    expect(within(rows[0]!).getByText("feed down")).toBeInTheDocument();
  });

  it("says so when the window holds no run", () => {
    view([], []);
    expect(screen.getByText("No Surveyor run in this window.")).toBeInTheDocument();
  });
});
