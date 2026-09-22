import { useEffect, useMemo, useRef, useState } from "react";
import type { GraphSnapshot } from "../api/types";
import { communitySlot, type Communities } from "../lib/communities";
import { focusPose, FOCUS_MS, framePose, zoomLimits, type Pose, type Vec3 } from "../lib/graph3d/camera";
import {
  AUTO_ROTATE_SPEED,
  IdleRotation,
  particleSpeed,
  planBursts,
} from "../lib/graph3d/motion";
import {
  LIVE_ALPHA,
  VELOCITY_DECAY,
  boundingRadius,
  configureForces,
  drift,
  sanitize,
  seedPosition,
  type DriftForce,
  type PhysicsNode,
} from "../lib/graph3d/physics";
import { nodeSize } from "../lib/layout";
import { fade } from "../lib/encoding";
import { collectionHue, cssVar, resolveSlots, type ColorBy } from "../lib/palette";
import { prefersReducedMotion } from "../lib/theme";

/** The 3D view draws every visible concept up to this many (D234). Above it
 *  the view says so and points at filters and the 2D atlas -- never a silent
 *  sample of the graph. */
export const MAX_3D_NODES = 10_000;
/** Above this many nodes spheres are drawn with fewer segments. */
const LOW_DETAIL_NODES = 2_000;
/** A selected node and its neighbours carry a name, up to this many. */
const LABEL_LIMIT = 24;

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
  /** Pixels on the right covered by the floating inspector. */
  occludedRight: number;
  onSelect(id: string | null): void;
  /** WebGL is missing or was lost: the caller falls back to the 2D atlas. */
  onUnavailable?(): void;
}

interface Node3D extends PhysicsNode {
  [key: string]: unknown;
  val: number;
  collection?: string;
  __threeObj?: import("three").Object3D;
}

interface Link3D {
  [key: string]: unknown;
  source: string | Node3D;
  target: string | Node3D;
}

type ForceGraph = import("3d-force-graph").ForceGraph3DInstance<Node3D, Link3D>;
type Orbit = import("three/examples/jsm/controls/OrbitControls.js").OrbitControls;
type CSS2DObjectCtor = typeof import("three/examples/jsm/renderers/CSS2DRenderer.js").CSS2DObject;

const endpoint = (end: string | Node3D) => (typeof end === "object" ? end.id : end);

/**
 * The 3D atlas: a living, elastic network (D234).
 *
 * Pull a node and its links stretch, its neighbours follow and the motion
 * travels through the rest of its component; let go and it settles. At rest
 * the network breathes and the panorama turns slowly; a selection sends a few
 * signals along the concept's links, in the direction the links point. Pause
 * and reduced motion stop everything autonomous.
 *
 * The split: lib/graph3d/physics owns the forces (and is the only writer of
 * coordinates), lib/graph3d/motion decides what moves on its own and when,
 * lib/graph3d/camera frames a selection. This component wires them to
 * 3d-force-graph and keeps the scene incremental: it is built once per
 * snapshot, and filters, colours, theme and selection update it in place --
 * a toggle never re-runs the layout.
 *
 * three.js and 3d-force-graph are several hundred KiB, so they are a separate
 * chunk loaded on first use, never part of the initial bundle.
 */
export function Graph3D(props: Props) {
  const { snapshot, hiddenIds, selected } = props;
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
  return <Scene {...props} selected={selected} />;
}

