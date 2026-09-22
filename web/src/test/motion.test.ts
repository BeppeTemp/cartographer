import { describe, expect, it } from "vitest";
import { focusDistance, focusPose, zoomLimits } from "../lib/graph3d/camera";
import {
  IDLE_RESUME_MS,
  IdleRotation,
  MAX_SIGNALS,
  SIGNAL_PASS_MS,
  SIGNAL_SPAN_MS,
  SIGNAL_WAVES,
  initialMotion,
  planBursts,
} from "../lib/graph3d/motion";

describe("autonomous motion", () => {
  it("starts still under reduced motion, whatever was stored", () => {
    expect(initialMotion(true, true)).toBe(false);
    expect(initialMotion(true, false)).toBe(true);
    expect(initialMotion(false, false)).toBe(false);
  });

  it("turns the panorama only after quiet time in the overview", () => {
    let now = 0;
    const idle = new IdleRotation(true, () => now);
    expect(idle.shouldRotate()).toBe(true);
    idle.input();
    now += IDLE_RESUME_MS - 1;
    expect(idle.shouldRotate()).toBe(false);
    now += 1;
    expect(idle.shouldRotate()).toBe(true);

    // Reading: never while a concept is selected ...
    idle.setSelected(true);
    now += IDLE_RESUME_MS * 3;
    expect(idle.shouldRotate()).toBe(false);
    // ... and not the instant it is closed.
    idle.setSelected(false);
    expect(idle.shouldRotate()).toBe(false);
    now += IDLE_RESUME_MS;
    expect(idle.shouldRotate()).toBe(true);

    idle.setFocusing(true);
    expect(idle.shouldRotate()).toBe(false);
    idle.setFocusing(false);
    idle.setLive(false);
    expect(idle.shouldRotate()).toBe(false);
  });
});

describe("signals", () => {
  const links = [
    { source: "hub", target: "a" },
    { source: "b", target: "hub" },
    { source: "a", target: "b" },
  ];
  const degree = (id: string) => ({ a: 5, b: 1, hub: 2 })[id] ?? 0;

  it("run only on the selection's own links, a finite number of waves", () => {
    const plan = planBursts("hub", links, degree, true);
    expect(plan).toHaveLength(2 * SIGNAL_WAVES);
    for (const s of plan) expect([s.link.source, s.link.target]).toContain("hub");
    expect(Math.max(...plan.map((s) => s.delayMs)) + SIGNAL_PASS_MS).toBeLessThanOrEqual(SIGNAL_SPAN_MS);
    // The link keeps its data direction: b -> hub is emitted on b -> hub,
    // not reversed to point away from the selection.
    expect(plan.some((s) => s.link.source === "b" && s.link.target === "hub")).toBe(true);
  });

  it("stay within the budget on a hub, best-connected neighbours first", () => {
    const hubLinks = Array.from({ length: 200 }, (_, i) => ({ source: "hub", target: `n${i}` }));
    const plan = planBursts("hub", hubLinks, (id) => (id.startsWith("n") ? Number(id.slice(1)) : 0), true);
    expect(plan.length).toBeLessThanOrEqual(MAX_SIGNALS);
    expect(plan[0]!.link.target).toBe("n199");
  });

  it("do not run when motion is off or nothing is selected", () => {
    expect(planBursts("hub", links, degree, false)).toEqual([]);
    expect(planBursts(null, links, degree, true)).toEqual([]);
  });

});

describe("camera focus", () => {
  const view = { width: 1400, height: 900, fov: 50, occludedRight: 0 };

  it("keeps the line of sight and stops at the focus distance", () => {
    const node = { x: 10, y: 0, z: 0 };
    const pose = focusPose(node, { x: 10, y: 0, z: 500 }, 400, view);
    expect(pose.lookAt).toEqual(node);
    expect(pose.position.x).toBeCloseTo(10);
    expect(pose.position.z).toBeCloseTo(focusDistance(400, view.fov));
  });

  it("centres the node in the strip the inspector leaves visible", () => {
    const node = { x: 0, y: 0, z: 0 };
    const open = focusPose(node, { x: 0, y: 0, z: 300 }, 300, { ...view, occludedRight: 452 });
    // Looking down -z with +y up, screen-right is +x: the look-at point moves
    // right, so the node sits left of the canvas centre.
    expect(open.lookAt.x).toBeGreaterThan(0);
    expect(open.position.x).toBeCloseTo(open.lookAt.x);
    const wider = focusPose(node, { x: 0, y: 0, z: 300 }, 300, { ...view, occludedRight: 800 });
    expect(wider.lookAt.x).toBeGreaterThan(open.lookAt.x);
  });

  it("has a defined pose from straight above", () => {
    const pose = focusPose({ x: 0, y: 0, z: 0 }, { x: 0, y: 300, z: 0 }, 300, { ...view, occludedRight: 400 });
    for (const v of [pose.position, pose.lookAt]) {
      expect(Number.isFinite(v.x) && Number.isFinite(v.y) && Number.isFinite(v.z)).toBe(true);
    }
  });

  it("bounds the zoom by the graph's size", () => {
    const small = zoomLimits(50);
    const large = zoomLimits(2000);
    expect(small.min).toBeGreaterThan(0);
    expect(large.max).toBeGreaterThan(small.max);
  });
});
