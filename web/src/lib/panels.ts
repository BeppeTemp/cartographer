/**
 * Which side panels are open, remembered per viewer. A layout preference, so
 * localStorage is the right store (never a token, never content), and every
 * access is guarded: a private window throws on the first read.
 */
export type PanelName = "rail" | "list" | "motion";

const PREFIX = "cartographer.panel.";

export function readPanel(name: PanelName, fallback: boolean): boolean {
  try {
    const v = localStorage.getItem(PREFIX + name);
    if (v === "1") return true;
    if (v === "0") return false;
  } catch {
    // Storage disabled: fall through to the default.
  }
  return fallback;
}

export function writePanel(name: PanelName, value: boolean): void {
  try {
    localStorage.setItem(PREFIX + name, value ? "1" : "0");
  } catch {
    // Nothing to remember it in; the default applies next time.
  }
}
