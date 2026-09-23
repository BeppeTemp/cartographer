/**
 * Colour and label helpers shared by the graph view and the legend.
 */

export function shortLabel(id: string): string {
  const cut = id.lastIndexOf("/");
  return cut === -1 ? id : id.slice(cut + 1);
}

function rgb(color: string): [number, number, number] | null {
  const hex = color.trim();
  if (!hex.startsWith("#") || (hex.length !== 7 && hex.length !== 4)) return null;
  const full =
    hex.length === 4 ? `#${hex[1]}${hex[1]}${hex[2]}${hex[2]}${hex[3]}${hex[3]}` : hex;
  return [parseInt(full.slice(1, 3), 16), parseInt(full.slice(3, 5), 16), parseInt(full.slice(5, 7), 16)];
}

/**
 * fade is "this colour at this opacity over the canvas", as an opaque colour.
 *
 * Sigma's WebGL renderer takes a colour string and has no per-node opacity,
 * and an rgba() colour is not the answer: Sigma writes it unpremultiplied into
 * a premultiplied canvas, so the colour is *added* to the page behind it. On a
 * dark canvas that passes for transparency; on paper every faded node and edge
 * turns white. Mixing towards the canvas colour gives the same picture in both
 * themes. Without a canvas colour it falls back to rgba().
 */
export function fade(color: string, alpha: number, canvas?: string): string {
  const fg = rgb(color);
  if (!fg) return color.trim();
  const bg = canvas ? rgb(canvas) : null;
  if (!bg) return `rgba(${fg[0]}, ${fg[1]}, ${fg[2]}, ${alpha})`;
  const mix = fg.map((c, i) => Math.round(c * alpha + bg[i]! * (1 - alpha)));
  return `#${mix.map((c) => c.toString(16).padStart(2, "0")).join("")}`;
}
