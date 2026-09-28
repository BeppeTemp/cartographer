import { afterEach, describe, expect, it, vi } from "vitest";
import { clampInspectorWidth, inspectorMax, readWidth, writeWidth } from "../lib/panels";

/** The reading panel's remembered width (D239): a guarded, clamped integer. */
describe("inspector width preference", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("round-trips an integer", () => {
    writeWidth("inspector.width", 512.4);
    expect(localStorage.getItem("cartographer.inspector.width")).toBe("512");
    expect(readWidth("inspector.width", 420)).toBe(512);
  });

  it.each(["", "abc", "12.5", "-40", "0", "NaN"])("reads %j as the fallback", (stored) => {
    localStorage.setItem("cartographer.inspector.width", stored);
    expect(readWidth("inspector.width", 420)).toBe(420);
  });

  it("falls back when storage throws", () => {
    // The test setup installs a plain in-memory Storage, not Storage.prototype.
    vi.spyOn(localStorage, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    vi.spyOn(localStorage, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(() => writeWidth("inspector.width", 500)).not.toThrow();
    expect(readWidth("inspector.width", 420)).toBe(420);
  });

  it("clamps between the minimum and what leaves 280px of graph", () => {
    // body 1600, rail 264 → max 1600 - 264 - 280 = 1056
    expect(clampInspectorWidth(100, 1600, 264)).toBe(320);
    expect(clampInspectorWidth(700, 1600, 264)).toBe(700);
    expect(clampInspectorWidth(2000, 1600, 264)).toBe(1056);
  });

  it("returns the minimum when the window cannot fit it", () => {
    expect(clampInspectorWidth(600, 700, 264)).toBe(320);
  });

  // A panel wider than its content only covers graph (D281, #436).
  it("never grows past what the reading measure can use", () => {
    // room 1056, reading 640 → max 640
    expect(inspectorMax(1600, 264, 640)).toBe(640);
    expect(clampInspectorWidth(2000, 1600, 264, 640)).toBe(640);
    expect(clampInspectorWidth(500, 1600, 264, 640)).toBe(500);
    // the room still wins when it is the smaller bound
    expect(inspectorMax(1000, 264, 640)).toBe(456);
    // unmeasured (0): the room alone, as before
    expect(inspectorMax(1600, 264, 0)).toBe(1056);
    // and never below the minimum
    expect(inspectorMax(1600, 264, 200)).toBe(320);
  });
});
