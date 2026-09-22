import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import Sigma from "sigma";
import type Graph from "graphology";
import type { GraphSnapshot } from "../api/types";
import {
  applyLayout,
  buildGraph,
  readCachedPositions,
  snapshotFingerprint,
  writeCachedPositions,
} from "../lib/layout";
import { edgeAppearance, nodeAppearance, type Palette } from "../lib/encoding";
import { collectionHue, cssVar, resolveSlots, type ColorBy } from "../lib/palette";
import { OTHER_SLOT, communitySlot, type Communities } from "../lib/communities";
import { makeHoverDrawer } from "../lib/halo";
import {
  DRIFT_NODE_LIMIT,
  createSimulation,
  startDrift,
  type DriftController,
  type Simulation,
} from "../lib/simulation";
import { prefersReducedMotion } from "../lib/theme";

interface Props {
  kb: string;
  scope: string | null;
  snapshot: GraphSnapshot;
  communities: Communities;
  colorBy: ColorBy;
  selected: string | null;
  highlighted: string | null;
  hiddenIds: Set<string>;
  severityByConcept: Map<string, string>;
  themeKey: string;
  onSelect(id: string | null): void;
  onExpand(id: string): void;
  /** Overlays drawn over the canvas, such as the legend. */
  children?: ReactNode;
}

/** The entry settle, and the share of it spent staggering node arrivals. This
 *  is the one place the UI takes longer than --motion-deliberate, because it
 *  happens once per graph. */
const ENTRY_MS = 900;
const STAGGER = 0.65;
/** Camera moves at --motion-slow (360ms), as the motion spec asks. Sigma drives
 *  the camera itself, so the token cannot be read by a transition. */
const CAMERA_MS = 360;
/** Nodes are drawn at this share of their layout size. Sizes are screen
 *  pixels (Sigma's default, growing only with the square root of the zoom),
 *  so zooming in opens space between nodes; drawn at full layout size, a
 *  LinLog-packed community at the default zoom reads as one blob. A
 *  "positions"-referenced size was tried and rejected: Sigma normalises the
 *  graph to its own frame, so those units are not the layout's, and a zoom
 *  onto a selection filled the canvas with overlapping discs. */
const DRAWN_SIZE = 0.6;

