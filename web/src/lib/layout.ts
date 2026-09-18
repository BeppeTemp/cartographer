import Graph from "graphology";
import forceAtlas2 from "graphology-layout-forceatlas2";
import type { GraphSnapshot } from "../api/types";

/**
 * Deterministic layout.
 *
 * ForceAtlas2 itself is deterministic given a starting position; what is not
 * is the usual random seeding. So the seed is a disc whose angle and radius
 * come from a hash of the node id, and the simulation runs a fixed number of
 * iterations. The same KB state therefore produces the same coordinates on
 * every machine and every reload -- a graph that reshuffles between two
 * identical requests is unusable as a navigation surface, because the user's
 * spatial memory of it is wrong every time.
 */
const ITERATIONS = 260;

function hash(text: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

/** A stable pseudo-random number in [0, 1) for a given key. */
export function seededUnit(key: string, salt = 0): number {
  return (hash(key + ":" + salt) % 100000) / 100000;
}

/**
 * snapshotFingerprint identifies a node set, so cached coordinates can be
 * reused only for the exact graph they were computed for. Built from the
 * server's own ordering, which is already sorted and stable.
 */
export function snapshotFingerprint(
  kb: string,
  scope: string | null,
  snapshot: GraphSnapshot,
): string {
  const ids = snapshot.nodes.map((n) => n.id).join("|");
  return `${kb}/${scope ?? ""}/${snapshot.nodes.length}.${snapshot.edges.length}.${hash(ids).toString(36)}`;
}

export interface Positions {
  [id: string]: { x: number; y: number };
}

export function buildGraph(snapshot: GraphSnapshot, positions?: Positions): Graph {
  const graph = new Graph({ type: "directed", multi: false });

  snapshot.nodes.forEach((node, index) => {
    const seeded = positions?.[node.id];
    // The seed spreads nodes over a disc rather than a ring: a ring starts
    // ForceAtlas2 from a degenerate configuration that takes far more
    // iterations to resolve.
    const angle = seededUnit(node.id, 1) * Math.PI * 2;
    const radius = Math.sqrt(seededUnit(node.id, 2)) * 100 + (index % 7);
    graph.addNode(node.id, {
      x: seeded?.x ?? Math.cos(angle) * radius,
      y: seeded?.y ?? Math.sin(angle) * radius,
      size: nodeSize(node.in_degree + node.out_degree),
      degree: node.in_degree + node.out_degree,
      collection: node.collection ?? "",
      expanded: node.expanded ?? false,
      selfLink: node.self_link ?? false,
      inDegree: node.in_degree,
      outDegree: node.out_degree,
    });
  });

  for (const edge of snapshot.edges) {
    if (!graph.hasNode(edge.source) || !graph.hasNode(edge.target)) continue;
    if (graph.hasDirectedEdge(edge.source, edge.target)) continue;
    graph.addDirectedEdge(edge.source, edge.target);
  }
  return graph;
}

/**
 * Bounded degree scaling. A hub with 300 links must read as a hub without
 * swallowing the layout, so size grows with the square root of degree and is
 * clamped: linear scaling turns one node into the whole canvas.
 */
export function nodeSize(degree: number): number {
  return Math.min(4 + Math.sqrt(degree) * 2.4, 18);
}

export function applyLayout(graph: Graph): Positions {
  if (graph.order === 0) return {};
  if (graph.order > 1) {
    forceAtlas2.assign(graph, {
      iterations: ITERATIONS,
      settings: {
        ...forceAtlas2.inferSettings(graph),
        barnesHutOptimize: graph.order > 500,
        adjustSizes: true,
        gravity: 1.2,
        slowDown: 8,
      },
    });
  }
  const positions: Positions = {};
  graph.forEachNode((id, attrs) => {
    positions[id] = { x: attrs.x as number, y: attrs.y as number };
  });
  return positions;
}

const CACHE_PREFIX = "cartographer.layout.";

/**
 * Coordinates are cached per fingerprint so a reload does not re-run the
 * simulation. Only coordinates: never a token, never a concept body. Every
 * access is guarded -- a private window throws on the first read.
 */
export function readCachedPositions(fingerprint: string): Positions | null {
  try {
    const raw = localStorage.getItem(CACHE_PREFIX + fingerprint);
    return raw ? (JSON.parse(raw) as Positions) : null;
  } catch {
    return null;
  }
}

export function writeCachedPositions(fingerprint: string, positions: Positions): void {
  try {
    localStorage.setItem(CACHE_PREFIX + fingerprint, JSON.stringify(positions));
  } catch {
    // Quota or a private window: the layout is recomputed next time, which is
    // slower but never wrong.
  }
}
