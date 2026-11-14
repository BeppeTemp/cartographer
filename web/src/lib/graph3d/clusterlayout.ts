import { forceCollide, type Simulation } from "d3-force-3d";
import {
  hashId,
  CHARGE_STRENGTH,
  GRAVITY,
  LINK_DISTANCE,
  endpoint,
  type PhysicsNode,
  type SpringLink,
} from "./physics";

/**
 * The layout of a graph with super-nodes (the cluster super-nodes decision), kept
 * apart from the scene so it can be run, and tested, without WebGL: the forces
 * of the folded clusters and of an open cluster (its members are laid out by the
 * simulation like any concepts).
 */

export interface LayoutNode extends PhysicsNode {
  id: string;
  /** 0..1 visual weight. */
  weight: number;
  cluster?: boolean;
  /** A loose concept with no link to a glyph: the glyph it is anchored to (a weak spring). */
  anchor?: string;
}

/** The smallest, in world units, a concept's disc is drawn (a leaf) and the largest (a hub). */
export const LEAF_RADIUS = LINK_DISTANCE * 0.12;
const HUB_RADIUS = LEAF_RADIUS * 2.36;
/** A folded cluster's glyph, in leaf radii: 1.95 for weight 0 up to 2.85 for the heaviest. Weights start at
 *  .35 (1.6x an average concept) and are already a square root of the members, so the glyph grows with them
 *  but is capped at 2x: clearly a group, never dominating. */
const GLYPH_MIN = 1.95;
const GLYPH_SPAN = 0.9;
/** The radius a node is drawn with, in world units. */
export function radiusOf(node: LayoutNode): number {
  if (node.cluster) return LEAF_RADIUS * (GLYPH_MIN + GLYPH_SPAN * Math.min(1, Math.max(0, node.weight)));
  return LEAF_RADIUS + (HUB_RADIUS - LEAF_RADIUS) * node.weight;
}

/** Space kept between two super-nodes' rings, in world units. */
const CLUSTER_PADDING = 14;
/** How far a super-node's repulsion reaches. */
const CLUSTER_CHARGE_REACH = 1200;
/** A concept the overview leaves open: held in towards the glyphs by this many
 *  times the gravity of a concept, firmer when it has no link at all. */
const LOOSE_GRAVITY = 1.5;
const LOOSE_ISOLATED_GRAVITY = 8;
/** A super-node with no super-edge is held in by this many times the gravity of a concept. */
const ISOLATED_GRAVITY = 5;

/** The air kept between two members' discs, in world units. */
const MEMBER_GAP = 4;

/** An open cluster: the members it unfolded into. */
export interface OpenHull {
  members: LayoutNode[];
}

export function centroid(nodes: LayoutNode[], fallback = { x: 0, y: 0, z: 0 }): { x: number; y: number; z: number } {
  const placed = nodes.filter((n) => Number.isFinite(n.x) && Number.isFinite(n.y) && Number.isFinite(n.z));
  if (placed.length === 0) return fallback;
  return {
    x: placed.reduce((sum, n) => sum + n.x!, 0) / placed.length,
    y: placed.reduce((sum, n) => sum + n.y!, 0) / placed.length,
    z: placed.reduce((sum, n) => sum + n.z!, 0) / placed.length,
  };
}

/** A point in the scene. */
export interface Point {
  x: number;
  y: number;
  z: number;
}

/**
 * Tunes a simulation (link, charge, x/y/z gravity) for super-nodes: springs
 * along super-edges that are shorter and stiffer with their weight, repulsion
 * that grows with the glyph and reaches across the picture, a centring firmest
 * on a cluster with no super-edge, and a collision of the glyph as drawn (an
 * open cluster's members keep a little air between their discs).
 */
