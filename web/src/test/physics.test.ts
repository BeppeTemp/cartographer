import { forceCenter, forceLink, forceManyBody, forceSimulation } from "d3-force-3d";
import { describe, expect, it } from "vitest";
import {
  LIVE_ALPHA,
  VELOCITY_DECAY,
  boundingRadius,
  configureForces,
  KNOCK,
  KNOCK_MAX_ORIGINS,
  KNOCK_VELOCITY_CAP,
  drift,
  knock,
  knockAll,
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
 * A bare d3-force-3d simulation with d3's stock forces (link + many-body +
 * centre, three dimensions), then handed to configureForces exactly as the
 * scene (lib/graph3d/scene) does. The physics can be measured here without
 * WebGL.
 */
function world(nodeCount = 240) {
  const snapshot = generateSnapshot(nodeCount, 8, 3);
  // A small component with no link to the rest: a ring of six, so gravity
  // and the drift are measured on a disconnected graph too.
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
  return { sim, nodes, byId, neighbours, island, driftForce };
}

type Vec = { x: number; y: number; z: number };
const at = (n: PhysicsNode): Vec => ({ x: n.x!, y: n.y!, z: n.z! });
const dist = (a: Vec, b: Vec) => Math.hypot(a.x - b.x, a.y - b.y, a.z - b.z);

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

describe("knockAll", () => {
  type N = PhysicsNode & { x: number; y: number; z: number; vx: number; vy: number; vz: number };
  // A ring of 1000 nodes with chords, so every node reaches the others quickly.
  const build = () => {
    const nodes: N[] = Array.from({ length: 1000 }, (_, i) => ({ id: `n${i}`, x: Math.cos(i) * 400, y: Math.sin(i) * 400, z: i % 50, vx: 0, vy: 0, vz: 0 }));
    const neighbours = new Map<string, N[]>(nodes.map((n) => [n.id, []]));
    for (let i = 0; i < nodes.length; i++) {
      for (const j of [(i + 1) % 1000, (i + 7) % 1000, (i + 31) % 1000]) {
        neighbours.get(nodes[i]!.id)!.push(nodes[j]!);
        neighbours.get(nodes[j]!.id)!.push(nodes[i]!);
      }
    }
    return { nodes, neighbours };
  };
  const speed = (n: N) => Math.hypot(n.vx, n.vy, n.vz);

  it("500 origins on 1000 nodes: no push at all, one reheat", () => {
    const { nodes, neighbours } = build();
    const origins = new Map(nodes.slice(0, 500).map((n) => [n, KNOCK] as const));
    expect(500).toBeGreaterThan(KNOCK_MAX_ORIGINS);
    expect(knockAll(origins, neighbours)).toBe("reheat");
    expect(Math.max(...nodes.map(speed))).toBe(0);
  });

  it("a few origins never push a node past the cap", () => {
    const { nodes, neighbours } = build();
    const origins = new Map(nodes.slice(0, KNOCK_MAX_ORIGINS).map((n) => [n, KNOCK] as const));
    expect(knockAll(origins, neighbours)).toBe("knock");
    expect(Math.max(...nodes.map(speed))).toBeLessThanOrEqual(KNOCK_VELOCITY_CAP + 1e-9);
  });

  it("one origin is exactly the plain knock", () => {
    const a = build();
    const b = build();
    expect(knockAll(new Map([[a.nodes[0]!, KNOCK]]), a.neighbours)).toBe("knock");
    knock(b.nodes[0]!, KNOCK, b.neighbours);
    expect(a.nodes.map((n) => [n.vx, n.vy, n.vz])).toEqual(b.nodes.map((n) => [n.vx, n.vy, n.vz]));
  });
});
