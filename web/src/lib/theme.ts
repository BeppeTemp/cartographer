import { useEffect, useState } from "react";

export type Theme = "dark" | "light" | "system";

const STORAGE_KEY = "cartographer.theme";

/**
 * Theme is a per-viewer convenience, so it is the one thing that belongs in
 * localStorage: losing it costs one click, and it reveals nothing.
 */
export function readTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "dark" || stored === "light" || stored === "system") return stored;
  } catch {
    // Storage disabled: fall through to the system's choice.
  }
  // No signature theme (D233): until the viewer picks one, the system decides.
  return "system";
}

export function applyTheme(theme: Theme): void {
  const resolved = theme === "system" ? systemTheme() : theme;
  document.documentElement.dataset.theme = resolved;
  try {
    localStorage.setItem(STORAGE_KEY, theme);
  } catch {
    // Nothing to persist; the applied theme still holds for this session.
  }
}

/**
 * onSystemThemeChange re-resolves the theme when the OS switches while the
 * viewer is on "system". Returns the unsubscribe function.
 */
export function onSystemThemeChange(listener: () => void): () => void {
  const query = window.matchMedia?.("(prefers-color-scheme: dark)");
  if (!query?.addEventListener) return () => {};
  query.addEventListener("change", listener);
  return () => query.removeEventListener("change", listener);
}

export function systemTheme(): "dark" | "light" {
  return window.matchMedia?.("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

/**
 * prefersReducedMotion is read in JavaScript as well as in CSS: the graph
 * camera is animated frame by frame, not by a transition, so the media query
 * alone would not stop it. Under the preference the camera jumps.
 */
export function prefersReducedMotion(): boolean {
  return window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
}

/** The --motion-* scale of tokens.css, in ms: the fallback for a page without
 *  the stylesheet (a test), and kept equal to it by tokens.test.ts. */
export const MOTION_MS = {
  "--motion-instant": 80,
  "--motion-fast": 140,
  "--motion-base": 220,
  "--motion-slow": 360,
  "--motion-deliberate": 520,
} as const;
export type MotionToken = keyof typeof MOTION_MS;

/**
 * motionMs is a --motion-* token in ms, for what JavaScript times: the graph
 * camera, a count, the timer that ends a CSS slide. One scale for both sides,
 * so a JS move and the CSS around it end together, and 0 under reduced motion.
 */
export function motionMs(token: MotionToken): number {
  if (prefersReducedMotion()) return 0;
  const raw = getComputedStyle(document.documentElement).getPropertyValue(token).trim();
  const m = /^(\d*\.?\d+)(ms|s)$/.exec(raw);
  if (!m) return MOTION_MS[token];
  return Number(m[1]) * (m[2] === "s" ? 1000 : 1);
}

/**
 * useAppliedTheme is the theme the page is painted in right now -- the
 * data-theme attribute, observed. Canvas views resolve their colours from CSS
 * custom properties, so they must re-read them after the attribute changes;
 * keying them on the stored choice ("system", "light", ...) re-read them
 * before it did, because a child's effects run before the parent effect that
 * applies the theme. The WebGL graph then kept the old theme's canvas colour.
 */
export function useAppliedTheme(): "light" | "dark" {
  const read = () => (document.documentElement.dataset.theme === "light" ? "light" : "dark");
  const [applied, setApplied] = useState<"light" | "dark">(read);
  useEffect(() => {
    const observer = new MutationObserver(() => setApplied(read()));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    setApplied(read());
    return () => observer.disconnect();
  }, []);
  return applied;
}
