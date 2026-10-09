import { useEffect, useMemo, useRef, useState } from "react";
import type { GraphSnapshot } from "../api/types";
import { communitySlot, type Communities } from "../lib/communities";
import { fade } from "../lib/encoding";
import type { Pose } from "../lib/graph3d/camera";
import { growthOrder, growthPace, type GrowthStep } from "../lib/graph3d/growth";
import { planBursts } from "../lib/graph3d/motion";
import { seedPosition } from "../lib/graph3d/physics";
import type { LivingScene, SceneLink, SceneNode } from "../lib/graph3d/scene";
import { collectionHue, cssVar, resolveSlots, type ColorBy } from "../lib/palette";
import { nameOf, shortNameOf } from "../lib/names";
import { prefersReducedMotion } from "../lib/theme";
import type { ReactNode } from "react";
import { GrowthTimeline } from "./GrowthTimeline";
import { GraphSeed } from "./States";
import { Icon } from "./Icon";

/** The 3D view draws every visible concept up to this many (D234): the graph
 *  API's own ceiling (kb.MaxGraphNodeLimit; the UI asks for the default 2,000,
 *  and a truncated graph already says so). Above it the view says so and
 *  points at the filters -- never a silent sample. */
export const MAX_3D_NODES = 5_000;
/** A selected node and its best-connected neighbours carry a name, up to
 *  this many: enough to read the neighbourhood, few enough not to cover it. */
const LABEL_LIMIT = 12;
/** How much of its hue a node outside the selection keeps: little, so the
 *  selected neighbourhood stands alone against the canvas. */
const RECEDED = 0.12;
/** How long the panels' width must hold still before a selection is framed
 *  again for it: longer than the gap between two pointer moves of a drag. */
const REFRAME_DEBOUNCE_MS = 150;

/** An artifact as the atlas draws it: a diamond linked to the concepts it
 *  references. */
export interface GraphArtifact {
  kind: string;
  name: string;
  concepts: string[];
}

const ARTIFACT_PREFIX = "artifact:";
/** The band kept clear for the replay's timeline before it has a measured
 *  height: its usual height plus the gap above it. */
const GROWTH_BAND_PX = 75;
/** The closest the replay's camera comes, as a share of the whole graph's
 *  fit: low enough to follow a KB as it grows, high enough that a handful of
 *  first concepts is not blown up. */
const GROWTH_MIN_ZOOM = 0.6;


interface Props {
  snapshot: GraphSnapshot;
  /** What the layout belongs to (the KB and Map): a new key starts a fresh
   *  scene, while a new snapshot under the same key -- a live refresh --
   *  updates the one on screen, keeping every node where it is. */
  layoutKey: string;
  /** When each concept entered the KB, by id: offered, the graph can replay
   *  its own growth from the first concept. */
  loadBirths?(): Promise<Record<string, string>>;
  /** Artifacts to draw beside the concepts; empty draws none. */
  artifacts?: GraphArtifact[];
  /** A click on an artifact's diamond. */
  onOpenArtifact?(kind: string, name: string): void;
  /** A concept to point at without selecting it (a link hovered in the
   *  reading panel). */
  highlighted?: string | null;
  /** A double click on a node. */
  onExpand?(id: string): void;
  /** Overlays drawn over the canvas (the legend). */
  children?: ReactNode;
  communities: Communities;
  colorBy: ColorBy;
  selected: string | null;
  hiddenIds: Set<string>;
  /** Changes with the theme: colours are re-resolved, the layout is kept. */
  themeKey: string;
  /** Live motion (drift, panorama, signals) on or off; the reader's own
   *  gestures work either way. */
  live: boolean;
  /** Turns live motion on or off: the toggle sits with the camera controls. */
  onToggleLive?(): void;
  /** Pixels on the right covered by the reading panel. */
  occludedRight: number;
  /** Pixels on the left covered by the concept list. */
  occludedLeft?: number;
  onSelect(id: string | null): void;
  /** WebGL is missing or was lost: the caller says so and keeps the list. */
  onUnavailable?(): void;
}

