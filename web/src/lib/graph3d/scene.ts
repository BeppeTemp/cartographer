import { forceLink, forceManyBody, forceSimulation, type Simulation } from "d3-force-3d";
import {
  BufferAttribute,
  BufferGeometry,
  Color,
  DynamicDrawUsage,
  Fog,
  InstancedMesh,
  LineBasicMaterial,
  LineSegments,
  Mesh,
  MeshBasicMaterial,
  Object3D,
  PlaneGeometry,
  PerspectiveCamera,
  Points,
  PointsMaterial,
  Scene,
  Sphere,
  SphereGeometry,
  Vector3,
  WebGLRenderer,
} from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import { centroid as centroidOf, configureClusterForces, LEAF_RADIUS, radiusOf as layoutRadius, type LayoutNode, type Point } from "./clusterlayout";
import { CAMERA_FOLD, CAMERA_MOVE, CAMERA_ZOOM, fitPose, focusPose, zoomLimits, type Pose } from "./camera";
import { MOTION_MS, motionMs, type MotionToken } from "../theme";
import { FrameTrace } from "./trace";
import { smooth } from "./transition";
import {
  AUTO_ROTATE_SPEED,
  READING_DRIFT,
  READING_ROTATE_SPEED,
  IdleRotation,
  DRIFT_REST_MS,
  DRIFT_SLOW_EVERY,
  MAX_SIGNALS,
  SIGNAL_PASS_MS,
  sustainsFullRate,
  frameEase,
  REF_FRAME_MS,
  MAX_FRAME_MS,
  type Signal,
} from "./motion";
import {
  LINK_DISTANCE,
  LIVE_ALPHA,
  VELOCITY_DECAY,
  KNOCK,
  KNOCK_ALPHA,
  KNOCK_BULK_ALPHA,
  boundingRadius,
  configureForces,
  drift,
  hashId,
  knockAll,
  sanitize,
  springsOf,
  type SpringLink,
  type DriftForce,
  type PhysicsNode,
} from "./physics";

/**
 * The living 3D atlas's scene (D234): one simulation, one draw call per kind of
 * thing -- every node in one instanced mesh, every link in one line set, the
 * selection's links, its signals and its ring each in one more.
 *
 * The look follows the brand kit's Studio 03: small unlit nodes (a flat colour,
 * no shading -- lit spheres read as toys), hairline links, a wireframe ring on
 * the selection, signals as points running along its links. Depth comes from
 * perspective and a faint fog towards the canvas colour, not from lighting.
 *
 * It is orbited, and its nodes are selected, never grabbed: see bindGestures,
 * D234 and D235.
 */

export interface SceneNode extends PhysicsNode {
  /** Visual weight, 0..1: 0 a leaf, 1 the best-connected node. */
  weight: number;
  /** An artifact (skill, agent, hook), drawn as a diamond, not a sphere. */
  artifact?: boolean;
  /** A folded cluster (the cluster super-nodes decision): a filled disc inside
   *  a soft translucent ring, larger than any concept. */
  cluster?: boolean;
  /** A loose concept with no link to a glyph: the glyph it is anchored to. */
  anchor?: string;
}

/** An open cluster: its id and the drawn members it unfolded into. */
export interface HullSpec {
  id: string;
  members: string[];
}

/** An open cluster as the scene tracks it (nothing of it is drawn: its members
 *  are concepts like any other). A folding one lives for the one setData that
 *  folds it, so its glyph comes back where its members were. */
interface Hull {
  id: string;
  members: SceneNode[];
  /** When it folded; null while open. */
  dying: number | null;
  /** The glyph's radius, the spread its members start in. */
  glyph: number;
  /** Where its members were when it folded. */
  c: { x: number; y: number; z: number };
  /** The transition's duration. */
  ms: number;
  /** Its glyph's place when it opened: where its members start. */
  anchor: { x: number; y: number; z: number };
}

export interface SceneLink {
  source: string | SceneNode;
  target: string | SceneNode;
}

export interface ScenePalette {
  background: string;
  edge: string;
  edgeActive: string;
  signal: string;
  ring: string;
}

/** An offline scene (D691): `width` x `height` CSS pixels drawn at
 *  `pixelRatio`, advanced only by `renderFrame`. */
export interface OffscreenOptions {
  width: number;
  height: number;
  pixelRatio: number;
}

export interface SceneCallbacks {
  onSelect(id: string | null): void;
  /** A double click on a node. */
  onExpand?(id: string): void;
  onHover(id: string | null, x: number, y: number): void;
  onLost(): void;
}

/** How long the camera follows a new graph as it settles, and how much of
 *  the way to its fit it eases each frame. */
const SETTLE_MS = 3500;
/** How far a leaf concept's colour recedes towards the background (a hub: not at all). */
const TONE_DEPTH = 0.4;
const SETTLE_EASE = 0.14;
/** How long the follow's pull takes to rise to SETTLE_EASE: short, so the camera keeps up with a view that changes. */
const SETTLE_RAMP_MS = 400;
/** While the panorama turns with nobody at the controls, the camera eases
 *  this much of the way to the fit every frame: hours of drift and rotation
 *  never carry the network off screen, and the reader never sees it move. */
const IDLE_FIT_EASE = 0.01;
const IDLE_FIT_EVERY = 10;
/** A settling graph's fit is recomputed this often (frames), eased towards on every one. */
const CHASE_EVERY = 4;
/** The share of the way the chase target glides to a fresh fit each 60 Hz frame. */
const CHASE_GLIDE = 0.3;
/** OrbitControls damping share per 60 Hz frame (see frameEase). */
const DAMPING = 0.08;
/** How long a new KB or Map takes to open out of its centre. Longer than the
 *  motion tokens on purpose: it is the graph's entrance, not the answer to a
 *  click, and at --motion-deliberate it read as a jump (live session). */
const BLOOM_MS = 1400;
/** A clustered KB's entrance starts from a layout the warm-up has already settled: it is reheated only a little, and never held warm (see applyMotion). */
const CLUSTER_BLOOM_ALPHA = 0.04;
const CLUSTER_VELOCITY_DECAY = 0.55;
/** The burst's raggedness: the latest a node sets off, and how much slower
 *  than the fastest the slowest flies, both as shares of BLOOM_MS. */
const BLOOM_STAGGER = 0.3;
const BLOOM_SPEED_SPREAD = 0.3;
/** Past this many nodes an entrance opens like a flower: from the centre
 *  outwards, ring after ring, and slower, so the frames it costs stay smooth. */
const FLOWER_NODES = 1500;
/** The flower's stagger: the outermost node sets off this share of the way in. */
const FLOWER_STAGGER = 0.6;
/** An entrance (bloom or morph) lasts longer with the graph's size, up to this many times its base. */
const ENTRANCE_MAX_STRETCH = 2.6;
/** Past this cost of one tick (ms) the simulation runs at most one tick per frame:
 *  it slows down rather than the frames do. */
const COSTLY_TICK_MS = 6;
/** The furthest ahead, in ticks of velocity, the camera aims at a spreading graph. */
const LOOKAHEAD_MAX = 8;
/** Past this many nodes the simulation's repulsion is thinned (see setData). */
const LARGE_SIM_NODES = 1500;
const LARGE_SIM_THETA = 1.2;

/** How much longer an entrance of `n` nodes lasts than a small one's. */
function entranceStretch(n: number): number {
  return Math.min(ENTRANCE_MAX_STRETCH, Math.max(1, Math.sqrt(n / 600)));
}

/** Each node's start delay and flight span (shares of the entrance) when a
 *  large graph opens as a flower: ordered by distance from `centre`, a little
 *  jitter per id so the rings are not drawn with a compass. */
function flowerFlight(nodes: SceneNode[], centre: Point): Map<SceneNode, { delay: number; span: number }> {
  const order = nodes
    .map((n) => ({ n, d: Math.hypot((n.x ?? 0) - centre.x, (n.y ?? 0) - centre.y, (n.z ?? 0) - centre.z) }))
    .sort((a, b) => a.d - b.d);
  const flight = new Map<SceneNode, { delay: number; span: number }>();
  order.forEach(({ n }, i) => {
    const jitter = (hashUnit(n.id) - 0.5) * 0.06;
    const delay = Math.min(FLOWER_STAGGER, Math.max(0, (i / order.length) * FLOWER_STAGGER + jitter));
    flight.set(n, { delay, span: 1 - FLOWER_STAGGER });
  });
  return flight;
}

/**
 * Makes `geometry` hold room for `vertices` positions and draws exactly that
 * many. The attribute is kept (its GL buffer with it) while it is large
 * enough; only growth allocates, and then the old buffer is released at once
 * rather than left to the GC.
 */
export function reuseAttribute(geometry: BufferGeometry, vertices: number): void {
  const attr = geometry.getAttribute("position") as BufferAttribute | undefined;
  if (!attr || attr.array.length < vertices * 3) {
    if (attr) geometry.dispose();
    geometry.setAttribute("position", new BufferAttribute(new Float32Array(vertices * 3), 3).setUsage(DynamicDrawUsage));
  }
  geometry.setDrawRange(0, vertices);
}

/** Out, past the mark, and back: the flight of a node in the burst. The
 *  flight is eased in first, so it is seen leaving the seed rather than
 *  being already there a frame later. */
function easeOutBack(t: number): number {
  const s = 1.1;
  const u = smooth(t) - 1;
  return 1 + (s + 1) * u * u * u + s * u * u;
}

function distanceOf(pose: Pose): number {
  return Math.hypot(pose.position.x - pose.lookAt.x, pose.position.y - pose.lookAt.y, pose.position.z - pose.lookAt.z);
}

/** A stable value in [0, 1) for an id. */
function hashUnit(id: string): number {
  return hashId(id) / 2 ** 32;
}

/** A transition is predicted only on a graph this small, and only for this long. */
const PREDICT_MAX_NODES = 600;
const PREDICT_TICKS = 250;
const PREDICT_BUDGET_MS = 60;

/**
 * Where the nodes of a just-reheated simulation will come to rest: it is run
 * ahead (within a time budget) and put back exactly as it was, so what is drawn
 * still moves by the simulation, frame by frame. Null on a graph too large to
 * run ahead.
 */
function predictSettle(sim: Simulation<SceneNode>, nodes: SceneNode[]): Map<SceneNode, Point> | null {
  if (nodes.length === 0 || nodes.length > PREDICT_MAX_NODES) return null;
  const saved = nodes.map((n) => [n.x, n.y, n.z, n.vx, n.vy, n.vz] as const);
  const alpha = sim.alpha();
  // The drift is set aside while running ahead: its phase is a tick count, which
  // the run would advance, and its breathing is no part of where nodes rest.
  const drift = sim.force("drift");
  sim.force("drift", null);
  const until = performance.now() + PREDICT_BUDGET_MS;
  for (let i = 0; i < PREDICT_TICKS && sim.alpha() > 0.001 && performance.now() < until; i++) sim.tick();
  sim.force("drift", drift as never);
  const place = new Map<SceneNode, Point>(nodes.map((n) => [n, { x: n.x ?? 0, y: n.y ?? 0, z: n.z ?? 0 }]));
  nodes.forEach((n, i) => {
    [n.x, n.y, n.z, n.vx, n.vy, n.vz] = saved[i]!;
  });
  sim.alpha(alpha);
  return place;
}

/** A Map change keeps the nodes both views share where they are: leavers shrink
 *  away and newcomers grow in over MORPH_MS, while the simulation relaxes the
 *  rest from MORPH_ALPHA (gently: nothing is rebuilt from a point). */
const MORPH_MS = 900;
/** The simulation's rate, the one its forces are tuned for (physics.ts). */
const SIM_HZ = 60;
/** At most this many ticks run in one frame. */
const SIM_MAX_CATCHUP = 3;
/** Draws are spaced at least this far apart: about 60 a second whatever the
 *  display's refresh, since a 120-144 Hz screen drew every frame twice over
 *  for a picture that moves at 60. */
const MIN_DRAW_MS = 12;
/** The longest the warm-up may hold the first frame of a new graph. */
const WARMUP_BUDGET_MS = 120;
const MORPH_ALPHA = 0.2;
/** A cluster opening or folding is the simulation's own motion, the same as switching to the flat view: the
 *  members are put inside the glyph, grow in, and the reheated simulation spreads them at its own pace; the
 *  camera follows the whole graph as it settles. */
const OPEN_ALPHA = 0.3;
const FOLD_ALPHA = 0.25;
const TRANSITION_FOLLOW_MS = 1500;
/** A layout come back from memory is already settled: only what is new to it
 *  needs the simulation. */
const RESTORE_ALPHA = 0.04;
/** How warm the simulation starts as it opens, and how warm it is kept
 *  until it is open. */
