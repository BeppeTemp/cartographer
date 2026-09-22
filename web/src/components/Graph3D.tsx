import { useEffect, useRef, useState } from "react";
import type { GraphSnapshot } from "../api/types";
import { communitySlot, type Communities } from "../lib/communities";
import { collectionHue, cssVar, resolveSlots, type ColorBy } from "../lib/palette";
import { nodeSize } from "../lib/layout";
import { prefersReducedMotion } from "../lib/theme";

interface Props {
  snapshot: GraphSnapshot;
  communities: Communities;
  colorBy: ColorBy;
  selected: string | null;
  hiddenIds: Set<string>;
  onSelect(id: string | null): void;
}

interface Node3D {
  id: string;
  [key: string]: unknown;
  color: string;
  val: number;
  x?: number;
  y?: number;
  z?: number;
}

interface Link3D {
  [key: string]: unknown;
  source: string | Node3D;
  target: string | Node3D;
}

type ForceGraph = import("3d-force-graph").ForceGraph3DInstance<Node3D, Link3D>;

/**
 * The 3D "Explore" view: the same snapshot, drawn as a constellation.
 *
 * It is a second way to look at the graph, not a replacement for the 2D atlas,
 * which stays the view for reading and navigating: in 3D nodes occlude each
 * other, labels are hover-only and precise picking is harder. What it adds is
 * the overview -- the shape of the whole knowledge base at a glance.
 *
 * Deliberately quiet: matte nodes, hairline links, no glow, no particles and
 * no idle rotation -- the same restraint as the 2D view, in depth.
 *
 * three.js and 3d-force-graph are several hundred KiB, so they are a separate
 * chunk loaded on first use, never part of the initial bundle.
 */
export function Graph3D({ snapshot, communities, colorBy, selected, hiddenIds, onSelect }: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const graphRef = useRef<ForceGraph | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let disposed = false;

    void import("3d-force-graph")
      .then(async ({ default: ForceGraph3D }) => {
        if (disposed) return;
        const slots = resolveSlots();
        const colourOf = (id: string, collection?: string) =>
          slots[colorBy === "community" ? communitySlot(communities, id) : collectionHue(collection ?? "")]!;
        const visible = snapshot.nodes.filter((n) => !hiddenIds.has(n.id));
        const ids = new Set(visible.map((n) => n.id));
        const data = {
          nodes: visible.map<Node3D>((n) => ({
            id: n.id,
            color: colourOf(n.id, n.collection),
            val: nodeSize(n.in_degree + n.out_degree) / 2,
          })),
          links: snapshot.edges
            .filter((e) => ids.has(e.source) && ids.has(e.target))
            .map<Link3D>((e) => ({ source: e.source, target: e.target })),
        };

        // The package's constructor type is not generic; the instance type is.
        const Create = ForceGraph3D as unknown as new (el: HTMLElement, cfg?: { controlType?: string }) => ForceGraph;
        const edge = cssVar("--graph-edge-3d");
        let framed = false;
        // Orphans float far out in a force layout; framing them would leave
        // the connected graph a speck in the middle.
        const linked = new Set<string>();
        for (const l of data.links) {
          linked.add(l.source as string);
          linked.add(l.target as string);
        }
        const graph = new Create(container, { controlType: "orbit" })
          .backgroundColor(cssVar("--surface-0"))
          .showNavInfo(false)
          .nodeRelSize(5)
          .nodeOpacity(0.92)
          .nodeResolution(24)
          .nodeColor((n) => n.color)
          .nodeLabel((n) => labelHTML(n.id))
          .linkColor(() => edge)
          .linkOpacity(0.55)
          .linkWidth(0)
          .warmupTicks(60)
          .cooldownTime(4000)
          .onEngineStop(() => {
            // Frame the settled graph once; later stops (after a drag) leave
            // the reader's camera alone.
            if (framed) return;
            framed = true;
            graph.zoomToFit(prefersReducedMotion() ? 0 : 800, 20, (n) => linked.has(n.id));
          })
          .onNodeClick((n) => onSelect(n.id))
          .onBackgroundClick(() => onSelect(null))
          .graphData(data);

        // No idle rotation: the view moves when the reader moves it.
        const resize = () => graph.width(container.clientWidth).height(container.clientHeight);
        resize();
        const observer = new ResizeObserver(resize);
        observer.observe(container);
        graphRef.current = graph;
        (graph as unknown as { __observer: ResizeObserver }).__observer = observer;
      })
      .catch((err) => {
        console.warn("Atlas: the 3D view could not start", err);
        if (!disposed) setFailed(true);
      });

    return () => {
      disposed = true;
      const graph = graphRef.current;
      if (graph) {
        (graph as unknown as { __observer?: ResizeObserver }).__observer?.disconnect();
        graph._destructor();
      }
      graphRef.current = null;
      container.replaceChildren();
    };
  }, [snapshot, communities, colorBy, hiddenIds, onSelect]);

  // A selection eases the camera toward the node and quiets everything it is
  // not linked to: its neighbourhood keeps its colour, the rest recedes.
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    const node = selected ? graph.graphData().nodes.find((n) => n.id === selected) : undefined;
    const endpoint = (end: string | Node3D) => (typeof end === "object" ? end.id : end);
    const near = new Set<string>();
    if (node) {
      near.add(node.id);
      for (const l of graph.graphData().links) {
        if (endpoint(l.source) === node.id) near.add(endpoint(l.target));
        if (endpoint(l.target) === node.id) near.add(endpoint(l.source));
      }
    }
    const accent = cssVar("--accent");
    const receded = cssVar("--graph-community-other");
    const edge = cssVar("--graph-edge-3d");
    const edgeActive = cssVar("--graph-edge-active");
    graph
      .nodeColor((n) => (!node ? n.color : n.id === node.id ? accent : near.has(n.id) ? n.color : receded))
      .nodeOpacity(node ? 0.8 : 0.92)
      .linkColor((l) =>
        node && (endpoint(l.source) === node.id || endpoint(l.target) === node.id) ? edgeActive : edge,
      )
      .linkWidth((l) => (node && (endpoint(l.source) === node.id || endpoint(l.target) === node.id) ? 0.6 : 0));
    if (node && node.x !== undefined) {
      const distance = 420;
      const ratio = 1 + distance / Math.max(1, Math.hypot(node.x ?? 0, node.y ?? 0, node.z ?? 0));
      graph.cameraPosition(
        { x: (node.x ?? 0) * ratio, y: (node.y ?? 0) * ratio, z: (node.z ?? 0) * ratio },
        { x: node.x ?? 0, y: node.y ?? 0, z: node.z ?? 0 },
        prefersReducedMotion() ? 0 : 1200,
      );
    }
  }, [selected]);

  if (failed) {
    return (
      <div className="state">
        <p className="state__title">The 3D view needs WebGL</p>
        <p className="state__detail">This browser could not start it. The 2D atlas shows the same graph.</p>
      </div>
    );
  }
  return <div ref={containerRef} className="graph3d" data-testid="graph-3d" />;
}

/** Node ids come from the KB: escaped before they reach the tooltip, which
 *  3d-force-graph renders as HTML. */
function labelHTML(id: string): string {
  const esc = id.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
  return `<span class="graph3d__label">${esc}</span>`;
}
