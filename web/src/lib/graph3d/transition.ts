import type { Pose } from "./camera";

/**
 * How a graph moves from one picture to the next, as pure logic the view and
 * the scene share: what a layout remembers when the Atlas is left, and which
 * concepts stay, enter and leave when a Map changes (D234: the network is
 * never rebuilt from a point when the reader already has it in front of them).
 */

type Point = { x: number; y: number; z: number };

/** What the Atlas keeps of a layout while another panel is open: where every
 *  node settled and where the reader had left the camera. */
export interface LayoutMemory {
  nodes: Map<string, Point>;
  artifacts: Map<string, Point>;
  pose: Pose;
  /** The screen shift the pose was drawn with (a selection beside the inspector). */
  shift: number;
  /** The pose a selection returns to, when one was open. */
  overview: Pose | null;
}

/** Layouts kept, oldest dropped first: a few KBs and Maps, not every one visited. */
export const MEMORY_LIMIT = 8;
const memory = new Map<string, LayoutMemory>();

export function rememberLayout(key: string, entry: LayoutMemory): void {
  if (!key) return;
  memory.delete(key);
  memory.set(key, entry);
  while (memory.size > MEMORY_LIMIT) memory.delete(memory.keys().next().value!);
}

export function recallLayout(key: string): LayoutMemory | null {
  return memory.get(key) ?? null;
}

export function forgetLayouts(): void {
  memory.clear();
}

/** The positions of nodes that have them, copied: the memory never shares an
 *  object with a live simulation. */
export function snapshotPositions(nodes: Iterable<[string, { x?: number; y?: number; z?: number }]>): Map<string, Point> {
  const out = new Map<string, Point>();
  for (const [id, n] of nodes) {
    if (Number.isFinite(n.x) && Number.isFinite(n.y) && Number.isFinite(n.z)) out.set(id, { x: n.x!, y: n.y!, z: n.z! });
  }
  return out;
}

/** A layout key is `<kb>\0<scope...>`: two keys of the same KB are two views
 *  of the same concepts, which is what a morph needs. */
export function sameKB(a: string, b: string): boolean {
  if (!a || !b) return false;
  return a.split("\u0000")[0] === b.split("\u0000")[0];
}

/** Which concepts a new view keeps, brings in and drops. */
export function transitionSets(before: ReadonlySet<string>, after: readonly string[]) {
  const next = new Set(after);
  return {
    kept: after.filter((id) => before.has(id)),
    entering: after.filter((id) => !before.has(id)),
    leaving: [...before].filter((id) => !next.has(id)),
  };
}

/** Eases 0..1 smoothly at both ends. */
export function smooth(t: number): number {
  const k = Math.min(1, Math.max(0, t));
  return k * k * (3 - 2 * k);
}