const BLOOM_ALPHA = 0.3;
const BLOOM_WARMTH = 0.12;
/** Radius of a leaf, in scene units, and of the best-connected node: the
 *  Studio 03 ratio (.053 : .125), one size up from it -- at the prototype's
 *  scale a few hundred real concepts read as dust, and on paper as nothing. */
/** A press that travels less than this is a click, not a drag or an orbit. */
const CLICK_SLOP_PX = 5;
/** Picking reaches this far around a node on screen: small nodes stay easy
 *  to hit (brand kit: 12-18 px hit area). */
const HIT_RADIUS_PX = 14;
const CLUSTER_HIT_RADIUS_PX = 18;

/** Resting links, and the same links behind a selection: they recede so the
 *  selection's own links carry the picture. */
const EDGE_REST = 0.22;
/** An octahedron reaches its radius only at its tips: scaled so it weighs like a sphere. */
const DIAMOND_SCALE = 1.3;
/** The quad of a body spans this many of its radii each way: room for the anti-aliased edge. */
const QUAD = 1.15;
const EDGE_BEHIND_SELECTION = 0.06;
/** A selection made or cleared recedes the rest of the graph, or brings it
 *  back, over this long: with the camera easing at the same time, a colour
 *  snapped at once reads as the whole picture jumping. */
const RECEDE: MotionToken = "--motion-slow";

const endpoint = (end: string | { id: string }) => (typeof end === "object" ? end.id : end);

/** The shapes a billboard is drawn as (the `uShape` uniform of the one program they share). */
const SHAPE = { disc: 0, diamond: 1, hexagon: 2, plain: 3, aura: 4 } as const;
/** A folded cluster stands out from the concepts: a soft hexagonal aura this many
 *  times its glyph, at this opacity, and a second, inner contour on the glyph. */
const AURA_SCALE = 1.55;
const AURA_OPACITY = 0.22;
const INNER_CONTOUR_AT = 0.2;
/** A body's border, in screen pixels, and how much of its colour it keeps (the rest goes dark). */
const BORDER_PX = 0.5;
const BORDER_KEEP = 0.55;

/**
 * A flat shape that always faces the camera, drawn by the fragment shader: each
 * instance keeps its centre and scale, a quad is laid out in view space and the
 * fragment decides what is inside the shape (a disc, a diamond, a regular
 * hexagon), with a thin darker border of a fixed width on screen, whatever the
 * zoom, and an anti-aliased edge. One quad per body and one program for all of
 * them: no inflated second mesh, no extra draw call, and a 2D glyph never turns
 * into a sliver as the panorama turns. The quad spans +-QUAD in local units, the
 * shape's own radius is 1.
 */
function billboard(material: MeshBasicMaterial, shape: number): MeshBasicMaterial {
  material.transparent = true;
  material.customProgramCacheKey = () => "billboard";
  material.onBeforeCompile = (shader) => {
    shader.uniforms.uShape = { value: shape };
    shader.vertexShader = shader.vertexShader
      .replace("#include <common>", "#include <common>\nvarying vec2 vLocal;")
      .replace(
        "#include <project_vertex>",
        `vLocal = position.xy;
      vec4 mvPosition = modelViewMatrix * vec4(instanceMatrix[3].xyz, 1.0);
      mvPosition.xy += position.xy * length(instanceMatrix[0].xyz);
      gl_Position = projectionMatrix * mvPosition;`,
      );
    shader.fragmentShader = shader.fragmentShader
      .replace("#include <common>", "#include <common>\nvarying vec2 vLocal;\nuniform float uShape;")
      .replace(
        "#include <color_fragment>",
        `#include <color_fragment>
      float sd = -1.0;
      if (uShape < 0.5) sd = length(vLocal) - 1.0;
      else if (uShape < 1.5) sd = (abs(vLocal.x) + abs(vLocal.y) - 1.0) * 0.70710678;
      else if (uShape < 2.5 || uShape > 3.5) {
        const vec3 hk = vec3(-0.866025404, 0.5, 0.577350269);
        vec2 hp = abs(vLocal);
        hp -= 2.0 * min(dot(hk.xy, hp), 0.0) * hk.xy;
        hp -= vec2(clamp(hp.x, -hk.z * 0.866025404, hk.z * 0.866025404), 0.866025404);
        sd = length(hp) * sign(hp.y);
      }
      float px = max(fwidth(sd), 1e-4);
      float cover = clamp(0.5 - sd / px, 0.0, 1.0);
      if (cover <= 0.0) discard;
      if (uShape > 3.5) {
        // The aura: no border, fading from the glyph's edge outwards.
        diffuseColor.a *= cover * (1.0 - smoothstep(-0.55, 0.0, sd));
      } else {
        float rim = smoothstep(-${BORDER_PX.toFixed(2)} * px - 0.5 * px, -${BORDER_PX.toFixed(2)} * px + 0.5 * px, sd);
        diffuseColor.rgb *= mix(1.0, ${BORDER_KEEP.toFixed(2)}, rim);
        if (uShape > 1.5 && uShape < 2.5) {
          // A glyph's inner contour, one pixel wide: a group, not a large disc.
          float inner = clamp(1.0 - (abs(sd + ${INNER_CONTOUR_AT.toFixed(2)}) - 0.5 * px) / px, 0.0, 1.0);
          diffuseColor.rgb *= mix(1.0, ${BORDER_KEEP.toFixed(2)}, inner);
        }
        diffuseColor.a *= cover;
      }`,
      );
  };
  return material;
}

export class LivingScene {
  readonly canvas: HTMLCanvasElement;
  private readonly renderer: WebGLRenderer;
  private readonly scene = new Scene();
  readonly camera = new PerspectiveCamera(44, 1, 1, 50_000);
  readonly controls: OrbitControls;
  private readonly idle: IdleRotation;
  private readonly driftForce: DriftForce<SceneNode>;
  private sim: Simulation<SceneNode> | null = null;

  private nodes: SceneNode[] = [];
  private links: SceneLink[] = [];
  private index = new Map<string, number>();
  private neighbours = new Map<string, SceneNode[]>();

  /** Every body is one camera-facing quad, shaped by its fragment shader (see `billboard`). */
  private readonly quad = new PlaneGeometry(2 * QUAD, 2 * QUAD);
  private nodeMesh: InstancedMesh | null = null;
  /** Artifacts get their own instanced mesh: a diamond reads as "not a
   *  concept" before any colour or label does. */
  private artifactMesh: InstancedMesh | null = null;
  /** A folded cluster is a flat hexagon in its colour, always facing the camera
   *  (billboard, see `billboard`): a shape no concept has, still at rest. */
  private readonly haloMaterial = billboard(new MeshBasicMaterial(), SHAPE.hexagon);
  private readonly auraMaterial = billboard(new MeshBasicMaterial({ opacity: AURA_OPACITY, depthWrite: false }), SHAPE.aura);
  private auraMesh: InstancedMesh | null = null;
  private readonly artifactMaterial = billboard(new MeshBasicMaterial(), SHAPE.diamond);
  private haloMesh: InstancedMesh | null = null;
  private hulls: Hull[] = [];
  /** The clusters the last setData opened. */
  private justOpened = new Set<string>();
  private clusterRadii = new Map<string, number>();
  private haloNodes: SceneNode[] = [];
  private haloSlot = new Map<SceneNode, number>();
  /** Each node's instance index within its own mesh, and each mesh's nodes. */
  private slot: number[] = [];
  private sphereNodes: SceneNode[] = [];
  private diamondNodes: SceneNode[] = [];
  /** One visual language, flat and unlit (2D): every body is its palette colour, edges are thin lines of a shade of it. */
  private readonly nodeMaterial = billboard(new MeshBasicMaterial(), SHAPE.disc);
  private readonly edgeGeometry = new BufferGeometry();
  private readonly edgeMaterial = new LineBasicMaterial({ transparent: true, opacity: EDGE_REST, depthWrite: false });
  private readonly edges = new LineSegments(this.edgeGeometry, this.edgeMaterial);
  private readonly activeGeometry = new BufferGeometry();
  private readonly activeMaterial = new LineBasicMaterial({ transparent: true, opacity: 0.85, depthWrite: false });
  private readonly active = new LineSegments(this.activeGeometry, this.activeMaterial);
  private readonly signalGeometry = new BufferGeometry();
  private readonly signalMaterial = new PointsMaterial({ size: 4, sizeAttenuation: false, transparent: true });
  private readonly signals = new Points(this.signalGeometry, this.signalMaterial);
  private readonly ring = new Mesh(
    new SphereGeometry(1, 10, 6),
    new MeshBasicMaterial({ wireframe: true, transparent: true, opacity: 0.75 }),
  );

  private colours: string[] = [];
  /** Node colours as drawn: what a colour change fades from. */
  private drawn: Color[] = [];
  /** The nodes `drawn` belongs to: another set is painted at once. */
  private drawnFor: SceneNode[] | null = null;
  private colourFade: { start: number; ms: number; from: Color[] } | null = null;
  private edgeFade: { start: number; ms: number; from: number } | null = null;
  private palette: ScenePalette | null = null;
  private selected: string | null = null;
  private activeLinks: SceneLink[] = [];
  private burst: { start: number; plan: Signal<SceneLink>[] } | null = null;

  private live: boolean;
  private readonly reducedMotion: boolean;
  private frame: number | null = null;
  private tween: { from: Pose; to: () => Pose; start: number; ms: number; shiftFrom: number; shiftTo: number } | null =
    null;
  /** Horizontal screen shift, in CSS pixels, applied through the camera's view
   *  offset: it centres a focused node in the strip the panels leave visible
   *  while the orbit still pivots on the node itself. */
  private shift = 0;
  private follow: Vector3 | null = null;
  private touched = false;
  /** The move being played: a cluster opening, folding, or one giving way to another. */
  /** Each member of a folding cluster, and the glyph it is drawn pulled into as it shrinks. */
  private ghostGlyph = new Map<SceneNode, SceneNode>();
  /** True from a transition's one camera move until the next explicit fit, a new
   *  graph, or a resize: the settle-follow and the idle refit stay off, so the
   *  camera is moved exactly once. */
  private cameraLocked = false;
  private radius = LINK_DISTANCE * 4;
  /** Set for a scene drawn frame by frame for a video (D691): no frame loop,
   *  no observers, no input; the frame size is fixed. */
  private readonly offscreen: OffscreenOptions | null;
  private virtualNow = 0;
  /** The scene's time in milliseconds: every tween, the burst and the
   *  settle read it, never the wall clock, so a scene driven by
   *  `renderFrame` is deterministic whatever the machine's speed. */
  clock: () => number = () => performance.now();
  private readonly cleanups: (() => void)[] = [];

  constructor(
    private readonly container: HTMLElement,
    private readonly callbacks: SceneCallbacks,
    options: { live: boolean; reducedMotion: boolean; offscreen?: OffscreenOptions },
  ) {
    this.driftForce = drift<SceneNode>(3);
    this.live = options.live && !options.reducedMotion;
    this.reducedMotion = options.reducedMotion;
    this.offscreen = options.offscreen ?? null;
    // Offscreen, time is the caller's: renderFrame sets it, frame by frame.
    if (this.offscreen) this.clock = () => this.virtualNow;
    if (import.meta.env.DEV && !this.offscreen) {
      this.trace = new FrameTrace();
      (window as unknown as { __atlasTrace?: FrameTrace }).__atlasTrace = this.trace;
    }
    this.idle = new IdleRotation(this.live, () => this.clock());

    this.renderer = new WebGLRenderer({ antialias: true, powerPreference: "high-performance" });
    this.canvas = this.renderer.domElement;
    container.appendChild(this.canvas);
    this.controls = new OrbitControls(this.camera, this.canvas);
    // No input reaches an offline scene: nobody orbits a render.
    if (this.offscreen) this.controls.disconnect();
    // Inertia after a release is motion nobody asked for: off under reduced motion.
    this.controls.enableDamping = !options.reducedMotion;
    this.controls.dampingFactor = DAMPING;
    this.controls.rotateSpeed = 0.6;
    this.controls.zoomSpeed = 0.8;
    this.controls.autoRotateSpeed = AUTO_ROTATE_SPEED;
    this.camera.position.set(0, 0, 600);

    for (const [geometry, object] of [
      [this.edgeGeometry, this.edges],
      [this.activeGeometry, this.active],
      [this.signalGeometry, this.signals],
    ] as const) {
      geometry.setAttribute("position", new BufferAttribute(new Float32Array(0), 3));
      object.frustumCulled = false;
      this.scene.add(object);
    }
    this.ring.visible = false;
    this.scene.add(this.ring);
    this.scene.add(this.camera);

    if (this.offscreen) {
      const { width, height, pixelRatio } = this.offscreen;
      this.renderer.setPixelRatio(pixelRatio);
      this.renderer.setSize(width, height, false);
      this.camera.aspect = width / height;
      this.camera.updateProjectionMatrix();
      return;
    }
    this.bindGestures();
    this.bindLifecycle();
    this.resize();
    this.start();
  }