const endpoint = (end: string | SceneNode) => (typeof end === "object" ? end.id : end);

/**
 * The atlas's graph: a living, elastic network in 3D (D234, D235).
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
export function GraphView(props: Props) {
  const { snapshot, hiddenIds } = props;
  const visibleCount = useMemo(
    () => snapshot.nodes.reduce((n, node) => n + (hiddenIds.has(node.id) ? 0 : 1), 0),
    [snapshot, hiddenIds],
  );
  if (visibleCount > MAX_3D_NODES) {
    return (
      <div className="state">
        <p className="state__title">Too many concepts to draw</p>
        <p className="state__detail">
          It draws up to {MAX_3D_NODES.toLocaleString("en")} concepts and {visibleCount.toLocaleString("en")} are
          visible. Narrow them with the filters.
        </p>
      </div>
    );
  }
  return <View {...props} />;
}

function View({
  artifacts = [],
  onOpenArtifact,
  highlighted = null,
  onExpand,
  children,
  snapshot: snap,
  layoutKey,
  loadBirths,
  communities,
  colorBy,
  selected,
  hiddenIds: filtered,
  themeKey,
  live,
  onToggleLive,
  occludedRight,
  occludedLeft = 0,
  onSelect,
  onUnavailable,
}: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const labelsRef = useRef<HTMLDivElement | null>(null);
  const tooltipRef = useRef<HTMLSpanElement | null>(null);
  const sceneRef = useRef<LivingScene | null>(null);
  const [ready, setReady] = useState(0);
  const [failed, setFailed] = useState(false);
  // False from a new snapshot until its first frame is on screen: the import of
  // the renderer and the synchronous warm-up take seconds on a large KB, and a
  // cover keeps that from reading as a blank canvas.
  const [drawn, setDrawn] = useState(false);
  const snapshot = snap;

  // The growth replay: the concepts in birth order and how many are out. A
  // concept not yet born is hidden like a filtered one, so every effect below
  // draws the replay without knowing about it.
  const [growth, setGrowth] = useState<{ order: GrowthStep[]; shown: number; playing: boolean } | null>(null);
  // The fit of the whole graph, taken before the replay hides it: the replay
  // never comes closer than a share of it, so its first concepts start small
  // in the middle instead of filling the canvas.
  const growthFloor = useRef(0);
  const hiddenIds = useMemo(() => {
    if (!growth) return filtered;
    const hidden = new Set(filtered);
    for (let i = growth.shown; i < growth.order.length; i++) hidden.add(growth.order[i]!.id);
    return hidden;
  }, [filtered, growth]);
  const startGrowth = () => {
    if (growth) return setGrowth(null);
    growthFloor.current = (sceneRef.current?.fitDistance(GROWTH_BAND_PX) ?? 0) * GROWTH_MIN_ZOOM;
    void loadBirths?.()
      .then((births) => setGrowth({ order: growthOrder(snapshot.nodes, snapshot.edges, births), shown: 1, playing: true }))
      .catch((err) => console.warn("Atlas: the growth replay could not start", err));
  };
  useEffect(() => {
    if (!growth?.playing) return;
    const done = growth.shown >= growth.order.length;
    const pace = growthPace(growth.order.length);
    // At the end it pauses on the last day and stays open: the timeline is
    // still there to scrub, and the close button ends the replay.
    const timer = window.setTimeout(
      () =>
        setGrowth(
          done
            ? { ...growth, playing: false }
            : { ...growth, shown: Math.min(growth.order.length, growth.shown + pace.perTick) },
        ),
      done ? 0 : pace.tickMs,
    );
    return () => window.clearTimeout(timer);
  }, [growth]);
  const seekGrowth = (shown: number) => setGrowth((g) => (g ? { ...g, shown, playing: false } : g));
  // Play from where it is; at the end, play starts over.
  const togglePlay = () =>
    setGrowth((g) =>
      g ? { ...g, playing: !g.playing, shown: !g.playing && g.shown >= g.order.length ? 1 : g.shown } : g,
    );
  // The camera keeps every concept that is out in sight, above the
  // timeline: while it plays, easing after the graph as it grows; paused,
  // once after each move along the timeline and again when the knock has
  // settled.
  const growthFrame = growth ? (growth.playing ? "playing" : `paused:${growth.shown}`) : null;
  useEffect(() => {
    if (!growthFrame) return;
    const fit = () => {
      const band = containerRef.current?.parentElement?.querySelector<HTMLElement>(".growth-timeline");
      sceneRef.current?.fitEverything(band ? band.offsetHeight + 24 : GROWTH_BAND_PX, 900, growthFloor.current);
    };
    fit();
    const timer =
      growthFrame === "playing" ? window.setInterval(fit, 500) : window.setTimeout(fit, 1200);
    return () => {
      window.clearInterval(timer);
      window.clearTimeout(timer);
    };
  }, [growthFrame]);
  // A new layout (another KB or Map) ends a replay.
  useEffect(() => setGrowth(null), [layoutKey]);

  // What the long-lived scene reads, through refs: a new callback identity from
  // the parent must never rebuild it.
  const onSelectRef = useRef(onSelect);
  onSelectRef.current = onSelect;
  const onExpandRef = useRef(onExpand);
  onExpandRef.current = onExpand;
  const onUnavailableRef = useRef(onUnavailable);
  onUnavailableRef.current = onUnavailable;
  const onOpenArtifactRef = useRef(onOpenArtifact);
  onOpenArtifactRef.current = onOpenArtifact;
  const liveRef = useRef(live);
  liveRef.current = live;
  const occludedRef = useRef({ left: occludedLeft, right: occludedRight });
  occludedRef.current = { left: occludedLeft, right: occludedRight };
  // The occlusion the current selection was last framed for.
  const focusedOcclusion = useRef<{ left: number; right: number } | null>(null);
  // The node the camera was last sent to: a refresh re-places the labels but
  // never re-runs the move, which would restart the camera on every update.
  const focusedId = useRef<string | null>(null);

  const reducedMotion = useMemo(() => prefersReducedMotion(), []);
  const savedPose = useRef<Pose | null>(null);

  // Names by id: the canvas labels a concept by its title where it has one.
  const byId = useMemo(() => new Map(snapshot.nodes.map((n) => [n.id, n])), [snapshot]);
  const byIdRef = useRef(byId);
  byIdRef.current = byId;

  // Node objects for this layout: they outlive filter changes and live
  // refreshes, so a node keeps its place when it is hidden and shown again or
  // when the KB moves under it. A node new to the layout starts beside a
  // neighbour already placed, and the simulation pulls it in from there.
  const nodeStore = useRef({
    key: "",
    nodes: new Map<string, SceneNode>(),
    artifacts: new Map<string, SceneNode>(),
  });
  if (nodeStore.current.key !== layoutKey) {
    nodeStore.current = { key: layoutKey, nodes: new Map(), artifacts: new Map() };
  }
  const nodes = useMemo(() => {
    const store = nodeStore.current.nodes;
    const max = Math.max(1, ...snapshot.nodes.map((n) => n.in_degree + n.out_degree));
    const placed = store.size > 0;
    const adjacent = new Map<string, string[]>();
    if (placed) {
      for (const e of snapshot.edges) {
        (adjacent.get(e.source) ?? adjacent.set(e.source, []).get(e.source)!).push(e.target);
        (adjacent.get(e.target) ?? adjacent.set(e.target, []).get(e.target)!).push(e.source);
      }
    }
    const next = new Map<string, SceneNode>();
    for (const n of snapshot.nodes) {
      const weight = Math.sqrt((n.in_degree + n.out_degree) / max);
      const kept = store.get(n.id);
      if (kept) {
        kept.weight = weight;
        next.set(n.id, kept);
        continue;
      }
      const anchor = placed ? (adjacent.get(n.id) ?? []).map((id) => store.get(id)).find(Boolean) : undefined;
      next.set(n.id, { id: n.id, weight, ...(anchor ? beside(anchor, n.id) : seedPosition(n.id, snapshot.nodes.length, 3)) });
    }
    nodeStore.current.nodes = next;
    return next;
  }, [snapshot, layoutKey]);

  // Artifact nodes: kept across toggles and refreshes like concept nodes, so
  // a diamond shown again returns to its place.
  const artifactNodes = nodeStore.current.artifacts;
  // The artifacts drawn now: only those that reference a visible concept,
  // each with its links to those concepts.
  const drawnArtifacts = useMemo(() => {
    const out: { node: SceneNode; label: string; targets: string[] }[] = [];
    for (const a of artifacts) {
      const targets = a.concepts.filter((id) => byId.has(id) && !hiddenIds.has(id));
      if (!targets.length) continue;
      const id = `${ARTIFACT_PREFIX}${a.kind}/${a.name}`;
      let node = artifactNodes.get(id);
      if (!node) {
        node = { id, weight: 0.35, artifact: true, ...seedPosition(id, snapshot.nodes.length, 3) };
        artifactNodes.set(id, node);
      }
      out.push({ node, label: `${a.name} · ${a.kind}`, targets });
    }
    return out;
  }, [artifacts, artifactNodes, byId, hiddenIds, snapshot]);
  const artifactLabels = useRef(new Map<string, string>());
  artifactLabels.current = new Map(drawnArtifacts.map((a) => [a.node.id, a.label]));

  // Build the scene once per snapshot.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let disposed = false;
    setDrawn(false);
    setReady(0);
    focusedId.current = null;
    void import("../lib/graph3d/scene")
      .then(({ LivingScene }) => {
        if (disposed) return;
        const scene = new LivingScene(
          container,
          {
            onSelect: (id) => {
              if (id?.startsWith(ARTIFACT_PREFIX)) {
                const ref = id.slice(ARTIFACT_PREFIX.length);
                const slash = ref.indexOf("/");
                onOpenArtifactRef.current?.(ref.slice(0, slash), ref.slice(slash + 1));
                return;
              }
              onSelectRef.current(id);
            },
            onExpand: (id) => {
              if (!id.startsWith(ARTIFACT_PREFIX)) onExpandRef.current?.(id);
            },
            onHover: (id, x, y) => {
              const tip = tooltipRef.current;
              if (!tip) return;
              tip.hidden = !id;
              if (id) {
                tip.textContent = artifactLabels.current.get(id) ?? nameOf(byIdRef.current.get(id) ?? { id });
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
      sceneRef.current?.dispose();
      sceneRef.current = null;
    };
    // Built once: a new snapshot -- a refresh, or another KB or Map -- goes
    // through setData below and keeps the scene and the camera, so a switch
    // is an animation of the graph, never a blank canvas.
  }, [reducedMotion]);

  // The visible set.
  const lastVisible = useRef(new Set<string>());
  const lastLayout = useRef("");
  useEffect(() => {
    const scene = sceneRef.current;
    if (!scene || !ready) return;
    const replace = lastLayout.current !== "" && lastLayout.current !== layoutKey;
    lastLayout.current = layoutKey;
    const visible = snapshot.nodes.filter((n) => !hiddenIds.has(n.id)).map((n) => nodes.get(n.id)!);
    const ids = new Set(visible.map((n) => n.id));
    if (growth) {
      // A concept born in the replay sprouts beside one already out, not
      // where the full layout had it, and the knock spreads it from there.
      for (const node of visible) {
        if (lastVisible.current.has(node.id)) continue;
        const anchor = snapshot.edges
          .flatMap((e) => (e.source === node.id ? [e.target] : e.target === node.id ? [e.source] : []))
          .find((id) => lastVisible.current.has(id));
        Object.assign(node, anchor ? beside(nodes.get(anchor)!, node.id) : { x: 0, y: 0, z: 0 }, { vx: 0, vy: 0, vz: 0 });
      }
    }
    lastVisible.current = ids;
    const links: SceneLink[] = snapshot.edges
      .filter((e) => ids.has(e.source) && ids.has(e.target))
      .map((e) => ({ source: e.source, target: e.target }));
    for (const a of drawnArtifacts) {
      visible.push(a.node);
      for (const t of a.targets) links.push({ source: a.node.id, target: t });
    }
    // Warm-up runs before the first frame and blocks it, so it is kept short:
    // the burst opens the graph while the simulation, still warm, finishes
    // settling it in view. Without the burst (reduced motion) it opens
    // nearly settled instead.
    const budget = reducedMotion ? 600_000 : 300_000;
    const warmup = Math.round(Math.max(60, Math.min(reducedMotion ? 300 : 150, budget / Math.max(1, visible.length))));
    scene.setData(visible, links, warmup, replace);
    // A new layout opens out of its centre and the camera follows it as it
    // settles (scene.setData): no timed refit, which would jump.
    // Two frames: the first renders the scene, the second runs after it is painted.
    let frame = requestAnimationFrame(() => {
      frame = requestAnimationFrame(() => setDrawn(true));
    });
    return () => cancelAnimationFrame(frame);
  }, [ready, snapshot, hiddenIds, nodes, drawnArtifacts]);

  // A selection hidden by a filter stays selected (the URL and the Inspector
  // keep it), but the graph draws the filtered set as if nothing were (D240).
  const shown = selected && !hiddenIds.has(selected) ? selected : null;

  // Colour: by community or Map, the selection's neighbourhood kept and the
  // rest receded into the canvas. Re-resolved from the tokens on a theme change.
  useEffect(() => {
    const scene = sceneRef.current;
    if (!scene) return;
    const slots = resolveSlots();
    const canvas = cssVar("--surface-0");
    const near = new Set<string>();
    if (shown) {
      near.add(shown);
      for (const n of scene.neighboursOf(shown)) near.add(n.id);
    }
    const colours = snapshot.nodes
      .filter((n) => !hiddenIds.has(n.id))
      .map((n) => {
        const base = slots[colorBy === "community" ? communitySlot(communities, n.id) : collectionHue(n.collection ?? "")]!;
        return !shown || near.has(n.id) ? base : fade(base, RECEDED, canvas);
      });
    // Diamonds wear the signal colour: an artifact is not in any community
    // or Map, and a colour of its own says so.
    const artifactColour = cssVar("--graph-signal");
    for (const a of drawnArtifacts) {
      colours.push(!shown || near.has(a.node.id) ? artifactColour : fade(artifactColour, RECEDED, canvas));
    }
    scene.setColours(colours, {
      background: canvas,
      edge: cssVar("--graph-edge-3d"),
      edgeActive: cssVar("--graph-edge-3d-active"),
      signal: cssVar("--graph-signal"),
      ring: cssVar("--graph-ring"),
    });
  }, [ready, snapshot, hiddenIds, colorBy, communities, themeKey, shown, drawnArtifacts]);

  // Motion on or off.
  useEffect(() => {
    sceneRef.current?.setLive(live && !reducedMotion);
  }, [ready, live, reducedMotion]);

  // Selection: focus the camera, name the neighbourhood, send the signals.
  // Declared after the visible-set effect on purpose: on a filter change it
  // re-runs in the same commit after `setData`, so the neighbours it names
  // are the visible ones.
  useEffect(() => {
    const scene = sceneRef.current;
    const layer = labelsRef.current;
    if (!scene || !layer) return;
    scene.setSelection(shown);
    const node = shown ? scene.nodeById(shown) : undefined;
    if (!node) {
      if (focusedId.current !== null) scene.unfocus(savedPose.current);
      focusedId.current = null;
      savedPose.current = null;
      return;
    }
    if (focusedId.current !== node.id) {
      focusedId.current = node.id;
      focusedOcclusion.current = occludedRef.current;
      scene.focus(node.id, occludedRef.current, () => {
        if (!savedPose.current) savedPose.current = scene.pose();
      });

      const plain = scene.linksOf(node.id).map((link) => ({ source: endpoint(link.source), target: endpoint(link.target), link }));
      const plan = planBursts(node.id, plain, (id) => scene.neighboursOf(id).length, liveRef.current && !reducedMotion);
      scene.sendSignals(plan.map((s) => ({ link: s.link.link, delayMs: s.delayMs })));
    }

    // Labels: the selection and its neighbours, placed after every frame.
    const byDegree = [...scene.neighboursOf(node.id)].sort(
      (a, b) => scene.neighboursOf(b.id).length - scene.neighboursOf(a.id).length,
    );
    return placeLabels(scene, layer, [node, ...byDegree].slice(0, LABEL_LIMIT), node, (id) =>
      shortNameOf(byId.get(id) ?? { id }),
    );
  }, [ready, shown, hiddenIds, reducedMotion, byId]);

  // The panels changed width under a selection (a splitter drag, the list
  // opening, a window resize re-clamping the reading panel): frame it again in
  // the strip they now leave, without the signals — nothing was selected anew.
  // Debounced, so a drag re-frames once it settles instead of restarting the
  // tween on every pointer move; and compared with the occlusion the selection
  // was last framed for, so a selection's own opening of the panel is left to
  // the effect above.
  useEffect(() => {
    if (!shown) return;
    const timer = window.setTimeout(() => {
      const scene = sceneRef.current;
      const node = scene?.nodeById(shown);
      const last = focusedOcclusion.current;
      if (!scene || !node) return;
      if (last && last.left === occludedLeft && last.right === occludedRight) return;
      focusedOcclusion.current = { left: occludedLeft, right: occludedRight };
      scene.focus(node.id, focusedOcclusion.current, () => {
        if (!savedPose.current) savedPose.current = scene.pose();
      });
    }, REFRAME_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [ready, shown, occludedLeft, occludedRight]);

  // The hovered link's node, named where it is.
  useEffect(() => {
    const scene = sceneRef.current;
    const layer = labelsRef.current;
    const node = highlighted ? scene?.nodeById(highlighted) : undefined;
    if (!scene || !layer || !node) return;
    const el = document.createElement("span");
    el.className = "graph3d__label graph3d__label--selected graph3d__label--preview";
    el.textContent = nameOf(byId.get(node.id) ?? node);
    layer.appendChild(el);
    const place = () => {
      const at = scene.project(node);
      el.hidden = !at;
      if (at) el.style.transform = `translate(-50%, -100%) translate(${Math.round(at.x)}px, ${Math.round(at.y - 9)}px)`;
    };
    scene.frameListeners.add(place);
    return () => {
      scene.frameListeners.delete(place);
      el.remove();
    };
  }, [ready, highlighted, byId]);

  if (failed) {
    return (
      <div className="state">
        <p className="state__title">The 3D view needs WebGL</p>
        <p className="state__detail">This browser could not start it. Every concept is still in the list and the search.</p>
      </div>
    );
  }
  const scene = () => sceneRef.current;
  return (
    <div
      className="graph graph--view"
      data-testid="graph-view"
      data-motion={live && !reducedMotion ? "live" : "still"}
      data-growing={growth !== null || undefined}
    >
      <div ref={containerRef} className="graph3d">
        <div ref={labelsRef} className="graph3d__labels" aria-hidden="true" />
        <span ref={tooltipRef} className="graph3d__label graph3d__tooltip" aria-hidden="true" hidden />
      </div>
      {/* Kept through the fade-out; visibility: hidden then drops it from the tree. */}
      <div className="graph__loading" data-drawn={drawn}>
        <GraphSeed label={`Charting ${snapshot.nodes.length} concepts`} />
      </div>
      <div className="graph__controls" role="group" aria-label="Graph camera">
        <button type="button" className="button button--icon" onClick={() => scene()?.zoomBy(1 / 1.35)} aria-label="Zoom in">
          <Icon name="plus" size={16} />
        </button>
        <button type="button" className="button button--icon" onClick={() => scene()?.zoomBy(1.35)} aria-label="Zoom out">
          <Icon name="minus" size={16} />
        </button>
        <button type="button" className="button button--icon" onClick={() => scene()?.frameAll()} aria-label="Fit graph to view">
          <Icon name="fit" size={16} />
        </button>
        {loadBirths && (
          <button
            type="button"
            className="button button--icon"
            onClick={startGrowth}
            aria-label="Replay growth"
            aria-pressed={growth !== null}
            title={growth ? "Stop the replay" : "Watch the KB grow from its first concept"}
          >
            <Icon name="grow" size={16} />
          </button>
        )}
        {onToggleLive && (
          <>
            <span className="graph__controls-sep" aria-hidden="true" />
            <button
              type="button"
              className="button button--icon"
              aria-label="Motion"
              aria-pressed={live}
              onClick={onToggleLive}
              title={live ? "Pause the drift, the panorama and the signals" : "Let the graph move on its own"}
            >
              <Icon name={live ? "pause" : "motion"} size={16} />
            </button>
          </>
        )}
      </div>
      {children}
      {growth && (
        <GrowthTimeline
          order={growth.order}
          shown={growth.shown}
          playing={growth.playing}
          onSeek={seekGrowth}
          onTogglePlay={togglePlay}
          onClose={() => setGrowth(null)}
        />
      )}
      {snapshot.truncated && (
        <div className="graph__banner banner" role="status">
          <span className="banner__glyph">
            <Icon name="info" size={16} />
          </span>
          <span>
            Showing {snapshot.nodes.length} of {snapshot.total_nodes} concepts. This graph is truncated &mdash; narrow it
            by Map, type or status to see a complete picture.
          </span>
        </div>
      )}
    </div>
  );
}