function Scene({
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
  const graphRef = useRef<ForceGraph | null>(null);
  const [ready, setReady] = useState(0);
  // graphData() is applied asynchronously (three-forcegraph debounces it, then
  // runs the warm-up and builds the node objects): `applied` moves on the
  // first engine tick after each data change, and everything that needs node
  // objects or settled positions -- framing, labels, focus -- waits for it.
  const [applied, setApplied] = useState(0);
  const dataVersion = useRef(0);
  const [failed, setFailed] = useState(false);

  // Callbacks and flags the long-lived scene reads, through refs: a new
  // callback identity from the parent must never rebuild the scene.
  const onSelectRef = useRef(onSelect);
  onSelectRef.current = onSelect;
  const onUnavailableRef = useRef(onUnavailable);
  onUnavailableRef.current = onUnavailable;
  const occludedRef = useRef(occludedRight);
  occludedRef.current = occludedRight;
  const liveRef = useRef(live);
  liveRef.current = live;

  const reducedMotion = useMemo(() => prefersReducedMotion(), []);
  const nodesById = useRef(new Map<string, Node3D>());
  const neighbours = useRef(new Map<string, Node3D[]>());
  const degree = useRef(new Map<string, number>());
  const driftRef = useRef<DriftForce<Node3D>>(drift<Node3D>());
  const idleRef = useRef(new IdleRotation(live && !reducedMotion));
  const css2d = useRef<CSS2DObjectCtor | null>(null);
  const tween = useRef<number | null>(null);
  const savedPose = useRef<Pose | null>(null);
  const radius = useRef(100);
  /** Set by the first user input: after it, the view never re-frames itself. */
  const touched = useRef(false);
  const framed = useRef(false);

  /** The pose that frames the visible graph, from the current viewpoint.
   *  Orphans float far out in a force layout, so only linked nodes count. */
  function frameAll(graph: ForceGraph): Pose {
    const nodes = graph.graphData().nodes;
    const linked = nodes.filter((n) => (degree.current.get(n.id) ?? 0) > 0);
    const body = linked.length ? linked : nodes;
    radius.current = boundingRadius(body);
    const centroid = body.reduce(
      (c, n) => ({ x: c.x + (n.x ?? 0) / body.length, y: c.y + (n.y ?? 0) / body.length, z: c.z + (n.z ?? 0) / body.length }),
      { x: 0, y: 0, z: 0 },
    );
    const camera = graph.camera() as import("three").PerspectiveCamera;
    const controls = graph.controls() as Orbit;
    const limits = zoomLimits(radius.current);
    controls.minDistance = limits.min;
    controls.maxDistance = limits.max;
    const size = graph.renderer().domElement.getBoundingClientRect();
    return framePose(centroid, radius.current, camera.position, controls.target, {
      width: size.width,
      height: size.height,
      fov: camera.fov,
      occludedRight: 0,
    });
  }

  // Build the scene once per snapshot.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let disposed = false;
    const cleanups: (() => void)[] = [];

    // Node objects outlive filter changes, so a node keeps its place when it
    // is hidden and shown again.
    const byId = new Map<string, Node3D>();
    for (const n of snapshot.nodes) {
      byId.set(n.id, {
        id: n.id,
        collection: n.collection,
        val: nodeSize(n.in_degree + n.out_degree) / 2,
        ...seedPosition(n.id, snapshot.nodes.length),
      });
    }
    nodesById.current = byId;
    const near = new Map<string, Node3D[]>();
    const deg = new Map<string, number>();
    for (const e of snapshot.edges) {
      const a = byId.get(e.source);
      const b = byId.get(e.target);
      if (!a || !b) continue;
      // A pair linked both ways is one neighbour, not two.
      const listA = near.get(a.id) ?? near.set(a.id, []).get(a.id)!;
      const listB = near.get(b.id) ?? near.set(b.id, []).get(b.id)!;
      if (!listA.includes(b)) listA.push(b);
      if (!listB.includes(a)) listB.push(a);
      deg.set(a.id, (deg.get(a.id) ?? 0) + 1);
      deg.set(b.id, (deg.get(b.id) ?? 0) + 1);
    }
    neighbours.current = near;
    degree.current = deg;

    void Promise.all([
      import("3d-force-graph"),
      import("three/examples/jsm/renderers/CSS2DRenderer.js"),
    ])
      .then(([{ default: ForceGraph3D }, { CSS2DRenderer, CSS2DObject }]) => {
        if (disposed) return;
        css2d.current = CSS2DObject;
        const labels = new CSS2DRenderer();
        // The package's constructor type is not generic; the instance type is.
        const Create = ForceGraph3D as unknown as new (
          el: HTMLElement,
          cfg?: { controlType?: string; extraRenderers?: unknown[] },
        ) => ForceGraph;
        const graph = new Create(container, { controlType: "orbit", extraRenderers: [labels] });
        const isLive = () => liveRef.current && !reducedMotion;

        graph
          .showNavInfo(false)
          .nodeRelSize(4)
          .nodeVal((n) => n.val)
          .nodeOpacity(0.95)
          .nodeResolution(snapshot.nodes.length > LOW_DETAIL_NODES ? 8 : 20)
          .nodeLabel((n) => labelHTML(n.id))
          .linkOpacity(0.6)
          .linkDirectionalParticles(0)
          .linkDirectionalParticleWidth(2.4)
          .linkDirectionalParticleSpeed(particleSpeed())
          .d3VelocityDecay(VELOCITY_DECAY)
          // Warm-up runs before the first frame: long enough that the graph
          // opens nearly settled, short enough on a large KB not to block.
          .warmupTicks(Math.round(Math.max(80, Math.min(260, 520_000 / Math.max(1, snapshot.nodes.length)))))
          .onNodeClick((n) => onSelectRef.current(n.id))
          .onBackgroundClick(() => onSelectRef.current(null));
        configureForces(
          (name, ...rest: unknown[]) =>
            rest.length ? graph.d3Force(name, rest[0] as never) : graph.d3Force(name),
          driftRef.current,
        );

        // Drag: 3d-force-graph pins the node on the camera plane and reheats
        // the simulation; on release it drops the alpha target to 0, after
        // our callback -- so live mode's floor is restored on the next turn.
        let dragging = false;
        graph
          .onNodeDrag(() => {
            dragging = true;
            idleRef.current.input();
          })
          .onNodeDragEnd(() => {
            dragging = false;
            window.setTimeout(() => applyMotion(graph, isLive()), 0);
          });

        // Every 30 ticks, repair a node a degenerate step left non-finite:
        // one NaN spreads to its whole component within a few ticks.
        let ticks = 0;
        let seen = 0;
        graph.onEngineTick(() => {
          if (++ticks % 30 === 0) sanitize(graph.graphData().nodes, neighbours.current);
          if (seen !== dataVersion.current) {
            seen = dataVersion.current;
            setApplied(seen);
          }
        });

        const renderer = graph.renderer();
        renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
        const canvas = renderer.domElement;

        // Gestures. Orbit on the background and drag on a node are distinct
        // (DragControls disables the orbit while it holds a node). A second
        // contact ends a node drag in place -- DragControls knows only
        // pointerup/pointerleave, so a cancelled or captured-away pointer, or
        // a second finger, is turned into the pointerup it would have seen.
        const pointers = new Set<number>();
        const releaseDrag = () => canvas.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
        const onDown = (event: PointerEvent) => {
          touched.current = true;
          pointers.add(event.pointerId);
          idleRef.current.input();
          cancelTween();
          if (pointers.size >= 2 && dragging) releaseDrag();
        };
        const onUp = (event: PointerEvent) => pointers.delete(event.pointerId);
        const onCancel = (event: PointerEvent) => {
          pointers.delete(event.pointerId);
          if (dragging) releaseDrag();
        };
        const onInput = () => {
          touched.current = true;
          idleRef.current.input();
          cancelTween();
        };
        canvas.addEventListener("pointerdown", onDown, true);
        canvas.addEventListener("pointerup", onUp, true);
        canvas.addEventListener("pointercancel", onCancel, true);
        canvas.addEventListener("lostpointercapture", onCancel, true);
        canvas.addEventListener("wheel", onInput, { passive: true, capture: true });
        cleanups.push(() => {
          canvas.removeEventListener("pointerdown", onDown, true);
          canvas.removeEventListener("pointerup", onUp, true);
          canvas.removeEventListener("pointercancel", onCancel, true);
          canvas.removeEventListener("lostpointercapture", onCancel, true);
          canvas.removeEventListener("wheel", onInput, true);
        });

        const controls = graph.controls() as Orbit;
        controls.autoRotateSpeed = AUTO_ROTATE_SPEED;
        // The panorama turns only when nobody is using the view (motion.ts).
        const rotation = window.setInterval(() => {
          controls.autoRotate = idleRef.current.shouldRotate();
        }, 250);
        cleanups.push(() => window.clearInterval(rotation));

        // A hidden tab renders nothing.
        const onVisibility = () => (document.hidden ? graph.pauseAnimation() : graph.resumeAnimation());
        document.addEventListener("visibilitychange", onVisibility);
        cleanups.push(() => document.removeEventListener("visibilitychange", onVisibility));

        const onLost = (event: Event) => {
          event.preventDefault();
          setFailed(true);
          onUnavailableRef.current?.();
        };
        canvas.addEventListener("webglcontextlost", onLost);
        cleanups.push(() => canvas.removeEventListener("webglcontextlost", onLost));

        const resize = () => graph.width(container.clientWidth).height(container.clientHeight);
        resize();
        const observer = new ResizeObserver(resize);
        observer.observe(container);
        cleanups.push(() => observer.disconnect());

        graphRef.current = graph;
        setReady((n) => n + 1);

        // The warm-up leaves the graph still contracting: frame it once more
        // when it has settled, unless the reader has taken the camera.
        const refit = window.setTimeout(() => {
          if (touched.current || savedPose.current || !graphRef.current) return;
          animateTo(graph.camera() as import("three").PerspectiveCamera, controls, frameAll(graph), reducedMotion ? 0 : FOCUS_MS);
        }, 1800);
        cleanups.push(() => window.clearTimeout(refit));
      })
      .catch((err) => {
        console.warn("Atlas: the 3D view could not start", err);
        if (!disposed) {
          setFailed(true);
          onUnavailableRef.current?.();
        }
      });

    function cancelTween() {
      if (tween.current !== null) cancelAnimationFrame(tween.current);
      tween.current = null;
      idleRef.current.setFocusing(false);
    }

    return () => {
      disposed = true;
      cancelTween();
      cleanups.forEach((fn) => fn());
      const graph = graphRef.current;
      if (graph) graph._destructor();
      graphRef.current = null;
      container.replaceChildren();
    };
  }, [snapshot, reducedMotion]);

  // Filters: swap the visible set, keeping every surviving node's object --
  // and so its position and velocity.
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    const nodes = snapshot.nodes.filter((n) => !hiddenIds.has(n.id)).map((n) => nodesById.current.get(n.id)!);
    const ids = new Set(nodes.map((n) => n.id));
    const links = snapshot.edges
      .filter((e) => ids.has(e.source) && ids.has(e.target))
      .map<Link3D>((e) => ({ source: e.source, target: e.target }));
    dataVersion.current++;
    graph.graphData({ nodes, links });
    applyMotion(graph, live && !reducedMotion);
  }, [ready, snapshot, hiddenIds]);

  // The first applied data frames the graph at once, before the selection
  // effect below runs: a deep link to a concept then focuses from a real
  // overview. Later data changes keep the reader's camera.
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph || !applied || framed.current || graph.graphData().nodes.length === 0) return;
    framed.current = true;
    animateTo(graph.camera() as import("three").PerspectiveCamera, graph.controls() as Orbit, frameAll(graph), 0);
  }, [applied]);

  // Colour: by community or Map, the selection's neighbourhood kept and the
  // rest receded. Re-resolved from the tokens on a theme change.
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph) return;
    const slots = resolveSlots();
    const base = (n: Node3D) =>
      slots[colorBy === "community" ? communitySlot(communities, n.id) : collectionHue(n.collection ?? "")]!;
    const near = new Set<string>();
    if (selected) {
      near.add(selected);
      for (const n of neighbours.current.get(selected) ?? []) near.add(n.id);
    }
    const accent = cssVar("--accent");
    const canvas = cssVar("--surface-0");
    // Receding, not greying out: the node keeps a trace of its hue, mixed
    // into the canvas (encoding.fade), so the context stays readable.
    const receded = (n: Node3D) => fade(base(n), 0.28, canvas);
    const edge = cssVar("--graph-edge-3d");
    const edgeActive = cssVar("--graph-edge-active");
    const touches = (l: Link3D) => !!selected && (endpoint(l.source) === selected || endpoint(l.target) === selected);
    graph
      .backgroundColor(cssVar("--surface-0"))
      .nodeColor((n) => (!selected ? base(n) : n.id === selected ? accent : near.has(n.id) ? base(n) : receded(n)))
      .linkColor((l) => (touches(l) ? edgeActive : edge))
      .linkWidth((l) => (touches(l) ? 0.8 : 0))
      .linkDirectionalParticleColor(() => cssVar("--clay"));
  }, [ready, colorBy, communities, themeKey, selected]);

  // Motion on or off.
  useEffect(() => {
    const graph = graphRef.current;
    const on = live && !reducedMotion;
    idleRef.current.setLive(on);
    if (graph) applyMotion(graph, on);
  }, [ready, live, reducedMotion]);

  // Selection: focus the camera, name the neighbourhood, send the signals.
  useEffect(() => {
    const graph = graphRef.current;
    if (!graph || !applied) return;
    idleRef.current.setSelected(!!selected);
    const node = selected ? nodesById.current.get(selected) : undefined;
    const camera = graph.camera() as import("three").PerspectiveCamera;
    const controls = graph.controls() as Orbit;

    // Camera. The first focus remembers where the reader was, and closing the
    // selection returns there.
    if (node && Number.isFinite(node.x)) {
      if (!savedPose.current) savedPose.current = currentPose(camera, controls);
      const size = graph.renderer().domElement.getBoundingClientRect();
      const from = { x: camera.position.x, y: camera.position.y, z: camera.position.z };
      // Recomputed every frame: the node is still moving under the physics.
      const target = () =>
        focusPose(node as Vec3, from, neighbourhoodRadius(node, neighbours.current.get(node.id) ?? []), {
          width: size.width,
          height: size.height,
          fov: camera.fov,
          occludedRight: occludedRef.current,
        });
      animateTo(camera, controls, target, reducedMotion ? 0 : FOCUS_MS);
    } else if (!node && savedPose.current) {
      animateTo(camera, controls, savedPose.current, reducedMotion ? 0 : FOCUS_MS);
      savedPose.current = null;
    }

    // Follow: once focused, the camera moves with the node, so the drift, a
    // settling layout or a drag of a neighbour never slides it out of view.
    // The reader can still orbit around it -- the orbit's target is the node.
    let follow: number | null = null;
    if (node) {
      let last = { x: node.x ?? 0, y: node.y ?? 0, z: node.z ?? 0 };
      const track = () => {
        const now = { x: node.x ?? 0, y: node.y ?? 0, z: node.z ?? 0 };
        if (tween.current === null && Number.isFinite(now.x + now.y + now.z)) {
          const d = { x: now.x - last.x, y: now.y - last.y, z: now.z - last.z };
          camera.position.set(camera.position.x + d.x, camera.position.y + d.y, camera.position.z + d.z);
          controls.target.set(controls.target.x + d.x, controls.target.y + d.y, controls.target.z + d.z);
        }
        last = now;
        follow = requestAnimationFrame(track);
      };
      follow = requestAnimationFrame(track);
    }

    // Labels on the selection and its neighbours, facing the camera.
    const attached: import("three").Object3D[] = [];
    const CSS2DObject = css2d.current;
    if (node && CSS2DObject) {
      const named = [node, ...(neighbours.current.get(node.id) ?? [])].slice(0, LABEL_LIMIT);
      for (const n of named) {
        if (!n.__threeObj) continue;
        const el = document.createElement("span");
        el.className = n === node ? "graph3d__label graph3d__label--selected" : "graph3d__label";
        el.textContent = shortId(n.id);
        const label = new CSS2DObject(el);
        label.position.set(0, Math.cbrt(n.val) * 4 + 6, 0);
        n.__threeObj.add(label);
        attached.push(label);
      }
    }

    // Signals, a finite burst: nothing when motion is off.
    const timers: number[] = [];
    const links = graph.graphData().links;
    const plain = links.map((l) => ({ source: endpoint(l.source), target: endpoint(l.target), link: l }));
    for (const signal of planBursts(selected, plain, (id) => degree.current.get(id) ?? 0, live && !reducedMotion)) {
      timers.push(window.setTimeout(() => graph.emitParticle(signal.link.link), signal.delayMs));
    }

    return () => {
      if (follow !== null) cancelAnimationFrame(follow);
      timers.forEach((t) => window.clearTimeout(t));
      for (const label of attached) {
        label.removeFromParent();
        (label as unknown as { element: HTMLElement }).element.remove();
      }
    };
  }, [applied, selected]);

  function animateTo(
    camera: import("three").PerspectiveCamera,
    controls: Orbit,
    destination: Pose | (() => Pose),
    ms: number,
  ) {
    if (tween.current !== null) cancelAnimationFrame(tween.current);
    tween.current = null;
    const pose = typeof destination === "function" ? destination : () => destination;
    if (ms <= 0) {
      const to = pose();
      // Immediate, and synchronous: a selection effect in the same commit
      // must see the pose this sets.
      camera.position.set(to.position.x, to.position.y, to.position.z);
      controls.target.set(to.lookAt.x, to.lookAt.y, to.lookAt.z);
      camera.lookAt(controls.target);
      controls.update();
      idleRef.current.setFocusing(false);
      return;
    }
    const from = currentPose(camera, controls);
    const start = performance.now();
    idleRef.current.setFocusing(true);
    const step = (now: number) => {
      const to = pose();
      const t = Math.min(1, (now - start) / ms);
      const k = t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2; // ease-in-out
      camera.position.set(lerp(from.position.x, to.position.x, k), lerp(from.position.y, to.position.y, k), lerp(from.position.z, to.position.z, k));
      controls.target.set(lerp(from.lookAt.x, to.lookAt.x, k), lerp(from.lookAt.y, to.lookAt.y, k), lerp(from.lookAt.z, to.lookAt.z, k));
      camera.lookAt(controls.target);
      if (t < 1) tween.current = requestAnimationFrame(step);
      else {
        tween.current = null;
        idleRef.current.setFocusing(false);
      }
    };
    tween.current = requestAnimationFrame(step);
  }

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

