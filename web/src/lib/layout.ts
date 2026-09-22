import Graph from "graphology";
import forceAtlas2 from "graphology-layout-forceatlas2";
import type { GraphSnapshot } from "../api/types";
import type { Communities } from "./communities";

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
/**
 * Iterations scale down with size, because the one-shot layout runs on the
 * main thread before the graph can be shown: 400 on a 2,000-node graph blocked
 * it for well over a second. The community-seeded start (seedPosition) is
 * already close to the answer, so a large graph needs fewer steps to settle,
 * and the live worker keeps refining after first paint. A function of the node
 * count only, so it stays deterministic.
 */
export function layoutIterations(order: number): number {
  if (order > 1000) return 100;
  if (order > 300) return 200;
  return 400;
}

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

/**
 * An edge inside one community pulls this many times harder than an edge
 * across two. It is what makes clusters read as bodies rather than as a
 * uniform mesh: the layout and the community colour then say the same thing,
 * and a bridge concept visibly sits between the groups it connects.
 */
export const INTRA_COMMUNITY_WEIGHT = 6;

export function buildGraph(
  snapshot: GraphSnapshot,
  positions?: Positions,
  communities?: Communities,
): Graph {
  const graph = new Graph({ type: "directed", multi: false });

  snapshot.nodes.forEach((node, index) => {
    const seeded = positions?.[node.id] ?? seedPosition(node.id, index, communities);
    graph.addNode(node.id, {
      x: seeded.x,
      y: seeded.y,
      size: nodeSize(node.in_degree + node.out_degree),
      degree: node.in_degree + node.out_degree,
      collection: node.collection ?? "",
      expanded: node.expanded ?? false,
      selfLink: node.self_link ?? false,
      inDegree: node.in_degree,
      outDegree: node.out_degree,
      community: communities?.rankOf.get(node.id) ?? -1,
    });
  });

  for (const edge of snapshot.edges) {
    if (!graph.hasNode(edge.source) || !graph.hasNode(edge.target)) continue;
    if (graph.hasDirectedEdge(edge.source, edge.target)) continue;
    const a = communities?.rankOf.get(edge.source);
    const b = communities?.rankOf.get(edge.target);
    graph.addDirectedEdge(edge.source, edge.target, {
      weight: a !== undefined && a === b ? INTRA_COMMUNITY_WEIGHT : 1,
    });
  }
  return graph;
}

/**
 * Where a node starts before ForceAtlas2 runs.
 *
 * With communities known, each community starts as a small disc of its own,
 * the discs laid out on a golden-angle spiral by rank. Starting from one
 * uniform disc instead, a few hundred iterations are not enough for LinLog to
 * pull communities apart and the result is an even, colour-speckled mesh --
 * the "flat" first pass. Everything is derived from the node id and the
 * community rank, never from Math.random, so the seed stays deterministic.
 *
 * Without communities the seed is a disc rather than a ring: a ring starts
 * ForceAtlas2 from a degenerate configuration that takes far more iterations
 * to resolve.
 */
function seedPosition(
  id: string,
  index: number,
  communities?: Communities,
): { x: number; y: number } {
  const angle = seededUnit(id, 1) * Math.PI * 2;
  const rank = communities?.rankOf.get(id);
  if (rank === undefined) {
    const radius = Math.sqrt(seededUnit(id, 2)) * 100 + (index % 7);
    return { x: Math.cos(angle) * radius, y: Math.sin(angle) * radius };
  }
  const size = communities!.list[rank]!.size;
  const centreAngle = rank * GOLDEN_ANGLE;
  const centreRadius = 60 * Math.sqrt(rank + 1);
  const spread = Math.sqrt(size) * 6 * Math.sqrt(seededUnit(id, 2));
  return {
    x: Math.cos(centreAngle) * centreRadius + Math.cos(angle) * spread,
    y: Math.sin(centreAngle) * centreRadius + Math.sin(angle) * spread,
  };
}

