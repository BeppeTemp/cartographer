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

/** Resolves a collection's colour to a concrete value Sigma's WebGL renderer
 *  can use: a canvas cannot read a CSS custom property. */
export function collectionColor(name: string, root: HTMLElement = document.documentElement): string {
  const value = getComputedStyle(root).getPropertyValue(`--hue-${collectionHue(name)}`).trim();
  return value || "#2dd4bf";
}

export function cssVar(name: string, root: HTMLElement = document.documentElement): string {
  return getComputedStyle(root).getPropertyValue(name).trim();
}