export function configureClusterForces(
  sim: Simulation<LayoutNode>,
  springs: SpringLink[],
  byId: Map<string, LayoutNode>,
  nodes: LayoutNode[],
  open: OpenHull[],
): void {
  const degree = new Map<string, number>();
  for (const l of springs) {
    for (const id of [endpoint(l.source), endpoint(l.target)]) degree.set(id, (degree.get(id) ?? 0) + 1);
  }
  const ends = (l: SpringLink) => [byId.get(endpoint(l.source)), byId.get(endpoint(l.target))] as const;
  const link = sim.force("link") as unknown as {
    strength(f: (l: SpringLink) => number): void;
    distance(f: (l: SpringLink) => number): void;
  };
  // A loose concept with no link to a glyph is tied to the glyph its Map has:
  // a weak spring resting a little outside it (spread by its id, so the loose
  // ones scatter around the glyph, not stack on it).
  const anchored = new Set<string>();
  const extra: SpringLink[] = [];
  for (const n of nodes) {
    if (n.cluster || !n.anchor || !byId.has(n.anchor)) continue;
    anchored.add(n.id);
    extra.push({ source: n.id, target: n.anchor, weight: 1, anchor: true } as SpringLink);
  }
  const linkForce = sim.force("link") as unknown as { links(): SpringLink[]; links(l: SpringLink[]): void };
  if (extra.length > 0) linkForce.links([...linkForce.links(), ...extra]);
  // An open cluster's members are concepts like any other: their links to the
  // folded clusters are the springs of a concept to a glyph, so they mix into
  // the graph by its general rules instead of standing apart as a lobe.
  const hullMembers = new Set(open.flatMap((h) => h.members));
  // A super-edge rests the two glyphs' radii apart plus a gap that shrinks as
  // it stands for more links: strongly linked clusters sit close.
  link.distance((l) => {
    const [a, b] = ends(l);
    if ((l as { anchor?: boolean }).anchor) {
      const glyph = a?.cluster ? a : b;
      const loose = a?.cluster ? b : a;
      return (glyph ? radiusOf(glyph) : 0) + 12 + 18 * ((hashId(loose?.id ?? "") % 1000) / 1000);
    }
    const gap =
      a?.cluster && b?.cluster ? (LINK_DISTANCE * 3) / (1 + 0.4 * Math.log2(1 + (l.weight ?? 1))) : LINK_DISTANCE;
    return gap + (a ? radiusOf(a) : 0) + (b ? radiusOf(b) : 0);
  });
  link.strength((l) => {
    const [a, b] = ends(l);
    // Stiff enough to hold against the glyph's own repulsion at a short rest length.
    if ((l as { anchor?: boolean }).anchor) return 0.9;
    // Two loose concepts, each tied to a glyph: their link must not pull them off it.
    if (!a?.cluster && !b?.cluster && a && b && anchored.has(a.id) && anchored.has(b.id) && a.anchor !== b.anchor) return 0.03;
    // A concept linked to a glyph stays beside it, against the glyph's repulsion.
    if (!!a?.cluster !== !!b?.cluster) return 0.9;
    if (a?.cluster || b?.cluster) return Math.min(0.7, 0.12 + 0.1 * Math.log2(1 + (l.weight ?? 1)));
    return 1 / Math.min(degree.get(endpoint(l.source)) ?? 1, degree.get(endpoint(l.target)) ?? 1);
  });
  const charge = sim.force("charge") as unknown as {
    strength(f: (n: LayoutNode) => number): void;
    distanceMax(v: number): void;
  };
  // Enough repulsion for the overview to breathe like a flat KB of the same size: a glyph pushes as hard as a body its size.
  charge.strength((n) => (n.cluster ? CHARGE_STRENGTH * (1 + 0.5 * (radiusOf(n) / LEAF_RADIUS) ** 2) : CHARGE_STRENGTH));
  charge.distanceMax(CLUSTER_CHARGE_REACH);
  for (const axis of ["x", "y", "z"]) {
    const gravity = sim.force(axis) as unknown as { strength(f: (n: LayoutNode) => number): void } | undefined;
    gravity?.strength((n) => {
      // Held in like a concept of a flat KB, a little firmer: the glyphs spread, they do not pack at the centre.
      if (n.cluster) return GRAVITY * (degree.has(n.id) ? 1.5 : ISOLATED_GRAVITY);
      // An open cluster's members are held like any concept: by their links and the centring.
      // A loose concept belongs among the glyphs, not on a ring around them: tied
      // to its glyph it needs little; with nothing to tie it, a firm centring.
      if (anchored.has(n.id)) return GRAVITY;
      return GRAVITY * (degree.has(n.id) ? LOOSE_GRAVITY : LOOSE_ISOLATED_GRAVITY);
    });
  }
  // The collision is the glyph as drawn, with a margin.
  sim.force(
    "collide",
    forceCollide<LayoutNode>((n) =>
      n.cluster ? radiusOf(n) * 1.25 + CLUSTER_PADDING : radiusOf(n) + (hullMembers.has(n) ? MEMBER_GAP : 0),
    )
      .strength(1)
      .iterations(4),
  );
}