  // --- data ---------------------------------------------------------------

  /**
   * setData replaces the visible graph. Node objects are the caller's and are
   * kept across calls, so a node hidden by a filter and shown again keeps its
   * place; the simulation is rebuilt around them and only lightly reheated.
   */
  setData(
    nodes: SceneNode[],
    links: SceneLink[],
    warmupTicks: number,
    replace = false,
    opts: { morph?: boolean; restore?: { pose: Pose; shift: number }; local?: Set<string>; hulls?: HullSpec[] } = {},
  ): void {
    this.invalidate();
    this.trace?.event("setData", { nodes: nodes.length, links: links.length, replace, morph: !!opts.morph, local: !!opts.local, hulls: opts.hulls?.length ?? 0 });
    this.chase = null;
    this.settleGoal = null;
    this.ahead.clear();
    for (const n of this.nodes) {
      n.fx = undefined;
      n.fy = undefined;
      n.fz = undefined;
    }
    const first = this.sim === null;
    // A morph needs a graph on screen to start from; with reduced motion the
    // swap is instant, as every other replacement is.
    const morph = !!opts.morph && !first && !this.reducedMotion && !this.offscreen;
    const restore = first && !this.offscreen ? opts.restore : undefined;
    const oldNodes = this.nodes;
    const oldColours = this.colours;
    // A transition in flight is not dropped: what is drawn at this instant is
    // where the next one starts (see carry).
    const drawnScale = this.snapshotScales();
    const drawnGlyph = new Map(this.ghostGlyph);
    const drawnPull = this.snapshotPulls();
    const linkKey = (l: SceneLink) => `${endpoint(l.source)}\u0000${endpoint(l.target)}`;
    const beforeNodes = new Set(this.nodes.map((n) => n.id));
    const beforeLinks = new Set(this.links.map(linkKey));
    const beforeNeighbours = this.neighbours;
    this.nodes = nodes;
    this.links = links;
    this.index = new Map(nodes.map((n, i) => [n.id, i]));
    this.neighbours = new Map(nodes.map((n) => [n.id, [] as SceneNode[]]));
    const byId = new Map(nodes.map((n) => [n.id, n]));
    this.updateHulls(opts.hulls ?? [], byId, oldNodes);
    for (const l of links) {
      const a = byId.get(endpoint(l.source));
      const b = byId.get(endpoint(l.target));
      if (!a || !b) continue;
      const listA = this.neighbours.get(a.id)!;
      const listB = this.neighbours.get(b.id)!;
      if (!listA.includes(b)) listA.push(b);
      if (!listB.includes(a)) listB.push(a);
    }

    this.sim?.stop();
    // With super-nodes a repeated link is one spring, weighted by its
    // multiplicity (the repeats are strokes for the eye, not pulls), and the
    // links drawn are resolved here, not by the link force.
    const clustered = nodes.some((n) => n.cluster);
    this.clustered = clustered;
    const springs: SpringLink[] = clustered ? springsOf(links, byId) : links;
    const sim = forceSimulation<SceneNode>(nodes, 3)
      .force(
        "link",
        forceLink<SceneNode, SpringLink>(springs).id((n) => n.id),
      )
      .force("charge", forceManyBody<SceneNode>())
      .velocityDecay(clustered ? CLUSTER_VELOCITY_DECAY : VELOCITY_DECAY)
      .stop();
    configureForces(
      (name, ...rest: unknown[]) => (rest.length ? sim.force(name, rest[0] as never) : sim.force(name)),
      this.driftForce,
      3,
    );
    if (nodes.length > LARGE_SIM_NODES) {
      // A large graph's repulsion is most of a tick: coarser (theta) and applied
      // every other tick at twice the strength -- the same push over time, half the cost.
      const charge = sim.force("charge") as unknown as ((alpha: number) => void) & {
        theta(t: number): void;
        initialize(nodes: SceneNode[], ...rest: unknown[]): void;
      };
      charge.theta(LARGE_SIM_THETA);
      let beat = 0;
      const halved = Object.assign((alpha: number) => {
        if (beat++ % 2 === 0) charge(alpha * 2);
      }, { initialize: (...args: Parameters<typeof charge.initialize>) => charge.initialize(...args) });
      sim.force("charge", halved as never);
    }
    if (clustered) {
      const open = this.hulls.filter((h) => h.dying === null).map((h) => ({ members: h.members }));
      configureClusterForces(sim as unknown as Simulation<LayoutNode>, springs, byId, nodes, open);
    }
    // A new graph -- the first, or another KB or Map in place of this one --
    // is laid out before it is drawn, then drawn opening out of its centre
    // (see bloom) while the camera eases onto it, and the camera is the
    // reader's again. An update to a graph already on screen is never ticked
    // ahead, so every move it causes is drawn and nothing jumps.
    if (morph) {
      const now = new Set(nodes.map((n) => n.id));
      this.bloom = null;
      this.edgeMaterial.opacity = this.selected ? EDGE_BEHIND_SELECTION : EDGE_REST;
      sim.alpha(MORPH_ALPHA);
      this.touched = false;
      this.trace?.event("morph");
      const entering = nodes.filter((n) => !beforeNodes.has(n.id));
      this.morph = {
        start: this.clock(),
        ms: MORPH_MS * entranceStretch(entering.length),
        leaving: oldNodes.filter((n) => !now.has(n.id)),
        entering: new Set(entering),
        flight: entering.length > FLOWER_NODES ? flowerFlight(entering, centroidOf(entering)) : undefined,
      };
      this.carry(this.morph, drawnScale, nodes, drawnGlyph, drawnPull);
      // Never offscreen: a video export must not depend on a wall-clock budget.
      this.settleGoal = this.offscreen ? null : predictSettle(sim, nodes);
    } else if (restore) {
      sim.alpha(RESTORE_ALPHA);
      this.touched = true;
    } else if (first || replace) {
      this.morph = null;
      sim.alpha(1);
      if (!this.live || this.offscreen) {
        // Still mode never finishes the settling on screen, and the video
        // export must be deterministic: both run the warm-up whole.
        const t0 = performance.now();
        sim.tick(warmupTicks);
        if (warmupTicks > 0) this.recordTick((performance.now() - t0) / warmupTicks);
      } else {
        // The warm-up blocks the first frame: on a large KB it stops at
        // WARMUP_BUDGET_MS, and the burst opens the graph while the still-warm
        // simulation finishes the settling in view.
        const until = performance.now() + WARMUP_BUDGET_MS;
        // A large graph keeps no minimum: its ticks are what hold the first frame.
        const minimum = nodes.length > FLOWER_NODES ? 4 : 30;
        for (let i = 0; i < warmupTicks && (i < minimum || performance.now() < until); i++) this.timedTick(sim);
      }
      this.touched = false;
      if (!this.reducedMotion && !this.offscreen) {
        // Kept warm while it opens (applyMotion): the nodes are already
        // moving as the graph unfolds, and when it is open the motion cools
        // into the usual drift with no seam between the two.
        sim.alpha(clustered ? CLUSTER_BLOOM_ALPHA : BLOOM_ALPHA);
        const c = nodes.reduce(
          (s, n) => ({
            x: s.x + (n.x ?? 0) / nodes.length,
            y: s.y + (n.y ?? 0) / nodes.length,
            z: s.z + (n.z ?? 0) / nodes.length,
          }),
          { x: 0, y: 0, z: 0 },
        );
        // Each node its own flight: a small start delay and its own speed,
        // stable per id, so the burst is ragged like a real one.
        let flight = new Map<SceneNode, { delay: number; span: number }>();
        if (nodes.length > FLOWER_NODES) flight = flowerFlight(nodes, c);
        else
          for (const n of nodes) {
            const h = hashUnit(n.id);
            flight.set(n, { delay: h * BLOOM_STAGGER, span: 1 - BLOOM_STAGGER - ((h * 7.31) % 1) * BLOOM_SPEED_SPREAD });
          }
        this.bloom = { start: this.clock(), ms: BLOOM_MS * entranceStretch(nodes.length), centre: c, flight };
        this.trace?.event("bloom", { nodes: nodes.length });
      }
    } else if (opts.local && !first) {
      // A cluster opening, folding, or one giving way to another: the simulation does it (see startOpening).
      this.startOpening(nodes, oldNodes, beforeNodes, sim, drawnScale, drawnGlyph, drawnPull);
    } else {
      // Every change to a graph already on screen knocks it from where it
      // happened -- a node arriving, the neighbours of one that left, both
      // ends of a link drawn or dropped: every node in the component takes a
      // push away from there, fading with each link, and the motion runs out
      // through the network as the simulation settles. No change, no motion.
      const origins = new Map<SceneNode, number>();
      const hit = (id: string, strength: number) => {
        const n = byId.get(id);
        if (n) origins.set(n, Math.max(origins.get(n) ?? 0, strength));
      };
      const current = new Set(nodes.map((n) => n.id));
      for (const n of nodes) if (!beforeNodes.has(n.id)) hit(n.id, KNOCK);
      for (const id of beforeNodes) {
        if (current.has(id)) continue;
        for (const m of beforeNeighbours.get(id) ?? []) hit(m.id, KNOCK * 0.6);
      }
      const nowLinks = new Set(links.map(linkKey));
      const changed = [...nowLinks]
        .filter((k) => !beforeLinks.has(k))
        .concat([...beforeLinks].filter((k) => !nowLinks.has(k)));
      for (const key of changed) {
        for (const id of key.split("\u0000")) hit(id, KNOCK * 0.6);
      }
      sim.alpha(this.sim?.alpha() ?? 0);
      if (origins.size) {
        // A bulk change (many origins) is one reheat, not a knock per origin;
        // a few origins share the push (knockAll).
        const bulk = this.reducedMotion ? false : knockAll(origins, this.neighbours) === "reheat";
        sim.alpha(Math.max(sim.alpha(), bulk ? KNOCK_BULK_ALPHA : KNOCK_ALPHA));
      }
    }
    this.sim = sim;
    // The warm-up has just timed this graph's ticks: a large one opens at the
    // slow drift rather than at the full rate for a few seconds first.
    if (!this.offscreen && this.tickMs !== null) {
      this.fullRate = sustainsFullRate(this.tickMs, this.fullRate);
      this.resting = this.tickMs > DRIFT_REST_MS;
    }
    this.applyMotion();

    this.sphereNodes = nodes.filter((n) => !n.artifact);
    this.diamondNodes = nodes.filter((n) => n.artifact);
    const sphereSlot = new Map(this.sphereNodes.map((n, i) => [n, i]));
    const diamondSlot = new Map(this.diamondNodes.map((n, i) => [n, i]));
    this.slot = nodes.map((n) => (n.artifact ? diamondSlot.get(n)! : sphereSlot.get(n)!));
    // Leavers keep a slot after the live ones, and are drawn shrinking until
    // the morph ends (sync).
    let spheres = this.sphereNodes.length;
    let diamonds = this.diamondNodes.length;
    // Looked up by node, not searched: All to a small Map leaves thousands.
    const ghostColour = new Map(this.ghosts.map((g) => [g.node, g.colour]));
    const oldIndex = new Map(oldNodes.map((n, i) => [n, i]));
    this.ghosts = (this.morph?.leaving ?? []).map((node) => ({
      node,
      slot: node.artifact ? diamonds++ : spheres++,
      // A leaver keeps the colour it was drawn with. A ghost carried over from
      // an earlier call already has it; a new one reads it off the old order.
      colour: ghostColour.get(node) ?? oldColours[oldIndex.get(node) ?? -1],
    }));
    const build = (material: MeshBasicMaterial, count: number) => {
      const mesh = new InstancedMesh(this.quad, material, Math.max(1, count));
      mesh.count = count;
      // Drawn over the links; picked on screen (pick), the quad faces the camera, its instance does not.
      mesh.renderOrder = 1;
      mesh.raycast = () => {};
      mesh.instanceMatrix.setUsage(DynamicDrawUsage);
      mesh.frustumCulled = false;
      // Nodes move every frame: a bounding sphere that holds everything, so
      // the raycaster never skips the mesh on a stale one.
      mesh.boundingSphere = new Sphere(new Vector3(), Number.POSITIVE_INFINITY);
      this.scene.add(mesh);
      return mesh;
    };
    for (const old of [this.nodeMesh, this.artifactMesh]) {
      old?.removeFromParent();
      old?.dispose();
    }
    this.nodeMesh = build(this.nodeMaterial, spheres);
    this.artifactMesh = build(this.artifactMaterial, diamonds);
    this.haloNodes = nodes.filter((n) => n.cluster);
    this.haloSlot = new Map(this.haloNodes.map((n, i) => [n, i]));
    // A glyph that leaves keeps its hexagon and aura, shrinking with it: they
    // are drawn after the live ones.
    let haloCount = this.haloNodes.length;
    for (const g of this.ghosts) if (g.node.cluster) g.halo = haloCount++;
    for (const old of [this.haloMesh, this.auraMesh]) {
      old?.removeFromParent();
      old?.dispose();
    }
    this.auraMesh = (() => {
      const m = new InstancedMesh(this.quad, this.auraMaterial, Math.max(1, haloCount));
      m.count = haloCount;
      m.instanceMatrix.setUsage(DynamicDrawUsage);
      m.frustumCulled = false;
      m.boundingSphere = new Sphere(new Vector3(), Number.POSITIVE_INFINITY);
      m.raycast = () => {};
      // Behind every body.
      m.renderOrder = -1;
      this.scene.add(m);
      return m;
    })();
    this.haloMesh = (() => {
      const m = new InstancedMesh(this.quad, this.haloMaterial, Math.max(1, haloCount));
      m.count = haloCount;
      m.instanceMatrix.setUsage(DynamicDrawUsage);
      m.frustumCulled = false;
      m.boundingSphere = new Sphere(new Vector3(), Number.POSITIVE_INFINITY);
      // Picked on screen (pick), not by ray: the hexagon faces the camera, its instance matrix does not.
      m.raycast = () => {};
      m.renderOrder = 1;
      this.scene.add(m);
      return m;
    })();
    reuseAttribute(this.edgeGeometry, links.length * 2);
    this.colourFade = null;
    this.applyColours();
    // The colours just painted were laid out for the previous node order: the
    // colours that follow for this one are painted at once, not faded into.
    this.drawnFor = null;
    this.setSelection(this.selected);
    this.sync();
    if (first && !restore) this.frameAll(0);
    // The burst leaves from the middle of the screen -- where the seed was
    // drawn while the graph loaded -- not from the graph's own centroid,
    // which the framing puts somewhere near it but rarely on it.
    if (this.bloom && (first || replace)) {
      const t = this.controls.target;
      this.bloom.centre = { x: t.x, y: t.y, z: t.z };
    }
    // A new graph (not a cluster opening or closing) fits itself again.
    if (morph || restore || first || replace) this.cameraLocked = false;
    if (morph) {
      this.radius = boundingRadius(this.nodes);
      this.settleUntil = this.clock() + SETTLE_MS;
      // The camera follows the new set as it settles (step), its pull rising
      // from nothing over the morph: no tween, so there is no hand-over from a
      // tween's end to the follow, where the camera would stop and set off again.
      this.settleFrom = this.clock();
      const fit = this.fitPoseFor(0);
      if (fit) this.setLimits(distanceOf(fit));
    } else if (restore) {
      // Back where the reader left it: the camera as it was, no opening, no
      // following (the layout is already settled).
      this.radius = boundingRadius(this.nodes);
      this.settleUntil = 0;
      this.applyPose(restore.pose);
      this.setShift(restore.shift);
      this.setLimits(distanceOf(restore.pose));
    } else if (first || replace) {
      this.radius = boundingRadius(this.nodes);
      this.settleUntil = this.reducedMotion || this.offscreen ? 0 : this.clock() + SETTLE_MS;
      this.settleFrom = 0;
      if (this.reducedMotion && replace) this.frameAll(0);
    } else this.radius = boundingRadius(this.linkedNodes());
  }

