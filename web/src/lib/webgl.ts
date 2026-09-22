/**
 * hasWebGL reports whether this browser can give the graph a WebGL context.
 *
 * Both views need one -- Sigma (2D) as much as three.js (3D) -- and neither
 * degrades on its own: Sigma throws on its first draw call, which takes the
 * whole page down to the error boundary. Asked once, before either view
 * mounts, so a browser without WebGL gets the concept list, search and the
 * inspector with a named state where the graph would be (D234).
 */
export function hasWebGL(): boolean {
  try {
    const canvas = document.createElement("canvas");
    return !!(canvas.getContext("webgl2") ?? canvas.getContext("webgl"));
  } catch {
    return false;
  }
}
