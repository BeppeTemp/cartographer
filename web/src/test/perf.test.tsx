import { describe, expect, it } from "vitest";
import { forceLink, forceManyBody, forceSimulation } from "d3-force-3d";
import { configureForces, drift, seedPosition, type PhysicsNode } from "../lib/graph3d/physics";
import { generateSnapshot } from "./fixtures";

/**
 * The 2,000-node budget fixture (docs/testing.md §Atlas UI budgets).
 *
 * The client-side pipeline -- the simulation's warm-up; communities come from
 * the server (D244) -- runs synchronously on first view of a graph, so its cost is paid before the
 * canvas can show anything. Ceilings here are generous on purpose: they catch
 * an order-of-magnitude regression (an O(n^2) pass, a lost Barnes-Hut) on any
 * CI runner, not a few percent. The measured values are printed and recorded
 * in docs/testing.md.
 *
 * A wall-clock budget means nothing on a loaded machine, so this file runs
 * alone: `npm test` excludes it, `npm run test:perf` runs it in one worker
 * (`make web-perf`, vite.config.ts).
 */
const FIXTURE = generateSnapshot(2000);

describe("2,000-node budget fixture", () => {
  it("is the size the budget names", () => {
    expect(FIXTURE.nodes).toHaveLength(2000);
    expect(FIXTURE.edges.length).toBeGreaterThan(2000);
  });

  it("warms the simulation up within budget", () => {
    const detected = performance.now();
    // The graph view's warm-up for this size (GraphView: 600,000 / n ticks,
    // clamped to 80..300), on the view's own force configuration.
    const nodes: PhysicsNode[] = FIXTURE.nodes.map((n) => ({ id: n.id, ...seedPosition(n.id, FIXTURE.nodes.length) }));
    const links = FIXTURE.edges.map((e) => ({ source: e.source, target: e.target }));
    const sim = forceSimulation<PhysicsNode>(nodes, 3)
      .force("link", forceLink<PhysicsNode, (typeof links)[number]>(links).id((n) => n.id))
      .force("charge", forceManyBody<PhysicsNode>())
      .stop();
    configureForces((name, ...rest: unknown[]) => (rest.length ? sim.force(name, rest[0] as never) : sim.force(name)), drift<PhysicsNode>());
    sim.tick(300);
    const warmed = performance.now();
    console.info(
      `budget: warm-up ${(warmed - detected).toFixed(0)}ms for 2,000 nodes / ${FIXTURE.edges.length} edges`,
    );
    // ~2.2 s on an Apple M5, ~5.4 s on a GitHub-hosted runner: the ceiling
    // leaves a shared runner its slack and still fails a 5x regression.
    expect(warmed - detected).toBeLessThan(12_000);
    // vitest's default 5 s timeout would cut the run before the budget does.
  }, 20_000);
});