  setColours(colours: string[], palette: ScenePalette): void {
    this.invalidate();
    // The same nodes recoloured (a selection made or cleared) fade to their
    // new colours; anything else -- a new graph, a theme -- is painted at once.
    const same = this.drawnFor === this.nodes && this.palette?.background === palette.background;
    this.colours = colours;
    this.palette = palette;
    if (same && !this.reducedMotion && !this.offscreen && this.drawn.length) {
      this.colourFade = {
        start: this.clock(),
        ms: Math.max(1, motionMs(RECEDE)),
        from: this.drawn.map((c) => c.clone()),
      };
    } else {
      this.colourFade = null;
    }
    this.applyColours();
  }

  /** The clock and length of the move the last setData began (see updateHulls). */
  private moveAt = 0;
  private moveMs = 0;

  /** The open clusters of a new data set: one that stays is kept, a new one
   *  opens from its glyph, one gone folds back into it. */
  private updateHulls(specs: HullSpec[], byId: Map<string, SceneNode>, previous: SceneNode[]): void {
    const now = this.clock();
    for (const n of previous) if (n.cluster) this.clusterRadii.set(n.id, this.radiusOf(n));
    for (const n of byId.values()) if (n.cluster) this.clusterRadii.set(n.id, this.radiusOf(n));
    const next: Hull[] = [];
    const kept = new Set<string>();
    this.justOpened = new Set();
    for (const spec of specs) {
      const members = spec.members.map((id) => byId.get(id)).filter((n): n is SceneNode => !!n);
      if (members.length === 0) continue;
      kept.add(spec.id);
      const old = this.hulls.find((h) => h.id === spec.id && h.dying === null);
      if (!old) this.justOpened.add(spec.id);
      // A cluster opens where its glyph stood.
      const glyphAt = previous.find((n) => n.id === spec.id);
      const centre =
        glyphAt && Number.isFinite(glyphAt.x) ? { x: glyphAt.x!, y: glyphAt.y ?? 0, z: glyphAt.z ?? 0 } : centroidOf(members);
      next.push(
        old
          ? { ...old, members }
          : {
              id: spec.id,
              members,
              ms: 0,
              dying: null,
              glyph: this.clusterRadii.get(spec.id) ?? 14,
              c: centre,
              anchor: centre,
            },
      );
    }
    // One choreography, one clock: opening (or switching) takes the deliberate
    // beat -- the members, the glyphs and the camera travel together -- folding
    // a quicker one, because nothing needs reading as it closes.
    this.moveAt = now;
    this.moveMs = this.cameraMs(this.justOpened.size > 0 ? CAMERA_MOVE : CAMERA_FOLD);
    for (const h of next) if (this.justOpened.has(h.id)) h.ms = this.moveMs;
    for (const h of this.hulls) {
      // A fold in progress is played by the setData that started it: the next one drops it.
      if (h.dying !== null || kept.has(h.id)) continue;
      if (this.reducedMotion || this.offscreen) continue;
      next.push({ ...h, dying: now, ms: this.moveMs, c: centroidOf(h.members, h.c) });
    }
    this.hulls = next;
  }

  /**
   * A cluster opening, folding, or one giving way to another, played by the
   * simulation itself, as the flat view takes a node that is added: the members
   * of the cluster opening are spread over its glyph (from their ids), the
   * glyph that comes back is put where its members were, every other node stays
   * exactly where it is, and the simulation is reheated moderately and cools at
   * its own pace: the members grow in and mix into the graph by its general
   * forces. The camera aims at where the graph will rest (predictSettle) and
   * follows it there.
   */
  private startOpening(
    nodes: SceneNode[],
    oldNodes: SceneNode[],
    before: Set<string>,
    sim: Simulation<SceneNode>,
    drawnScale: Map<string, number>,
    drawnGlyph: Map<SceneNode, SceneNode>,
    drawnPull: Map<string, number>,
  ): void {
    const now = this.moveAt;
    const ms = this.moveMs;
    const opening = this.justOpened.size > 0;
    const current = new Set(nodes.map((n) => n.id));
    const byId = new Map(nodes.map((n) => [n.id, n]));
    const park = (n: SceneNode, at: Point, spread: number) => {
      const off = (salt: string) => (hashUnit(n.id + salt) - 0.5) * spread;
      n.x = at.x + off("x");
      n.y = at.y + off("y");
      n.z = at.z + off("z");
      n.vx = 0;
      n.vy = 0;
      n.vz = 0;
    };
    for (const h of this.hulls) {
      // Spread over the glyph they replace, so they expand from it, not burst out of a point.
      if (h.dying === null && this.justOpened.has(h.id)) for (const m of h.members) park(m, h.anchor, h.glyph * 1.6);
    }
    this.ghostGlyph = new Map();
    for (const h of this.hulls) {
      if (h.dying === null) continue;
      const glyph = byId.get(h.id);
      if (!glyph) continue;
      park(glyph, h.c, 0);
      for (const m of h.members) if (!current.has(m.id)) this.ghostGlyph.set(m, glyph);
    }
    if (ms > 0) {
      this.morph = {
        start: now,
        ms,
        leaving: oldNodes.filter((n) => !current.has(n.id)),
        entering: new Set(nodes.filter((n) => !before.has(n.id))),
      };
      this.carry(this.morph, drawnScale, nodes, drawnGlyph, drawnPull);
    }
    this.trace?.event("open", { opening, ms });
    sim.alpha(opening ? OPEN_ALPHA : FOLD_ALPHA);
    if (ms > 0 && !this.offscreen) this.settleGoal = predictSettle(sim, nodes);
    if (ms <= 0) {
      // Reduced motion (or an export): the layout is simply laid out, and the camera put on it.
      for (let i = 0; i < 150; i++) sim.tick();
      sim.alpha(0);
    }
    // One smooth follow of the whole graph as it settles; nothing refits on its own afterwards.
    this.settleUntil = 0;
    if (!this.selected) {
      this.radius = boundingRadius(nodes);
      this.followFit(0, 0, TRANSITION_FOLLOW_MS);
    }
  }

  private applyColours(): void {
    const mesh = this.nodeMesh;
    const palette = this.palette;
    if (!mesh || !palette) return;
    this.paintNodes(this.colourFade ? 0 : 1);
    const colour = new Color();
    for (const g of this.ghosts) {
      (g.node.artifact ? this.artifactMesh : mesh)?.setColorAt(g.slot, colour.set(g.colour ?? palette.edge));
      if (g.halo !== undefined) {
        this.haloMesh?.setColorAt(g.halo, colour);
        this.auraMesh?.setColorAt(g.halo, colour);
      }
    }
    for (const m of [mesh, this.artifactMesh, this.haloMesh, this.auraMesh]) if (m?.instanceColor) m.instanceColor.needsUpdate = true;
    const background = new Color(palette.background);
    this.renderer.setClearColor(background);
    this.scene.fog = new Fog(background, 1, 2);
    this.edgeMaterial.color.set(palette.edge);
    this.activeMaterial.color.set(palette.edgeActive);
    this.signalMaterial.color.set(palette.signal);
    (this.ring.material as MeshBasicMaterial).color.set(palette.ring);
    this.updateFog();
  }

  /** Writes the node colours, `k` of the way from the fade's start to the
   *  colours asked (1: the colours asked). */
  private paintNodes(k: number): void {
    const mesh = this.nodeMesh;
    const palette = this.palette;
    if (!mesh || !palette) return;
    const from = this.colourFade?.from;
    const to = new Color();
    const background = new Color(palette.background);
    // Painted every frame of a fade: the colours are reused, not reallocated.
    if (this.drawn.length !== this.nodes.length) this.drawn = this.nodes.map(() => new Color());
    this.nodes.forEach((n, i) => {
      to.set(this.colours[i] ?? palette.edge);
      // Depth within the palette: a concept recedes towards the background as it is lighter,
      // a hub keeps its Map's colour whole. Glyphs and artifacts keep theirs.
      if (!n.cluster && !n.artifact) to.lerp(background, TONE_DEPTH * (1 - Math.min(1, Math.max(0, n.weight))));
      const c = this.drawn[i]!;
      if (from?.[i] && k < 1) c.copy(from[i]!).lerp(to, k);
      else c.copy(to);
      (n.artifact ? this.artifactMesh : mesh)?.setColorAt(this.slot[i]!, c);
      if (n.cluster) {
        this.haloMesh?.setColorAt(this.haloSlot.get(n)!, c);
        this.auraMesh?.setColorAt(this.haloSlot.get(n)!, c);
      }
    });
    this.drawnFor = this.nodes;
    for (const m of [mesh, this.artifactMesh, this.haloMesh, this.auraMesh]) if (m?.instanceColor) m.instanceColor.needsUpdate = true;
  }

  // --- selection ----------------------------------------------------------

  setSelection(id: string | null): void {
    this.invalidate();
    const node = id ? this.nodes[this.index.get(id) ?? -1] : undefined;
    this.selected = node ? node.id : null;
    this.activeLinks = node
      ? this.links.filter((l) => endpoint(l.source) === node.id || endpoint(l.target) === node.id)
      : [];
    reuseAttribute(this.activeGeometry, this.activeLinks.length * 2);
    this.ring.visible = !!node;
    this.idle.setSelected(!!node);
    this.applyMotion();
    const opacity = node ? EDGE_BEHIND_SELECTION : EDGE_REST;
    // Faded like the node colours (see setColours), unless nothing is on
    // screen yet to fade from.
    this.edgeFade =
      opacity !== this.edgeMaterial.opacity &&
      !this.reducedMotion &&
      !this.offscreen &&
      this.drawn.length &&
      !this.bloom
        ? { start: this.clock(), ms: Math.max(1, motionMs(RECEDE)), from: this.edgeMaterial.opacity }
        : null;
    this.edgeMaterial.opacity = this.edgeFade ? this.edgeFade.from : opacity;
    this.burst = null;
    reuseAttribute(this.signalGeometry, MAX_SIGNALS);
    this.signals.visible = false;
  }

