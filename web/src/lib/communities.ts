import type { GraphSnapshot } from "../api/types";

/**
 * Communities: the colour channel that shows how a KB actually clusters, as
 * opposed to how it is filed.
 *
 * The per-collection hue answers "which Map is this in", and inside one Map
 * it paints every node the same colour. Communities of the link graph answer
 * "what does this belong with", which is what a force layout is already
 * drawing: the colour and the geometry then agree.
 *
 * The server computes them (D244), once, on the caller's whole visible graph:
 * Louvain in node order with no randomness, every community split into its
 * connected components, communities ranked by size, then by their smallest
 * member id. Computing them there, not here, is what keeps a scoped or
 * truncated view in whole-KB colours and lets agents see the same structure.
 * Ranks become colour slots: the twelve largest real communities take the
 * twelve hues in order, and everything else -- singletons, and the long tail a
 * large KB always has -- shares the neutral "other" slot, 0. This module only
 * adapts the server's answer to the shape the view reads.
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

/** The server's communities (D244) in the shape the view reads. A snapshot
 *  without them -- an older server -- colours every node with OTHER_SLOT. */
export function snapshotCommunities(snapshot: GraphSnapshot): Communities {
  const list: Community[] = (snapshot.communities ?? []).map((c) => ({
    rank: c.rank,
    slot: c.slot,
    size: c.size,
    anchor: c.anchor,
  }));
  const rankOf = new Map<string, number>();
  for (const node of snapshot.nodes) {
    if (node.community !== undefined && list[node.community]) rankOf.set(node.id, node.community);
  }
  return { rankOf, list };
}

/** The colour slot of a node: 1..12, or OTHER_SLOT. */
export function communitySlot(communities: Communities, id: string): number {
  const rank = communities.rankOf.get(id);
  return rank === undefined ? OTHER_SLOT : communities.list[rank]!.slot;
}
