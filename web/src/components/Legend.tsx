import type { ClusterUnfold, GraphCluster, GraphSnapshot } from "../api/types";
import { collectionHue, slotVar } from "../lib/palette";
import { clusterSlot, unfoldedMemberIds } from "../lib/clusters";
import { GrowthButton } from "./GraphView";
import { Icon } from "./Icon";

/** Legend rows shown before the rest are summarised: past this a legend is a
 *  second node list, not a key. */
const ROWS = 6;
/** In the cluster view: how many clusters (the open one included), and how many
 *  Maps of open concepts, are listed. With their two summary rows they add up
 *  to the flat view's ROWS plus its one: the card keeps its height when the KB
 *  is expanded or grouped. */
const CLUSTER_ROWS = 3;
const LOOSE_ROWS = ROWS - CLUSTER_ROWS - 1;

const plural = (n: number, one: string) => `${n.toLocaleString("en")} ${one}${n === 1 ? "" : "s"}`;

interface Row {
  key: string;
  slot: number;
  label: string;
  count: number;
}

/**
 * The card over the graph: what the snapshot holds (the KB or Map, how many
 * concepts and links; the filters in the page do not narrow it), what the
 * colours mean (the Map's, always), the switch between the grouped and the
 * full view of a large KB, and the growth replay.
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
  clusters,
  open,
  grouping,
}: {
  snapshot: GraphSnapshot;
  /** The folded clusters of a large KB: the snapshot holds only the unfolded
   *  concepts, so the rows are counted over the clusters, in the same form. */
  clusters?: GraphCluster[];
  /** The open cluster, if any: its members are one row of their own, not part of the rest. */
  open?: ClusterUnfold[];
  /** Only on a KB served clustered: whether it is grouped now, and the switch to the other view. */
  grouping?: { grouped: boolean; onToggle(): void };
}) {
  // Colour is the Map's, everywhere (the rail's colours): the card lists the
  // clusters first, when the KB is grouped, then the Maps.
  const members = new Set((open ?? []).flatMap((u) => unfoldedMemberIds(u, snapshot)));
  const rest = members.size > 0 ? { ...snapshot, nodes: snapshot.nodes.filter((n) => !members.has(n.id)) } : snapshot;
  const loose = collectionRows(rest);
  const list: { key: string; slot: number | null; label: string; count: number }[] = [];
  if (clusters) {
    // The open cluster first: "Catalyst · 244, open", in the colour it was folded in.
    for (const u of open ?? []) {
      list.push({
        key: u.id,
        slot: clusterSlot({ collection: u.collection }),
        label: `${u.label}, open`,
        count: u.total,
      });
    }
    const all = clusterRows(clusters).rows;
    const top = all.slice(0, Math.max(0, CLUSTER_ROWS - (open ?? []).length));
    list.push(...top);
    const more = all.slice(top.length);
    if (more.length > 0) {
      list.push({ key: "more", slot: null, label: plural(more.length, "more cluster"), count: more.reduce((n, r) => n + r.count, 0) });
    }
  }
  const shownMaps = loose.rows.slice(0, clusters ? LOOSE_ROWS : ROWS);
  list.push(...shownMaps);
  const moreMaps = loose.rows.slice(shownMaps.length);
  if (moreMaps.length > 0) {
    list.push({ key: "rest", slot: null, label: plural(moreMaps.length, "more Map"), count: moreMaps.reduce((n, r) => n + r.count, 0) });
  }
  // Folded concepts count too: the snapshot holds only the unfolded ones.
  const nodes = clusters ? snapshot.total_nodes : snapshot.nodes.length;
  const edges = clusters ? snapshot.total_edges : snapshot.edges.length;

  // The switch's next step: an open cluster folds first, then the KB expands whole, then groups again.
  const step = !grouping?.grouped
    ? ({ label: "Group into clusters", text: "Group", icon: "components" } as const)
    : (open ?? []).length > 0
      ? ({ label: "Fold the open cluster", text: "Compact", icon: "components" } as const)
      : ({ label: "Show all concepts", text: "Expand", icon: "fit" } as const);

  return (
    <section className="legend" aria-label="Graph legend">
      <div className="legend__head">
        <p className="legend__context">
          <span className="legend__facts">
            {nodes.toLocaleString("en")} concept{nodes === 1 ? "" : "s"} · {edges.toLocaleString("en")} link
            {edges === 1 ? "" : "s"}
          </span>
        </p>
      </div>
      <div className="legend__actions">
        <GrowthButton />
        {grouping && (
          <button
            type="button"
            className="legend__grow"
            onClick={grouping.onToggle}
            aria-label={step.label}
            title={step.label}
          >
            <Icon name={step.icon} size={14} />
            <span aria-hidden="true">{step.text}</span>
          </button>
        )}
      </div>
      <ul className="legend__rows">
        {list.map((row) => (
          <li key={row.key} className={row.slot === null ? "legend__row legend__row--rest" : "legend__row"}>
            {/* No swatch on a summary: the groups it sums do not share one colour. */}
            <span
              className={row.slot === null ? "legend__swatch legend__swatch--none" : "legend__swatch"}
              aria-hidden="true"
              style={row.slot === null ? undefined : { background: slotVar(row.slot) }}
            />
            <span className="legend__label">{row.label}</span>
            <span className="legend__count">{row.count.toLocaleString("en")}</span>
          </li>
        ))}
      </ul>
    </section>
  );
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

/** The folded clusters as legend rows, largest first, each in the colour it is drawn in. */
function clusterRows(clusters: GraphCluster[]): { rows: Row[]; tail: number } {
  const rows = [...clusters]
    .sort((a, b) => b.size - a.size || a.id.localeCompare(b.id))
    .map((c) => ({ key: c.id, slot: clusterSlot(c), label: c.label, count: c.size }));
  return { rows, tail: 0 };
}
