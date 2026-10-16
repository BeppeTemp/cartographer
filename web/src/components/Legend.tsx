import type { GraphSnapshot } from "../api/types";
import { OTHER_SLOT, type Communities } from "../lib/communities";
import { shortLabel } from "../lib/encoding";
import { nameOf } from "../lib/names";
import { collectionHue, slotVar, type ColorBy } from "../lib/palette";
import { GrowthButton } from "./GraphView";

/** Legend rows shown before the rest are summarised: past this a legend is a
 *  second node list, not a key. */
const ROWS = 6;

interface Row {
  key: string;
  slot: number;
  label: string;
  count: number;
}

/**
 * The card over the whole-KB graph: what is drawn (the KB, how many concepts
 * and links), what the colours mean and the switch between the two things
 * they can mean, and the growth replay.
 *
 * Colour is never the only carrier here either: every row names its group and
 * counts it, and the inspector states a concept's Map in words.
 *
 * A small floating control, placed by the caller: shown only while no concept
 * is open (the reading panel then has the reader's attention, and the card
 * comes back when it closes) and never over a Map's graph, where the Map's
 * contract panel takes the left edge. It sits inside GraphView so the replay
 * button reaches the view's growth control.
 */
export function Legend({
  snapshot,
  communities,
  colorBy,
  onColorBy,
}: {
  snapshot: GraphSnapshot;
  communities: Communities;
  colorBy: ColorBy;
  onColorBy(value: ColorBy): void;
}) {
  const { rows, tail } = colorBy === "community" ? communityRows(communities, snapshot) : collectionRows(snapshot);
  const shown = rows.slice(0, ROWS);
  const rest = rows.slice(ROWS).reduce((sum, row) => sum + row.count, tail);
  const nodes = snapshot.nodes.length;
  const edges = snapshot.edges.length;

  return (
    <section className="legend" aria-label="Graph legend">
      <div className="legend__head">
        <p className="legend__context">
          <span className="legend__facts">
            {nodes} concept{nodes === 1 ? "" : "s"} · {edges} link{edges === 1 ? "" : "s"}
          </span>
        </p>
        <GrowthButton />
      </div>
      <div className="legend__switch" role="group" aria-label="Colour nodes by">
        {(
          [
            ["community", "Community"],
            ["map", "Map"],
          ] as const
        ).map(([value, label]) => (
          <button
            key={value}
            type="button"
            className="legend__option"
            aria-pressed={colorBy === value}
            onClick={() => onColorBy(value)}
          >
            {label}
          </button>
        ))}
      </div>
      <ul className="legend__rows">
        {shown.map((row) => (
          <li key={row.key} className="legend__row">
            <span className="legend__swatch" aria-hidden="true" style={{ background: slotVar(row.slot) }} />
            <span className="legend__label">{row.label}</span>
            <span className="legend__count">{row.count}</span>
          </li>
        ))}
        {rest > 0 && (
          <li className="legend__row legend__row--rest">
            {/* No swatch: the summarised groups do not share one colour. */}
            <span className="legend__swatch legend__swatch--none" aria-hidden="true" />
            <span className="legend__label">{colorBy === "community" ? "Smaller groups" : "Other Maps"}</span>
            <span className="legend__count">{rest}</span>
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
    else
      rows.push({
        key: `c${c.rank}`,
        slot: c.slot,
        label: c.anchorTitle || titles.get(c.anchor) || shortLabel(c.anchor),
        count,
      });
  }
  rows.sort((a, b) => b.count - a.count);
  return { rows, tail };
}

function collectionRows(snapshot: GraphSnapshot): { rows: Row[]; tail: number } {
  const counts = new Map<string, number>();
  for (const node of snapshot.nodes) {
    const name = node.collection ?? "";
    counts.set(name, (counts.get(name) ?? 0) + 1);
  }
  const rows = [...counts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([name, count]) => ({
      key: `m${name}`,
      slot: collectionHue(name),
      label: name || "(root)",
      count,
    }));
  return { rows, tail: 0 };
}
