import { forceCenter, forceLink, forceManyBody, forceSimulation } from "d3-force-3d";
import { describe, expect, it } from "vitest";
import {
  LIVE_ALPHA,
  VELOCITY_DECAY,
  boundingRadius,
  configureForces,
  drift,
  sanitize,
  seedPosition,
  type ForceHost,
  type PhysicsNode,
} from "../lib/graph3d/physics";
import { generateSnapshot } from "./fixtures";

interface Link {
  source: string | PhysicsNode;
  target: string | PhysicsNode;
}

/**
 * A bare d3-force-3d simulation set up the way three-forcegraph sets up its d3
 * engine (link + many-body + centre, three dimensions), then handed to
 * configureForces exactly as Graph3D hands over 3d-force-graph. The physics
 * can be measured here without WebGL.
 */
function world(nodeCount = 240) {
  const snapshot = generateSnapshot(nodeCount, 8, 3);
  // A small component with no link to the rest: a ring of six.
  const island = Array.from({ length: 6 }, (_, i) => `island/n${i}`);
  const ids = [...snapshot.nodes.map((n) => n.id), ...island];
  const edges: { source: string; target: string }[] = [
    ...snapshot.edges.map((e) => ({ source: e.source, target: e.target })),
    ...island.map((id, i) => ({ source: id, target: island[(i + 1) % island.length]! })),
  ];
  const nodes: PhysicsNode[] = ids.map((id) => ({ id, ...seedPosition(id, ids.length) }));
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const links: Link[] = edges.map((e) => ({ ...e }));
  const sim = forceSimulation<PhysicsNode>(nodes, 3)
    .force("link", forceLink<PhysicsNode, Link>(links).id((n) => n.id))
    .force("charge", forceManyBody<PhysicsNode>())
    .force("center", forceCenter<PhysicsNode>())
    .velocityDecay(VELOCITY_DECAY)
    .stop();
  const host: ForceHost = (name, ...rest: unknown[]) =>
    rest.length ? sim.force(name, rest[0] as never) : sim.force(name);
  const driftForce = drift<PhysicsNode>();
  configureForces(host, driftForce);
  // Settle as the view does at start: warm-up ticks, then live mode.
  sim.alpha(1);
  sim.tick(300);
  sim.alphaTarget(LIVE_ALPHA);

  const neighbours = new Map<string, Set<string>>(ids.map((id) => [id, new Set()]));
  for (const e of edges) {
    neighbours.get(e.source)!.add(e.target);
    neighbours.get(e.target)!.add(e.source);
  }
  const degree = (id: string) => neighbours.get(id)!.size;
  return { sim, nodes, byId, neighbours, degree, island, driftForce };
}

type Vec = { x: number; y: number; z: number };
const at = (n: PhysicsNode): Vec => ({ x: n.x!, y: n.y!, z: n.z! });
const dist = (a: Vec, b: Vec) => Math.hypot(a.x - b.x, a.y - b.y, a.z - b.z);
const centroid = (ns: PhysicsNode[]): Vec => ({
  x: ns.reduce((s, n) => s + n.x!, 0) / ns.length,
  y: ns.reduce((s, n) => s + n.y!, 0) / ns.length,
  z: ns.reduce((s, n) => s + n.z!, 0) / ns.length,
});

/** Pins `node` and drags it by `offset` over `ticks`, as DragControls does
 *  (fx/fy/fz follow the pointer, alpha target raised), then releases it. */
function drag(w: ReturnType<typeof world>, node: PhysicsNode, offset: Vec, ticks = 40) {
  const from = at(node);
  w.sim.alphaTarget(0.3);
  for (let i = 1; i <= ticks; i++) {
    node.fx = node.x = from.x + (offset.x * i) / ticks;
    node.fy = node.y = from.y + (offset.y * i) / ticks;
    node.fz = node.z = from.z + (offset.z * i) / ticks;
    w.sim.tick();
  }
}

function release(w: ReturnType<typeof world>, node: PhysicsNode) {
  node.fx = node.fy = node.fz = undefined;
  w.sim.alphaTarget(LIVE_ALPHA);
}

/** Mean displacement of `ids` between two position snapshots. */
function moved(before: Map<string, Vec>, w: ReturnType<typeof world>, ids: Iterable<string>) {
  const list = [...ids];
  return list.reduce((s, id) => s + dist(before.get(id)!, at(w.byId.get(id)!)), 0) / list.length;
}

const snapshotOf = (w: ReturnType<typeof world>) => new Map(w.nodes.map((n) => [n.id, at(n)]));

