import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import Sigma from "sigma";
import { createNodeBorderProgram } from "@sigma/node-border";
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
import { makeHoverDrawer, makeLabelDrawer } from "../lib/halo";
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
  /** Pixels at the right edge hidden behind a panel (the inspector): a
   *  selection is centred in what remains visible, not under the panel. */
  occludedRight?: number;
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
const DRAWN_SIZE = 0.5;
/** A disc with a fixed 1.5px outline in the node's borderColor. */
const NodeBorder = createNodeBorderProgram({
  borders: [
    { size: { value: 1.5, mode: "pixels" }, color: { attribute: "borderColor" } },
    { size: { fill: true }, color: { attribute: "color" } },
  ],
});

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
  occludedRight = 0,
  children,
}: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  const graphRef = useRef<Graph | null>(null);
  /** The deterministic coordinates of the current graph, kept aside so a
   *  dragged-apart picture can be put back exactly (Reset layout). */
  const basePositionsRef = useRef<Record<string, { x: number; y: number }>>({});
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
      accent: cssVar("--accent") || "#b5d6bd",
      severityError: cssVar("--sev-error") || "#e9a199",
      severityWarning: cssVar("--sev-warning") || "#ddb56f",
      edge: cssVar("--graph-edge") || "#3b473e",
      edgeActive: cssVar("--graph-edge-active") || "#91b9a0",
      nodeStroke: cssVar("--graph-node-stroke") || "#191f1c",
      canvas: cssVar("--surface-0") || "#191f1c",
      slots: resolveSlots(),
      label: cssVar("--text-secondary") || "#bcc5ba",
      labelBackground: cssVar("--surface-1") || "#222b25",
      labelBorder: cssVar("--border-strong") || "#7d8c7d",
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

  // --- Renderer and interaction ---
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const fingerprint = snapshotFingerprint(kb, scope, snapshot);
    const cached = readCachedPositions(fingerprint);
    const graph = buildGraph(snapshot, cached ?? undefined, communities);
    const positions = cached ?? applyLayout(graph);
    if (!cached) writeCachedPositions(fingerprint, positions);
    basePositionsRef.current = positions;
    graphRef.current = graph;

    const renderer = new Sigma(graph, container, {
      allowInvalidContainer: true,
      renderEdgeLabels: false,
      defaultEdgeType: "line",
      labelFont: cssVar("--font-sans") || "sans-serif",
      labelSize: 11.5,
      labelWeight: "500",
      // Labels only where they can be read: the larger nodes at rest, more
      // as the user zooms in. The focused node's neighbourhood is labelled
      // regardless (encoding.ts, forceLabel).
      labelDensity: 0.5,
      labelGridCellSize: 90,
      labelRenderedSizeThreshold: 9,
      zIndex: true,
      nodeProgramClasses: { border: NodeBorder },
      // Tighter than the stock zoom, so the wheel moves in steps the eye can
      // follow instead of jumps.
      zoomingRatio: 1.4,
    });
    sigmaRef.current = renderer;

    // The picture is still. There is no live force simulation and no idle
    // motion: both were tried, and together they fought over the same
    // coordinates -- the graph shivered at rest and flew apart when a node was
    // dragged, because the simulation re-ran around a node pinned far from
    // its neighbours. The deterministic layout is the picture; the entry
    // settle below is the only motion it makes on its own.

    renderer.on("clickNode", ({ node }) => onSelect(node));
    renderer.on("doubleClickNode", ({ node, event }) => {
      event.preventSigmaDefault();
      onExpand(node);
    });
    renderer.on("clickStage", () => onSelect(null));
    renderer.on("enterNode", ({ node }) => setHovered(node));
    renderer.on("leaveNode", () => setHovered(null));

    // Dragging moves the one node under the cursor and nothing else: its
    // edges follow it, its neighbours stay where the layout put them. The
    // move lasts for the session; Reset layout puts every node back.
    renderer.on("downNode", ({ node }) => {
      draggedRef.current = node;
      setDragging(node);
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
      if (!draggedRef.current) return;
      draggedRef.current = null;
      setDragging(null);
    };
    mouse.on("mouseup", endDrag);
    mouse.on("mouseleave", endDrag);

    return () => {
      renderer.kill();
      sigmaRef.current = null;
      graphRef.current = null;
    };
  }, [kb, scope, snapshot, communities, onSelect, onExpand, reducedMotion]);

  // The entry stagger: nodes grow in by degree rank, once per graph. It is
  // an appearance change only (size and opacity in the reducers), never a
  // coordinate change, so the layout under it is already final.
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
      "defaultDrawNodeLabel",
      makeLabelDrawer({ text: palette.label, halo: cssVar("--surface-0") || "#191f1c" }),
    );
    renderer.setSetting(
      "defaultDrawNodeHover",
      makeHoverDrawer({
        labelBackground: palette.labelBackground,
        labelBorder: palette.labelBorder,
        labelText: cssVar("--text-primary") || "#eeede5",
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
    // Centre on the node, zooming in only a little: the neighbourhood is the
    // point of a selection, and a close-up hides it.
    const ratio = Math.min(camera.ratio, 0.8);
    // Shift the camera right by half the occluded strip, measured in the
    // framed-graph units the camera uses at the target zoom, so the node lands
    // in the middle of the visible area.
    const { width } = renderer.getDimensions();
    const a = renderer.viewportToFramedGraph({ x: width / 2, y: 0 });
    const b = renderer.viewportToFramedGraph({ x: width / 2 + occludedRight / 2, y: 0 });
    const shift = (b.x - a.x) * (ratio / camera.ratio);
    const to = { x: position.x + shift, y: position.y, ratio };
    markCamera(containerRef.current, reducedMotion);
    if (reducedMotion) camera.setState(to);
    else camera.animate(to, { duration: CAMERA_MS, easing: "cubicInOut" });
  }, [selected, highlighted, reducedMotion, occludedRight]);

  const moveCamera = useCallback(
    (to: Record<string, number>, duration: number) => {
      const camera = sigmaRef.current?.getCamera();
      if (!camera) return;
      markCamera(containerRef.current, reducedMotion);
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
    <div
      className={`graph${dragging ? " graph--dragging" : ""}`}
      data-motion={reducedMotion ? "reduced" : "full"}
      data-entry={entry < 1 ? "settling" : "settled"}
    >
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
            const graph = graphRef.current;
            if (!graph) return;
            for (const [id, p] of Object.entries(basePositionsRef.current)) {
              if (graph.hasNode(id)) graph.mergeNodeAttributes(id, { x: p.x, y: p.y });
            }
            moveCamera({ x: 0.5, y: 0.5, ratio: 1, angle: 0 }, CAMERA_MS);
          }}
          aria-label="Reset layout"
          title="Reset layout"
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

/** Records how the last camera move was made, "jump" or "tween", on the canvas
 *  element. The camera lives inside a WebGL renderer, so this attribute is the
 *  only way a browser test can tell reduced motion was honoured (D228). */
function markCamera(container: HTMLElement | null, reducedMotion: boolean): void {
  if (container) container.dataset.camera = reducedMotion ? "jump" : "tween";
}

