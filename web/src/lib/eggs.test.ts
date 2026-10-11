import { describe, expect, it } from "vitest";
import { eggsFor, toggleEggs, vibeColour } from "./eggs";

describe("easter eggs", () => {
  it("answer only their whole phrase, in any case and spacing", () => {
    expect(eggsFor("Liquid   Glass ")).toEqual(["glass"]);
    expect(eggsFor("Tako Mode")).toEqual(["vibe"]);
    expect(eggsFor("liquid tako")).toEqual(["glass", "vibe"]);
    expect(eggsFor("liquid")).toBeNull();
    expect(eggsFor("tako")).toBeNull();
  });

  it("turn on, and off when typed again", () => {
    const on = toggleEggs(new Set(), ["glass"]);
    expect([...on]).toEqual(["glass"]);
    expect([...toggleEggs(on, ["glass"])]).toEqual([]);
    expect([...toggleEggs(on, ["glass", "vibe"])].sort()).toEqual(["glass", "vibe"]);
  });

  it("paints the rainbow as hex, which the graph's fading can read", () => {
    expect(vibeColour(0, 6)).toMatch(/^#[0-9a-f]{6}$/);
    expect(vibeColour(0, 6)).not.toBe(vibeColour(3, 6));
  });
});