  /** Signals for the current selection (planned by motion.planBursts). */
  sendSignals(plan: Signal<SceneLink>[]): void {
    this.invalidate();
    this.burst = plan.length ? { start: this.clock(), plan: plan.slice(0, MAX_SIGNALS * 3) } : null;
  }

  /** The drawn links touching a node, in their data orientation. */
  linksOf(id: string): SceneLink[] {
    return this.links.filter((l) => endpoint(l.source) === id || endpoint(l.target) === id);
  }

  neighboursOf(id: string): SceneNode[] {
    return this.neighbours.get(id) ?? [];
  }

  nodeById(id: string): SceneNode | undefined {
    const i = this.index.get(id);
    return i === undefined ? undefined : this.nodes[i];
  }

  /** Screen position of a node, in CSS pixels relative to the canvas, or null
   *  when it is behind the camera. */
  project(node: { x?: number; y?: number; z?: number }): { x: number; y: number } | null {
    const v = new Vector3(node.x ?? 0, node.y ?? 0, node.z ?? 0).project(this.camera);
    if (v.z < -1 || v.z > 1) return null;
    const w = this.canvas.clientWidth;
    const h = this.canvas.clientHeight;
    return { x: ((v.x + 1) / 2) * w, y: ((1 - v.y) / 2) * h };
  }

  radiusOf(node: SceneNode): number {
    return layoutRadius(node);
  }

  // --- motion and camera --------------------------------------------------

  setLive(live: boolean): void {
    this.invalidate();
    this.live = live && !this.reducedMotion;
    this.idle.setLive(this.live);
    this.applyMotion();
    if (!this.live) {
      this.burst = null;
      this.signals.visible = false;
    }
  }

  /** Drift and the live simulation follow the motion toggle. With a concept
   *  open the drift eases down to a fifth (calm(), over about two seconds, so
   *  the click never jolts the network): labels that breathe around a still
   *  point are hard to read beside the inspector, and the camera circles the
   *  concept slowly instead. */
  private applyMotion(): void {
    const drifting = this.live && !this.resting;
    this.driftForce.enabled(drifting);
    this.driftForce.calm(this.selected ? READING_DRIFT : 1);
    const target = drifting ? LIVE_ALPHA : 0;
    // A clustered layout is already settled when it opens (its stiff springs and collisions would shiver if held warm): no warmth.
    this.sim?.alphaTarget(this.bloom && !this.clustered ? Math.max(target, BLOOM_WARMTH) : target);
  }

  /** Eases the camera onto a node's neighbourhood in the strip the panels
   *  leave visible, then follows it while it moves. */
  focus(id: string, occluded: { left: number; right: number }, savePose: () => void): void {
    const node = this.nodeById(id);
    if (!node) return;
    savePose();
    const from = this.camera.position.clone();
    // The look-at point is the node itself, so dragging orbits around it; the
    // panels' occlusion is compensated by shifting the image, not the pivot.
    const target = () =>
      focusPose(node as Required<SceneNode>, from, this.neighbourhoodRadius(node), {
        width: this.canvas.clientWidth,
        height: this.canvas.clientHeight,
        fov: this.camera.fov,
        occludedRight: 0,
        occludedLeft: 0,
      });
    const shift = (Math.max(0, occluded.right) - Math.max(0, occluded.left)) / 2;
    this.animateTo(target, this.cameraMs(CAMERA_MOVE), shift);
    this.follow = new Vector3(node.x, node.y, node.z);
  }

  /** Moves the picture, as it is, into the strip the panels leave visible:
   *  for an open concept the graph does not draw (an index page), so the
   *  graph is not left under the reading panel. */
  centreIn(occluded: { left: number; right: number }): void {
    this.follow = null;
    const shift = (Math.max(0, occluded.right) - Math.max(0, occluded.left)) / 2;
    if (Math.abs(shift - this.shift) < 0.5) return;
    const pose = this.pose();
    this.animateTo(() => pose, this.cameraMs(CAMERA_MOVE), shift);
  }

  unfocus(pose: Pose | null): void {
    this.follow = null;
    if (!pose) {
      this.setShift(0);
      return;
    }
    // A pose saved while the graph was still settling is a stale picture of
    // it: returning there would end with the camera sliding on to the fit, a
    // second move after the first. Ease straight onto the fit, from the same
    // direction, instead (the settle then has nothing left to do).
    let to = pose;
    if (!this.touched && this.clock() < this.settleUntil) to = this.fitPoseFor(0, pose) ?? pose;
    this.animateTo(() => to, this.cameraMs(CAMERA_MOVE), 0);
  }

  /** The screen shift now in force, in CSS pixels (see `shift`). */
  shiftPx(): number {
    return this.shift;
  }

  pose(): Pose {
    const p = this.camera.position;
    const t = this.controls.target;
    return { position: { x: p.x, y: p.y, z: p.z }, lookAt: { x: t.x, y: t.y, z: t.z } };
  }

  private neighbourhoodRadius(node: SceneNode): number {
    const d = this.neighboursOf(node.id)
      .map((n) => Math.hypot((n.x ?? 0) - (node.x ?? 0), (n.y ?? 0) - (node.y ?? 0), (n.z ?? 0) - (node.z ?? 0)))
      .filter(Number.isFinite)
      .sort((a, b) => a - b);
    const reach = d.length ? d[Math.min(d.length - 1, Math.floor(d.length * 0.9))]! : 0;
    return Math.max(LINK_DISTANCE * 2.5, reach);
  }

  private linkedNodes(): SceneNode[] {
    const linked = this.nodes.filter((n) => (this.neighbours.get(n.id)?.length ?? 0) > 0);
    return linked.length ? linked : this.nodes;
  }

  /** Frames the whole visible graph, every node included, from the current
   *  viewpoint. */
  frameAll(ms = this.cameraMs(CAMERA_MOVE)): void {
    this.fitEverything(0, ms);
  }

  /** Frames every drawn node, outliers and loose ones included, in the
   *  canvas above `occludedBottom` pixels, from the current viewpoint: the
   *  growth replay keeps the whole graph in sight with it as it grows. */
  fitEverything(occludedBottom: number, ms = this.cameraMs(CAMERA_MOVE), minDistance = 0): void {
    this.cameraLocked = false;
    this.trace?.event("fitEverything", { ms });
    const pose = this.fitWithFloor(occludedBottom, minDistance);
    if (!pose) return;
    this.radius = boundingRadius(this.nodes);
    this.setLimits(distanceOf(pose));
    this.animateTo(() => pose, ms);
  }

  /**
   * Keeps every node in frame above `occludedBottom` pixels, never nearer
   * than `minDistance`, for `ms` (until stopFollowingFit without one): the
   * camera eases a little of the way to the fit on every frame, as a settling
   * graph is followed. The growth replay uses it while the graph grows: a
   * tween restarted on each step would set off from rest every time, and a
   * camera that stops and starts again is what the eye reads as a cut.
   * Called again while following, it keeps its momentum.
   */
  followFit(occludedBottom: number, minDistance = 0, ms = Infinity): void {
    this.invalidate();
    this.cameraLocked = false;
    const now = this.clock();
    if (!this.fitFollow || now >= this.fitFollow.until) this.settleFrom = now;
    this.trace?.event("followFit", { ms });
    this.cancelTween();
    this.chase = null;
    this.fitFollow = { occludedBottom, minDistance, until: now + ms };
    if (this.reducedMotion) {
      const pose = this.fitWithFloor(occludedBottom, minDistance);
      if (pose) this.applyPose(pose);
    }
  }

  stopFollowingFit(): void {
    this.invalidate();
    this.fitFollow = null;
  }

  /** Each node where its present velocity carries it (a geometric series of the
   *  simulation's damping, doubled for the forces still pushing): null with no simulation. */
  private lookahead(): Map<SceneNode, Point> | null {
    const sim = this.sim;
    if (!sim) return null;
    const decay = (sim as unknown as { velocityDecay(): number }).velocityDecay();
    const k = Math.min(LOOKAHEAD_MAX, (2 * (1 - decay)) / Math.max(0.05, decay));
    // One map and its points, reused: refreshed every few frames on graphs of thousands.
    const ahead = this.ahead;
    if (ahead.size > this.nodes.length * 2) ahead.clear();
    for (const n of this.nodes) {
      let p = ahead.get(n);
      if (!p) ahead.set(n, (p = { x: 0, y: 0, z: 0 }));
      p.x = (n.x ?? 0) + (n.vx ?? 0) * k;
      p.y = (n.y ?? 0) + (n.vy ?? 0) * k;
      p.z = (n.z ?? 0) + (n.vz ?? 0) * k;
    }
    return ahead;
  }
  private readonly ahead = new Map<SceneNode, Point>();
  /** The bodies a fit weighs, reused across fits. */
  private bodies: { x: number; y: number; z: number; radius: number }[] = [];

  private fitWithFloor(occludedBottom: number, minDistance: number, place?: Map<SceneNode, Point>): Pose | null {
    const pose = this.fitPoseFor(occludedBottom, undefined, place);
    if (!pose) return null;
    const along = {
      x: pose.position.x - pose.lookAt.x,
      y: pose.position.y - pose.lookAt.y,
      z: pose.position.z - pose.lookAt.z,
    };
    const distance = Math.hypot(along.x, along.y, along.z);
    if (distance > 0 && distance < minDistance) {
      const k = minDistance / distance;
      pose.position = {
        x: pose.lookAt.x + along.x * k,
        y: pose.lookAt.y + along.y * k,
        z: pose.lookAt.z + along.z * k,
      };
    }
    return pose;
  }

  private setLimits(distance: number): void {
    const limits = zoomLimits(this.radius);
    this.controls.minDistance = limits.min;
    this.controls.maxDistance = Math.max(limits.max, distance * 1.5);
  }

  /** How far fitEverything would put the camera now: the growth replay reads
   *  it off the full graph before hiding it, as a floor for its own fits. */
  fitDistance(occludedBottom: number): number {
    const pose = this.fitPoseFor(occludedBottom);
    if (!pose) return 0;
    return Math.hypot(
      pose.position.x - pose.lookAt.x,
      pose.position.y - pose.lookAt.y,
      pose.position.z - pose.lookAt.z,
    );
  }

  private fitPoseFor(occludedBottom: number, from?: Pose, place?: Map<SceneNode, Point>): Pose | null {
    // One pass into a reused array: the follow and the idle refit call this every few frames.
    const bodies = this.bodies;
    let count = 0;
    for (const n of this.nodes) {
      const p = place?.get(n) ?? n;
      if (!Number.isFinite(p.x) || !Number.isFinite(p.y) || !Number.isFinite(p.z)) continue;
      const b = bodies[count] ?? (bodies[count] = { x: 0, y: 0, z: 0, radius: 0 });
      b.x = p.x!;
      b.y = p.y!;
      b.z = p.z!;
      b.radius = this.radiusOf(n);
      count++;
    }
    bodies.length = count;
    const { width, height } = this.viewSize();
    // The view stays centred on the canvas, so a band at the bottom is kept
    // clear by fitting the height left once it is taken off both edges.
    const usable = Math.max(1, height - 2 * Math.max(0, occludedBottom));
    const fov =
      (2 * Math.atan(Math.tan((this.camera.fov * Math.PI) / 360) * (usable / Math.max(1, height))) * 180) / Math.PI;
    const eye = from ? new Vector3(from.position.x, from.position.y, from.position.z) : this.camera.position;
    const look = from ? new Vector3(from.lookAt.x, from.lookAt.y, from.lookAt.z) : this.controls.target;
    const pose = fitPose(bodies, eye, look, {
      width,
      height: usable,
      fov,
      occludedRight: 0,
      // Super-nodes are big by design: bounded by the view, not by the node cap.
      maxBodyPx: this.clustered ? Infinity : undefined,
    });
    return pose;
  }

  /** Moves the camera towards (factor < 1) or away from the look-at point,
   *  within the zoom limits: the + / - controls. */
  zoomBy(factor: number): void {
    this.touched = true;
    this.idle.input();
    const t = this.controls.target;
    const offset = this.camera.position.clone().sub(t);
    const d = Math.min(this.controls.maxDistance, Math.max(this.controls.minDistance, offset.length() * factor));
    offset.setLength(d);
    const to: Pose = {
      position: { x: t.x + offset.x, y: t.y + offset.y, z: t.z + offset.z },
      lookAt: { x: t.x, y: t.y, z: t.z },
    };
    this.animateTo(() => to, this.cameraMs(CAMERA_ZOOM));
  }

