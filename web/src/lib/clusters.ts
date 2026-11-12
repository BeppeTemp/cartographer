import { currentSource, STATIC_MODE } from "../api/static";
import type { ClusterGraph, GraphSnapshot } from "../api/types";

/**
 * Above this many visible concepts the Atlas asks for the clustered graph
 * (`kb.ClusterThreshold`): where the flat view starts truncating. A clusters
 * response carries the server's own value, which replaces this one.
 */
export const DEFAULT_CLUSTER_THRESHOLD = 2000;

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

/** The unfold set in a stable order, for a cache key. */
export function unfoldKey(unfold: string[]): string {
  return [...unfold].sort().join(",");
}
