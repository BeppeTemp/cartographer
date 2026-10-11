import { describe, expect, it } from "vitest";
import { eggsFor, toggleEggs, takoColour } from "./eggs";

describe("easter eggs", () => {
  it("answer only their whole phrase, in any case and spacing", () => {
    expect(eggsFor("Liquid   Glass ")).toEqual(["glass"]);
    expect(eggsFor("Tako Mode")).toEqual(["tako"]);
    expect(eggsFor("glass mode")).toBeNull();
    expect(eggsFor("liquid")).toBeNull();
    expect(eggsFor("tako")).toBeNull();
  });

  it("turn on, and off when typed again", () => {
    const on = toggleEggs(new Set(), ["glass"]);
    expect([...on]).toEqual(["glass"]);
    expect([...toggleEggs(on, ["glass"])]).toEqual([]);
    expect([...toggleEggs(on, ["tako"])].sort()).toEqual(["glass", "tako"]);
  });

  it("paints the rainbow as hex, which the graph's fading can read", () => {
    expect(takoColour(0, 6)).toMatch(/^#[0-9a-f]{6}$/);
    expect(takoColour(0, 6)).not.toBe(takoColour(3, 6));
  });
});
