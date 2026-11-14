import { createContext, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { ClusterEdge, ClusterUnfold, GraphCluster, GraphSnapshot } from "../api/types";
import { radiusOf as layoutRadius } from "../lib/graph3d/clusterlayout";
import { anchorGlyphs, budgetEdges, clusterSlot, isClusterID, unfoldedMemberIds } from "../lib/clusters";
import { communitySlot, type Communities } from "../lib/communities";
import { fade } from "../lib/encoding";
import type { Pose } from "../lib/graph3d/camera";
import {
  GROWTH_MIN_ZOOM,
  GROWTH_TICK_MIN_MS,
  growthOrder,
  growthSchedule,
  seedGrowth,
  shownAt,
  stepTime,
  type GrowthStep,
} from "../lib/graph3d/growth";
import { planBursts } from "../lib/graph3d/motion";
import { beside, seedPosition } from "../lib/graph3d/physics";
import { recallLayout, rememberLayout, sameKB, snapshotPositions, type LayoutMemory } from "../lib/graph3d/transition";
import type { LivingScene, SceneLink, SceneNode } from "../lib/graph3d/scene";
import { collectionHue, cssVar, resolveSlots, type ColorBy } from "../lib/palette";
import { useModal } from "../lib/modal";
import { nameOf, shortNameOf } from "../lib/names";
import { prefersReducedMotion } from "../lib/theme";
import type { CSSProperties, ReactNode } from "react";
import { ASPECTS, type Aspect } from "../lib/graph3d/record";
import { GrowthTimeline } from "./GrowthTimeline";
import { useGrowthExport } from "./useGrowthExport";
import { GraphSeed } from "./States";
import { Icon } from "./Icon";

/** The 3D view draws every visible concept up to this many (D234): the graph
 *  API's own ceiling (kb.MaxGraphNodeLimit, which the UI asks for: GRAPH_ALL_NODES;
 *  a truncated graph already says so). Above it the view says so and
 *  points at the filters -- never a silent sample. */
export const MAX_3D_NODES = 7_500;
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
/** How long the camera keeps following after the replay pauses or is moved
 *  along its timeline: the time the knock of the move takes to settle. */
const GROWTH_SETTLE_MS = 2500;

interface Props {
  snapshot: GraphSnapshot;
  /** What the layout belongs to (the KB and Map): a new key starts a fresh
   *  scene, while a new snapshot under the same key -- a live refresh --
   *  updates the one on screen, keeping every node where it is. */
  layoutKey: string;
  /** When each concept entered the KB, by id: offered, the graph can replay
   *  its own growth from the first concept. */
  loadBirths?(): Promise<Record<string, string>>;
  /** The KB's name: on the exported video and in its file name. */
  kbName?: string;
  /** Artifacts to draw beside the concepts; empty draws none. */
  artifacts?: GraphArtifact[];
  /** A click on an artifact's diamond. */
  onOpenArtifact?(kind: string, name: string): void;
  /** A concept to point at without selecting it (a link hovered in the
   *  reading panel). */
  highlighted?: string | null;
  /** A double click on a node. */
  onExpand?(id: string): void;
  /** Overlays drawn over the canvas (the legend card, a Map's contract). */
  children?: ReactNode;
  communities: Communities;
  /** Colour is the Map's (the default): the rail, the legend and the graph agree. Community colouring is kept for the renderer's tests only. */
  colorBy?: ColorBy;
  selected: string | null;
  hiddenIds: Set<string>;
  /** Changes with the theme: colours are re-resolved, the layout is kept. */
  themeKey: string;
  /** Live motion (drift, panorama, signals) on or off; the reader's own
   *  gestures work either way. */
  live: boolean;
  /** Turns live motion on or off: the toggle sits with the camera controls. */
  onToggleLive?(): void;
  /** The camera controls (zoom, fit, motion) over the canvas: the whole KB's
   *  view only, a Map's view leaves the space to its contract panel. */
  cameraControls?: boolean;
  /** Pixels on the right covered by the reading panel. */
  occludedRight: number;
  /** Pixels on the left covered by the concept list. */
  occludedLeft?: number;
  onSelect(id: string | null): void;
  /** WebGL is missing or was lost: the caller says so and keeps the list. */
  onUnavailable?(): void;
  /** The clustered graph, when the snapshot is the unfolded part of one. */
  clusterView?: ClusterView | null;
}

/** The clustered graph's folded communities and what reaches them
 *  (D1986): drawn as sized super-nodes beside the
 *  snapshot's concepts, which are the unfolded members. */
export interface ClusterView {
  clusters: GraphCluster[];
  /** The edges with at least one cluster end; concept links stay in the snapshot. */
  edges: ClusterEdge[];
  unfolded: ClusterUnfold[];
  /** The visible concepts in scope, folded or not. */
  totalNodes: number;
  onUnfold(id: string): void;
  onFold(id: string): void;
}

const NO_CLUSTERS: GraphCluster[] = [];
const NO_CLUSTER_EDGES: ClusterEdge[] = [];
const NO_UNFOLDED: ClusterUnfold[] = [];
/** How large a super-node may be against its share of concepts: a square-root
 *  scale (its area follows its member count), floored so a small cluster is
 *  still a thing to click. */
const CLUSTER_MIN_WEIGHT = 0.35;
/** A cluster unfolds when a zoom brings it this large in the view (its
 *  radius over its distance from the camera) near the middle of the canvas. */
const UNFOLD_APPARENT_SIZE = 0.09;
const UNFOLD_ZOOM_WINDOW_MS = 700;

export function clusterWeight(size: number, maxSize: number): number {
  return CLUSTER_MIN_WEIGHT + (1 - CLUSTER_MIN_WEIGHT) * Math.sqrt(Math.min(1, size / Math.max(1, maxSize)));
}

/** How many strokes draw a super-edge of this weight: WebGL lines are one
 *  pixel wide whatever the material says, so a heavier edge is drawn by
 *  overlaying its stroke (and a lone link already by three, to read between
 *  super-nodes at the default zoom): 3, 5 from 4 links, 8 from 32. */
export function edgeStrokes(weight: number): number {
  return weight >= 32 ? 8 : weight >= 4 ? 5 : 3;
}

const plural = (n: number, one: string) => `${n.toLocaleString("en")} ${one}${n === 1 ? "" : "s"}`;

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
  kbName = "Knowledge base",
  communities,
  colorBy = "map",
  selected,
  hiddenIds: filtered,
  themeKey,
  live,
  onToggleLive,
  cameraControls = true,
  occludedRight,
  occludedLeft = 0,
  onSelect,
  onUnavailable,
  clusterView = null,
}: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const labelsRef = useRef<HTMLDivElement | null>(null);
  const tooltipRef = useRef<HTMLSpanElement | null>(null);
  const sceneRef = useRef<LivingScene | null>(null);
  const [ready, setReady] = useState(0);
  const [failed, setFailed] = useState(false);
  // The scene chunk could not be fetched (a tab opened before an upgrade asks
  // for a hashed file the new build no longer has): reloading fixes it, which
  // is not what a lost WebGL context needs.
  const [stale, setStale] = useState(false);
  // False from a new snapshot until its first frame is on screen: the import of
  // the renderer and the synchronous warm-up take seconds on a large KB, and a
  // cover keeps that from reading as a blank canvas.
  const [drawn, setDrawn] = useState(false);
  const snapshot = snap;

  const reducedMotion = useMemo(() => prefersReducedMotion(), []);

  // The growth replay: the concepts in birth order and how many are out. A
  // concept not yet born is hidden like a filtered one, so every effect below
  // draws the replay without knowing about it.
  // Keyed by the layout it was opened on and derived in render: another KB or
  // Map shows no replay in the very commit that changes it, never one frame of
  // the old order against the new snapshot.
  const [growthState, setGrowth] = useState<{
    key: string;
    order: GrowthStep[];
    shown: number;
    playing: boolean;
  } | null>(null);
  const growth = growthState?.key === layoutKey ? growthState : null;
  // The fit of the whole graph, taken before the replay hides it: the replay
  // never comes closer than a share of it, so its first concepts start small
  // in the middle instead of filling the canvas.
  const growthFloor = useRef(0);
  const wasGrowing = useRef(false);
  const hiddenIds = useMemo(() => {
    if (!growth) return filtered;
    const hidden = new Set(filtered);
    for (let i = growth.shown; i < growth.order.length; i++) hidden.add(growth.order[i]!.id);
    return hidden;
  }, [filtered, growth]);
  const exp = useGrowthExport({
    loadBirths,
    nodes: snapshot.nodes,
    edges: snapshot.edges,
    kbName,
    live: live && !reducedMotion,
    // What the video starts from, read when the export begins: the layout on
    // screen and the colours the graph wears now.
    layout: () => {
      const slots = resolveSlots();
      const positions = new Map<string, { x: number; y: number; z: number }>();
      const weights = new Map<string, number>();
      for (const [id, n] of nodes) {
        positions.set(id, { x: n.x ?? 0, y: n.y ?? 0, z: n.z ?? 0 });
        weights.set(id, n.weight);
      }
      return {
        positions,
        weights,
        colourOf: (id) =>
          slots[
            colorBy === "community" ? communitySlot(communities, id) : collectionHue(byId.get(id)?.collection ?? "")
          ]!,
        palette: {
          background: cssVar("--surface-0"),
          edge: cssVar("--graph-edge-3d"),
          edgeActive: cssVar("--graph-edge-3d-active"),
          signal: cssVar("--graph-signal"),
          ring: cssVar("--graph-ring"),
        },
      };
    },
  });
  const startGrowth = () => {
    if (growth) return setGrowth(null);
    growthFloor.current = (sceneRef.current?.fitDistance(GROWTH_BAND_PX) ?? 0) * GROWTH_MIN_ZOOM;
    const key = layoutKeyRef.current;
    void loadBirths?.()
      .then((births) => {
        // Births that arrive after a KB switch, or an unmount, open nothing.
        if (!alive.current || layoutKeyRef.current !== key) return;
        setGrowth({ key, order: growthOrder(snapshot.nodes, snapshot.edges, births), shown: 1, playing: true });
      })
      .catch((err) => console.warn("Atlas: the growth replay could not start", err));
  };
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const growthPlan = useMemo(() => (growth ? growthSchedule(growth.order) : null), [growth?.order]);
  // Play runs on a clock anchored where it starts, so a scrub or a pause
  // resumes from the position it left; null while paused.
  const growthClock = useRef<number | null>(null);
  useEffect(() => {
    if (!growth?.playing || !growthPlan) {
      growthClock.current = null;
      return;
    }
    const now = performance.now();
    growthClock.current ??= now - stepTime(growthPlan, Math.max(0, growth.shown - 1));
    const t0 = growthClock.current;
    const done = growth.shown >= growth.order.length;
    // At the end it holds the last beat, then pauses on the last day and stays
    // open: the timeline is still there to scrub, and the close button ends the replay.
    const next = done ? growthPlan.totalMs : stepTime(growthPlan, growth.shown);
    const timer = window.setTimeout(
      () =>
        setGrowth(
          done
            ? { ...growth, playing: false }
            : { ...growth, shown: Math.max(growth.shown + 1, shownAt(growthPlan, performance.now() - t0)) },
        ),
      Math.max(done ? 0 : GROWTH_TICK_MIN_MS, t0 + next - now),
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
  // timeline, easing after the graph continuously (scene.followFit): while it
  // plays, until it pauses; paused, until the knock of a move along the
  // timeline has settled.
  const growthFrame = growth ? (growth.playing ? "playing" : `paused:${growth.shown}`) : null;
  useEffect(() => {
    const scene = sceneRef.current;
    if (!scene) return;
    if (!growthFrame) {
      scene.stopFollowingFit();
      return;
    }
    const band = containerRef.current?.parentElement?.querySelector<HTMLElement>(".growth-timeline");
    const reserve = band ? band.offsetHeight + 24 : GROWTH_BAND_PX;
    scene.followFit(reserve, growthFloor.current, growthFrame === "playing" ? Infinity : GROWTH_SETTLE_MS);
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
  const clusterViewRef = useRef(clusterView);
  clusterViewRef.current = clusterView;
  // While the growth replay is open (playing or paused) the graph is a film,
  // not a map: a click opens nothing.
  const growingRef = useRef(false);
  growingRef.current = growth !== null;
  const clusterList = clusterView?.clusters ?? NO_CLUSTERS;
  const clusterEdges = clusterView?.edges ?? NO_CLUSTER_EDGES;
  const unfolded = clusterView?.unfolded ?? NO_UNFOLDED;
  const clusterLabels = useRef(new Map<string, string>());
  clusterLabels.current = new Map(clusterList.map((c) => [c.id, `${c.label} · ${plural(c.size, "concept")}`]));
  // The members each cluster unfolded into, as first drawn: folding it again
  // brings the cluster back where they are, not from a seed.
  const membersOf = useRef(new Map<string, string[]>());
  const local = useRef<Set<string> | null>(null);
  const liveRef = useRef(live);
  liveRef.current = live;
  const occludedRef = useRef({ left: occludedLeft, right: occludedRight });
  occludedRef.current = { left: occludedLeft, right: occludedRight };
  // The occlusion the current selection was last framed for.
  const focusedOcclusion = useRef<{ left: number; right: number } | null>(null);
  // The node the camera was last sent to: a refresh re-places the labels but
  // never re-runs the move, which would restart the camera on every update.
  const focusedId = useRef<string | null>(null);

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
    const before = nodeStore.current;
    if (before.key === "") {
      // First layout of this mount: a layout the Atlas was left on comes back
      // where it settled, not from a seed.
      const memory = recallLayout(layoutKey);
      const place = (positions: Map<string, { x: number; y: number; z: number }>, artifact: boolean) =>
        new Map<string, SceneNode>(
          [...positions].map(([id, p]) => {
            const node: SceneNode = { id, weight: artifact ? 0.35 : 0, ...p, vx: 0, vy: 0, vz: 0 };
            if (artifact) node.artifact = true;
            return [id, node];
          }),
        );
      nodeStore.current = {
        key: layoutKey,
        nodes: memory ? place(memory.nodes, false) : new Map(),
        artifacts: memory ? place(memory.artifacts, true) : new Map(),
      };
    } else if (sameKB(before.key, layoutKey)) {
      // Another Map of the same KB: the concepts both views draw keep their
      // places and the scene morphs (setData), instead of growing from a point.
      nodeStore.current = { key: layoutKey, nodes: before.nodes, artifacts: before.artifacts };
    } else {
      nodeStore.current = { key: layoutKey, nodes: new Map(), artifacts: new Map() };
      // Another KB's clusters: their members are not this one's.
      membersOf.current = new Map();
    }
  }
  const layoutKeyRef = useRef(layoutKey);
  layoutKeyRef.current = layoutKey;
  // Set for the first data a new scene receives: only then is a remembered
  // camera restored.
  const restoreNext = useRef<LayoutMemory | null>(null);
  // This memo moves the node store along and decides what the next layout moves
  // alone and where the camera goes, so it must not run twice for one input:
  // StrictMode calls it twice in development, and the second call would see the
  // store already advanced and decide nothing. Same inputs, same answer.
  const nodesMemo = useRef<{ deps: unknown[]; value: Map<string, SceneNode> } | null>(null);
  const nodes = useMemo(() => {
    const deps = [snapshot, layoutKey, clusterList, unfolded, clusterEdges];
    if (nodesMemo.current && nodesMemo.current.deps.every((d, i) => d === deps[i])) return nodesMemo.current.value;
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
    // A cluster that has just unfolded (drawn before, gone now): when it is
    // the only one, its new members grow out of where it stood.
    const clusterIds = new Set(clusterList.map((c) => c.id));
    const opened = [...store.keys()].filter((id) => isClusterID(id) && !clusterIds.has(id));
    const openedFrom = opened.length === 1 ? store.get(opened[0]!) : undefined;
    const openedRadius = openedFrom ? layoutRadius(openedFrom) : 0;
    const newMembers: string[] = [];
    for (const n of snapshot.nodes) {
      const weight = Math.sqrt((n.in_degree + n.out_degree) / max);
      const kept = store.get(n.id);
      if (kept) {
        kept.weight = weight;
        next.set(n.id, kept);
        continue;
      }
      const anchor =
        openedFrom ?? (placed ? (adjacent.get(n.id) ?? []).map((id) => store.get(id)).find(Boolean) : undefined);
      if (openedFrom) newMembers.push(n.id);
      // An opening cluster's members start on a small sphere around it (so
      // they do not begin inside one another), settling from there.
      const start = openedFrom
        ? (() => {
            // On a small sphere inside the glyph, to expand from outward only.
            const s = seedPosition(n.id, Math.min(snapshot.nodes.length, 400), 3);
            const reach = Math.max(1, Math.hypot(s.x, s.y, s.z));
            const k = (openedRadius * 0.9) / reach;
            return { x: (openedFrom.x ?? 0) + s.x * k, y: (openedFrom.y ?? 0) + s.y * k, z: (openedFrom.z ?? 0) + s.z * k };
          })()
        : anchor
          ? beside(anchor, n.id)
          : seedPosition(n.id, snapshot.nodes.length, 3);
      next.set(n.id, { id: n.id, weight, ...start });
    }
    if (openedFrom && newMembers.length > 0) membersOf.current.set(opened[0]!, newMembers);
    // What the next layout moves alone: an opening cluster's members, or a
    // closing one's cluster (the cluster super-nodes decision); anything else
    // lays the whole graph out as before.
    local.current = openedFrom && newMembers.length > 0 ? new Set(newMembers) : null;
    const maxSize = Math.max(1, ...clusterList.map((c) => c.size));
    const total = snapshot.nodes.length + clusterList.length;
    for (const c of clusterList) {
      const weight = clusterWeight(c.size, maxSize);
      const kept = store.get(c.id);
      if (kept) {
        kept.weight = weight;
        // A remembered layout keeps positions only: the flag that makes it a glyph is set again here.
        kept.cluster = true;
        next.set(c.id, kept);
        continue;
      }
      // A cluster folded back comes back at the centre of the members it had.
      const members = (membersOf.current.get(c.id) ?? []).map((id) => store.get(id)).filter(Boolean) as SceneNode[];
      const at = members.length
        ? {
            x: members.reduce((sum, m) => sum + (m.x ?? 0), 0) / members.length,
            y: members.reduce((sum, m) => sum + (m.y ?? 0), 0) / members.length,
            z: members.reduce((sum, m) => sum + (m.z ?? 0), 0) / members.length,
          }
        : seedPosition(c.id, total, 3);
      next.set(c.id, { id: c.id, weight, cluster: true, ...at });
      if (members.length) {
        (local.current ??= new Set()).add(c.id);
      }
    }
    for (const u of unfolded) {
      const ids = unfoldedMemberIds(u, snapshot);
      if (ids.length) membersOf.current.set(u.id, ids);
    }
    // The concepts the overview leaves open, with no link to a glyph, are tied to
    // the glyph of their Map for the layout.
    const anchors = anchorGlyphs(
      snapshot.nodes,
      clusterList,
      clusterEdges,
      new Set(unfolded.flatMap((u) => unfoldedMemberIds(u, snapshot))),
      snapshot.edges,
    );
    for (const n of snapshot.nodes) {
      const node = next.get(n.id);
      if (node) node.anchor = anchors.get(n.id);
    }
    nodeStore.current.nodes = next;
    nodesMemo.current = { deps, value: next };
    return next;
  }, [snapshot, layoutKey, clusterList, unfolded, clusterEdges]);

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
      .then(
        ({ LivingScene }) => {
          if (disposed) return;
          const scene = new LivingScene(
            container,
            {
              onSelect: (id) => {
                // A click ends the hover: the tooltip of a node that is about to
                // open, close or move must not stay where the cursor was.
                if (tooltipRef.current) tooltipRef.current.hidden = true;
                if (growingRef.current) return;
                if (id?.startsWith(ARTIFACT_PREFIX)) {
                  const ref = id.slice(ARTIFACT_PREFIX.length);
                  const slash = ref.indexOf("/");
                  onOpenArtifactRef.current?.(ref.slice(0, slash), ref.slice(slash + 1));
                  return;
                }
                // A cluster is not a concept: a click unfolds it.
                if (id && isClusterID(id)) {
                  clusterViewRef.current?.onUnfold(id);
                  return;
                }
                // A click on the background clears the selection only: an open
                // cluster has no drawn outline to click outside of, it folds by
                // Escape, the breadcrumb or the legend's Compact.
                onSelectRef.current(id);
              },
              onExpand: (id) => {
                if (growingRef.current) return;
                if (!id.startsWith(ARTIFACT_PREFIX) && !isClusterID(id)) onExpandRef.current?.(id);
              },
              onHover: (id, x, y) => {
                const tip = tooltipRef.current;
                if (!tip) return;
                tip.hidden = !id;
                if (id) {
                  tip.textContent =
                    artifactLabels.current.get(id) ??
                    clusterLabels.current.get(id) ??
                    nameOf(byIdRef.current.get(id) ?? { id });
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
          const memory = recallLayout(layoutKeyRef.current);
          restoreNext.current = memory;
          setReady((n) => n + 1);
        },
        (err: unknown) => {
          console.warn("Atlas: the 3D view chunk could not be loaded", err);
          if (!disposed) setStale(true);
        },
      )
      .catch((err) => {
        console.warn("Atlas: the 3D view could not start", err);
        if (!disposed) {
          setFailed(true);
          onUnavailableRef.current?.();
        }
      });
    return () => {
      disposed = true;
      const leaving = sceneRef.current;
      if (leaving) {
        // The Atlas is being left (another panel, or a scene rebuilt): keep
        // the settled layout and the camera for the way back. The scene itself
        // is disposed -- a hidden WebGL context and frame loop is not worth a
        // faster return.
        // With a concept open, the camera kept is the overview it left, not
        // its close-up beside the inspector: the way back may have nothing
        // selected, and a selection is framed anew when it opens.
        const overview = savedPose.current;
        rememberLayout(nodeStore.current.key, {
          nodes: snapshotPositions(nodeStore.current.nodes),
          artifacts: snapshotPositions(nodeStore.current.artifacts),
          pose: overview ?? leaving.pose(),
          shift: overview ? 0 : leaving.shiftPx(),
        });
      }
      leaving?.dispose();
      sceneRef.current = null;
      lastPushed.current = "";
    };
    // Built once: a new snapshot -- a refresh, or another KB or Map -- goes
    // through setData below and keeps the scene and the camera, so a switch
    // is an animation of the graph, never a blank canvas.
  }, [reducedMotion]);

  // The visible set.
  const lastVisible = useRef(new Set<string>());
  const lastLayout = useRef("");
  const lastPushed = useRef("");
  useEffect(() => {
    const scene = sceneRef.current;
    if (!scene || !ready) return;
    const replace = lastLayout.current !== "" && lastLayout.current !== layoutKey;
    // A Map of the same KB sharing concepts with what is drawn morphs; any
    // other new layout (another KB, or nothing in common) opens from its centre.
    const morph =
      replace &&
      sameKB(lastLayout.current, layoutKey) &&
      snapshot.nodes.some((n) => !hiddenIds.has(n.id) && lastVisible.current.has(n.id));
    const restore = restoreNext.current;
    restoreNext.current = null;
    lastLayout.current = layoutKey;
    const visible = snapshot.nodes.filter((n) => !hiddenIds.has(n.id)).map((n) => nodes.get(n.id)!);
    const ids = new Set(visible.map((n) => n.id));
    if (growth) {
      // A concept born in the replay sprouts beside one already out, not
      // where the full layout had it, and the knock spreads it from there
      // (seedGrowth). A replay of another layout, or a refresh that dropped a
      // concept already out, must not seed against ids this snapshot lacks.
      const out = new Set([...lastVisible.current].filter((id) => nodes.has(id)));
      seedGrowth(
        visible.filter((n) => !out.has(n.id)),
        out,
        snapshot.edges,
        nodes,
      );
    }
    lastVisible.current = ids;
    const closedReplay = !growth && wasGrowing.current;
    wasGrowing.current = growth !== null;
    for (const c of clusterList) {
      visible.push(nodes.get(c.id)!);
      ids.add(c.id);
    }
    const degreeOf = new Map(snapshot.nodes.map((n) => [n.id, n.in_degree + n.out_degree]));
    const links: SceneLink[] = budgetEdges(
      snapshot.edges.filter((e) => ids.has(e.source) && ids.has(e.target)),
      (id) => degreeOf.get(id) ?? 0,
      snapshot.nodes.length,
    ).map((e) => ({ source: e.source, target: e.target }));
    for (const e of clusterEdges) {
      if (!ids.has(e.source) || !ids.has(e.target)) continue;
      for (let k = 0; k < edgeStrokes(e.weight); k++) links.push({ source: e.source, target: e.target });
    }
    for (const a of drawnArtifacts) {
      visible.push(a.node);
      for (const t of a.targets) links.push({ source: a.node.id, target: t });
    }
    // Warm-up runs before the first frame and blocks it, so it is kept short:
    // the burst opens the graph while the simulation, still warm, finishes
    // settling it in view. Without the burst (reduced motion) it opens
    // nearly settled instead.
    const budget = reducedMotion ? 600_000 : 300_000;
    // A few heavy bodies (a folded KB) need more ticks to spread than thousands of light nodes.
    const warmupCap = reducedMotion || visible.length < 120 ? 300 : 150;
    const warmup = Math.round(Math.max(60, Math.min(warmupCap, budget / Math.max(1, visible.length))));
    // The other clusters move too: an opening cluster takes their room.
    const settle = local.current && new Set([...local.current, ...clusterList.map((c) => c.id)]);
    local.current = null;
    // The scene is pushed what is new, once: the same picture again (a filter or a
    // KB switch that re-ran this effect while the data still belonged to the old
    // KB, say) would only knock it. A new layout, a morph, a restore, a local move
    // or a replay always goes through.
    const signature = pictureSignature(layoutKey, visible, links, unfolded.map((u) => u.id));
    if (signature === lastPushed.current && !replace && !morph && !restore && !settle && !growth && !closedReplay) {
      return;
    }
    lastPushed.current = signature;
    scene.setData(visible, links, warmup, replace, {
      morph,
      local: settle && !replace ? settle : undefined,
      hulls: unfolded.map((u) => ({ id: u.id, members: unfoldedMemberIds(u, snapshot) })),
      restore: restore ? { pose: restore.pose, shift: restore.shift } : undefined,
    });
    // The camera keeps the whole graph in frame, centred, in every state: the scene eases
    // it once, to the fit of the picture as the transition leaves it.
    // The replay closed: the whole graph is back, and the camera, which
    // framed only what was out, goes back to the fit of all of it.
    if (closedReplay && !replace) scene.followFit(0, 0, GROWTH_SETTLE_MS);
    // A new layout opens out of its centre and the camera follows it as it
    // settles (scene.setData): no timed refit, which would jump.
    // Two frames: the first renders the scene, the second runs after it is painted.
    let frame = requestAnimationFrame(() => {
      frame = requestAnimationFrame(() => setDrawn(true));
    });
    return () => cancelAnimationFrame(frame);
  }, [ready, snapshot, hiddenIds, nodes, drawnArtifacts, clusterList, clusterEdges, unfolded]);

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
    // Only a drawn concept makes the rest recede: an index page is no node,
    // and fading every node around nothing would turn the graph grey.
    const focal = shown && scene.nodeById(shown) ? shown : null;
    const near = new Set<string>();
    if (focal) {
      near.add(focal);
      for (const n of scene.neighboursOf(focal)) near.add(n.id);
    }
    const drawn = snapshot.nodes.filter((n) => !hiddenIds.has(n.id));
    // An open cluster's members, like the concepts the overview leaves open,
    // are coloured as concepts are in the flat view, by Map.
    const openMembers = new Set<string>();
    for (const u of unfolded) for (const id of unfoldedMemberIds(u, snapshot)) openMembers.add(id);
    const colours = drawn.map((n) => {
      const byMap = slots[collectionHue(n.collection ?? "")]!;
      const community = colorBy === "community" ? communitySlot(communities, n.id) : 0;
      const base = !clusterView
        ? slots[colorBy === "community" ? communitySlot(communities, n.id) : collectionHue(n.collection ?? "")]!
        : colorBy === "community" && community > 0 && !openMembers.has(n.id)
          ? slots[community]!
          : byMap;
      return !focal || near.has(n.id) ? base : fade(base, RECEDED, canvas);
    });
    // A cluster wears its community's colour, as its members do in the flat view.
    for (const c of clusterList) {
      const base = slots[clusterSlot(c)]!;
      colours.push(!focal || near.has(c.id) ? base : fade(base, RECEDED, canvas));
    }
    // Diamonds wear the signal colour: an artifact is not in any community
    // or Map, and a colour of its own says so.
    const artifactColour = cssVar("--graph-signal");
    for (const a of drawnArtifacts) {
      colours.push(!focal || near.has(a.node.id) ? artifactColour : fade(artifactColour, RECEDED, canvas));
    }
    scene.setColours(colours, {
      background: canvas,
      edge: cssVar("--graph-edge-3d"),
      edgeActive: cssVar("--graph-edge-3d-active"),
      signal: cssVar("--graph-signal"),
      ring: cssVar("--graph-ring"),
    });
  }, [ready, snapshot, hiddenIds, colorBy, communities, themeKey, shown, drawnArtifacts, clusterList, unfolded, clusterView]);

  // Zooming in on a cluster unfolds it: the one nearest the middle of the
  // canvas, once it looks large enough. Only a zoom the reader made counts
  // (the wheel or the zoom-in button), and never under reduced motion, where a
  // click is the only way.
  const zoomedIn = useRef(0);
  useEffect(() => {
    const scene = sceneRef.current;
    const el = containerRef.current;
    if (!scene || !el || clusterList.length === 0 || reducedMotion) return;
    const onWheel = (event: WheelEvent) => {
      if (event.deltaY < 0) zoomedIn.current = performance.now();
    };
    el.addEventListener("wheel", onWheel, { passive: true });
    const off = scene.onFrame(() => {
      if (performance.now() - zoomedIn.current > UNFOLD_ZOOM_WINDOW_MS) return;
      const eye = scene.pose().position;
      const w = el.clientWidth;
      const h = el.clientHeight;
      let best: { id: string; off: number } | null = null;
      for (const c of clusterList) {
        const node = scene.nodeById(c.id);
        const p = node && scene.project(node);
        if (!node || !p) continue;
        const dist = Math.hypot((node.x ?? 0) - eye.x, (node.y ?? 0) - eye.y, (node.z ?? 0) - eye.z);
        const centre = Math.hypot(p.x / w - 0.5, p.y / h - 0.5);
        if (dist > 0 && scene.radiusOf(node) / dist >= UNFOLD_APPARENT_SIZE && centre < 0.2) {
          if (!best || centre < best.off) best = { id: c.id, off: centre };
        }
      }
      if (best) {
        zoomedIn.current = 0;
        clusterViewRef.current?.onUnfold(best.id);
      }
    });
    return () => {
      el.removeEventListener("wheel", onWheel);
      off();
    };
  }, [ready, clusterList, reducedMotion]);

  // What folds an open cluster from the keyboard: Escape, one at a time, the last opened first.
  useEffect(() => {
    if (unfolded.length === 0) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, [role='dialog'], [role='menu'], [role='listbox']")) return;
      event.preventDefault();
      clusterViewRef.current?.onFold(unfolded[unfolded.length - 1]!.id);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [unfolded]);

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
    const node = shown ? scene.nodeById(shown) : undefined;
    // A concept the graph does not draw (an index page) selects nothing: a
    // selection makes everything else recede, and with no node to stand out
    // the whole graph would go grey.
    scene.setSelection(node ? shown : null);
    if (!node) {
      if (focusedId.current !== null) scene.unfocus(savedPose.current);
      focusedId.current = null;
      savedPose.current = null;
      // An open concept the graph does not draw (an index page) still has
      // its panel open: the graph moves beside it rather than under it.
      if (shown) scene.centreIn(occludedRef.current);
      return;
    }
    if (focusedId.current !== node.id) {
      focusedId.current = node.id;
      focusedOcclusion.current = occludedRef.current;
      scene.focus(node.id, occludedRef.current, () => {
        if (!savedPose.current) savedPose.current = scene.pose();
      });

      const plain = scene
        .linksOf(node.id)
        .map((link) => ({ source: endpoint(link.source), target: endpoint(link.target), link }));
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
      if (scene && !node) scene.centreIn({ left: occludedLeft, right: occludedRight });
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
    const off = scene.onFrame(place);
    return () => {
      off();
      el.remove();
    };
  }, [ready, highlighted, byId]);

  if (stale) {
    return (
      <div className="state">
        <p className="state__title">The graph could not be loaded</p>
        <p className="state__detail">
          This page is older than the server&apos;s Atlas. Reload the page to get the current one.
        </p>
      </div>
    );
  }
  if (failed) {
    return (
      <div className="state">
        <p className="state__title">The 3D view needs WebGL</p>
        <p className="state__detail">
          This browser could not start it. Every concept is still in the list and the search.
        </p>
      </div>
    );
  }
  const scene = () => sceneRef.current;
  // The replay animates concepts as they are born: in the cluster view most
  // of them are folded, so it is offered only on the Nodes level.
  const growthControl = loadBirths
    ? {
        start: startGrowth,
        active: growth !== null,
        disabled: clusterView ? "The replay draws every concept: switch to Nodes to watch it" : undefined,
      }
    : null;
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
      {clusterView && unfolded.length > 0 && (
        // Where the reader is, as quiet text: the KB (a link back to the
        // overview) then the cluster opened last.
        <nav className="graph__crumbs" aria-label="Open cluster">
          <button
            type="button"
            className="graph__crumb-root"
            onClick={() => unfolded.forEach((u) => clusterView.onFold(u.id))}
          >
            {kbName}
          </button>
          <span className="graph__crumb-sep" aria-hidden="true">
            &rsaquo;
          </span>
          <span className="graph__crumb-current" aria-current="location">
            {unfolded[unfolded.length - 1]!.label} &middot; {unfolded[unfolded.length - 1]!.total.toLocaleString("en")}
          </span>
        </nav>
      )}
      {clusterView && (
        <>
          {/* Folded clusters, for the keyboard and a screen reader: the canvas
              is not in the tree. Visible only while one has focus. */}
          <ul className="graph__clusters-a11y" aria-label="Clusters">
            {clusterList.map((c) => (
              <li key={c.id}>
                <button type="button" onClick={() => clusterView.onUnfold(c.id)}>
                  {`cluster ${c.label}, ${plural(c.size, "concept")}`}
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
      {/* Kept through the fade-out; visibility: hidden then drops it from the tree. */}
      <div className="graph__loading" data-drawn={drawn}>
        <GraphSeed label={`Charting ${snapshot.nodes.length} concepts`} />
      </div>
      {cameraControls && (
        <div className="graph__controls" role="group" aria-label="Graph camera">
          <button
            type="button"
            className="button button--icon"
            onClick={() => {
              zoomedIn.current = performance.now();
              scene()?.zoomBy(1 / 1.35);
            }}
            aria-label="Zoom in"
          >
            <Icon name="plus" size={16} />
          </button>
          <button
            type="button"
            className="button button--icon"
            onClick={() => scene()?.zoomBy(1.35)}
            aria-label="Zoom out"
          >
            <Icon name="minus" size={16} />
          </button>
          <button
            type="button"
            className="button button--icon"
            onClick={() => scene()?.frameAll()}
            aria-label="Fit graph to view"
          >
            <Icon name="fit" size={16} />
          </button>
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
      )}
      {/* The replay starts from the legend card (GrowthButton), which reaches it
          through this context; the camera itself is driven on the canvas. */}
      <GrowthControl.Provider value={growthControl}>{children}</GrowthControl.Provider>
      {exp.dialog && (
        <ExportDialog webm={exp.webm} onExport={(aspect) => void exp.start(aspect)} onClose={exp.closeDialog} />
      )}
      {exp.status && (
        <div
          className="growth-export"
          role="status"
          data-done={exp.status.done || undefined}
          style={{ "--progress": `${exp.status.progress * 100}%` } as CSSProperties}
        >
          <span className="growth-export__label">
            {exp.status.done ? `Video saved (${exp.status.aspect})` : `Exporting video (${exp.status.aspect})`}
          </span>
          {!exp.status.done && (
            <>
              <span className="growth-export__pct">{Math.floor(exp.status.progress * 100)}%</span>
              <button type="button" className="button button--icon" onClick={exp.cancel} aria-label="Cancel the export">
                <Icon name="close" size={14} />
              </button>
            </>
          )}
        </div>
      )}
      {exp.error && (
        <div className="graph__banner banner" role="alert">
          <span className="banner__glyph">
            <Icon name="info" size={16} />
          </span>
          <span>{exp.error}</span>
          <button type="button" className="button button--icon" onClick={exp.dismissError} aria-label="Dismiss">
            <Icon name="close" size={16} />
          </button>
        </div>
      )}
      {growth && (
        <GrowthTimeline
          order={growth.order}
          shown={growth.shown}
          playing={growth.playing}
          onSeek={seekGrowth}
          onTogglePlay={togglePlay}
          onClose={() => setGrowth(null)}
          onExport={exp.supported ? exp.openDialog : undefined}
        />
      )}
      {clusterView ? null : snapshot.truncated && (
        <div className="graph__banner banner" role="status">
          <span className="banner__glyph">
            <Icon name="info" size={16} />
          </span>
          <span>
            Showing {snapshot.nodes.length} of {snapshot.total_nodes} concepts. This graph is truncated &mdash; narrow
            it by Map, type or status to see a complete picture.
          </span>
        </div>
      )}
    </div>
  );
}

/** How an overlay drawn over the canvas starts the growth replay; null where
 *  the view offers none (no births to replay). */
const GrowthControl = createContext<{ start(): void; active: boolean; disabled?: string } | null>(null);

/** The replay's trigger, for an overlay to place: nothing where the view has
 *  no replay to offer. */
export function GrowthButton() {
  const control = useContext(GrowthControl);
  if (!control) return null;
  return (
    <button
      type="button"
      className="legend__grow"
      onClick={control.start}
      aria-label="Replay growth"
      aria-pressed={control.active}
      disabled={control.disabled !== undefined}
      title={control.disabled ?? "Watch the KB grow from its first concept"}
    >
      <Icon name="grow" size={14} />
      <span aria-hidden="true">Replay</span>
    </button>
  );
}

/** The choice made before exporting: the frame, and what this browser will
 *  encode. */
function ExportDialog({ webm, onExport, onClose }: { webm: boolean; onExport(aspect: Aspect): void; onClose(): void }) {
  const [aspect, setAspect] = useState<Aspect>("9:16");
  const panelRef = useRef<HTMLDivElement | null>(null);
  const onKeyDown = useModal(panelRef, onClose);
  return (
    <div className="sheet" onKeyDown={onKeyDown}>
      <div className="sheet__scrim" aria-hidden="true" onClick={onClose} />
      <div ref={panelRef} className="export-dialog" role="dialog" aria-modal="true" aria-label="Export video">
        <h2 className="export-dialog__title">Export video</h2>
        <fieldset className="export-dialog__aspects">
          <legend>Frame</legend>
          <div className="export-dialog__tiles">
            {(Object.keys(ASPECTS) as Aspect[]).map((a) => {
              const { width, height } = ASPECTS[a];
              const long = Math.max(width, height);
              return (
                <label key={a} className="export-dialog__aspect" data-checked={aspect === a || undefined}>
                  <input
                    className="sr-only"
                    type="radio"
                    name="export-aspect"
                    checked={aspect === a}
                    onChange={() => setAspect(a)}
                  />
                  {/* The frame to scale, its longer side 28px. */}
                  <span
                    className="export-dialog__frame"
                    aria-hidden="true"
                    style={{ width: (28 * width) / long, height: (28 * height) / long }}
                  />
                  <span className="export-dialog__ratio">{a}</span>
                  <span className="export-dialog__size">
                    {width}&times;{height}
                  </span>
                </label>
              );
            })}
          </div>
        </fieldset>
        {webm && <p className="export-dialog__note">This browser encodes WebM; Chrome, Edge or Safari encode MP4.</p>}
        <div className="export-dialog__actions">
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
          <button type="button" className="button button--primary" onClick={() => onExport(aspect)}>
            Export
          </button>
        </div>
      </div>
    </div>
  );
}

/** Names `named` on the label layer, following them every frame; returns the
 *  cleanup. The selected node's name is set in the editorial voice. */
function placeLabels(
  scene: LivingScene,
  layer: HTMLElement,
  named: SceneNode[],
  selected: SceneNode | null,
  name: (id: string) => string,
  extra = "",
) {
  const els = named.map((n) => {
    const el = document.createElement("span");
    el.className = `${n === selected ? "graph3d__label graph3d__label--selected" : "graph3d__label"} ${extra}`.trim();
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
  const off = scene.onFrame(place);
  return () => {
    off();
    els.forEach((el) => el.remove());
  };
}

/** What the scene would be drawing: the layout, the drawn bodies (with their size) and links, the open clusters. */
function pictureSignature(
  layoutKey: string,
  nodes: readonly { id: string; weight: number; cluster?: boolean; artifact?: boolean }[],
  links: readonly { source: unknown; target: unknown }[],
  open: readonly string[],
): string {
  let h = 2166136261;
  const mix = (text: string) => {
    for (let i = 0; i < text.length; i++) h = Math.imul(h ^ text.charCodeAt(i), 16777619);
  };
  for (const n of nodes) mix(`${n.id}${n.weight.toFixed(2)}${n.cluster ? "c" : ""}${n.artifact ? "a" : ""}`);
  for (const l of links) mix(`${endpointId(l.source)}>${endpointId(l.target)}`);
  return `${layoutKey}|${nodes.length}|${links.length}|${open.join(",")}|${h >>> 0}`;
}

const endpointId = (end: unknown): string => (typeof end === "object" && end !== null ? (end as { id: string }).id : String(end));
