import Graph from "graphology";
import louvain from "graphology-communities-louvain";
import type { GraphSnapshot } from "../api/types";

/**
 * Community detection: the colour channel that shows how a KB actually
 * clusters, as opposed to how it is filed.
 *
 * The per-collection hue answers "which Map is this in", and inside one Map
 * it paints every node the same colour -- the flatness the first pass was
 * criticised for. Louvain over the link graph answers "what does this belong
 * with", which is what a force layout is already drawing: the colour and the
 * geometry then agree.
 *
 * Three things make it deterministic, and all three are load-bearing, because
 * a node that changes colour on reload is as disorienting as one that moves:
 *   - the input graph is built in the server's order, which is sorted;
 *   - Louvain runs without its random walk and with a seeded generator, so
 *     nothing it does depends on Math.random;
 *   - Louvain's community numbers are arbitrary, so they are thrown away and
 *     communities are re-ranked by size, then by their smallest member id.
 * Ranks become colour slots: the twelve largest real communities take the
 * twelve hues in order, and everything else -- singletons, and the long tail a
 * large KB always has -- shares the neutral "other" slot, 0. Thirteen colours
 * cannot be told apart; twelve plus a quiet remainder can.
 */

/** Hue slots available in tokens.css (--hue-1 .. --hue-12). */
export const COMMUNITY_SLOTS = 12;
/** The slot for singletons and the long tail: --graph-community-other. */
export const OTHER_SLOT = 0;

export interface Community {
  /** 0-based rank: 0 is the largest community. */
  rank: number;
  /** 1..12, or OTHER_SLOT. */
  slot: number;
  size: number;
  /** The member with the most links (then the smallest id): what the legend
   *  names the community after, since communities have no name of their own. */
  anchor: string;
}

export interface Communities {
  /** node id -> rank */
  rankOf: Map<string, number>;
  /** ordered by rank */
  list: Community[];
}

/** mulberry32: a tiny seeded PRNG, so Louvain never reaches for Math.random. */
function seeded(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const SEED = 0x5eed;

export function detectCommunities(snapshot: GraphSnapshot): Communities {
  // Undirected on purpose: "A links to B" and "B links to A" are the same
  // evidence that A and B belong together, and directed modularity splits
  // reference hubs from the pages that cite them.
  const graph = new Graph({ type: "undirected", multi: false });
  for (const node of snapshot.nodes) graph.addNode(node.id);
  for (const edge of snapshot.edges) {
    if (edge.source === edge.target) continue;
    if (!graph.hasNode(edge.source) || !graph.hasNode(edge.target)) continue;
    if (graph.hasEdge(edge.source, edge.target)) continue;
    graph.addEdge(edge.source, edge.target);
  }

  const raw =
    graph.order === 0
      ? {}
      : louvain(graph, { randomWalk: false, rng: seeded(SEED), getEdgeWeight: null });

  const degree = new Map<string, number>();
  for (const node of snapshot.nodes) degree.set(node.id, node.in_degree + node.out_degree);

  const members = new Map<number, string[]>();
  for (const node of snapshot.nodes) {
    const id = raw[node.id];
    if (id === undefined) continue;
    const list = members.get(id);
    if (list) list.push(node.id);
    else members.set(id, [node.id]);
  }

  const groups = [...members.values()].map((ids) => {
    const sorted = [...ids].sort();
    let anchor = sorted[0]!;
    for (const id of sorted) {
      if ((degree.get(id) ?? 0) > (degree.get(anchor) ?? 0)) anchor = id;
    }
    return { ids: sorted, anchor };
  });
  groups.sort((a, b) => b.ids.length - a.ids.length || (a.ids[0]! < b.ids[0]! ? -1 : 1));

  const rankOf = new Map<string, number>();
  const list: Community[] = groups.map((group, rank) => {
    for (const id of group.ids) rankOf.set(id, rank);
    const real = group.ids.length > 1;
    return {
      rank,
      slot: real && rank < COMMUNITY_SLOTS ? rank + 1 : OTHER_SLOT,
      size: group.ids.length,
      anchor: group.anchor,
    };
  });
  return { rankOf, list };
}

/** The colour slot of a node: 1..12, or OTHER_SLOT. */
export function communitySlot(communities: Communities, id: string): number {
  const rank = communities.rankOf.get(id);
  return rank === undefined ? OTHER_SLOT : communities.list[rank]!.slot;
}
