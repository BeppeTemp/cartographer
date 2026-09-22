import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { detectCommunities } from "../lib/communities";
import { forceLink, forceManyBody, forceSimulation } from "d3-force-3d";
import { configureForces, drift, seedPosition, type PhysicsNode } from "../lib/graph3d/physics";
import { generateSnapshot, json, stubApi, openPanels } from "./fixtures";

/**
 * The 2,000-node budget fixture (docs/testing.md §Atlas UI budgets).
 *
 * The client-side pipeline -- communities plus the simulation's warm-up --
 * runs synchronously on first view of a graph, so its cost is paid before the
 * canvas can show anything. Ceilings here are generous on purpose: they catch
 * an order-of-magnitude regression (an O(n^2) pass, a lost Barnes-Hut) on any
 * CI runner, not a few percent. The measured values are printed and recorded
 * in docs/testing.md.
 */
const FIXTURE = generateSnapshot(2000);

describe("2,000-node budget fixture", () => {
  it("is the size the budget names", () => {
    expect(FIXTURE.nodes).toHaveLength(2000);
    expect(FIXTURE.edges.length).toBeGreaterThan(2000);
  });

  it("detects communities and warms the simulation up within budget", () => {
    const started = performance.now();
    detectCommunities(FIXTURE);
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
      `budget: communities ${(detected - started).toFixed(0)}ms, warm-up ${(warmed - detected).toFixed(0)}ms for 2,000 nodes / ${FIXTURE.edges.length} edges`,
    );
    expect(detected - started).toBeLessThan(1000);
    expect(warmed - detected).toBeLessThan(4000);
  });

  describe("selection feedback", () => {
    beforeEach(() => {
      window.history.replaceState(null, "", "/ui/");
      localStorage.clear();
    openPanels();
    });
    afterEach(() => vi.unstubAllGlobals());

    it("shows the inspector skeleton before the concept arrives", async () => {
      // The concept request never resolves: what the user sees in the
      // meantime is the skeleton, and it must not wait on the network.
      stubApi({
        "/graph": () => json(FIXTURE),
        "/concept": () => new Promise<Response>(() => {}) as unknown as Response,
      });
      const user = userEvent.setup();
      render(<App />);
      // Plain selectors, not *ByRole: role queries compute every accessible
      // name in a 2,000-row list, which on a CI runner alone blows the test
      // timeout -- the cost is jsdom's, not the UI's.
      const id = FIXTURE.nodes[0]!.id;
      const row = await waitFor(
        () => {
          const el = document.querySelector<HTMLElement>(`[data-concept-id="${CSS.escape(id)}"]`);
          if (!el) throw new Error(`row ${id} not rendered yet`);
          return el;
        },
        { timeout: 10000 },
      );
      await user.click(row);
      await waitFor(() =>
        expect(document.querySelector(`aside[aria-label="Inspector for ${id}"]`)).not.toBeNull(),
      );
      // Skeleton, not content: it rendered while the request is still pending.
      expect(screen.getByText(`Loading ${FIXTURE.nodes[0]!.id}`)).toBeInTheDocument();
    }, 20000);
  });
});
