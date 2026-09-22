/**
 * The graph's visual language, as pure functions.
 *
 * It lives outside the component because the renderer cannot be exercised in a
 * unit test -- jsdom has no WebGL -- and the one defect that matters here is
 * invisible to a mocked renderer: Sigma throws "could not find a suitable
 * program for node type" for any node type it has no program for, which takes
 * down the whole page. Stock Sigma registers exactly one node program
 * ("circle") and two edge programs ("line", "arrow"). Anything else needs an
 * extra dependency, so this module never emits one, and a test holds it to it.
 *
 * What encodes what, given that constraint:
 *   - hue        -> community (lib/communities) or collection, user's choice
 *   - size       -> degree, square-root scaled and clamped
 *   - ring/tint  -> selection, and lint severity
 *   - dimming    -> "not related to what is selected", to 0.25 opacity
 *   - edge tint  -> an edge inside one colour group takes that group's hue at
 *                   low alpha; an edge across groups stays neutral, so the
 *                   bridges between clusters are the lines that stand apart
 * A shape channel would need a node program this UI does not ship.
 */

/** The node types stock Sigma can render. */
export const RENDERABLE_NODE_TYPES = ["circle"] as const;
/** The edge types stock Sigma can render. */
export const RENDERABLE_EDGE_TYPES = ["line", "arrow"] as const;

export type NodeType = (typeof RENDERABLE_NODE_TYPES)[number];
export type EdgeType = (typeof RENDERABLE_EDGE_TYPES)[number];

export interface Palette {
  accent: string;
  severityError: string;
  severityWarning: string;
  edge: string;
  edgeActive: string;
}

/** Non-neighbours of the focused node fade to this opacity (plan: 0.25). */
export const DIM_ALPHA = 0.25;
/** Resting opacity of an edge tinted by its group's hue. */
export const GROUP_EDGE_ALPHA = 0.32;
/** Above this many neighbours, a focused node's neighbours are not all
 *  labelled: a hub's hundred labels are unreadable and hide the graph. */
export const NEIGHBOUR_LABEL_LIMIT = 24;

export interface NodeInput {
  id: string;
  baseSize: number;
  /** The node's colour in the active colour mode (community or collection). */
  hueColor: string;
  expanded: boolean;
  /** "error" | "warning" when lint has something to say about this concept. */
  severity?: string;
  /** 0..1 entry progress for this node; 0 means "not arrived yet". */
  entry: number;
  hiddenByFilter: boolean;
  /** The concept the view is focused on: selected, previewed or hovered. */
  focus: string | null;
  isNeighbourOfFocus: boolean;
  /** How many neighbours the focused node has: decides whether they are all
   *  labelled. */
  focusDegree?: number;
}

export interface NodeAppearance {
  type: NodeType;
  hidden: boolean;
  color: string;
  size: number;
  label: string;
  zIndex: number;
  highlighted: boolean;
  forceLabel: boolean;
}

export function nodeAppearance(node: NodeInput, palette: Palette): NodeAppearance {
  const base: NodeAppearance = {
    type: "circle",
    hidden: false,
    color: node.hueColor,
    size: node.baseSize,
    label: shortLabel(node.id),
    zIndex: 0,
    highlighted: false,
    forceLabel: false,
  };

  if (node.hiddenByFilter || node.entry <= 0) {
    return { ...base, hidden: true };
  }

  // An expanded concept is a concept that grew into a directory: it reads as
  // slightly heavier, since a shape channel is not available.
  const expansion = node.expanded ? 1.35 : 1;
  base.size = node.baseSize * expansion * (0.35 + 0.65 * node.entry);

  if (node.severity === "error") base.color = palette.severityError;
  else if (node.severity === "warning") base.color = palette.severityWarning;

  if (!node.focus) return base;

  if (node.id === node.focus) {
    return { ...base, color: palette.accent, zIndex: 2, highlighted: true };
  }
  if (node.isNeighbourOfFocus) {
    // The 1-hop context is named, not just coloured: the point of selecting a
    // node is to see what it touches.
    const labelled = (node.focusDegree ?? 0) <= NEIGHBOUR_LABEL_LIMIT;
    return { ...base, zIndex: 1, forceLabel: labelled };
  }
  // Dimmed, never hidden: a node the user can no longer see is a node they
  // cannot click their way back to.
  return { ...base, color: withAlpha(base.color, DIM_ALPHA), label: "" };
}

export interface EdgeInput {
  source: string;
  target: string;
  hiddenByFilter: boolean;
  /** Edges only appear once most nodes have arrived, or the entry animation
   *  reads as a tangle resolving rather than a picture forming. */
  edgesVisible: boolean;
  focus: string | null;
  /** The source node's colour, set only when both ends share a colour group:
   *  the edge then carries the group's hue instead of the neutral. */
  groupColor?: string;
}

export interface EdgeAppearance {
  type: EdgeType;
  hidden: boolean;
  color: string;
  size: number;
  zIndex: number;
}

export function edgeAppearance(edge: EdgeInput, palette: Palette): EdgeAppearance {
  if (edge.hiddenByFilter || !edge.edgesVisible) {
    return { type: "arrow", hidden: true, color: palette.edge, size: 1, zIndex: 0 };
  }
  const touchesFocus =
    edge.focus !== null && (edge.source === edge.focus || edge.target === edge.focus);
  if (touchesFocus) {
    return { type: "arrow", hidden: false, color: palette.edgeActive, size: 2, zIndex: 1 };
  }
  if (edge.focus) {
    return { type: "arrow", hidden: false, color: withAlpha(palette.edge, DIM_ALPHA), size: 1, zIndex: 0 };
  }
  if (edge.groupColor) {
    return {
      type: "arrow",
      hidden: false,
      color: withAlpha(edge.groupColor, GROUP_EDGE_ALPHA),
      size: 1.2,
      zIndex: 0,
    };
  }
  return { type: "arrow", hidden: false, color: palette.edge, size: 1, zIndex: 0 };
}

export function shortLabel(id: string): string {
  const cut = id.lastIndexOf("/");
  return cut === -1 ? id : id.slice(cut + 1);
}

/** Sigma's WebGL renderer takes a colour string, so dimming rewrites the
 *  colour: there is no per-node opacity attribute to set. */
export function withAlpha(color: string, alpha: number): string {
  const hex = color.trim();
  if (!hex.startsWith("#") || (hex.length !== 7 && hex.length !== 4)) return hex;
  const full =
    hex.length === 4 ? `#${hex[1]}${hex[1]}${hex[2]}${hex[2]}${hex[3]}${hex[3]}` : hex;
  const r = parseInt(full.slice(1, 3), 16);
  const g = parseInt(full.slice(3, 5), 16);
  const b = parseInt(full.slice(5, 7), 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}
