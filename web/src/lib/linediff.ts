/**
 * A line diff (Myers' O(ND) greedy algorithm) and its hunks with context, for
 * the Activity page's "what changed" view. Pure: no DOM, tested on its own.
 */
export type DiffLine = { kind: "same" | "add" | "del"; text: string; a?: number; b?: number };
export type Hunk = { lines: DiffLine[]; /** Unchanged lines folded before it. */ skipped: number };

/** Beyond these the script is not computed: its trace costs O(D²) memory. */
export const MAX_DIFF_LINES = 20000;
export const MAX_DIFF_EDITS = 1000;

/** The edit script turning `before` into `after`, line by line; null when the
 *  texts are too large or too different to compare (see the limits above). */
export function diffLines(before: string, after: string, maxEdits = MAX_DIFF_EDITS): DiffLine[] | null {
  const a = before === "" ? [] : before.replace(/\n$/, "").split("\n");
  const b = after === "" ? [] : after.replace(/\n$/, "").split("\n");
  const n = a.length;
  const m = b.length;
  const max = n + m;
  if (max > MAX_DIFF_LINES) return null;
  const v = new Map<number, number>([[1, 0]]);
  const trace: Map<number, number>[] = [];
  outer: for (let d = 0; d <= max; d++) {
    if (d > maxEdits) return null;
    trace.push(new Map(v));
    for (let k = -d; k <= d; k += 2) {
      let x = k === -d || (k !== d && (v.get(k - 1) ?? 0) < (v.get(k + 1) ?? 0)) ? v.get(k + 1) ?? 0 : (v.get(k - 1) ?? 0) + 1;
      let y = x - k;
      while (x < n && y < m && a[x] === b[y]) {
        x++;
        y++;
      }
      v.set(k, x);
      if (x >= n && y >= m) break outer;
    }
  }
  // Walk the trace back to the start.
  const out: DiffLine[] = [];
  let x = n;
  let y = m;
  for (let d = trace.length - 1; d >= 0; d--) {
    const vd = trace[d]!;
    const k = x - y;
    const prevK = k === -d || (k !== d && (vd.get(k - 1) ?? 0) < (vd.get(k + 1) ?? 0)) ? k + 1 : k - 1;
    const prevX = vd.get(prevK) ?? 0;
    const prevY = prevX - prevK;
    while (x > prevX && y > prevY) {
      out.push({ kind: "same", text: a[x - 1]!, a: x, b: y });
      x--;
      y--;
    }
    if (d > 0) {
      if (x === prevX) out.push({ kind: "add", text: b[y - 1]!, b: y });
      else out.push({ kind: "del", text: a[x - 1]!, a: x });
    }
    x = prevX;
    y = prevY;
  }
  return out.reverse();
}

/** Changed lines with `context` unchanged lines around them; the rest folded. */
export function hunks(lines: DiffLine[], context = 3): Hunk[] {
  const keep = lines.map(() => false);
  lines.forEach((l, i) => {
    if (l.kind === "same") return;
    for (let j = Math.max(0, i - context); j <= Math.min(lines.length - 1, i + context); j++) keep[j] = true;
  });
  const out: Hunk[] = [];
  let skipped = 0;
  let cur: Hunk | null = null;
  for (let i = 0; i < lines.length; i++) {
    if (!keep[i]) {
      cur = null;
      skipped++;
      continue;
    }
    if (!cur) {
      cur = { lines: [], skipped };
      out.push(cur);
      skipped = 0;
    }
    cur.lines.push(lines[i]!);
  }
  return out;
}
