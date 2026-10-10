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
