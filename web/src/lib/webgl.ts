/**
 * hasWebGL reports whether this browser can give the graph a WebGL context.
 *
 * The 3D view needs one and does not degrade on its own: a renderer that
 * throws on its first call takes the whole page down to the error boundary.
 * Asked once, before the view mounts, so a browser without WebGL gets the
 * concept list, search and the inspector with a named state where the graph
 * would be (D234).
 */
export function hasWebGL(): boolean {
  try {
    const canvas = document.createElement("canvas");
    return !!(canvas.getContext("webgl2") ?? canvas.getContext("webgl"));
  } catch {
    return false;
  }
}
