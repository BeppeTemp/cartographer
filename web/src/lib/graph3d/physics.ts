import { forceX, forceY, forceZ, type Force, type SimNode } from "d3-force-3d";

/**
 * The physics of the living 3D atlas (D234).
 *
 * One writer of node coordinates: the d3-force-3d simulation the scene runs
 * (scene.ts). Everything that moves a node -- the settle, the drag, the
 * residual drift -- is a force in that simulation, never a tween or a second
 * loop writing positions. That is the lesson of D227: a per-node animation and
 * a layout worker writing the same coordinates made the 2D graph shiver at
 * rest and fly apart on a drag.
 *
 * The forces:
 *   - springs on the links (d3's link force, default strength 1/min(degree),
 *     so a hub is held by many springs and a leaf by one);
 *   - many-body repulsion, Barnes-Hut over an octree -- not O(n^2);
 *   - weak gravity towards the origin on each axis, instead of d3's centring
 *     force: forceCenter translates every node by the same offset, so a drag
 *     in one component would slide every other component with it;
 *   - velocity decay as damping, so a released node settles instead of
 *     oscillating;
 *   - a residual drift: a deterministic, zero-mean sinusoid per node, small
 *     enough to read as breathing and never as jitter. Off when paused or
 *     under reduced motion.
 */

export interface PhysicsNode extends SimNode {
  id: string;
}

/** Rest length of a link, in scene units. Node radii are ~2-8 units. */
export const LINK_DISTANCE = 22;
/** Many-body strength (negative repels). */
export const CHARGE_STRENGTH = -70;
/** Repulsion ignores pairs farther apart than this: far-apart components do
 *  not push each other around, and the octree walk stays short. */
export const CHARGE_DISTANCE_MAX = 420;
/** Per-axis pull towards the origin: enough to hold disconnected components
 *  and orphans in view, too weak to compete with a spring. */
export const GRAVITY = 0.035;
/** d3's velocity decay: the share of velocity lost per tick. */
export const VELOCITY_DECAY = 0.38;
/** The alpha floor of live mode: the simulation never quite cools, so the
 *  springs keep answering the drift and a drag keeps propagating. */
export const LIVE_ALPHA = 0.012;
/** Drift acceleration per tick, in scene units. See drift(). */
export const DRIFT_AMPLITUDE = 0.006;
/** Drift angular speed, in radians per tick: a period of ~10 s at 60 fps. */
export const DRIFT_SPEED = 0.0105;

