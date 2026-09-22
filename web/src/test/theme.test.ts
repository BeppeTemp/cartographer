import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { applyTheme, onSystemThemeChange, readTheme } from "../lib/theme";

type Listener = () => void;

/** A matchMedia whose colour scheme the test can flip, with its listeners. */
function fakeScheme(dark: boolean) {
  const listeners = new Set<Listener>();
  const state = { dark };
  vi.stubGlobal("matchMedia", (query: string) => ({
    get matches() {
      if (query.includes("dark")) return state.dark;
      if (query.includes("light")) return !state.dark;
      return false;
    },
    media: query,
    addEventListener: (_: string, l: Listener) => listeners.add(l),
    removeEventListener: (_: string, l: Listener) => listeners.delete(l),
  }));
  return {
    flip(next: boolean) {
      state.dark = next;
      listeners.forEach((l) => l());
    },
    listeners,
  };
}

describe("theme", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => vi.unstubAllGlobals());

  it("follows the system until the viewer picks one", () => {
    expect(readTheme()).toBe("system");
  });

  it("keeps an explicit choice", () => {
    localStorage.setItem("cartographer.theme", "light");
    expect(readTheme()).toBe("light");
  });

  it("re-resolves on an OS switch only through the subscription", () => {
    const scheme = fakeScheme(true);
    applyTheme("system");
    expect(document.documentElement.dataset.theme).toBe("dark");
    const stop = onSystemThemeChange(() => applyTheme("system"));
    scheme.flip(false);
    expect(document.documentElement.dataset.theme).toBe("light");
    stop();
    expect(scheme.listeners.size).toBe(0);
  });

  it("the pre-paint script resolves the same way as applyTheme", () => {
    // public/theme-init.js runs before the bundle; if it disagreed with
    // lib/theme the page would flash from one theme to the other on load.
    const script = readFileSync(join(process.cwd(), "public/theme-init.js"), "utf8");
    for (const [stored, dark, expected] of [
      [null, true, "dark"],
      [null, false, "light"],
      ["light", true, "light"],
      ["dark", false, "dark"],
      ["system", true, "dark"],
      ["bogus", false, "light"],
    ] as const) {
      localStorage.clear();
      if (stored) localStorage.setItem("cartographer.theme", stored);
      fakeScheme(dark);
      delete document.documentElement.dataset.theme;
      new Function(script)();
      expect(document.documentElement.dataset.theme, `${stored} / dark=${dark}`).toBe(expected);
      applyTheme(readTheme());
      expect(document.documentElement.dataset.theme).toBe(expected);
      vi.unstubAllGlobals();
    }
  });
});
