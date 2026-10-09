import type { ConceptChange } from "../api/types";

/**
 * What a change was, as a reader of the Activity page cares about it:
 * - "new": the concept was written for the first time;
 * - "edited": its content changed (a write, a patch, an edit made outside the
 *   server -- anything that is not one of the kinds below);
 * - "reorganised": it moved, was superseded or was removed;
 * - "maintenance": only the server's own upkeep touched it (auto-repair), the
 *   reader's knowledge did not change.
 */
export type ChangeKind = "new" | "edited" | "reorganised" | "maintenance";

const REORGANISING_OPS = ["concept_move", "concept_delete", "supersede"];
const MAINTENANCE_OPS = ["auto-repair", "kb_repair"];

const opName = (op: string) => op.split(":")[0]!.trim();

export function changeKind(c: Pick<ConceptChange, "change" | "ops" | "last_edit_at" | "added">): ChangeKind {
  if (c.change === "deleted") return "reorganised";
  if (c.change === "added" || c.added) return "new";
  if (c.change === "deleted" || c.change === "moved" || c.change === "renamed") return "reorganised";
  const ops = (c.ops ?? []).map(opName);
  // last_edit_at is the server's word on it; ops, capped at five, the
  // fallback for a server that does not send it.
  if (c.last_edit_at === undefined && ops.length > 0 && ops.every((op) => MAINTENANCE_OPS.some((m) => op.startsWith(m))))
    return "maintenance";
  if (ops.some((op) => REORGANISING_OPS.includes(op)) && !ops.some(isContentOp)) return "reorganised";
  return "edited";
}

function isContentOp(op: string): boolean {
  return !REORGANISING_OPS.includes(op) && !MAINTENANCE_OPS.some((m) => op.startsWith(m));
}

/** When a change belongs on the timeline: the last time its content changed,
 *  or, for the server's upkeep, the last time it was touched at all. */
export function changedAt(c: Pick<ConceptChange, "last_at" | "last_edit_at">): string {
  return c.last_edit_at ?? c.last_at;
}

/** The reasons worth showing for a change: the server's own upkeep says
 *  nothing about why the content changed, unless upkeep is all there is. */
export function shownReasons(c: Pick<ConceptChange, "reasons">): string[] {
  const all = c.reasons ?? [];
  const meaningful = all.filter((r) => !MAINTENANCE_OPS.some((m) => r.startsWith(m)));
  return meaningful.length ? meaningful : all;
}

/** How many content operations a change carries: how much it was worked on. */
export function workCount(c: Pick<ConceptChange, "ops">): number {
  return (c.ops ?? []).map(opName).filter(isContentOp).length;
}

/** The concept worked on most in a set of changes, if any was worked on. */
export function mostWorked<C extends Pick<ConceptChange, "id" | "ops" | "change">>(rows: C[]): { change: C; count: number } | null {
  let best: { change: C; count: number } | null = null;
  for (const c of rows) {
    const kind = changeKind(c);
    if (kind !== "edited" && kind !== "new") continue;
    const count = workCount(c);
    if (count > 0 && (!best || count > best.count)) best = { change: c, count };
  }
  return best;
}
