import type { GraphSnapshot } from "../api/types";
import { OTHER_SLOT, type Communities } from "../lib/communities";
import { shortLabel } from "../lib/encoding";
import { nameOf } from "../lib/names";
import { slotVar } from "../lib/palette";

interface Row {
  key: string;
  slot: number;
  label: string;
  count: number;
}

/**
 * What the colours mean when nodes are coloured by community: every community
 * drawn, in the rail under the Maps, with how many of its nodes are drawn.
 * Coloured by Map the rail's own Map swatches are the key, so nothing is
 * repeated here.
 *
 * Colour is never the only carrier: every row names its group and counts it,
 * and the inspector states a concept's Map in words.
 */
export function Legend({ snapshot, communities }: { snapshot: GraphSnapshot; communities: Communities }) {
  const { rows, tail } = communityRows(communities, snapshot);
  if (!rows.length && !tail) return null;
  return (
    <section className="rail__group" aria-label="Graph legend">
      <h2 className="rail__title">Communities</h2>
      <ul className="rail__collections">
        {rows.map((row) => (
          <li key={row.key} className="rail__key" title={`${row.label}: ${row.count}`}>
            <span className="rail__swatch" aria-hidden="true" style={{ background: slotVar(row.slot) }} />
            <span className="rail__collection-name">{row.label}</span>
            <span className="rail__collection-count">{row.count}</span>
          </li>
        ))}
        {tail > 0 && (
          <li className="rail__key">
            {/* No swatch: the summarised groups do not share one colour. */}
            <span className="rail__swatch rail__swatch--none" aria-hidden="true" />
            <span className="rail__collection-name">Smaller groups</span>
            <span className="rail__collection-count">{tail}</span>
          </li>
        )}
      </ul>
    </section>
  );
}

function communityRows(communities: Communities, snapshot: GraphSnapshot): { rows: Row[]; tail: number } {
  // A community is named after its anchor: by the anchor's title, which a
  // reader recognises, rather than its id's slug.
  const titles = new Map(snapshot.nodes.map((n) => [n.id, nameOf(n)]));
  // Real communities get rows; singletons and the long tail share one neutral
  // and are only ever summed into the closing row.
  // Counted over what is drawn, so a graph narrowed to one Map keys its own
  // nodes rather than the whole KB's.
  const drawn = new Map<number, number>();
  for (const n of snapshot.nodes) {
    const rank = communities.rankOf.get(n.id);
    if (rank !== undefined) drawn.set(rank, (drawn.get(rank) ?? 0) + 1);
  }
  const rows: Row[] = [];
  let tail = 0;
  for (const c of communities.list) {
    const count = drawn.get(c.rank) ?? 0;
    if (!count) continue;
    if (c.slot === OTHER_SLOT) tail += count;
    else rows.push({ key: `c${c.rank}`, slot: c.slot, label: c.anchorTitle || titles.get(c.anchor) || shortLabel(c.anchor), count });
  }
  rows.sort((a, b) => b.count - a.count);
  return { rows, tail };
}
