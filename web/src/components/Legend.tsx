import type { GraphSnapshot } from "../api/types";
import { OTHER_SLOT, type Communities } from "../lib/communities";
import { shortLabel } from "../lib/encoding";
import { collectionHue, slotVar, type ColorBy } from "../lib/palette";

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
 * What the colours mean, and the switch between the two things they can mean.
 *
 * Colour is never the only carrier here either: every row names its group and
 * counts it, and the inspector states a concept's Map in words.
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
  const { rows, tail } =
    colorBy === "community" ? communityRows(communities) : collectionRows(snapshot);
  const shown = rows.slice(0, ROWS);
  const rest = rows.slice(ROWS).reduce((sum, row) => sum + row.count, tail);

  return (
    <section className="legend" aria-label="Graph legend">
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
            <span
              className="legend__swatch"
              aria-hidden="true"
              style={{ background: slotVar(row.slot) }}
            />
            <span className="legend__label">{row.label}</span>
            <span className="legend__count">{row.count}</span>
          </li>
        ))}
        {rest > 0 && (
          <li className="legend__row legend__row--rest">
            {/* No swatch: the summarised groups do not share one colour. */}
            <span className="legend__swatch legend__swatch--none" aria-hidden="true" />
            <span className="legend__label">
              {colorBy === "community" ? "Smaller groups" : "Other Maps"}
            </span>
            <span className="legend__count">{rest}</span>
          </li>
        )}
      </ul>
    </section>
  );
}

function communityRows(communities: Communities): { rows: Row[]; tail: number } {
  // Real communities get rows; singletons and the long tail share one neutral
  // and are only ever summed into the closing row.
  const rows: Row[] = [];
  let tail = 0;
  for (const c of communities.list) {
    if (c.slot === OTHER_SLOT) tail += c.size;
    else rows.push({ key: `c${c.rank}`, slot: c.slot, label: shortLabel(c.anchor), count: c.size });
  }
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
