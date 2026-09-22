/**
 * Collection colours come from the fixed twelve-hue wheel in tokens.css,
 * picked by a stable hash of the collection name.
 *
 * Deliberately not "the next colour in the list": that order depends on which
 * collections the current principal can see, so a narrowed token would render
 * the same Map in a different colour than an admin does, and the same Map
 * would change colour as a KB grows. A hash is stable across sessions,
 * machines and permissions.
 */
const HUES = 12;

export function collectionHue(name: string): number {
  // FNV-1a: short, dependency-free, and well-spread over the 12 buckets for
  // the short lowercase strings collection names actually are.
  let hash = 0x811c9dc5;
  for (let i = 0; i < name.length; i++) {
    hash ^= name.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return (hash % HUES) + 1;
}

export function collectionVar(name: string): string {
  return `var(--hue-${collectionHue(name)})`;
}

export function cssVar(name: string, root: HTMLElement = document.documentElement): string {
  return getComputedStyle(root).getPropertyValue(name).trim();
}

/** What node colour means: the Map a concept is filed in, or the community
 *  its links place it in (lib/communities). */
export type ColorBy = "community" | "map";

const COLOR_BY_KEY = "cartographer.colorBy";

/** A per-viewer preference, so localStorage is the right home: it reveals
 *  nothing, and losing it costs one click. */
export function readColorBy(): ColorBy {
  try {
    const stored = localStorage.getItem(COLOR_BY_KEY);
    if (stored === "community" || stored === "map") return stored;
  } catch {
    // Storage disabled: fall through to the default.
  }
  return "community";
}

export function writeColorBy(value: ColorBy): void {
  try {
    localStorage.setItem(COLOR_BY_KEY, value);
  } catch {
    // Nothing to persist; the choice still holds for this session.
  }
}

/** The CSS variable for a colour slot: 1..12 are the hue wheel, 0 is the
 *  neutral shared by singletons and the long tail. */
export function slotVar(slot: number): string {
  return slot === 0 ? "var(--graph-community-other)" : `var(--hue-${slot})`;
}

/**
 * resolveSlots reads the whole wheel once, as concrete values for the WebGL
 * renderer. Resolving per node per frame would call getComputedStyle a few
 * thousand times per refresh on a large graph; the table only changes with the
 * theme.
 */
export function resolveSlots(root: HTMLElement = document.documentElement): string[] {
  const style = getComputedStyle(root);
  const slots = [style.getPropertyValue("--graph-community-other").trim()];
  for (let i = 1; i <= HUES; i++) slots.push(style.getPropertyValue(`--hue-${i}`).trim());
  // A missing token (a test environment without the stylesheet) falls back
  // to the first resolved hue, then to the canvas default.
  const fallback = slots.find(Boolean) || "#365d50";
  return slots.map((value) => value || fallback);
}
