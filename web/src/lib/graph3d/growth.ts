/** One step of the growth replay: a concept and when it entered the KB. */
export interface GrowthStep {
  id: string;
  /** RFC 3339; absent for a concept with no history, which comes last. */
  born?: string;
}

interface GrowthNode {
  id: string;
  pagerank?: number;
}

interface GrowthEdge {
  source: string;
  target: string;
}

/**
 * The order the replay brings the concepts in: by birth, oldest first. The
 * concepts born in the same commit -- an import lands dozens at once -- grow
 * along their links instead of all at once: first those touching what is
 * already there, then outward from them, and a part linked to nothing yet
 * starts from its best-ranked concept.
 */
export function growthOrder(nodes: GrowthNode[], edges: GrowthEdge[], births: Record<string, string>): GrowthStep[] {
  const adjacent = new Map<string, string[]>(nodes.map((n) => [n.id, []]));
  for (const e of edges) {
    adjacent.get(e.source)?.push(e.target);
    adjacent.get(e.target)?.push(e.source);
  }
  const rank = new Map(nodes.map((n) => [n.id, n.pagerank ?? 0]));
  const byRank = (a: string, b: string) => rank.get(b)! - rank.get(a)! || a.localeCompare(b);

  // Batches by birth instant, oldest first; no history is the last batch.
  const batches = new Map<string, string[]>();
  for (const n of nodes) {
    const key = births[n.id] ?? "";
    (batches.get(key) ?? batches.set(key, []).get(key)!).push(n.id);
  }
  const keys = [...batches.keys()].sort((a, b) => (a === "" ? 1 : b === "" ? -1 : Date.parse(a) - Date.parse(b)));

  const placed = new Set<string>();
  const order: GrowthStep[] = [];
  for (const key of keys) {
    const pending = new Set(batches.get(key)!);
    const queue = [...pending].filter((id) => adjacent.get(id)!.some((m) => placed.has(m))).sort(byRank);
    while (pending.size) {
      if (!queue.length) queue.push([...pending].sort(byRank)[0]!);
      const id = queue.shift()!;
      if (!pending.delete(id)) continue;
      placed.add(id);
      order.push(key ? { id, born: key } : { id });
      for (const m of adjacent.get(id)!) if (pending.has(m)) queue.push(m);
    }
  }
  return order;
}

/** The replay's pace: it lasts between eight and twenty seconds whatever the
 *  size of the KB, and never redraws the graph more often than every
 *  GROWTH_TICK_MIN_MS -- a large KB brings several concepts out per beat. */
export const GROWTH_TICK_MIN_MS = 60;
export function growthPace(steps: number): { tickMs: number; perTick: number } {
  const total = Math.min(20_000, Math.max(8_000, steps * 120));
  const tickMs = Math.max(GROWTH_TICK_MIN_MS, total / Math.max(1, steps));
  return { tickMs, perTick: Math.max(1, Math.round((steps * tickMs) / total)) };
}

/** The closest the replay's camera comes, as a share of the whole graph's
 *  fit: low enough to follow a KB as it grows, high enough that a handful of
 *  first concepts is not blown up. */
export const GROWTH_MIN_ZOOM = 0.6;
