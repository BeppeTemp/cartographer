import { useEffect, useMemo, useRef, useState } from "react";
import type { GraphSnapshot } from "../api/types";
import { communitySlot, type Communities } from "../lib/communities";
import { fade } from "../lib/encoding";
import type { Pose } from "../lib/graph3d/camera";
import { planBursts } from "../lib/graph3d/motion";
import { seedPosition } from "../lib/graph3d/physics";
import type { LivingScene, SceneLink, SceneNode } from "../lib/graph3d/scene";
import { collectionHue, cssVar, resolveSlots, type ColorBy } from "../lib/palette";
import { prefersReducedMotion } from "../lib/theme";

/** The 3D view draws every visible concept up to this many (D234): the graph
 *  API's own ceiling (kb.MaxGraphNodeLimit; the UI asks for the default 2,000,
 *  and a truncated graph already says so). Above it the view says so and
 *  points at filters and the 2D atlas -- never a silent sample. */
export const MAX_3D_NODES = 5_000;
/** A selected node and its best-connected neighbours carry a name, up to
 *  this many: enough to read the neighbourhood, few enough not to cover it. */
const LABEL_LIMIT = 12;
/** How much of its hue a node outside the selection keeps. */
const RECEDED = 0.32;

interface Props {
  snapshot: GraphSnapshot;
  communities: Communities;
  colorBy: ColorBy;
  selected: string | null;
  hiddenIds: Set<string>;
  /** Changes with the theme: colours are re-resolved, the layout is kept. */
  themeKey: string;
  /** Live motion (drift, panorama, signals) on or off; the reader's own
   *  gestures work either way. */
  live: boolean;
  /** Pixels on the right covered by the reading panel. */
  occludedRight: number;
  onSelect(id: string | null): void;
  /** WebGL is missing or was lost: the caller falls back to the 2D atlas. */
  onUnavailable?(): void;
}

const endpoint = (end: string | SceneNode) => (typeof end === "object" ? end.id : end);

/**
 * The 3D atlas: a living, elastic network (D234).
 *
 * Pull a node and its links stretch, its neighbours follow and the motion
 * travels through the rest of its component; let go and it settles. At rest
 * the network breathes and the panorama turns slowly; a selection sends a few
 * signals along the concept's links, in the direction the links point. Pause
 * and reduced motion stop everything autonomous.
 *
 * lib/graph3d/scene draws it (three.js directly, one draw call per kind of
 * thing) and runs the physics of lib/graph3d/physics; this component feeds it
 * data, colours and the selection, and places the DOM labels. The scene is
 * built once per snapshot; filters, colours, theme and selection update it in
 * place -- a toggle never re-runs the layout.
 *
 * three.js is a separate chunk loaded on first use, never part of the initial
 * bundle.
 */
export function Graph3D(props: Props) {
  const { snapshot, hiddenIds } = props;
  const visibleCount = useMemo(
    () => snapshot.nodes.reduce((n, node) => n + (hiddenIds.has(node.id) ? 0 : 1), 0),
    [snapshot, hiddenIds],
  );
  if (visibleCount > MAX_3D_NODES) {
    return (
      <div className="state">
        <p className="state__title">Too many concepts for the 3D view</p>
        <p className="state__detail">
          It draws up to {MAX_3D_NODES.toLocaleString("en")} concepts and {visibleCount.toLocaleString("en")} are
          visible. Narrow them with the filters, or read the graph in 2D.
        </p>
      </div>
    );
  }
  return <View {...props} />;
}