/** FNV-1a over the id, as an unsigned 32-bit integer. */
export function hashId(id: string, salt = 0): number {
  let hash = (0x811c9dc5 ^ salt) >>> 0;
  for (let i = 0; i < id.length; i++) {
    hash ^= id.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash;
}

const unit = (id: string, salt: number) => hashId(id, salt) / 2 ** 32;

/**
 * seedPosition is a node's starting point: a function of its id and of the
 * size of the graph, uniform in a ball. The same KB state starts from the same
 * picture and settles into the same shape -- a reload never reshuffles the
 * graph, as the 2D layout never does (D227).
 */
export function seedPosition(id: string, nodeCount: number): { x: number; y: number; z: number } {
  const radius = LINK_DISTANCE * 1.6 * Math.cbrt(Math.max(1, nodeCount));
  const u = unit(id, 1);
  const v = unit(id, 2);
  const r = radius * Math.cbrt(unit(id, 3));
  const theta = 2 * Math.PI * u;
  const phi = Math.acos(2 * v - 1);
  return {
    x: r * Math.sin(phi) * Math.cos(theta),
    y: r * Math.sin(phi) * Math.sin(theta),
    z: r * Math.cos(phi),
  };
}

export interface DriftForce<N extends PhysicsNode> extends Force<N> {
  enabled(on: boolean): DriftForce<N>;
  isEnabled(): boolean;
}

/**
 * drift is the residual motion of live mode, as a force. Each node gets a
 * smooth, zero-mean acceleration on each axis with a phase from its id, so the
 * network breathes without two nodes ever moving in lockstep and without any
 * randomness: the same tick count gives the same motion. It ignores alpha on
 * purpose -- alpha is the layout's temperature, and the drift is what keeps
 * the settled layout from looking frozen.
 */
export function drift<N extends PhysicsNode>(): DriftForce<N> {
  let nodes: N[] = [];
  let phases: Float64Array = new Float64Array(0);
  let on = true;
  let tick = 0;
  const force = ((() => {
    if (!on) return;
    tick++;
    const t = tick * DRIFT_SPEED;
    for (let i = 0; i < nodes.length; i++) {
      const node = nodes[i]!;
      if (node.fx != null) continue;
      const p = phases[i]!;
      node.vx = (node.vx ?? 0) + DRIFT_AMPLITUDE * Math.sin(t + p);
      node.vy = (node.vy ?? 0) + DRIFT_AMPLITUDE * Math.cos(t * 0.87 + p * 1.3);
      node.vz = (node.vz ?? 0) + DRIFT_AMPLITUDE * Math.sin(t * 0.71 + p * 0.7);
    }
  }) as unknown) as DriftForce<N>;
  force.initialize = (next: N[]) => {
    nodes = next;
    phases = new Float64Array(next.length);
    next.forEach((node, i) => (phases[i] = unit(node.id, 4) * 2 * Math.PI));
  };
  force.enabled = (value: boolean) => {
    on = value;
    return force;
  };
  force.isEnabled = () => on;
  return force;
}

/** Where the scene's simulation (or a bare one, in tests) takes its forces. */
export type ForceHost = (name: string, force?: unknown) => unknown;

interface Configurable {
  strength?(value: number): Configurable;
  distance?(value: number): Configurable;
  distanceMax?(value: number): Configurable;
}

/**
 * configureForces turns a simulation with d3's link and many-body forces into
 * the atlas's: it removes any centring force, adds per-axis gravity and the
 * drift, and tunes the link and many-body forces in place.
 */
export function configureForces<N extends PhysicsNode>(host: ForceHost, driftForce: DriftForce<N>): void {
  host("center", null);
  host("x", forceX<N>(0).strength(GRAVITY));
  host("y", forceY<N>(0).strength(GRAVITY));
  host("z", forceZ<N>(0).strength(GRAVITY));
  const charge = host("charge") as Configurable | undefined;
  charge?.strength?.(CHARGE_STRENGTH);
  charge?.distanceMax?.(CHARGE_DISTANCE_MAX);
  const link = host("link") as Configurable | undefined;
  link?.distance?.(LINK_DISTANCE);
  host("drift", driftForce);
}

/**
 * sanitize puts any node with a non-finite coordinate back at the centroid of
 * its finite neighbours (the origin if it has none) and stops it. A drag far
 * off-screen, or a degenerate zero-length link, can produce NaN inside the
 * simulation; one NaN spreads to every node it touches within a few ticks and
 * the whole graph disappears. Returns how many nodes were repaired.
 */
export function sanitize<N extends PhysicsNode>(nodes: N[], neighbours: Map<string, N[]>): number {
  let repaired = 0;
  const finite = (n: N) => Number.isFinite(n.x) && Number.isFinite(n.y) && Number.isFinite(n.z);
  for (const node of nodes) {
    if (finite(node)) continue;
    const near = (neighbours.get(node.id) ?? []).filter(finite);
    const mean = (axis: "x" | "y" | "z") =>
      near.length ? near.reduce((sum, n) => sum + (n[axis] as number), 0) / near.length : 0;
    node.x = mean("x");
    node.y = mean("y");
    node.z = mean("z");
    node.vx = node.vy = node.vz = 0;
    if (node.fx != null && !Number.isFinite(node.fx)) node.fx = node.fy = node.fz = undefined;
    repaired++;
  }
  return repaired;
}

/**
 * boundingRadius is the radius of the settled graph around its centroid, at
 * the 95th percentile: a handful of orphans at the rim must not decide the
 * zoom limits or the focus distance.
 */
export function boundingRadius(nodes: readonly SimNode[]): number {
  const placed = nodes.filter((n) => Number.isFinite(n.x) && Number.isFinite(n.y) && Number.isFinite(n.z));
  if (placed.length === 0) return LINK_DISTANCE * 4;
  let cx = 0;
  let cy = 0;
  let cz = 0;
  for (const n of placed) {
    cx += n.x!;
    cy += n.y!;
    cz += n.z!;
  }
  cx /= placed.length;
  cy /= placed.length;
  cz /= placed.length;
  const d = placed.map((n) => Math.hypot(n.x! - cx, n.y! - cy, n.z! - cz)).sort((a, b) => a - b);
  return Math.max(LINK_DISTANCE * 2, d[Math.min(d.length - 1, Math.floor(d.length * 0.95))]!);
}
