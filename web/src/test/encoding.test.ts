import { describe, expect, it } from "vitest";
import { fade, shortLabel } from "../lib/encoding";

describe("fade", () => {
  it("mixes towards the canvas into an opaque colour", () => {
    // Opaque on purpose: an rgba() colour is added to the page by Sigma's
    // unpremultiplied blending and turns white on a light canvas.
    expect(fade("#000000", 0.25, "#ffffff")).toBe("#bfbfbf");
    expect(fade("#2dd4bf", 1, "#000000")).toBe("#2dd4bf");
    expect(fade("#abc", 0, "#123456")).toBe("#123456");
  });

  it("falls back to rgba without a canvas and leaves non-hex alone", () => {
    expect(fade("#2dd4bf", 0.25)).toBe("rgba(45, 212, 191, 0.25)");
    expect(fade("rebeccapurple", 0.5, "#ffffff")).toBe("rebeccapurple");
    expect(fade("", 0.5)).toBe("");
  });
});

describe("shortLabel", () => {
  it("keeps the last path segment", () => {
    expect(shortLabel("infra/dns")).toBe("dns");
    expect(shortLabel("root")).toBe("root");
  });
});