describe("the living 3D physics", () => {
  it("seeds the same picture for the same KB", () => {
    expect(seedPosition("a/b", 100)).toEqual(seedPosition("a/b", 100));
    expect(seedPosition("a/b", 100)).not.toEqual(seedPosition("a/c", 100));
    const a = world(120);
    const b = world(120);
    expect(a.nodes.map(at)).toEqual(b.nodes.map(at));
  });

  it("replaces d3's centring force with per-axis gravity", () => {
    const w = world(60);
    expect(w.sim.force("center")).toBeUndefined();
    for (const axis of ["x", "y", "z", "drift"]) expect(w.sim.force(axis)).toBeDefined();
  });

  it("propagates a hub drag through the springs, fading with distance", () => {
    const w = world();
    w.driftForce.enabled(false);
    const hub = [...w.nodes].sort((a, b) => w.degree(b.id) - w.degree(a.id))[0]!;
    const oneHop = new Set(w.neighbours.get(hub.id));
    const twoHop = new Set<string>();
    for (const id of oneHop) for (const next of w.neighbours.get(id)!) if (next !== hub.id && !oneHop.has(next)) twoHop.add(next);
    const before = snapshotOf(w);
    const D = boundingRadius(w.nodes) * 0.6;
    drag(w, hub, { x: D, y: 0, z: 0 });

    const near = moved(before, w, oneHop);
    const far = moved(before, w, twoHop);
    // The island's control: the same reheat, with no drag. Whatever the island
    // does in both worlds is settling, not propagation.
    const twin = world();
    twin.driftForce.enabled(false);
    twin.sim.alphaTarget(0.3);
    twin.sim.tick(40);
    const islandShift = dist(
      centroid(twin.island.map((id) => twin.byId.get(id)!)),
      centroid(w.island.map((id) => w.byId.get(id)!)),
    );
    expect(near).toBeGreaterThan(D * 0.25);
    expect(far).toBeGreaterThan(D * 0.05);
    expect(near).toBeGreaterThan(far);
    // The island shares no link with the hub: only gravity and distant
    // repulsion reach it, never the springs. What is left is the component
    // it is repelled by moving away -- a nudge, not a tow (~1.4% measured).
    expect(islandShift).toBeLessThan(D * 0.03);
    expect(islandShift).toBeLessThan(far);
  });

  it("moves a leaf's neighbourhood less than a hub's for the same drag", () => {
    const measure = (pickHub: boolean) => {
      const w = world();
      w.driftForce.enabled(false);
      const ranked = w.nodes
        .filter((n) => !n.id.startsWith("island/") && w.degree(n.id) > 0)
        .sort((a, b) => w.degree(b.id) - w.degree(a.id));
      const node = pickHub ? ranked[0]! : ranked[ranked.length - 1]!;
      const before = snapshotOf(w);
      const D = boundingRadius(w.nodes) * 0.6;
      drag(w, node, { x: 0, y: D, z: 0 });
      const others = w.nodes.filter((n) => n !== node && !n.id.startsWith("island/")).map((n) => n.id);
      return moved(before, w, others);
    };
    expect(measure(false)).toBeLessThan(measure(true));
  });

  it("settles softly after a release, with no teleport and no NaN", () => {
    const w = world();
    const hub = [...w.nodes].sort((a, b) => w.degree(b.id) - w.degree(a.id))[0]!;
    const D = boundingRadius(w.nodes) * 3;
    drag(w, hub, { x: D, y: -D, z: D });
    const pinned = at(hub);
    release(w, hub);
    let previous = at(hub);
    let maxStep = 0;
    for (let i = 0; i < 400; i++) {
      w.sim.tick();
      maxStep = Math.max(maxStep, dist(previous, at(hub)));
      previous = at(hub);
    }
    for (const n of w.nodes) {
      expect(Number.isFinite(n.x) && Number.isFinite(n.y) && Number.isFinite(n.z), n.id).toBe(true);
    }
    // It returns towards its neighbours, but never in one jump.
    expect(dist(pinned, at(hub))).toBeGreaterThan(D * 0.2);
    expect(maxStep).toBeLessThan(dist(pinned, at(hub)) * 0.25);
  });

  it("breathes at rest without jitter, and stops when drift is off", () => {
    const w = world();
    const radius = boundingRadius(w.nodes);
    // Let the live floor settle, then measure per-tick motion.
    w.sim.tick(600);
    const steps: number[] = [];
    let previous = snapshotOf(w);
    for (let i = 0; i < 240; i++) {
      w.sim.tick();
      const now = snapshotOf(w);
      for (const n of w.nodes) steps.push(dist(previous.get(n.id)!, now.get(n.id)!));
      previous = now;
    }
    steps.sort((a, b) => a - b);
    const p95 = steps[Math.floor(steps.length * 0.95)]!;
    const mean = steps.reduce((s, v) => s + v, 0) / steps.length;
    // Alive: nodes do move ...
    expect(mean).toBeGreaterThan(radius * 0.00005);
    // ... but never by more than half a percent of the graph per frame.
    expect(p95).toBeLessThan(radius * 0.005);

    // Paused: drift off and the simulation cooled -- motion dies out.
    w.driftForce.enabled(false);
    w.sim.alphaTarget(0);
    w.sim.tick(600);
    const before = snapshotOf(w);
    w.sim.tick();
    expect(moved(before, w, w.nodes.map((n) => n.id))).toBeLessThan(radius * 0.00005);
  });

  it("repairs a non-finite node at its neighbours' centroid", () => {
    const a: PhysicsNode = { id: "a", x: 0, y: 0, z: 0 };
    const b: PhysicsNode = { id: "b", x: 10, y: 20, z: 30 };
    const broken: PhysicsNode = { id: "c", x: NaN, y: 1, z: Infinity, vx: 5, fx: NaN };
    const repaired = sanitize([a, b, broken], new Map([["c", [a, b]]]));
    expect(repaired).toBe(1);
    expect(at(broken)).toEqual({ x: 5, y: 10, z: 15 });
    expect(broken.vx).toBe(0);
    expect(broken.fx).toBeUndefined();
  });
});