  /** A camera move's length: the motion token on screen, 0 under reduced
   *  motion; a video export keeps the scale whatever the exporter's setting. */
  private cameraMs(token: MotionToken): number {
    if (this.offscreen) return MOTION_MS[token];
    return this.reducedMotion ? 0 : motionMs(token);
  }

  private animateTo(to: () => Pose, ms: number, shift = this.shift, start = this.clock()): void {
    this.invalidate();
    // One camera animation at a time: a move replaces the one in flight (it
    // sets off from where the camera is) and ends any follow, so nothing
    // pulls the camera two ways at once.
    this.fitFollow = null;
    this.chase = null;
    const from = this.pose();
    if (ms <= 0) {
      this.tween = null;
      this.setShift(shift);
      this.applyPose(to());
      return;
    }
    this.trace?.event("camera tween", { ms });
    this.tween = { from, to, start, ms, shiftFrom: this.shift, shiftTo: shift };
    this.idle.setFocusing(true);
  }

  private setShift(px: number): void {
    this.invalidate();
    this.shift = px;
    const w = Math.max(1, this.container.clientWidth);
    const h = Math.max(1, this.container.clientHeight);
    if (Math.abs(px) < 0.5) this.camera.clearViewOffset();
    else this.camera.setViewOffset(w, h, px, 0, w, h);
  }

  private applyPose(p: Pose): void {
    this.camera.position.set(p.position.x, p.position.y, p.position.z);
    this.controls.target.set(p.lookAt.x, p.lookAt.y, p.lookAt.z);
    this.camera.lookAt(this.controls.target);
    this.updateFog();
  }

  private updateFog(): void {
    // Depth by distance, never by darkness: the far side of the graph fades
    // gently into the canvas, the near side is fully drawn.
    const fog = this.scene.fog as Fog | null;
    if (!fog) return;
    const d = this.camera.position.distanceTo(this.controls.target);
    fog.near = Math.max(1, d);
    fog.far = d + this.radius * 5;
  }

  private cancelTween(): void {
    this.tween = null;
    this.idle.setFocusing(false);
  }

  // --- frame loop ---------------------------------------------------------

  private start(): void {
    // A tab coming back draws at once: its canvas may have been discarded.
    this.invalidate();
    if (this.frame === null && !this.offscreen) this.frame = requestAnimationFrame(this.tick);
  }

  private stop(): void {
    if (this.frame !== null) cancelAnimationFrame(this.frame);
    this.frame = null;
    // A restart measures from its first frame, not across the pause.
    this.lastFrame = null;
    this.simDebt = 0;
  }

  private ticks = 0;
  /** Something the frame loop does not see changed what is drawn (the data,
   *  the colours, the camera's goal, a frame listener): the next frame draws
   *  even if nothing moves. A frame where nothing changed draws nothing. */
  /** DEV only: the frame trace (see trace.ts); null in a production build. */
  private trace: FrameTrace | null = null;
  /** DEV only: where every node was last drawn (the burst moves it, the simulation does not), for the trace. */
  private drawnPositions = new Float32Array(0);
  private dirty = true;
  /** The simulation moved the nodes since the buffers were last synced. */
  private stale = true;
  private lastDraw = Number.NEGATIVE_INFINITY;
  /** The running average cost of one simulation tick, in ms. */
  private tickMs: number | null = null;
  /** Whether the drift ticks at the full rate: false while a tick costs more
   *  than the drift budget allows (sustainsFullRate), and it ticks at half. */
  private fullRate = true;
  /** Simulation steps skipped or taken by the slowed drift. */
  private slowSteps = 0;
  /** A tick past DRIFT_REST_MS: the drift rests and the network cools. */
  private resting = false;
  /** The idle refit's target, refreshed every IDLE_FIT_EVERY frames. */
  private readonly scratch = new Vector3();
  /** The pose a settling or growing graph is chased to, refreshed every CHASE_EVERY frames: the fit walks every node. */
  /** A scene with folded clusters: its entrance starts from a settled layout, calmly (see CLUSTER_BLOOM_ALPHA). */
  private clustered = false;
  private chase: { goal: Pose | null; cur: Pose | null; frames: number } | null = null;
  /** Where a transition's nodes will come to rest (predictSettle): the camera
   *  aims at that fit from the start instead of trailing the layout as it spreads. */
  private settleGoal: Map<SceneNode, Point> | null = null;
  private idleFit: Pose | null = null;
  private idleFrames = 0;

  private invalidate(): void {
    this.dirty = true;
  }

  /** One simulation tick, timed into tickMs. */
  private timedTick(sim: Simulation<SceneNode>): void {
    const t0 = performance.now();
    sim.tick();
    this.recordTick(performance.now() - t0);
  }

  private recordTick(ms: number): void {
    this.tickMs = this.tickMs === null ? ms : this.tickMs + (ms - this.tickMs) * 0.1;
  }

  /** Slows the drift of a network too costly to tick at the full rate, or
   *  restores it once a tick is cheap again (a smaller Map). An offline scene
   *  keeps the full rate: its frames must not depend on the exporting
   *  machine's speed. */
  private reconsiderDrift(): void {
    if (this.offscreen || this.tickMs === null) return;
    this.fullRate = sustainsFullRate(this.tickMs, this.fullRate);
    const resting = this.tickMs > DRIFT_REST_MS;
    if (resting === this.resting) return;
    this.resting = resting;
    this.applyMotion();
  }

  /** The simulation runs at a fixed SIM_HZ whatever the display's refresh:
   *  the forces are tuned per tick at 60 Hz, and a 120-144 Hz screen ticking
   *  once per frame drifted twice as fast at twice the CPU. Frames between two
   *  ticks only draw. */
  private simDebt = 0;
  private lastFrame: number | null = null;
  private readonly tick = (): void => {
    this.frame = requestAnimationFrame(this.tick);
    const now = this.clock();
    // Capped: a tab coming back from the background does not catch up.
    // A costly simulation never catches up: one tick a frame, the layout slows, the frames do not.
    const cap = this.tickMs !== null && this.tickMs > COSTLY_TICK_MS ? 1 : SIM_MAX_CATCHUP;
    this.simDebt = Math.min(cap, this.simDebt + (now - (this.lastFrame ?? now)) / (1000 / SIM_HZ));
    this.lastFrame = now;
    const ticks = Math.floor(this.simDebt);
    this.simDebt -= ticks;
    this.step(now, ticks);
  };

  /**
   * Advances an offline scene to `nowMs` on its virtual clock and renders one
   * frame: the same body as a live frame (simulation, tweens, fit easing,
   * sync, render). The canvas holds the frame until the caller's task ends,
   * so it must be copied before awaiting anything.
   */
  renderFrame(nowMs: number): void {
    if (!this.offscreen) throw new Error("renderFrame needs an offscreen scene");
    this.virtualNow = nowMs;
    this.step(nowMs, 1, true);
  }

  /** Where the nodes start, by id (the visible scene's layout): the offline
   *  scene draws the video from the picture on screen. */
  seedPositions(positions: Map<string, { x: number; y: number; z: number }>): void {
    this.invalidate();
    for (const n of this.nodes) {
      const p = positions.get(n.id);
      if (p) Object.assign(n, { x: p.x, y: p.y, z: p.z, vx: 0, vy: 0, vz: 0 });
    }
    this.sync();
  }

  private lastStepAt: number | null = null;
  private step(now: number, simTicks = 1, force = false): void {
    // The camera's easing, damping and rotation follow the real time between
    // two steps, not the number of steps, so a 120 Hz screen and the 60 fps
    // export move the camera alike.
    const dtMs = this.lastStepAt === null ? REF_FRAME_MS : Math.min(MAX_FRAME_MS, Math.max(0, now - this.lastStepAt));
    this.lastStepAt = now;
    const sim = this.sim;
    // Still mode lets the simulation cool and then stops ticking it: at rest
    // the frame costs only the draw.
    let ticked = 0;
    for (let step = 0; step < simTicks && sim && ((this.live && !this.resting) || sim.alpha() > 0.002); step++) {
      // A costly network only breathing (nothing reheated it) skips steps;
      // a drag, a knock or a new layout always runs at the full rate.
      if (!this.fullRate && this.live && sim.alpha() <= LIVE_ALPHA * 1.5 && ++this.slowSteps % DRIFT_SLOW_EVERY !== 0)
        continue;
      this.timedTick(sim);
      ticked++;
      if (++this.ticks % 30 === 0) sanitize(this.nodes, this.neighbours);
    }
    if (ticked > 0) {
      this.stale = true;
      this.reconsiderDrift();
    }
    let moved = ticked > 0 || this.tween !== null;
    if (this.tween) {
      const t = Math.min(1, (now - this.tween.start) / this.tween.ms);
      const k = smooth(t);
      const to = this.tween.to();
      const f = this.tween.from;
      const lerp = (a: number, b: number) => a + (b - a) * k;
      this.setShift(lerp(this.tween.shiftFrom, this.tween.shiftTo));
      this.applyPose({
        position: {
          x: lerp(f.position.x, to.position.x),
          y: lerp(f.position.y, to.position.y),
          z: lerp(f.position.z, to.position.z),
        },
        lookAt: {
          x: lerp(f.lookAt.x, to.lookAt.x),
          y: lerp(f.lookAt.y, to.lookAt.y),
          z: lerp(f.lookAt.z, to.lookAt.z),
        },
      });
      if (t >= 1) {
        this.cancelTween();
        // The tween aimed at where the node is now, not where it was at the
        // start: the follow continues from there, not from the old anchor.
        const node = this.follow && this.selected ? this.nodeById(this.selected) : undefined;
        if (node && Number.isFinite(node.x)) this.follow!.set(node.x!, node.y!, node.z!);
      }
    } else if (this.follow && this.selected) {
      moved ||= ticked > 0;
      // Follow the selection as the physics moves it.
      const node = this.nodeById(this.selected);
      if (node && Number.isFinite(node.x)) {
        const d = this.scratch.set(node.x!, node.y!, node.z!).sub(this.follow);
        this.camera.position.add(d);
        this.controls.target.add(d);
        this.follow.set(node.x!, node.y!, node.z!);
      }
    } else if (
      !this.selected &&
      !this.cameraLocked &&
      ((this.fitFollow && now < this.fitFollow.until) || (now < this.settleUntil && !this.touched))
    ) {
      // A graph still settling (or growing, see followFit): ease a little of
      // the way to its fit every frame, so the camera follows it without a
      // jump and arrives as it comes to rest.
      moved = true;
      const follow = this.fitFollow && now < this.fitFollow.until ? this.fitFollow : null;
      // The fit is recomputed every CHASE_EVERY frames and the target the camera
      // eases towards glides to it each frame, so the camera never steps.
      if (!this.chase || ++this.chase.frames % CHASE_EVERY === 0) {
        // Aimed where the nodes are going: the predicted rest (a small graph), else
        // each node carried on by its velocity, so a large graph spreading out stays in frame.
        const ahead = this.settleGoal ?? this.lookahead();
        const fresh = follow
          ? this.fitWithFloor(follow.occludedBottom, follow.minDistance, ahead ?? undefined)
          : this.fitPoseFor(0, undefined, ahead ?? undefined);
        const cur = this.chase?.cur ?? fresh;
        this.chase = { goal: fresh, cur: cur ? structuredClone(cur) : null, frames: this.chase?.frames ?? 0 };
      }
      const glide = frameEase(CHASE_GLIDE, dtMs);
      const { goal, cur } = this.chase;
      if (goal && cur) {
        for (const key of ["position", "lookAt"] as const) {
          cur[key].x += (goal[key].x - cur[key].x) * glide;
          cur[key].y += (goal[key].y - cur[key].y) * glide;
          cur[key].z += (goal[key].z - cur[key].z) * glide;
        }
      }
      const pose = cur;
      if (pose) {
        const p = this.camera.position;
        const t = this.controls.target;
        const e = frameEase(SETTLE_EASE * smooth((now - this.settleFrom) / SETTLE_RAMP_MS), dtMs);
        p.set(p.x + (pose.position.x - p.x) * e, p.y + (pose.position.y - p.y) * e, p.z + (pose.position.z - p.z) * e);
        t.set(t.x + (pose.lookAt.x - t.x) * e, t.y + (pose.lookAt.y - t.y) * e, t.z + (pose.lookAt.z - t.z) * e);
        this.controls.maxDistance = Math.max(this.controls.maxDistance, p.distanceTo(t) * 1.5);
      }
    }
    // An expired follow lets the idle refit below run again.
    if (this.fitFollow && now >= this.fitFollow.until) this.fitFollow = null;
    this.controls.autoRotate = !this.tween && this.idle.shouldRotate();
    this.controls.autoRotateSpeed = this.selected ? READING_ROTATE_SPEED : AUTO_ROTATE_SPEED;
    if (!this.controls.autoRotate) this.idleFit = null;
    if (this.controls.autoRotate && !this.selected && !this.fitFollow && !this.cameraLocked) {
      // Idle: the network breathes and the panorama turns around a pivot
      // the drift slowly leaves. Keep the whole of it in frame, gently. The
      // fit walks every node, and the drift moves it slowly: refreshed every
      // IDLE_FIT_EVERY frames, eased towards on every one.
      if (this.idleFit === null || ++this.idleFrames % IDLE_FIT_EVERY === 0) this.idleFit = this.fitPoseFor(0);
      const pose = this.idleFit;
      if (pose) {
        const p = this.camera.position;
        const t = this.controls.target;
        const e = frameEase(IDLE_FIT_EASE, dtMs);
        p.set(p.x + (pose.position.x - p.x) * e, p.y + (pose.position.y - p.y) * e, p.z + (pose.position.z - p.z) * e);
        t.set(t.x + (pose.lookAt.x - t.x) * e, t.y + (pose.lookAt.y - t.y) * e, t.z + (pose.lookAt.z - t.z) * e);
        this.controls.maxDistance = Math.max(this.controls.maxDistance, p.distanceTo(t) * 1.5);
        moved = true;
      }
    }
    // True while the camera moves: the reader's drag, its damping, the panorama.
    this.controls.dampingFactor = frameEase(DAMPING, dtMs);
    if (this.controls.update(dtMs / 1000)) moved = true;
    const animating = !!(this.bloom || this.morph || this.colourFade || this.edgeFade || (this.burst && this.live));
    if (!force) {
      // Nothing moved and nothing changed: the picture on screen is still
      // right, and drawing it again costs a frame of GPU for nothing.
      if ((!moved && !animating && !this.dirty) || (!this.dirty && now - this.lastDraw < MIN_DRAW_MS)) {
        this.traceFrame(false);
        return;
      }
    }
    this.updateFog();
    // The buffers follow the nodes only when the simulation moved them or an
    // animation draws them somewhere else; a frame where only the camera
    // moved finds them already right.
    if (this.stale || animating || this.dirty) {
      this.sync(now);
      this.stale = false;
    }
    this.dirty = false;
    this.lastDraw = now;
    this.renderer.render(this.scene, this.camera);
    this.traceFrame(true);
    for (const listener of this.frameListeners) listener();
  }

