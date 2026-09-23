import { describe, expect, it } from "vitest";
import { collectionHue } from "../lib/palette";
import { readViewState, viewStateToSearch, type ViewState } from "../lib/viewstate";


describe("view state round-trips through the URL", () => {
  it("restores everything it writes", () => {
    const view: ViewState = {
      kb: "homelab",
      scope: "infra",
      concept: "infra/gateway",
      panel: "observatory",
    };
    expect(readViewState(viewStateToSearch(view))).toEqual(view);
  });

  it("omits empty values instead of writing blanks", () => {
    expect(viewStateToSearch({ kb: null, scope: null, concept: null, panel: "atlas" })).toBe("");
    expect(viewStateToSearch({ kb: "kb", scope: null, concept: null, panel: "atlas" })).toBe("?kb=kb");
  });

  it("falls back to the atlas for an unknown panel", () => {
    expect(readViewState("?panel=nonsense").panel).toBe("atlas");
  });

  it("survives a concept id containing a slash", () => {
    const view: ViewState = { kb: "k", scope: null, concept: "a/b/c", panel: "atlas" };
    expect(readViewState(viewStateToSearch(view)).concept).toBe("a/b/c");
  });
});

describe("collection colour", () => {
  it("is stable for a name and inside the wheel", () => {
    for (const name of ["infra", "notes", "incidents", ""]) {
      const hue = collectionHue(name);
      expect(hue).toBe(collectionHue(name));
      expect(hue).toBeGreaterThanOrEqual(1);
      expect(hue).toBeLessThanOrEqual(12);
    }
  });
});
