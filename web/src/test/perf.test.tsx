import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { detectCommunities } from "../lib/communities";
import { applyLayout, buildGraph } from "../lib/layout";
import { generateSnapshot, json, stubApi, openPanels } from "./fixtures";

vi.mock("sigma", () => import("./sigmaStub"));

/**
 * The 2,000-node budget fixture (docs/testing.md §Atlas UI budgets).
 *
 * The client-side pipeline -- communities plus the deterministic layout --
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

  it("detects communities and lays the graph out within budget", () => {
    const started = performance.now();
    const communities = detectCommunities(FIXTURE);
    const detected = performance.now();
    applyLayout(buildGraph(FIXTURE, undefined, communities));
    const laid = performance.now();
    console.info(
      `budget: communities ${(detected - started).toFixed(0)}ms, layout ${(laid - detected).toFixed(0)}ms for 2,000 nodes / ${FIXTURE.edges.length} edges`,
    );
    expect(detected - started).toBeLessThan(1000);
    expect(laid - detected).toBeLessThan(2000);
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
