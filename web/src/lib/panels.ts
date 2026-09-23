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

/** A remembered panel width in px, under `cartographer.<name>`. Anything but a
 *  positive integer reads as the fallback. */
export type WidthName = "inspector.width" | "artifacts.width";

export function readWidth(name: WidthName, fallback: number): number {
  try {
    const v = localStorage.getItem("cartographer." + name);
    if (v !== null && /^\d+$/.test(v) && Number(v) > 0) return Number(v);
  } catch {
    // Storage disabled: fall through to the default.
  }
  return fallback;
}

export function writeWidth(name: WidthName, px: number): void {
  try {
    localStorage.setItem("cartographer." + name, String(Math.round(px)));
  } catch {
    // Nothing to remember it in; the default applies next time.
  }
}

export const INSPECTOR_DEFAULT = 420;
export const INSPECTOR_MIN = 320;
/** The graph a reading panel never covers. */
export const GRAPH_MIN_VISIBLE = 280;

/** The largest reading panel that still leaves GRAPH_MIN_VISIBLE of graph
 *  beside the rail, never below INSPECTOR_MIN. */
export function inspectorMax(bodyWidth: number, railWidth: number): number {
  return Math.max(INSPECTOR_MIN, bodyWidth - railWidth - GRAPH_MIN_VISIBLE);
}

export function clampInspectorWidth(px: number, bodyWidth: number, railWidth: number): number {
  return Math.round(Math.min(Math.max(px, INSPECTOR_MIN), inspectorMax(bodyWidth, railWidth)));
}
