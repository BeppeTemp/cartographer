/**
 * Camera framing for the 3D atlas, as pure vector math (D234).
 *
 * The camera follows a selection without flying through the graph: it keeps
 * the direction it is looking from, stops at a distance proportional to the
 * graph's size, and centres the node in the strip of canvas the inspector
 * leaves visible rather than in the middle of the canvas under it.
 */

export interface Vec3 {
  x: number;
  y: number;
  z: number;
}

export interface Pose {
  position: Vec3;
  lookAt: Vec3;
}

export interface Viewport {
  width: number;
  height: number;
  /** Vertical field of view, in degrees (three.js PerspectiveCamera.fov). */
  fov: number;
  /** Pixels on the right hidden behind a floating panel. */
  occludedRight: number;
  /** Pixels on the left hidden behind a floating panel (the concept list). */
  occludedLeft?: number;
}

/** Focus lasts this long, eased in and out, cancelled by any input. */
export const FOCUS_MS = 750;

const sub = (a: Vec3, b: Vec3): Vec3 => ({ x: a.x - b.x, y: a.y - b.y, z: a.z - b.z });
const add = (a: Vec3, b: Vec3): Vec3 => ({ x: a.x + b.x, y: a.y + b.y, z: a.z + b.z });
const scale = (a: Vec3, k: number): Vec3 => ({ x: a.x * k, y: a.y * k, z: a.z * k });
const length = (a: Vec3) => Math.hypot(a.x, a.y, a.z);
const normalize = (a: Vec3): Vec3 => {
  const l = length(a);
  return l > 1e-9 ? scale(a, 1 / l) : { x: 0, y: 0, z: 1 };
};
const cross = (a: Vec3, b: Vec3): Vec3 => ({
  x: a.y * b.z - a.z * b.y,
  y: a.z * b.x - a.x * b.z,
  z: a.x * b.y - a.y * b.x,
});

/** How far from a focused node the camera stops: far enough that a sphere of
 *  `radius` around it -- its neighbourhood -- fits the vertical field of view,
 *  never closer than a few node diameters. */
export function focusDistance(radius: number, fov = 50): number {
  const half = ((fov / 2) * Math.PI) / 180;
  return Math.max(200, (radius * 1.5) / Math.sin(half));
}

/**
 * focusPose is where the camera goes to focus `node`: along the current line of
 * sight, at focusDistance for a neighbourhood of `radius`, shifted sideways so
 * the node lands in the middle of the visible strip.
 */
export function focusPose(node: Vec3, camera: Vec3, radius: number, view: Viewport): Pose {
  const back = normalize(sub(camera, node));
  const distance = focusDistance(radius, view.fov);
  const forward = scale(back, -1);
  let right = cross(forward, { x: 0, y: 1, z: 0 });
  // Looking straight up or down: any horizontal axis will do.
  if (length(right) < 1e-6) right = { x: 1, y: 0, z: 0 };
  right = normalize(right);
  const worldPerPixel = (2 * distance * Math.tan(((view.fov / 2) * Math.PI) / 180)) / Math.max(1, view.height);
  // Moving the look-at point right by d moves the node left on screen by d.
  const net = Math.max(0, view.occludedRight) - Math.max(0, view.occludedLeft ?? 0);
  const shift = scale(right, (net / 2) * worldPerPixel);
  const lookAt = add(node, shift);
  return { position: add(lookAt, scale(back, distance)), lookAt };
}

/**
 * framePose frames a whole graph: the look-at point on its centroid, the
 * camera back along its current line of sight until a sphere of `radius`
 * fits the narrower of the two fields of view, with a little margin.
 */