  private traceFrame(drawn: boolean): void {
    if (this.trace) this.trace.frame(drawn, this.camera.position, this.controls.target, this.sim?.alpha() ?? 0, this.drawnPositions);
  }

  /** Called after each drawn frame (the component places its labels); the
   *  first call comes with the next frame. Returns the remover. */
  onFrame(listener: () => void): () => void {
    this.frameListeners.add(listener);
    this.invalidate();
    return () => this.frameListeners.delete(listener);
  }
  private readonly frameListeners = new Set<() => void>();

  private readonly dummy = new Object3D();
  /** A new graph opening out of its centre: drawn positions run from the
   *  centre to the laid-out ones over BLOOM_MS. Only the drawing moves; the
   *  simulation runs on the true positions throughout. */
  private bloom: {
    start: number;
    ms: number;
    centre: { x: number; y: number; z: number };
    flight: Map<SceneNode, { delay: number; span: number }>;
  } | null = null;
  /** Until when a new graph is followed by the camera as it settles. */
  private settleUntil = 0;
  /** When the follow's pull started rising (a morph); 0 pulls at full
   *  strength at once (a new graph, whose camera already starts on its fit). */
  private settleFrom = 0;
  /** The growth replay's framing, followed until `until` (see followFit). */
  private fitFollow: { occludedBottom: number; minDistance: number; until: number } | null = null;
  /** A Map change in progress: what leaves shrinks (drawn from `ghosts`),
   *  what enters grows. Only the drawing animates. */
  private morph: {
    start: number;
    ms?: number;
    leaving: SceneNode[];
    entering: Set<SceneNode>;
    /** A large set entering grows in as a flower (see flowerFlight). */
    flight?: Map<SceneNode, { delay: number; span: number }>;
    /** The scale each node was drawn at when the previous transition was
     *  interrupted (by id): an entering node grows from it, a leaving one
     *  shrinks from it. Absent: from nothing / from full size. */
    from?: Map<string, number>;
    /** How far a leaver was already pulled into its glyph (by id) when the
     *  previous transition was interrupted: the pull goes on from there. */
    pull?: Map<string, number>;
  } | null = null;
  /** `halo`: the instance a leaving glyph's hexagon and aura are drawn in. */
  private ghosts: { node: SceneNode; slot: number; colour: string | undefined; halo?: number }[] = [];

  /** What the morph draws at `now`: how grown a live node is, how far the
   *  morph has run, and how big a leaver still is (relative to its radius). */
  private growth(now: number) {
    const m = this.morph;
    let morphT = 1;
    let grow: (n: SceneNode) => number = () => 1;
    let ghostScale: (n: SceneNode) => number = () => 0;
    let pullOf: (n: SceneNode) => number = () => 0;
    if (m) {
      const span = m.ms ?? MORPH_MS;
      morphT = smooth((now - m.start) / span);
      const raw = Math.min(1, Math.max(0, (now - m.start) / span));
      grow = (n) => {
        if (!m.entering.has(n)) return 1;
        const f = m.flight?.get(n);
        const k = f ? smooth(Math.min(1, Math.max(0, (raw - f.delay) / f.span))) : morphT;
        const from = m.from?.get(n.id) ?? 0;
        return from + (1 - from) * k;
      };
      ghostScale = (n) => (m.from?.get(n.id) ?? 1) * (1 - morphT);
      pullOf = (n) => {
        const from = m.pull?.get(n.id) ?? 0;
        return from + (1 - from) * morphT;
      };
    }
    return { grow, morphT, ghostScale, pullOf };
  }

  /** The scale every node and leaver is drawn at right now, by id: the start
   *  of whatever transition replaces the one in flight. Empty when none is. */
  private snapshotScales(): Map<string, number> {
    const out = new Map<string, number>();
    if (!this.morph) return out;
    const { grow, ghostScale } = this.growth(this.clock());
    for (const n of this.nodes) {
      const s = grow(n);
      if (s < 1) out.set(n.id, s);
    }
    for (const g of this.ghosts) out.set(g.node.id, ghostScale(g.node));
    return out;
  }

  /** How far each leaver is pulled into its glyph right now, by id. */
  private snapshotPulls(): Map<string, number> {
    const out = new Map<string, number>();
    if (!this.morph) return out;
    const { pullOf } = this.growth(this.clock());
    for (const g of this.ghosts) if (this.ghostGlyph.has(g.node)) out.set(g.node.id, pullOf(g.node));
    return out;
  }

  /** Starts `m` from the scales `drawn` (see snapshotScales): a node still
   *  growing keeps growing from where it was, a node returning from the
   *  previous transition's leavers grows back from its size, a leaver shrinks
   *  from its size, and the previous leavers not in the new graph keep
   *  shrinking instead of vanishing. */
  private carry(
    m: NonNullable<LivingScene["morph"]>,
    drawn: Map<string, number>,
    nodes: SceneNode[],
    glyphs: Map<SceneNode, SceneNode>,
    pulls: Map<string, number>,
  ): void {
    if (!drawn.size) return;
    const from = new Map<string, number>();
    const current = new Set(nodes.map((n) => n.id));
    for (const n of nodes) {
      const s = drawn.get(n.id);
      if (s === undefined || s >= 1) continue;
      m.entering.add(n);
      from.set(n.id, s);
    }
    const leaving = new Set(m.leaving.map((n) => n.id));
    for (const g of this.ghosts) {
      if (current.has(g.node.id) || leaving.has(g.node.id)) continue;
      m.leaving.push(g.node);
      // A member pulled into its glyph keeps going from where it was drawn.
      const glyph = glyphs.get(g.node);
      if (glyph && !this.ghostGlyph.has(g.node)) {
        this.ghostGlyph.set(g.node, glyph);
        (m.pull ??= new Map()).set(g.node.id, pulls.get(g.node.id) ?? 0);
      }
    }
    for (const n of m.leaving) {
      const s = drawn.get(n.id);
      if (s !== undefined && s < 1) from.set(n.id, s);
    }
    if (from.size) m.from = from;
  }