export function GraphCanvas({
  kb,
  scope,
  snapshot,
  communities,
  colorBy,
  selected,
  highlighted,
  hiddenIds,
  severityByConcept,
  themeKey,
  onSelect,
  onExpand,
  children,
}: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  const graphRef = useRef<Graph | null>(null);
  const simulationRef = useRef<Simulation | null>(null);
  const driftRef = useRef<DriftController | null>(null);
  const draggedRef = useRef<string | null>(null);
  const [hovered, setHovered] = useState<string | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  const [entry, setEntry] = useState(1);

  const reducedMotion = useMemo(() => prefersReducedMotion(), []);

  // Resolved from the CSS custom properties, and re-resolved when the theme
  // changes: a WebGL canvas cannot read a custom property, so these are the
  // one place token values are turned into concrete colours.
  const palette = useMemo<Palette & { slots: string[]; label: string; labelBackground: string; labelBorder: string }>(
    () => ({
      accent: cssVar("--accent") || "#f5b544",
      severityError: cssVar("--sev-error") || "#f87171",
      severityWarning: cssVar("--sev-warning") || "#fbbf24",
      edge: cssVar("--graph-edge") || "#24384f",
      edgeActive: cssVar("--graph-edge-active") || "#2dd4bf",
      slots: resolveSlots(),
      label: cssVar("--text-secondary") || "#9fb3c8",
      labelBackground: cssVar("--surface-1") || "#0d1622",
      labelBorder: cssVar("--border-strong") || "#2c445f",
    }),
    [themeKey],
  );

  // What each node is coloured by, and which colour group it belongs to (an
  // edge inside one group takes the group's hue). Recomputed per snapshot and
  // per colour mode -- never per frame.
  const colouring = useMemo(() => {
    const slotOf = new Map<string, number>();
    const groupOf = new Map<string, string>();
    for (const node of snapshot.nodes) {
      if (colorBy === "community") {
        const slot = communitySlot(communities, node.id);
        slotOf.set(node.id, slot);
        // The "other" slot is a colour, not a community: two unrelated
        // singletons share it and must not look connected.
        if (slot !== OTHER_SLOT) groupOf.set(node.id, `c${communities.rankOf.get(node.id)}`);
      } else {
        const collection = node.collection ?? "";
        slotOf.set(node.id, collectionHue(collection));
        groupOf.set(node.id, `m${collection}`);
      }
    }
    return { slotOf, groupOf };
  }, [snapshot, communities, colorBy]);
  const arrivalDelay = useMemo(() => {
    const ordered = [...snapshot.nodes].sort(
      (a, b) => b.in_degree + b.out_degree - (a.in_degree + a.out_degree),
    );
    const delays = new Map<string, number>();
    ordered.forEach((node, index) => {
      delays.set(node.id, ordered.length <= 1 ? 0 : (index / (ordered.length - 1)) * STAGGER);
    });
    return delays;
  }, [snapshot]);

  const neighbours = useMemo(() => {
    const map = new Map<string, Set<string>>();
    for (const edge of snapshot.edges) {
      if (!map.has(edge.source)) map.set(edge.source, new Set());
      if (!map.has(edge.target)) map.set(edge.target, new Set());
      map.get(edge.source)!.add(edge.target);
      map.get(edge.target)!.add(edge.source);
    }
    return map;
  }, [snapshot]);

  // --- Renderer, simulation and interaction ---
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const fingerprint = snapshotFingerprint(kb, scope, snapshot);
    const cached = readCachedPositions(fingerprint);
    const graph = buildGraph(snapshot, cached ?? undefined, communities);
    if (!cached) writeCachedPositions(fingerprint, applyLayout(graph));
    graphRef.current = graph;

    const renderer = new Sigma(graph, container, {
      allowInvalidContainer: true,
      renderEdgeLabels: false,
      defaultEdgeType: "arrow",
      labelFont: cssVar("--font-sans") || "sans-serif",
      labelSize: 12,
      labelWeight: "600",
      labelDensity: 0.7,
      labelGridCellSize: 64,
      labelRenderedSizeThreshold: 6,
      zIndex: true,
      // Tighter than the stock zoom, so the wheel moves in steps the eye can
      // follow instead of jumps.
      zoomingRatio: 1.4,
    });
    sigmaRef.current = renderer;

    const simulation = reducedMotion ? null : createSimulation(graph);
    simulationRef.current = simulation;
    // A freshly laid-out graph gets one settle; a cached one is already where
    // it belongs and only needs the drift.
    // Bounded by the entry settle (ENTRY_MS): the motion spec allows one
    // settle of at most 900ms on first load, not a graph that keeps moving.
    if (!cached) simulation?.nudge(ENTRY_MS);

    const drift =
      !reducedMotion && snapshot.nodes.length <= DRIFT_NODE_LIMIT ? startDrift(graph) : null;
    driftRef.current = drift;

    renderer.on("clickNode", ({ node }) => onSelect(node));
    renderer.on("doubleClickNode", ({ node, event }) => {
      event.preventSigmaDefault();
      onExpand(node);
    });
    renderer.on("clickStage", () => onSelect(null));
    renderer.on("enterNode", ({ node }) => setHovered(node));
    renderer.on("leaveNode", () => setHovered(null));

    // Pinching the web: pressing a node pins it under the cursor and keeps the
    // force layout running, so its neighbourhood reorganises around the hand
    // instead of the node tearing free of the graph.
    renderer.on("downNode", ({ node }) => {
      draggedRef.current = node;
      setDragging(node);
      graph.setNodeAttribute(node, "fixed", true);
      // A held node must not also breathe, or it fights the cursor.
      drift?.exclude(node);
      simulation?.hold();
    });

    const mouse = renderer.getMouseCaptor();
    mouse.on("mousemovebody", (event) => {
      const node = draggedRef.current;
      if (!node) return;
      const position = renderer.viewportToGraph(event);
      graph.setNodeAttribute(node, "x", position.x);
      graph.setNodeAttribute(node, "y", position.y);
      // Stop Sigma from panning the camera at the same time.
      event.preventSigmaDefault();
      event.original.preventDefault();
      event.original.stopPropagation();
    });

    const endDrag = () => {
      const node = draggedRef.current;
      if (!node) return;
      draggedRef.current = null;
      setDragging(null);
      if (graph.hasNode(node)) graph.removeNodeAttribute(node, "fixed");
      drift?.exclude(null);
      simulation?.release();
      // The graph genuinely moved, so the drift gets a new resting place and
      // the cache is updated -- with the base coordinates, never the drifted
      // ones, or every reload would bake one frame of the breath into the
      // layout.
      window.setTimeout(() => {
        drift?.rebase();
        writeCachedPositions(fingerprint, drift ? drift.basePositions() : readGraphPositions(graph));
      }, 1000);
    };
    mouse.on("mouseup", endDrag);
    mouse.on("mouseleave", endDrag);

    return () => {
      drift?.stop();
      simulation?.kill();
      renderer.kill();
      driftRef.current = null;
      simulationRef.current = null;
      sigmaRef.current = null;
      graphRef.current = null;
    };
  }, [kb, scope, snapshot, communities, onSelect, onExpand, reducedMotion]);

  // The entry stagger. The resting drift is a separate loop that writes
  // coordinates on the graph (see lib/simulation), so it needs no React state
  // and causes no re-render.
  useEffect(() => {
    if (reducedMotion || snapshot.nodes.length === 0) {
      setEntry(1);
      return;
    }
    setEntry(0);
    let frame = 0;
    const started = performance.now();
    const step = (now: number) => {
      const progress = Math.min((now - started) / ENTRY_MS, 1);
      setEntry(progress);
      if (progress < 1) frame = requestAnimationFrame(step);
    };
    frame = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frame);
  }, [snapshot, reducedMotion]);

  // --- Appearance ---
  useEffect(() => {
    const renderer = sigmaRef.current;
    if (!renderer) return;

    renderer.setSetting("labelColor", { color: palette.label });
    renderer.setSetting(
      "defaultDrawNodeHover",
      makeHoverDrawer({
        labelBackground: palette.labelBackground,
        labelBorder: palette.labelBorder,
        labelText: cssVar("--text-primary") || "#e6eef7",
      }),
    );

    const focus = selected ?? highlighted ?? hovered;
    const focusNeighbours = focus ? (neighbours.get(focus) ?? new Set<string>()) : null;
    const { slotOf, groupOf } = colouring;
    renderer.setSetting("nodeReducer", (id, data) => {
      const delay = arrivalDelay.get(id) ?? 0;
      const appearance = nodeAppearance(
        {
          id,
          baseSize: (data.size as number) * DRAWN_SIZE,
          hueColor: palette.slots[slotOf.get(id) ?? OTHER_SLOT]!,
          expanded: Boolean(data.expanded),
          severity: severityByConcept.get(id),
          entry: (entry - delay) / (1 - STAGGER),
          hiddenByFilter: hiddenIds.has(id),
          focus,
          isNeighbourOfFocus: focusNeighbours?.has(id) ?? false,
          focusDegree: focusNeighbours?.size ?? 0,
        },
        palette,
      );

      return { ...data, ...appearance };
    });

    renderer.setSetting("edgeReducer", (edge, data) => {
      const graph = graphRef.current;
      if (!graph) return data;
      const [source, target] = graph.extremities(edge);
      const group = groupOf.get(source!);
      const sameGroup = group !== undefined && group === groupOf.get(target!);
      return {
        ...data,
        ...edgeAppearance(
          {
            source: source!,
            target: target!,
            hiddenByFilter: hiddenIds.has(source!) || hiddenIds.has(target!),
            edgesVisible: entry >= STAGGER,
            focus,
            groupColor: sameGroup ? palette.slots[slotOf.get(source!) ?? OTHER_SLOT] : undefined,
          },
          palette,
        ),
      };
    });

    renderer.refresh();
  }, [
    selected,
    highlighted,
    hovered,
    hiddenIds,
    severityByConcept,
    neighbours,
    entry,
    arrivalDelay,
    palette,
    colouring,
  ]);

  // Selecting a node brings the camera to it. Under reduced motion it jumps:
  // the CSS media query cannot reach an animation Sigma drives itself.
  useEffect(() => {
    const renderer = sigmaRef.current;
    const target = selected ?? highlighted;
    if (!renderer || !target || !renderer.getGraph().hasNode(target)) return;
    if (draggedRef.current) return;
    const position = renderer.getNodeDisplayData(target);
    if (!position) return;
    const camera = renderer.getCamera();
    const to = { x: position.x, y: position.y, ratio: Math.min(camera.ratio, 0.55) };
    if (reducedMotion) camera.setState(to);
    else camera.animate(to, { duration: CAMERA_MS, easing: "cubicInOut" });
  }, [selected, highlighted, reducedMotion]);

  const moveCamera = useCallback(
    (to: Record<string, number>, duration: number) => {
      const camera = sigmaRef.current?.getCamera();
      if (!camera) return;
      if (reducedMotion) camera.setState(to);
      else camera.animate(to, { duration, easing: "cubicInOut" });
    },
    [reducedMotion],
  );

  const zoom = (factor: number) => {
    const camera = sigmaRef.current?.getCamera();
    if (!camera) return;
    moveCamera({ ratio: camera.ratio * factor }, CAMERA_MS);
  };

  return (
    <div className={`graph${dragging ? " graph--dragging" : ""}`}>
      <div
        ref={containerRef}
        className="graph__canvas"
        role="presentation"
        data-testid="graph-canvas"
      />
      <div className="graph__controls">
        <button
          type="button"
          className="button button--icon"
          onClick={() => zoom(1 / 1.3)}
          aria-label="Zoom in"
        >
          +
        </button>
        <button
          type="button"
          className="button button--icon"
          onClick={() => zoom(1.3)}
          aria-label="Zoom out"
        >
          &minus;
        </button>
        <button
          type="button"
          className="button button--icon"
          onClick={() => moveCamera({ x: 0.5, y: 0.5, ratio: 1, angle: 0 }, CAMERA_MS)}
          aria-label="Fit graph to view"
        >
          &#9633;
        </button>
        <button
          type="button"
          className="button button--icon"
          onClick={() => {
            simulationRef.current?.nudge(2200);
            window.setTimeout(() => driftRef.current?.rebase(), 2400);
          }}
          aria-label="Re-settle the layout"
          title="Re-settle the layout"
        >
          &#8635;
        </button>
      </div>
      {children}
      {snapshot.truncated && (
        <div className="graph__banner banner" role="status">
          <span className="banner__glyph" aria-hidden="true">
            &#9650;
          </span>
          <span>
            Showing {snapshot.nodes.length} of {snapshot.total_nodes} concepts. This graph is
            truncated &mdash; narrow it by Map, type or status to see a complete picture.
          </span>
        </div>
      )}
    </div>
  );
}

function readGraphPositions(graph: Graph): Record<string, { x: number; y: number }> {
  const positions: Record<string, { x: number; y: number }> = {};
  graph.forEachNode((id, attrs) => {
    positions[id] = { x: attrs.x as number, y: attrs.y as number };
  });
  return positions;
}
