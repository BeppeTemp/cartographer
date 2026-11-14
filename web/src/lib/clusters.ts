import { currentSource, STATIC_MODE } from "../api/static";
import type { ClusterGraph, ClusterUnfold, GraphCluster, GraphSnapshot } from "../api/types";
import { collectionHue } from "./palette";
import { hashId } from "./graph3d/physics";

/**
 * Above this many visible concepts the Atlas asks for the clustered graph
 * (`kb.ClusterThreshold`): where the flat view starts truncating. A clusters
 * response carries the server's own value, which replaces this one.
 */
export const DEFAULT_CLUSTER_THRESHOLD = 1200;

/** A cluster id: never a concept id, which cannot start with `c:`. */
export function isClusterID(id: string): boolean {
  return id.startsWith("c:");
}

/**
 * Whether a source can answer the clusters route: a static snapshot exports
 * the flat graph only (demo/export.mjs), a live origin answers like a server.
 */
export function clustersAvailable(): boolean {
  return !STATIC_MODE || currentSource() === "live";
}

/**
 * The flat snapshot the rest of the Atlas reads (filters, node list, palette,
 * legend): the drawn concepts and the links between two of them. Clusters and
 * the edges that reach one travel apart, in the ClusterGraph itself.
 */
export function clusterSnapshot(g: ClusterGraph): GraphSnapshot {
  return {
    nodes: g.nodes,
    edges: g.edges
      .filter((e) => !isClusterID(e.source) && !isClusterID(e.target))
      .map(({ source, target }) => ({ source, target })),
    truncated: g.truncated,
    total_nodes: g.total_nodes,
    total_edges: g.total_edges,
    limit: g.threshold,
    communities: g.communities,
  };
}

/** The colour slot a super-node is drawn in: its dominant Map's hue, so that none is grey and the
 *  legend, the rail and the graph agree. */
export function clusterSlot(c: { collection?: string }): number {
  return collectionHue(c.collection ?? "");
}

/**
 * How many links the Nodes view draws and lays out before it thins them: past
 * this a hairball of links adds cost, not information. Every node keeps its
 * strongest links (towards the best-connected neighbours), so the skeleton of
 * the graph stays and the weak cross links go.
 */
export const EDGE_BUDGET = 6000;

/**
 * `edges` thinned to at most `budget`: each node keeps its `floor(budget /
 * nodes)` links to the neighbours of highest degree (at least one), the union
 * of what each node keeps being drawn. Under the budget it is returned whole.
 */
export function budgetEdges<E extends { source: string; target: string }>(
  edges: E[],
  degree: (id: string) => number,
  nodeCount: number,
  budget = EDGE_BUDGET,
): E[] {
  if (edges.length <= budget) return edges;
  const k = Math.max(1, Math.floor(budget / Math.max(1, nodeCount)));
  const incident = new Map<string, number[]>();
  edges.forEach((e, i) => {
    for (const id of [e.source, e.target]) (incident.get(id) ?? incident.set(id, []).get(id)!).push(i);
  });
  const kept = new Set<number>();
  for (const [id, list] of incident) {
    const other = (i: number) => {
      const e = edges[i]!;
      return degree(e.source === id ? e.target : e.source);
    };
    list.sort((a, b) => other(b) - other(a) || a - b);
    for (const i of list.slice(0, k)) kept.add(i);
  }
  return edges.filter((_, i) => kept.has(i));
}

/**
 * The glyph each loose concept component (the concepts the overview leaves open
 * that link one another, or one alone) is anchored to, for the layout: one with
 * a link to a glyph needs none; any other component is tied, every concept of it, to one glyph of its dominant Map, spread over that Map's glyphs
 * by a hash of the component's id (else over all glyphs), so a small community
 * stays together and scatters among the glyphs rather than being torn apart.
 * `skip` holds what is not loose (the members of an open cluster).
 */
export function anchorGlyphs(
  nodes: { id: string; collection?: string }[],
  clusters: GraphCluster[],
  edges: { source: string; target: string }[],
  skip: Set<string>,
  looseEdges: { source: string; target: string }[] = [],
): Map<string, string> {
  const out = new Map<string, string>();
  if (clusters.length === 0) return out;
  const glyphs = new Set(clusters.map((c) => c.id));
  const linked = new Set<string>();
  for (const e of edges) {
    if (glyphs.has(e.target)) linked.add(e.source);
    if (glyphs.has(e.source)) linked.add(e.target);
  }
  const bySize = [...clusters].sort((a, b) => b.size - a.size || a.id.localeCompare(b.id));
  const byMap = new Map<string, GraphCluster[]>();
  for (const c of bySize) (byMap.get(c.collection ?? "") ?? byMap.set(c.collection ?? "", []).get(c.collection ?? "")!).push(c);
  // The loose concepts, grouped into the components their links make.
  const loose = nodes.filter((n) => !skip.has(n.id));
  const parent = new Map(loose.map((n) => [n.id, n.id]));
  const find = (id: string): string => {
    let r = id;
    while (parent.get(r) !== r) r = parent.get(r)!;
    for (let x = id; x !== r; ) {
      const next = parent.get(x)!;
      parent.set(x, r);
      x = next;
    }
    return r;
  };
  for (const e of looseEdges) {
    if (parent.has(e.source) && parent.has(e.target)) parent.set(find(e.source), find(e.target));
  }
  const components = new Map<string, typeof loose>();
  for (const n of loose) (components.get(find(n.id)) ?? components.set(find(n.id), []).get(find(n.id))!).push(n);
  for (const members of components.values()) {
    // A component with a link to a glyph is held by it already.
    if (members.some((n) => linked.has(n.id))) continue;
    const rep = members.reduce((a, b) => (b.id < a.id ? b : a));
    const counts = new Map<string, number>();
    for (const n of members) counts.set(n.collection ?? "", (counts.get(n.collection ?? "") ?? 0) + 1);
    const dominant = [...counts.entries()].sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1))[0]![0];
    const pool = byMap.get(dominant) ?? bySize;
    // All of them to the same glyph (each at its own distance): the community stays together.
    const glyph = pool[hashId(rep.id) % pool.length]!.id;
    for (const n of members) out.set(n.id, glyph);
  }
  return out;
}

/** An unfolded cluster's drawn members: its `members` are positions in the snapshot's nodes. */
export const unfoldedMemberIds = (u: ClusterUnfold, snapshot: GraphSnapshot): string[] =>
  u.members.flatMap((i) => (snapshot.nodes[i] ? [snapshot.nodes[i]!.id] : []));

/** The unfold set in a stable order, for a cache key. */
export function unfoldKey(unfold: string[]): string {
  return [...unfold].sort().join(",");
}