function View({
  snapshot,
  communities,
  colorBy,
  selected,
  hiddenIds,
  themeKey,
  live,
  occludedRight,
  onSelect,
  onUnavailable,
}: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const labelsRef = useRef<HTMLDivElement | null>(null);
  const tooltipRef = useRef<HTMLSpanElement | null>(null);
  const sceneRef = useRef<LivingScene | null>(null);
  const [ready, setReady] = useState(0);
  const [failed, setFailed] = useState(false);

  // What the long-lived scene reads, through refs: a new callback identity from
  // the parent must never rebuild it.
  const onSelectRef = useRef(onSelect);
  onSelectRef.current = onSelect;
  const onUnavailableRef = useRef(onUnavailable);
  onUnavailableRef.current = onUnavailable;
  const liveRef = useRef(live);
  liveRef.current = live;
  const occludedRef = useRef(occludedRight);
  occludedRef.current = occludedRight;

  const reducedMotion = useMemo(() => prefersReducedMotion(), []);
  const savedPose = useRef<Pose | null>(null);

  // Node objects for this snapshot: they outlive filter changes, so a node
  // keeps its place when it is hidden and shown again.
  const nodes = useMemo(() => {
    const max = Math.max(1, ...snapshot.nodes.map((n) => n.in_degree + n.out_degree));
    return new Map<string, SceneNode>(
      snapshot.nodes.map((n) => [
        n.id,
        {
          id: n.id,
          weight: Math.sqrt((n.in_degree + n.out_degree) / max),
          ...seedPosition(n.id, snapshot.nodes.length),
        },
      ]),
    );
  }, [snapshot]);

  // Build the scene once per snapshot.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let disposed = false;
    let refit: number | undefined;
    void import("../lib/graph3d/scene")
      .then(({ LivingScene }) => {
        if (disposed) return;
        const scene = new LivingScene(
          container,
          {
            onSelect: (id) => onSelectRef.current(id),
            onHover: (id, x, y) => {
              const tip = tooltipRef.current;
              if (!tip) return;
              tip.hidden = !id;
              if (id) {
                tip.textContent = id;
                tip.style.transform = `translate(${Math.round(x + 14)}px, ${Math.round(y + 14)}px)`;
              }
            },
            onLost: () => {
              setFailed(true);
              onUnavailableRef.current?.();
            },
          },
          { live: liveRef.current, reducedMotion },
        );
        sceneRef.current = scene;
        setReady((n) => n + 1);
        // The warm-up leaves the graph still contracting: frame it once more
        // when it has settled, unless the reader has taken the camera.
        refit = window.setTimeout(() => scene.refitIfUntouched(), 1800);
      })
      .catch((err) => {
        console.warn("Atlas: the 3D view could not start", err);
        if (!disposed) {
          setFailed(true);
          onUnavailableRef.current?.();
        }
      });
    return () => {
      disposed = true;
      window.clearTimeout(refit);
      sceneRef.current?.dispose();
      sceneRef.current = null;
    };
  }, [snapshot, reducedMotion]);

  // The visible set.
  useEffect(() => {
    const scene = sceneRef.current;
    if (!scene) return;
    const visible = snapshot.nodes.filter((n) => !hiddenIds.has(n.id)).map((n) => nodes.get(n.id)!);
    const ids = new Set(visible.map((n) => n.id));
    const links: SceneLink[] = snapshot.edges
      .filter((e) => ids.has(e.source) && ids.has(e.target))
      .map((e) => ({ source: e.source, target: e.target }));
    // Warm-up runs before the first frame: long enough that the graph opens
    // nearly settled, short enough on a large KB not to block.
    const warmup = Math.round(Math.max(80, Math.min(300, 600_000 / Math.max(1, visible.length))));
    scene.setData(visible, links, warmup);
  }, [ready, snapshot, hiddenIds, nodes]);

  // Colour: by community or Map, the selection's neighbourhood kept and the
  // rest receded into the canvas. Re-resolved from the tokens on a theme change.
  useEffect(() => {
    const scene = sceneRef.current;
    if (!scene) return;
    const slots = resolveSlots();
    const canvas = cssVar("--surface-0");
    const near = new Set<string>();
    if (selected) {
      near.add(selected);
      for (const n of scene.neighboursOf(selected)) near.add(n.id);
    }
    const colours = snapshot.nodes
      .filter((n) => !hiddenIds.has(n.id))
      .map((n) => {
        const base = slots[colorBy === "community" ? communitySlot(communities, n.id) : collectionHue(n.collection ?? "")]!;
        return !selected || near.has(n.id) ? base : fade(base, RECEDED, canvas);
      });
    scene.setColours(colours, {
      background: canvas,
      edge: cssVar("--graph-edge-3d"),
      edgeActive: cssVar("--graph-edge-3d-active"),
      signal: cssVar("--graph-signal"),
      ring: cssVar("--graph-ring"),
    });
  }, [ready, snapshot, hiddenIds, colorBy, communities, themeKey, selected]);

  // Motion on or off.
  useEffect(() => {
    sceneRef.current?.setLive(live && !reducedMotion);
  }, [ready, live, reducedMotion]);

  // Selection: focus the camera, name the neighbourhood, send the signals.
  useEffect(() => {
    const scene = sceneRef.current;
    const layer = labelsRef.current;
    if (!scene || !layer) return;
    scene.setSelection(selected);
    const node = selected ? scene.nodeById(selected) : undefined;
    if (!node) {
      scene.unfocus(savedPose.current);
      savedPose.current = null;
      return;
    }
    scene.focus(node.id, occludedRef.current, () => {
      if (!savedPose.current) savedPose.current = scene.pose();
    });

    const plain = scene.linksOf(node.id).map((link) => ({ source: endpoint(link.source), target: endpoint(link.target), link }));
    const plan = planBursts(node.id, plain, (id) => scene.neighboursOf(id).length, liveRef.current && !reducedMotion);
    scene.sendSignals(plan.map((s) => ({ link: s.link.link, delayMs: s.delayMs })));

    // Labels: the selection and its neighbours, placed after every frame.
    const byDegree = [...scene.neighboursOf(node.id)].sort(
      (a, b) => scene.neighboursOf(b.id).length - scene.neighboursOf(a.id).length,
    );
    const named = [node, ...byDegree].slice(0, LABEL_LIMIT);
    const els = named.map((n) => {
      const el = document.createElement("span");
      el.className = n === node ? "graph3d__label graph3d__label--selected" : "graph3d__label";
      el.textContent = shortId(n.id);
      layer.appendChild(el);
      return el;
    });
    scene.afterFrame = () => {
      named.forEach((n, i) => {
        const at = scene.project(n);
        const el = els[i]!;
        el.hidden = !at;
        if (at) el.style.transform = `translate(-50%, -100%) translate(${Math.round(at.x)}px, ${Math.round(at.y - 9)}px)`;
      });
    };
    return () => {
      scene.afterFrame = null;
      els.forEach((el) => el.remove());
    };
  }, [ready, selected, reducedMotion]);

  if (failed) {
    return (
      <div className="state">
        <p className="state__title">The 3D view needs WebGL</p>
        <p className="state__detail">This browser could not start it. The 2D atlas shows the same graph.</p>
      </div>
    );
  }
  return (
    <div ref={containerRef} className="graph3d" data-testid="graph-3d">
      <div ref={labelsRef} className="graph3d__labels" aria-hidden="true" />
      <span ref={tooltipRef} className="graph3d__label graph3d__tooltip" aria-hidden="true" hidden />
    </div>
  );
}

function shortId(id: string): string {
  const cut = id.lastIndexOf("/");
  return cut === -1 ? id : id.slice(cut + 1);
}
