import { describe, expect, it } from "vitest";
import { collectionHue } from "../lib/palette";
import { readViewState, viewStateToSearch, type ViewState } from "../lib/viewstate";


describe("view state round-trips through the URL", () => {
  it("restores everything it writes", () => {
    const view: ViewState = {
      kb: "kb-a",
      scope: "infra",
      concept: "infra/gateway",
      panel: "health",
      artifact: null,
      hmap: null,
    };
    expect(readViewState(viewStateToSearch(view))).toEqual(view);
  });

  it("omits empty values instead of writing blanks", () => {
    expect(viewStateToSearch({ kb: null, scope: null, concept: null, panel: "atlas", artifact: null, hmap: null })).toBe("");
    expect(viewStateToSearch({ kb: "kb", scope: null, concept: null, panel: "atlas", artifact: null, hmap: null })).toBe("?kb=kb");
  });

  it("sends the old Observatory and Maintenance links to Health (D338)", () => {
    expect(readViewState("?panel=observatory").panel).toBe("health");
    expect(readViewState("?panel=maintenance").panel).toBe("health");
    expect(viewStateToSearch({ kb: "k", scope: null, concept: null, panel: "health", artifact: null, hmap: null })).toBe(
      "?kb=k&panel=health",
    );
  });

  it("carries Health's own Map filter on Health only", () => {
    const view: ViewState = { kb: "k", scope: null, concept: null, panel: "health", artifact: null, hmap: "infra" };
    expect(viewStateToSearch(view)).toBe("?kb=k&panel=health&hmap=infra");
    expect(readViewState(viewStateToSearch(view))).toEqual(view);
    expect(viewStateToSearch({ ...view, panel: "atlas" })).toBe("?kb=k");
    expect(readViewState("?kb=k&hmap=infra").hmap).toBeNull();
  });

  it("falls back to the atlas for an unknown panel", () => {
    expect(readViewState("?panel=nonsense").panel).toBe("atlas");
  });

  it("carries the selected artifact on its panel only", () => {
    const view: ViewState = { kb: "k", scope: null, concept: null, panel: "artifacts", artifact: "skill/review", hmap: null };
    expect(viewStateToSearch(view)).toBe("?kb=k&panel=artifacts&artifact=skill%2Freview");
    expect(readViewState(viewStateToSearch(view))).toEqual(view);
    expect(viewStateToSearch({ ...view, panel: "atlas" })).toBe("?kb=k");
    expect(readViewState("?kb=k&artifact=skill%2Freview").artifact).toBeNull();
  });

  it("survives a concept id containing a slash", () => {
    const view: ViewState = { kb: "k", scope: null, concept: "a/b/c", panel: "atlas", artifact: null, hmap: null };
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