/** Live: drift on and the simulation kept just warm, forever. Still: drift
 *  off and the simulation left to cool until it stops. A drag reheats it in
 *  both modes -- the reader asked for that motion. */
function applyMotion(graph: ForceGraph, live: boolean) {
  const force = graph.d3Force("drift") as DriftForce<Node3D> | undefined;
  force?.enabled(live);
  graph.d3AlphaMin(live ? 0 : 0.001).cooldownTime(live ? Infinity : 15_000);
  const inner = engine(graph);
  inner?.d3AlphaTarget(live ? LIVE_ALPHA : 0);
  inner?.resetCountdown();
}

interface Engine {
  d3AlphaTarget(value: number): Engine;
  resetCountdown(): Engine;
}

/**
 * engine is the three-forcegraph object inside the scene. 3d-force-graph
 * hides d3AlphaTarget and resetCountdown from its own API (it drives them for
 * drags), but the alpha floor of live mode needs both; the inner object is the
 * scene child that has them. If a future version moves it, live mode degrades
 * to "settles and stops" rather than failing -- physics.test pins the rest.
 */
function engine(graph: ForceGraph): Engine | undefined {
  return graph
    .scene()
    .children.find((child) => typeof (child as unknown as Partial<Engine>).d3AlphaTarget === "function") as
    | Engine
    | undefined;
}

