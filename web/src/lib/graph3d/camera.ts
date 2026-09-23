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