const GOLDEN_ANGLE = Math.PI * (3 - Math.sqrt(5));

/**
 * Bounded degree scaling. A hub with 300 links must read as a hub without
 * swallowing the layout, so size grows with the square root of degree and is
 * clamped: linear scaling turns one node into the whole canvas.
 */
export function nodeSize(degree: number): number {
  return Math.min(3 + Math.sqrt(degree) * 2.2, 14);
}

/**
 * The force model, shared by the one-shot deterministic layout and the live
 * worker so a drag re-settles under the same physics the picture was drawn
 * with -- two different parameter sets make the graph lurch the moment the
 * worker takes over.
 *
 * LinLog mode is what gives the dense, clustered look: attraction grows with
 * the log of distance, so communities contract into bodies with air between
 * them instead of spreading into one even disc. Strong gravity keeps
 * disconnected components from drifting off-screen, which LinLog otherwise
 * encourages. Edge weights (INTRA_COMMUNITY_WEIGHT) and the community-seeded
 * start (seedPosition) do the rest.
 */
export function layoutSettings(graph: Graph, slowDown = 6) {
  return {
    ...forceAtlas2.inferSettings(graph),
    linLogMode: true,
    strongGravityMode: true,
    gravity: 0.08,
    scalingRatio: 14,
    edgeWeightInfluence: 1,
    barnesHutOptimize: graph.order > 300,
    // Anti-collision: LinLog packs a community tightly, and without this its
    // members pile onto each other.
    adjustSizes: true,
    slowDown,
  };
}

export function applyLayout(graph: Graph): Positions {
  if (graph.order === 0) return {};
  if (graph.order > 1) {
    forceAtlas2.assign(graph, {
      iterations: layoutIterations(graph.order),
      settings: layoutSettings(graph),
    });
    ringIsolates(graph);
  }
  const positions: Positions = {};
  graph.forEachNode((id, attrs) => {
    positions[id] = { x: attrs.x as number, y: attrs.y as number };
  });
  return positions;
}

/**
 * ringIsolates puts every node with no link on a ring just outside the
 * connected body, evenly spaced in id order. Repulsion alone flings them far
 * out, and since the renderer fits the whole bounding box to the screen, a
 * handful of stray orphans shrank the part of the graph that has structure to
 * a small blob in the middle. On the ring they stay visible -- an orphan is a
 * finding, not noise -- without deciding the zoom. Deterministic: ids sort the
 * same way everywhere.
 */
export function ringIsolates(graph: Graph): void {
  const isolates: string[] = [];
  let cx = 0;
  let cy = 0;
  let linked = 0;
  graph.forEachNode((id, attrs) => {
    if (graph.degree(id) === 0) {
      isolates.push(id);
      return;
    }
    cx += attrs.x as number;
    cy += attrs.y as number;
    linked++;
  });
  if (isolates.length === 0 || linked === 0) return;
  cx /= linked;
  cy /= linked;
  let radius = 0;
  graph.forEachNode((id, attrs) => {
    if (graph.degree(id) === 0) return;
    radius = Math.max(radius, Math.hypot((attrs.x as number) - cx, (attrs.y as number) - cy));
  });
  radius *= 1.12;
  isolates.sort();
  isolates.forEach((id, i) => {
    const angle = (i / isolates.length) * Math.PI * 2 - Math.PI / 2;
    graph.mergeNodeAttributes(id, { x: cx + Math.cos(angle) * radius, y: cy + Math.sin(angle) * radius });
  });
}

/**
 * Bumped whenever layoutSettings, the seed or the edge weights change: the
 * cache is keyed by node set, not by physics, so without the version a
 * returning viewer would keep the old picture forever.
 */
export const LAYOUT_VERSION = 4;
const CACHE_PREFIX = `cartographer.layout.v${LAYOUT_VERSION}.`;

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