/** How far a node's direct neighbours reach from it, at the 90th percentile:
 *  the sphere a focus keeps in view. One far-flung neighbour must not pull the
 *  camera back to the whole graph. */
function neighbourhoodRadius(node: Node3D, near: Node3D[]): number {
  const d = near
    .map((n) => Math.hypot((n.x ?? 0) - (node.x ?? 0), (n.y ?? 0) - (node.y ?? 0), (n.z ?? 0) - (node.z ?? 0)))
    .filter(Number.isFinite)
    .sort((a, b) => a - b);
  if (d.length === 0) return 70;
  return Math.max(70, d[Math.min(d.length - 1, Math.floor(d.length * 0.9))]!);
}

function currentPose(camera: import("three").PerspectiveCamera, controls: Orbit): Pose {
  return {
    position: { x: camera.position.x, y: camera.position.y, z: camera.position.z },
    lookAt: { x: controls.target.x, y: controls.target.y, z: controls.target.z },
  };
}

const lerp = (a: number, b: number, k: number) => a + (b - a) * k;

function shortId(id: string): string {
  const cut = id.lastIndexOf("/");
  return cut === -1 ? id : id.slice(cut + 1);
}

/** Node ids come from the KB: escaped before they reach the tooltip, which
 *  3d-force-graph renders as HTML. */
function labelHTML(id: string): string {
  const esc = id.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
  return `<span class="graph3d__label">${esc}</span>`;
}