  private sync(now = this.clock()): void {
    const mesh = this.nodeMesh;
    if (!mesh) return;
    const d = this.dummy;
    const diamonds = this.artifactMesh;
    const halo = this.haloMesh;
    // The burst: every node flies out of the seed along its own line,
    // overshoots its place a little and springs back. The panorama keeps
    // turning at its own pace throughout; the burst adds no turn of its own.
    const bloom = this.bloom;
    let t = 1;
    if (bloom) {
      t = Math.min(1, (now - bloom.start) / bloom.ms);
      if (t >= 1) {
        this.bloom = null;
        this.applyMotion();
      }
    }
    const c = bloom?.centre ?? { x: 0, y: 0, z: 0 };
    // Links fade in as the nodes land: a link from a node already out to one
    // still at the seed would otherwise be drawn as a long thread across the
    // burst.
    if (bloom) {
      const base = this.selected ? EDGE_BEHIND_SELECTION : EDGE_REST;
      const fade = Math.min(1, Math.max(0, (t - 0.35) / 0.55));
      this.edgeMaterial.opacity = base * fade * fade;
    }
    if (this.colourFade) {
      const k = smooth((now - this.colourFade.start) / this.colourFade.ms);
      this.paintNodes(k);
      if (k >= 1) this.colourFade = null;
    }
    if (this.edgeFade && !bloom) {
      const k = smooth((now - this.edgeFade.start) / this.edgeFade.ms);
      const to = this.selected ? EDGE_BEHIND_SELECTION : EDGE_REST;
      this.edgeMaterial.opacity = this.edgeFade.from + (to - this.edgeFade.from) * k;
      if (k >= 1) this.edgeFade = null;
    }
    // The morph: a node entering grows from nothing, a node leaving shrinks
    // to it; a link to a growing node is drawn growing out of its other end.
    const { grow, morphT, ghostScale, pullOf } = this.growth(now);
    const kOf = (n: SceneNode) => {
      if (!bloom || t >= 1) return 1;
      const f = bloom.flight.get(n);
      return easeOutBack(f ? Math.min(1, Math.max(0, (t - f.delay) / f.span)) : t);
    };
    const px = (n: SceneNode) => c.x + ((n.x ?? 0) - c.x) * kOf(n);
    const py = (n: SceneNode) => c.y + ((n.y ?? 0) - c.y) * kOf(n);
    const pz = (n: SceneNode) => c.z + ((n.z ?? 0) - c.z) * kOf(n);
    for (let i = 0; i < this.nodes.length; i++) {
      const n = this.nodes[i]!;
      // Once per node: the flight's progress is a map lookup during a bloom.
      const k = kOf(n);
      d.position.set(c.x + ((n.x ?? 0) - c.x) * k, c.y + ((n.y ?? 0) - c.y) * k, c.z + ((n.z ?? 0) - c.z) * k);
      if (this.trace) {
        if (this.drawnPositions.length !== this.nodes.length * 3) this.drawnPositions = new Float32Array(this.nodes.length * 3);
        this.drawnPositions[i * 3] = d.position.x;
        this.drawnPositions[i * 3 + 1] = d.position.y;
        this.drawnPositions[i * 3 + 2] = d.position.z;
      }
      const size = this.radiusOf(n) * (0.2 + 0.8 * Math.min(1, k)) * grow(n);
      d.scale.setScalar(size * (n.artifact ? DIAMOND_SCALE : n.cluster ? 0 : 1));
      d.updateMatrix();
      (n.artifact ? diamonds : mesh)?.setMatrixAt(this.slot[i]!, d.matrix);
      if (n.cluster && halo) {
        // A flat hexagon at rest: it only moves with its node.
        d.scale.setScalar(size);
        d.updateMatrix();
        halo.setMatrixAt(this.haloSlot.get(n)!, d.matrix);
        d.scale.setScalar(size * AURA_SCALE);
        d.updateMatrix();
        this.auraMesh?.setMatrixAt(this.haloSlot.get(n)!, d.matrix);
      }
    }
    for (const g of this.ghosts) {
      // A member leaving is drawn pulled into its glyph as it shrinks.
      const glyph = this.ghostGlyph.get(g.node);
      const into = glyph ? pullOf(g.node) : 0;
      d.position.set(
        (g.node.x ?? 0) + ((glyph?.x ?? g.node.x ?? 0) - (g.node.x ?? 0)) * into,
        (g.node.y ?? 0) + ((glyph?.y ?? g.node.y ?? 0) - (g.node.y ?? 0)) * into,
        (g.node.z ?? 0) + ((glyph?.z ?? g.node.z ?? 0) - (g.node.z ?? 0)) * into,
      );
      const size = this.radiusOf(g.node) * ghostScale(g.node);
      d.scale.setScalar(size * (g.node.artifact ? DIAMOND_SCALE : g.node.cluster ? 0 : 1));
      d.updateMatrix();
      (g.node.artifact ? diamonds : mesh)?.setMatrixAt(g.slot, d.matrix);
      if (g.halo !== undefined && halo) {
        d.scale.setScalar(size);
        d.updateMatrix();
        halo.setMatrixAt(g.halo, d.matrix);
        d.scale.setScalar(size * AURA_SCALE);
        d.updateMatrix();
        this.auraMesh?.setMatrixAt(g.halo, d.matrix);
      }
    }
    mesh.instanceMatrix.needsUpdate = true;
    if (diamonds) diamonds.instanceMatrix.needsUpdate = true;
    if (halo) halo.instanceMatrix.needsUpdate = true;
    if (this.auraMesh) this.auraMesh.instanceMatrix.needsUpdate = true;
    if (this.morph && morphT >= 1) {
      // Done: leavers are no longer drawn, and their slots no longer counted.
      this.morph = null;
      this.ghosts = [];
      mesh.count = this.sphereNodes.length;
      if (diamonds) diamonds.count = this.diamondNodes.length;
      if (halo) halo.count = this.haloNodes.length;
      if (this.auraMesh) this.auraMesh.count = this.haloNodes.length;
    }

    const write = (attr: BufferAttribute, i: number, l: SceneLink) => {
      const a = l.source as SceneNode;
      const b = l.target as SceneNode;
      const arr = attr.array as Float32Array;
      const ga = grow(a);
      const gb = grow(b);
      // Scalars, not arrays: this runs per link per frame while the graph moves.
      const ax = px(a), ay = py(a), az = pz(a), bx = px(b), by = py(b), bz = pz(b);
      // The more grown end stays; the other is reached the share of the way
      // its own growth allows.
      const s = Math.min(ga, gb);
      const o = i * 6;
      if (s >= 1) {
        arr[o] = ax; arr[o + 1] = ay; arr[o + 2] = az; arr[o + 3] = bx; arr[o + 4] = by; arr[o + 5] = bz;
      } else if (ga >= gb) {
        arr[o] = ax; arr[o + 1] = ay; arr[o + 2] = az;
        arr[o + 3] = ax + (bx - ax) * s; arr[o + 4] = ay + (by - ay) * s; arr[o + 5] = az + (bz - az) * s;
      } else {
        arr[o] = bx + (ax - bx) * s; arr[o + 1] = by + (ay - by) * s; arr[o + 2] = bz + (az - bz) * s;
        arr[o + 3] = bx; arr[o + 4] = by; arr[o + 5] = bz;
      }
    };
    const edges = this.edgeGeometry.getAttribute("position") as BufferAttribute;
    this.links.forEach((l, i) => write(edges, i, l));
    edges.needsUpdate = true;
    const active = this.activeGeometry.getAttribute("position") as BufferAttribute;
    this.activeLinks.forEach((l, i) => write(active, i, l));
    active.needsUpdate = true;

    const node = this.selected ? this.nodeById(this.selected) : undefined;
    if (node) {
      this.ring.position.set(node.x ?? 0, node.y ?? 0, node.z ?? 0);
      this.ring.scale.setScalar(this.radiusOf(node) * 1.45 + LEAF_RADIUS * 0.6);
    }

    // Signals: a point per planned pass, from the link's source to its target.
    const burst = this.burst;
    if (burst && this.live) {
      const attr = this.signalGeometry.getAttribute("position") as BufferAttribute;
      const arr = attr.array as Float32Array;
      let alive = 0;
      let pending = false;
      for (const s of burst.plan) {
        const f = (now - burst.start - s.delayMs) / SIGNAL_PASS_MS;
        if (f < 0) pending = true;
        if (f < 0 || f > 1 || alive >= MAX_SIGNALS) continue;
        const a = s.link.source as SceneNode;
        const b = s.link.target as SceneNode;
        arr[alive * 3] = (a.x ?? 0) + ((b.x ?? 0) - (a.x ?? 0)) * f;
        arr[alive * 3 + 1] = (a.y ?? 0) + ((b.y ?? 0) - (a.y ?? 0)) * f;
        arr[alive * 3 + 2] = (a.z ?? 0) + ((b.z ?? 0) - (a.z ?? 0)) * f;
        alive++;
      }
      this.signalGeometry.setDrawRange(0, alive);
      attr.needsUpdate = true;
      this.signals.visible = alive > 0;
      if (!alive && !pending) this.burst = null;
    } else {
      this.signals.visible = false;
    }
  }

  // --- gestures -----------------------------------------------------------

  /** The node under a screen point: the ray first, then the nearest node
   *  within HIT_RADIUS_PX, so a small or distant node is still easy to take. */
  pick(clientX: number, clientY: number): SceneNode | null {
    if (!this.nodeMesh || this.nodes.length === 0) return null;
    const r = this.canvas.getBoundingClientRect();
    // On screen, for every body: each is a billboard (a flat shape facing the camera), so a ray
    // against its instance would miss what is drawn. A body is a target of its own size on
    // screen, at least 14 px of radius (18 for a glyph); a hit inside the shape beats a near miss.
    const pxPerUnit = r.height / (2 * Math.tan((this.camera.fov * Math.PI) / 360));
    let best: SceneNode | null = null;
    let bestScore = 1;
    const v = this.scratch;
    for (const n of this.nodes) {
      v.set(n.x ?? 0, n.y ?? 0, n.z ?? 0);
      const depth = Math.max(1, this.camera.position.distanceTo(v));
      v.project(this.camera);
      if (v.z < -1 || v.z > 1) continue;
      const dx = ((v.x + 1) / 2) * r.width - (clientX - r.left);
      const dy = ((1 - v.y) / 2) * r.height - (clientY - r.top);
      const radiusPx = (this.radiusOf(n) * (n.artifact ? DIAMOND_SCALE : 1) * pxPerUnit) / depth;
      const limit = Math.max(n.cluster ? CLUSTER_HIT_RADIUS_PX : HIT_RADIUS_PX, radiusPx);
      const dist2 = dx * dx + dy * dy;
      const score = (dist2 / (limit * limit)) * (dist2 <= radiusPx * radiusPx ? 0.25 : 1);
      if (score < bestScore) {
        bestScore = score;
        best = n;
      }
    }
    return best;
  }

  /**
   * Nodes are selected, never grabbed -- every drag orbits, the wheel and a
   * pinch zoom. A press that does not travel is a click: on a node it selects
   * it, on the background it clears the selection. Picking runs on click and
   * a throttled hover, never per frame.
   */
  private bindGestures(): void {
    const canvas = this.canvas;
    const pointers = new Set<number>();
    let press: { x: number; y: number; moved: boolean } | null = null;
    let lastHover = 0;

    // A transition's follow (finite) yields to the reader's hand; the growth
    // replay's (until it ends) keeps framing what grows.
    const yieldFollow = () => {
      if (this.fitFollow && Number.isFinite(this.fitFollow.until)) this.fitFollow = null;
    };
    const onDown = (e: PointerEvent) => {
      this.touched = true;
      this.idle.input();
      this.cancelTween();
      yieldFollow();
      pointers.add(e.pointerId);
      // A second contact is a pinch, never a click.
      press = pointers.size > 1 ? null : { x: e.clientX, y: e.clientY, moved: false };
    };

    const onMove = (e: PointerEvent) => {
      if (press && Math.hypot(e.clientX - press.x, e.clientY - press.y) > CLICK_SLOP_PX) press.moved = true;
      if (pointers.size > 0) {
        this.idle.input();
        return;
      }
      if (e.timeStamp - lastHover > 50) {
        lastHover = e.timeStamp;
        const hover = this.pick(e.clientX, e.clientY);
        canvas.style.cursor = hover ? "pointer" : "";
        const r = canvas.getBoundingClientRect();
        this.callbacks.onHover(hover?.id ?? null, e.clientX - r.left, e.clientY - r.top);
      }
    };

    const onUp = (e: PointerEvent) => {
      pointers.delete(e.pointerId);
      const p = press;
      press = null;
      if (p && !p.moved && pointers.size === 0) this.callbacks.onSelect(this.pick(p.x, p.y)?.id ?? null);
    };
    const onCancel = (e: PointerEvent) => {
      pointers.delete(e.pointerId);
      press = null;
    };
    const onWheel = () => {
      this.touched = true;
      this.idle.input();
      this.cancelTween();
      yieldFollow();
    };
    const onLeave = () => this.callbacks.onHover(null, 0, 0);
    const onDouble = (e: MouseEvent) => {
      const node = this.pick(e.clientX, e.clientY);
      if (node) this.callbacks.onExpand?.(node.id);
    };
    canvas.addEventListener("dblclick", onDouble);
    this.cleanups.push(() => canvas.removeEventListener("dblclick", onDouble));

    canvas.addEventListener("pointerdown", onDown, { capture: true });
    canvas.addEventListener("pointermove", onMove);
    canvas.addEventListener("pointerup", onUp);
    canvas.addEventListener("pointercancel", onCancel);
    canvas.addEventListener("lostpointercapture", onCancel);
    canvas.addEventListener("wheel", onWheel, { passive: true });
    canvas.addEventListener("pointerleave", onLeave);
    this.cleanups.push(() => {
      canvas.removeEventListener("pointerdown", onDown, { capture: true });
      canvas.removeEventListener("pointermove", onMove);
      canvas.removeEventListener("pointerup", onUp);
      canvas.removeEventListener("pointercancel", onCancel);
      canvas.removeEventListener("lostpointercapture", onCancel);
      canvas.removeEventListener("wheel", onWheel);
      canvas.removeEventListener("pointerleave", onLeave);
    });
  }

  // --- lifecycle ----------------------------------------------------------

  private bindLifecycle(): void {
    const onVisibility = () => (document.hidden ? this.stop() : this.start());
    document.addEventListener("visibilitychange", onVisibility);
    const onLost = (e: Event) => {
      e.preventDefault();
      this.stop();
      this.callbacks.onLost();
    };
    this.canvas.addEventListener("webglcontextlost", onLost);
    const observer = new ResizeObserver(() => this.resize());
    observer.observe(this.container);
    this.cleanups.push(() => {
      document.removeEventListener("visibilitychange", onVisibility);
      this.canvas.removeEventListener("webglcontextlost", onLost);
      observer.disconnect();
    });
  }

  /** The frame the camera sees: the canvas, or the video's own frame offline
   *  (a detached canvas has no layout box). */
  private viewSize(): { width: number; height: number } {
    return this.offscreen ?? { width: this.canvas.clientWidth, height: this.canvas.clientHeight };
  }

  private resize(): void {
    // An offline scene keeps the size it was made with.
    if (this.offscreen) return;
    this.cameraLocked = false;
    const w = Math.max(1, this.container.clientWidth);
    const h = Math.max(1, this.container.clientHeight);
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
    this.renderer.setSize(w, h);
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
    this.setShift(this.shift);
    // setSize clears the drawing buffer, and the observer runs after this
    // frame's draw: without a draw here the browser shows an empty canvas for
    // a frame, a flash on every step of a canvas that is being resized (the
    // Map's contract panel sliding in).
    this.renderer.render(this.scene, this.camera);
    this.lastDraw = this.clock();
    for (const listener of this.frameListeners) listener();
  }

  dispose(): void {
    this.stop();
    this.sim?.stop();
    this.cleanups.forEach((fn) => fn());
    this.controls.dispose();
    this.nodeMesh?.dispose();
    this.artifactMesh?.dispose();
    this.haloMesh?.dispose();
    this.auraMesh?.dispose();
    for (const g of [
      this.quad,
      this.edgeGeometry,
      this.activeGeometry,
      this.signalGeometry,
      this.ring.geometry,
    ])
      g.dispose();
    for (const m of [
      this.nodeMaterial,
      this.artifactMaterial,
      this.haloMaterial,
      this.auraMaterial,
      this.edgeMaterial,
      this.activeMaterial,
      this.signalMaterial,
      this.ring.material as MeshBasicMaterial,
    ])
      m.dispose();
    this.renderer.dispose();
    // An offline scene is one of two contexts alive at once (the visible one
    // is the other): release it now, not when the collector gets to it.
    if (this.offscreen) this.renderer.forceContextLoss();
    this.canvas.remove();
  }
}