export function framePose(centroid: Vec3, radius: number, camera: Vec3, lookAt: Vec3, view: Viewport): Pose {
  const back = normalize(sub(camera, lookAt));
  const vertical = ((view.fov / 2) * Math.PI) / 180;
  const visible = view.width - Math.max(0, view.occludedRight) - Math.max(0, view.occludedLeft ?? 0);
  const aspect = Math.max(1, visible) / Math.max(1, view.height);
  const horizontal = Math.atan(Math.tan(vertical) * aspect);
  const half = Math.min(vertical, horizontal);
  const distance = (radius * 1.12) / Math.sin(half);
  return { position: add(centroid, scale(back, distance)), lookAt: centroid };
}

/** Zoom limits for a graph of this radius: never inside a node, never so far
 *  that the graph is a speck. */
export function zoomLimits(radius: number): { min: number; max: number } {
  return { min: 12, max: Math.max(600, radius * 5) };
}

/** A point to keep in frame and how far around it must show too. */
export interface Body extends Vec3 {
  radius: number;
}

/** Breathing room around a fitted graph, as a share of the view. */
const FIT_MARGIN = 0.08;
/** The closest a fit comes, whatever the graph. */
export const FIT_MIN_DISTANCE = 140;
/** The largest a node is drawn by a fit, in screen pixels of radius: a KB of
 *  a handful of concepts is framed as a small constellation, not blown up
 *  until its spheres fill the canvas. */
export const FIT_MAX_NODE_PX = 14;

/**
 * The pose that shows every body in the strip the panels leave, as tight as
 * the view allows, from the current viewing direction. Unlike framePose's
 * bounding sphere -- which must hold the graph from any side and so leaves a
 * flat or elongated graph floating in space -- it measures the graph as the
 * camera sees it: across, up and in depth, each against its own half-angle.
 */
export function fitPose(bodies: readonly Body[], camera: Vec3, lookAt: Vec3, view: Viewport): Pose | null {
  if (bodies.length === 0) return null;
  const back = normalize(sub(camera, lookAt));
  const worldUp = Math.abs(back.y) > 0.99 ? { x: 0, y: 0, z: 1 } : { x: 0, y: 1, z: 0 };
  const right = normalize(cross(worldUp, back));
  const up = cross(back, right);
  const dot = (a: Vec3, b: Vec3) => a.x * b.x + a.y * b.y + a.z * b.z;

  // Centre the graph across and up, as the camera sees it.
  const origin = bodies[0]!;
  const local = bodies.map((b) => {
    const d = sub(b, origin);
    return { x: dot(d, right), y: dot(d, up), z: dot(d, back), r: b.radius };
  });
  const span = (key: "x" | "y") => {
    let lo = Infinity;
    let hi = -Infinity;
    for (const p of local) {
      lo = Math.min(lo, p[key] - p.r);
      hi = Math.max(hi, p[key] + p.r);
    }
    return (lo + hi) / 2;
  };
  const cx = span("x");
  const cy = span("y");
  let zMid = 0;
  for (const p of local) zMid += p.z / local.length;
  const centre = add(origin, add(add(scale(right, cx), scale(up, cy)), scale(back, zMid)));

  // The nearest distance at which every body fits both half-angles.
  const tanV = Math.tan(((view.fov / 2) * Math.PI) / 180) * (1 - FIT_MARGIN);
  const visible = Math.max(1, view.width - Math.max(0, view.occludedRight) - Math.max(0, view.occludedLeft ?? 0));
  const tanH = tanV * (visible / Math.max(1, view.height));
  const largest = Math.max(...local.map((p) => p.r));
  const pixelsPerUnitAt1 = view.height / 2 / (tanV / (1 - FIT_MARGIN));
  let distance = Math.max(FIT_MIN_DISTANCE, (largest * pixelsPerUnitAt1) / FIT_MAX_NODE_PX);
  for (const p of local) {
    const depth = p.z - zMid;
    distance = Math.max(
      distance,
      depth + (Math.abs(p.x - cx) + p.r) / tanH,
      depth + (Math.abs(p.y - cy) + p.r) / tanV,
    );
  }
  return { position: add(centre, scale(back, distance)), lookAt: centre };
}
