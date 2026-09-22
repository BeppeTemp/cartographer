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
    // Storage disabled: fall through to the signature theme.
  }
  return "dark";
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

export function systemTheme(): "dark" | "light" {
  return window.matchMedia?.("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

/**
 * prefersReducedMotion is read in JavaScript as well as in CSS: the graph
 * camera is animated by Sigma, not by a transition, so the media query alone
 * would not stop it. Under the preference the camera jumps.
 */
export function prefersReducedMotion(): boolean {
  return window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
}
