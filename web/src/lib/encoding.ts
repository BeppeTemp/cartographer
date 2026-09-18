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
 *   - hue        -> collection
 *   - size       -> degree, square-root scaled and clamped
 *   - ring/tint  -> selection, and lint severity
 *   - dimming    -> "not related to what is selected"
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

export interface NodeInput {
  id: string;
  baseSize: number;
  collectionColor: string;
  expanded: boolean;
  /** "error" | "warning" when lint has something to say about this concept. */
  severity?: string;
  /** 0..1 entry progress for this node; 0 means "not arrived yet". */
  entry: number;
  hiddenByFilter: boolean;
  /** The concept the view is focused on: selected, previewed or hovered. */
  focus: string | null;
  isNeighbourOfFocus: boolean;
}

export interface NodeAppearance {
  type: NodeType;
  hidden: boolean;
  color: string;
  size: number;
  label: string;
  zIndex: number;
  highlighted: boolean;
}

export function nodeAppearance(node: NodeInput, palette: Palette): NodeAppearance {
  const base: NodeAppearance = {
    type: "circle",
    hidden: false,
    color: node.collectionColor,
    size: node.baseSize,
    label: shortLabel(node.id),
    zIndex: 0,
    highlighted: false,
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
    return { ...base, zIndex: 1 };
  }
  // Dimmed, never hidden: a node the user can no longer see is a node they
  // cannot click their way back to.
  return { ...base, color: withAlpha(base.color, 0.25), label: "" };
}

export interface EdgeInput {
  source: string;
  target: string;
  hiddenByFilter: boolean;
  /** Edges only appear once most nodes have arrived, or the entry animation
   *  reads as a tangle resolving rather than a picture forming. */
  edgesVisible: boolean;
  focus: string | null;
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
  return {
    type: "arrow",
    hidden: false,
    color: edge.focus ? withAlpha(palette.edge, 0.35) : palette.edge,
    size: 1,
    zIndex: 0,
  };
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
