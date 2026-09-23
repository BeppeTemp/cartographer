import type { GraphNode } from "../api/types";

/** What the UI calls a concept: its title, or its id when it has none. */
export function nameOf(node: Pick<GraphNode, "id" | "title">): string {
  return node.title || node.id;
}

/** A name short enough to sit on the canvas: the title, or the id's last
 *  segment -- the path before it is what the colour already says. */
export function shortNameOf(node: Pick<GraphNode, "id" | "title">): string {
  if (node.title) return node.title;
  const cut = node.id.lastIndexOf("/");
  return cut === -1 ? node.id : node.id.slice(cut + 1);
}
