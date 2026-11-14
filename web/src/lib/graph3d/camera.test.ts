import { FIT_MAX_NODE_PX, FIT_MIN_DISTANCE, fitPose, type Body, type Viewport } from "./camera";

const view: Viewport = { width: 1600, height: 900, fov: 50, occludedRight: 0 };
const camera = { x: 0, y: 0, z: 1000 };
const lookAt = { x: 0, y: 0, z: 0 };

/** Where a body lands on screen, in normalised device units (-1..1). */
function project(b: Body, pose: NonNullable<ReturnType<typeof fitPose>>, v = view) {
  // The camera looks down -z in these tests: x and y are screen axes.
  const depth = pose.position.z - b.z;
  const tanV = Math.tan(((v.fov / 2) * Math.PI) / 180);
  const tanH = tanV * (v.width / v.height);
  return {
    x: (Math.abs(b.x - pose.lookAt.x) + b.radius) / (depth * tanH),
    y: (Math.abs(b.y - pose.lookAt.y) + b.radius) / (depth * tanV),
  };
}

describe("fitPose of big bodies (super-nodes, an open cluster's hull)", () => {
  const wide: Viewport = { width: 1240, height: 730, fov: 44, occludedRight: 0 };
  const tanHalf = Math.tan((22 * Math.PI) / 180);
  /** The share of the view's half height a sphere of radius r fills at distance d. */
  const fill = (r: number, d: number) => r / (d * tanHalf);

  it("is not held to the node cap: a hull of 80 units fills the view", () => {
    const hull: Body[] = [{ x: 0, y: 0, z: 0, radius: 80 }];
    const capped = fitPose(hull, camera, lookAt, wide)!;
    const free = fitPose(hull, camera, lookAt, { ...wide, maxBodyPx: Infinity })!;
    // The node cap shows it as a speck, 14 px in radius: the bug a hull met.
    const cappedFill = fill(80, capped.position.z);
    expect(cappedFill * (wide.height / 2)).toBeCloseTo(FIT_MAX_NODE_PX, 0);
    // Uncapped it fills at least 80% of the half height, and no more than the view.
    const freeFill = fill(80, free.position.z);
    expect(freeFill).toBeGreaterThanOrEqual(0.8);
    expect(freeFill).toBeLessThanOrEqual(1);
  });

  it("frames a handful of super-nodes spread over 150 units across most of the view", () => {
    const bodies: Body[] = [
      { x: -70, y: 0, z: 0, radius: 12 },
      { x: 70, y: 10, z: 0, radius: 12 },
      { x: 0, y: -55, z: 10, radius: 9 },
      { x: 5, y: 60, z: -10, radius: 9 },
    ];
    const pose = fitPose(bodies, camera, lookAt, { ...wide, maxBodyPx: Infinity })!;
    const p = bodies.map((b) => project(b, pose, wide));
    expect(Math.max(...p.map((q) => q.y))).toBeGreaterThan(0.8);
    expect(Math.max(...p.map((q) => Math.max(q.x, q.y)))).toBeLessThanOrEqual(1);
  });
});

describe("fitPose", () => {
  it("keeps every node on screen and fills the tighter axis", () => {
    const bodies: Body[] = Array.from({ length: 300 }, (_, i) => ({
      x: Math.cos(i) * 400 + 120,
      y: Math.sin(i * 1.7) * 150 - 40,
      z: Math.sin(i * 0.3) * 200,
      radius: 4,
    }));
    const pose = fitPose(bodies, camera, lookAt, view)!;
    const extents = bodies.map((b) => project(b, pose));
    const widest = Math.max(...extents.map((e) => Math.max(e.x, e.y)));
    expect(widest).toBeLessThanOrEqual(1);
    // Tight: the graph uses most of the view, not half of it.
    expect(widest).toBeGreaterThan(0.85);
  });

  it("centres a graph that sits off the origin", () => {
    const bodies: Body[] = [
      { x: 500, y: 300, z: 0, radius: 2 },
      { x: 700, y: 300, z: 0, radius: 2 },
    ];
    const pose = fitPose(bodies, camera, lookAt, view)!;
    expect(pose.lookAt.x).toBeCloseTo(600);
    expect(pose.lookAt.y).toBeCloseTo(300);
  });

  it("does not blow a tiny KB up to fill the canvas", () => {
    for (const radius of [0.5, 3, 8]) {
      const pose = fitPose(
        [
          { x: 0, y: 0, z: 0, radius },
          { x: 30, y: 10, z: 0, radius },
        ],
        camera,
        lookAt,
        view,
      )!;
      const distance = pose.position.z - pose.lookAt.z;
      const px = (radius * (view.height / 2)) / (distance * Math.tan((25 * Math.PI) / 180));
      expect(distance).toBeGreaterThanOrEqual(FIT_MIN_DISTANCE);
      expect(px).toBeLessThanOrEqual(FIT_MAX_NODE_PX + 0.01);
    }
  });

  it("has nothing to fit in an empty graph", () => {
    expect(fitPose([], camera, lookAt, view)).toBeNull();
  });
});