/** A start for a node new to a layout already on screen: a short, stable
 *  offset from a neighbour, so it grows out of the graph rather than flying in
 *  from the seed ball. */
function beside(anchor: SceneNode, id: string): { x: number; y: number; z: number } {
  const p = seedPosition(id, 1, 3);
  return { x: (anchor.x ?? 0) + p.x * 0.5, y: (anchor.y ?? 0) + p.y * 0.5, z: (anchor.z ?? 0) + p.z * 0.5 };
}

/** Names `named` on the label layer, following them every frame; returns the
 *  cleanup. The selected node's name is set in the editorial voice. */
function placeLabels(
  scene: LivingScene,
  layer: HTMLElement,
  named: SceneNode[],
  selected: SceneNode | null,
  name: (id: string) => string,
) {
  const els = named.map((n) => {
    const el = document.createElement("span");
    el.className = n === selected ? "graph3d__label graph3d__label--selected" : "graph3d__label";
    el.textContent = name(n.id);
    layer.appendChild(el);
    return el;
  });
  const place = () => {
    named.forEach((n, i) => {
      const at = scene.project(n);
      const el = els[i]!;
      el.hidden = !at;
      if (at) el.style.transform = `translate(-50%, -100%) translate(${Math.round(at.x)}px, ${Math.round(at.y - 9)}px)`;
    });
  };
  scene.frameListeners.add(place);
  return () => {
    scene.frameListeners.delete(place);
    els.forEach((el) => el.remove());
  };
}
