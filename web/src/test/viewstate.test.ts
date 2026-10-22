import { describe, expect, it } from "vitest";
import { artifactPanel, readViewState, viewStateToSearch } from "../lib/viewstate";

describe("the Artifacts section's panels (D1057)", () => {
  it("routes each kind to its tab", () => {
    expect(artifactPanel("instructions")).toBe("instructions");
    expect(artifactPanel("template")).toBe("templates");
    for (const kind of ["skill", "agent", "hook", "mcp", "something-new"]) expect(artifactPanel(kind)).toBe("artifacts");
  });

  it("round-trips the instructions and templates panels", () => {
    expect(readViewState("?kb=kb-a&panel=instructions").panel).toBe("instructions");
    expect(readViewState("?kb=kb-a&panel=templates").panel).toBe("templates");
    const view = readViewState("?kb=kb-a&panel=templates&artifact=template%2Frunbook");
    expect(view.artifact).toBe("template/runbook");
    expect(viewStateToSearch(view)).toBe("?kb=kb-a&panel=templates&artifact=template%2Frunbook");
    // The instructions are one text: no artifact rides on their panel.
    const instr = readViewState("?kb=kb-a&panel=instructions&artifact=instructions%2Fx");
    expect(instr.artifact).toBeNull();
    expect(viewStateToSearch(instr)).toBe("?kb=kb-a&panel=instructions");
  });

  it("reads an old artifacts link to a template or the instructions as the tab of its kind", () => {
    const tpl = readViewState("?kb=kb-a&panel=artifacts&artifact=template%2Frunbook");
    expect(tpl.panel).toBe("templates");
    expect(tpl.artifact).toBe("template/runbook");
    const instr = readViewState("?kb=kb-a&panel=artifacts&artifact=instructions%2Fmain");
    expect(instr.panel).toBe("instructions");
    expect(instr.artifact).toBeNull();
    const skill = readViewState("?kb=kb-a&panel=artifacts&artifact=skill%2Freview");
    expect(skill.panel).toBe("artifacts");
    expect(skill.artifact).toBe("skill/review");
  });
});
