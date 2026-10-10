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
  OctahedronGeometry,
  PerspectiveCamera,
  Points,
  PointsMaterial,
  Raycaster,
  Scene,
  Sphere,
  SphereGeometry,
  Vector2,
  Vector3,
  WebGLRenderer,
} from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import { fitPose, focusPose, FOCUS_MS, zoomLimits, type Pose } from "./camera";
import { AUTO_ROTATE_SPEED, IdleRotation, MAX_SIGNALS, SIGNAL_PASS_MS, type Signal } from "./motion";
import {
  LINK_DISTANCE,
  LIVE_ALPHA,
  VELOCITY_DECAY,
  boundingRadius,
  configureForces,
  drift,
  sanitize,
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

/** Radius of a leaf, in scene units, and of the best-connected node: the
 *  Studio 03 ratio (.053 : .125), one size up from it -- at the prototype's
 *  scale a few hundred real concepts read as dust, and on paper as nothing. */
/** How long the camera follows a new graph as it settles, and how much of
 *  the way to its fit it eases each frame. */
const SETTLE_MS = 3500;
const SETTLE_EASE = 0.06;
/** How long a new KB or Map takes to open out of its centre. */
const BLOOM_MS = 1800;
/** The burst's raggedness: the latest a node sets off, and how much slower
 *  than the fastest the slowest flies, both as shares of BLOOM_MS. */
const BLOOM_STAGGER = 0.3;
const BLOOM_SPEED_SPREAD = 0.3;

/** Out, past the mark, and back: the flight of a node in the burst. The
 *  flight is eased in first, so it is seen leaving the seed rather than
 *  being already there a frame later. */
function easeOutBack(t: number): number {
  const s = 1.1;
  const u = (t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2) - 1;
  return 1 + (s + 1) * u * u * u + s * u * u;
}

/** A stable value in [0, 1) for an id. */
function hashUnit(id: string): number {
  let h = 2166136261;
  for (let i = 0; i < id.length; i++) h = Math.imul(h ^ id.charCodeAt(i), 16777619);
  return (h >>> 0) / 4294967296;
}
/** How warm the simulation starts as it opens, and how warm it is kept
 *  until it is open. */
const BLOOM_ALPHA = 0.3;
const BLOOM_WARMTH = 0.12;
/** The velocity a new node gives its neighbours (scene units per tick). */
const KNOCK = 6;
/** What is left of a knock after each link it travels through. */
const KNOCK_FALLOFF = 0.55;
/** How hot the simulation runs again after a knock, so it travels. */
const KNOCK_ALPHA = 0.45;
const LEAF_RADIUS = LINK_DISTANCE * 0.12;
const HUB_RADIUS = LEAF_RADIUS * 2.36;
/** A press that travels less than this is a click, not a drag or an orbit. */
const CLICK_SLOP_PX = 5;
/** Picking reaches this far around a node on screen: small nodes stay easy
 *  to hit (brand kit: 12-18 px hit area). */
const HIT_RADIUS_PX = 14;

/** Resting links, and the same links behind a selection: they recede so the
 *  selection's own links carry the picture. */
const EDGE_REST = 0.22;
const EDGE_BEHIND_SELECTION = 0.06;

const endpoint = (end: string | SceneNode) => (typeof end === "object" ? end.id : end);

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

  private readonly sphere = new SphereGeometry(1, 12, 9);
  private nodeMesh: InstancedMesh | null = null;
  /** Artifacts get their own instanced mesh: a diamond reads as "not a
   *  concept" before any colour or label does. */
  private readonly diamond = new OctahedronGeometry(1, 0);
  private artifactMesh: InstancedMesh | null = null;
  /** Each node's instance index within its own mesh, and each mesh's nodes. */
  private slot: number[] = [];
  private sphereNodes: SceneNode[] = [];
  private diamondNodes: SceneNode[] = [];
  private readonly nodeMaterial = new MeshBasicMaterial();
  private readonly edgeGeometry = new BufferGeometry();
  private readonly edgeMaterial = new LineBasicMaterial({ transparent: true, opacity: EDGE_REST, depthWrite: false });
  private readonly edges = new LineSegments(this.edgeGeometry, this.edgeMaterial);
  private readonly activeGeometry = new BufferGeometry();
  private readonly activeMaterial = new LineBasicMaterial({ transparent: true, opacity: 0.85, depthWrite: false });
  private readonly active = new LineSegments(this.activeGeometry, this.activeMaterial);
  private readonly signalGeometry = new BufferGeometry();
  private readonly signalMaterial = new PointsMaterial({ size: 4, sizeAttenuation: false, transparent: true });
  private readonly signals = new Points(this.signalGeometry, this.signalMaterial);
  private readonly ring = new Mesh(new SphereGeometry(1, 10, 6), new MeshBasicMaterial({ wireframe: true, transparent: true, opacity: 0.75 }));

  private colours: string[] = [];
  private palette: ScenePalette | null = null;
  private selected: string | null = null;
  private activeLinks: SceneLink[] = [];
  private burst: { start: number; plan: Signal<SceneLink>[] } | null = null;

  private live: boolean;
  private readonly reducedMotion: boolean;
  private frame: number | null = null;
  private tween: { from: Pose; to: () => Pose; start: number; ms: number; shiftFrom: number; shiftTo: number } | null = null;
  /** Horizontal screen shift, in CSS pixels, applied through the camera's view
   *  offset: it centres a focused node in the strip the panels leave visible
   *  while the orbit still pivots on the node itself. */
  private shift = 0;
  private follow: Vector3 | null = null;
  private touched = false;
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
    this.idle = new IdleRotation(this.live, () => this.clock());

    this.renderer = new WebGLRenderer({ antialias: true, powerPreference: "high-performance" });
    this.canvas = this.renderer.domElement;
    container.appendChild(this.canvas);
    this.controls = new OrbitControls(this.camera, this.canvas);
    // No input reaches an offline scene: nobody orbits a render.
    if (this.offscreen) this.controls.disconnect();
    this.controls.enableDamping = true;
    this.controls.dampingFactor = 0.08;
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
  setData(nodes: SceneNode[], links: SceneLink[], warmupTicks: number, replace = false): void {
    const first = this.sim === null;
    const linkKey = (l: SceneLink) => `${endpoint(l.source)}\u0000${endpoint(l.target)}`;
    const beforeNodes = new Set(this.nodes.map((n) => n.id));
    const beforeLinks = new Set(this.links.map(linkKey));
    const beforeNeighbours = this.neighbours;
    this.nodes = nodes;
    this.links = links;
    this.index = new Map(nodes.map((n, i) => [n.id, i]));
    this.neighbours = new Map(nodes.map((n) => [n.id, [] as SceneNode[]]));
    const byId = new Map(nodes.map((n) => [n.id, n]));
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
    const sim = forceSimulation<SceneNode>(nodes, 3)
      .force("link", forceLink<SceneNode, SceneLink>(links).id((n) => n.id))
      .force("charge", forceManyBody<SceneNode>())
      .velocityDecay(VELOCITY_DECAY)
      .stop();
    configureForces(
      (name, ...rest: unknown[]) => (rest.length ? sim.force(name, rest[0] as never) : sim.force(name)),
      this.driftForce,
      3,
    );
    // A new graph -- the first, or another KB or Map in place of this one --
    // is laid out before it is drawn, then drawn opening out of its centre
    // (see bloom) while the camera eases onto it, and the camera is the
    // reader's again. An update to a graph already on screen is never ticked
    // ahead, so every move it causes is drawn and nothing jumps.
    if (first || replace) {
      sim.alpha(1);
      sim.tick(warmupTicks);
      this.touched = false;
      if (!this.reducedMotion && !this.offscreen) {
        // Kept warm while it opens (applyMotion): the nodes are already
        // moving as the graph unfolds, and when it is open the motion cools
        // into the usual drift with no seam between the two.
        sim.alpha(BLOOM_ALPHA);
        const c = nodes.reduce(
          (s, n) => ({ x: s.x + (n.x ?? 0) / nodes.length, y: s.y + (n.y ?? 0) / nodes.length, z: s.z + (n.z ?? 0) / nodes.length }),
          { x: 0, y: 0, z: 0 },
        );
        // Each node its own flight: a small start delay and its own speed,
        // stable per id, so the burst is ragged like a real one.
        const flight = new Map<SceneNode, { delay: number; span: number }>();
        for (const n of nodes) {
          const h = hashUnit(n.id);
          flight.set(n, { delay: h * BLOOM_STAGGER, span: 1 - BLOOM_STAGGER - ((h * 7.31) % 1) * BLOOM_SPEED_SPREAD });
        }
        this.bloom = { start: this.clock(), centre: c, flight };
      }
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
      const changed = [...nowLinks].filter((k) => !beforeLinks.has(k)).concat([...beforeLinks].filter((k) => !nowLinks.has(k)));
      for (const key of changed) {
        for (const id of key.split("\u0000")) hit(id, KNOCK * 0.6);
      }
      sim.alpha(this.sim?.alpha() ?? 0);
      if (origins.size) {
        if (!this.reducedMotion) for (const [n, strength] of origins) this.knock(n, strength);
        sim.alpha(Math.max(sim.alpha(), KNOCK_ALPHA));
      }
    }
    this.sim = sim;
    this.applyMotion();

    this.sphereNodes = nodes.filter((n) => !n.artifact);
    this.diamondNodes = nodes.filter((n) => n.artifact);
    const sphereSlot = new Map(this.sphereNodes.map((n, i) => [n, i]));
    const diamondSlot = new Map(this.diamondNodes.map((n, i) => [n, i]));
    this.slot = nodes.map((n) => (n.artifact ? diamondSlot.get(n)! : sphereSlot.get(n)!));
    const build = (geometry: SphereGeometry | OctahedronGeometry, count: number) => {
      const mesh = new InstancedMesh(geometry, this.nodeMaterial, Math.max(1, count));
      mesh.count = count;
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
    this.nodeMesh = build(this.sphere, this.sphereNodes.length);
    this.artifactMesh = build(this.diamond, this.diamondNodes.length);
    this.edgeGeometry.setAttribute("position", new BufferAttribute(new Float32Array(links.length * 6), 3).setUsage(DynamicDrawUsage));
    this.applyColours();
    this.setSelection(this.selected);
    this.sync();
    if (first) this.frameAll(0);
    // The burst leaves from the middle of the screen -- where the seed was
    // drawn while the graph loaded -- not from the graph's own centroid,
    // which the framing puts somewhere near it but rarely on it.
    if (this.bloom && (first || replace)) {
      const t = this.controls.target;
      this.bloom.centre = { x: t.x, y: t.y, z: t.z };
    }
    if (first || replace) {
      this.radius = boundingRadius(this.nodes);
      this.settleUntil = this.reducedMotion || this.offscreen ? 0 : this.clock() + SETTLE_MS;
      if (this.reducedMotion && replace) this.frameAll(0);
    } else this.radius = boundingRadius(this.linkedNodes());
  }

  /** Colours per node, in node order, and the scene's own colours. */
  /** Pushes every node reachable from a new one away from it, weaker by
   *  KNOCK_FALLOFF with each link in between. */
  private knock(origin: SceneNode, initial: number): void {
    const push = (n: SceneNode, strength: number) => {
      const dx = (n.x ?? 0) - (origin.x ?? 0);
      const dy = (n.y ?? 0) - (origin.y ?? 0);
      const dz = (n.z ?? 0) - (origin.z ?? 0);
      const d = Math.hypot(dx, dy, dz) || 1;
      n.vx = (n.vx ?? 0) + (dx / d) * strength;
      n.vy = (n.vy ?? 0) + (dy / d) * strength;
      n.vz = (n.vz ?? 0) + (dz / d) * strength;
    };
    const seen = new Set<SceneNode>([origin]);
    let ring = [origin];
    for (let strength = initial; ring.length && strength > KNOCK * 0.05; strength *= KNOCK_FALLOFF) {
      const next: SceneNode[] = [];
      for (const n of ring) {
        for (const m of this.neighbours.get(n.id) ?? []) {
          if (seen.has(m)) continue;
          seen.add(m);
          push(m, strength);
          next.push(m);
        }
      }
      ring = next;
    }
  }

  setColours(colours: string[], palette: ScenePalette): void {
    this.colours = colours;
    this.palette = palette;
    this.applyColours();
  }

  private applyColours(): void {
    const mesh = this.nodeMesh;
    const palette = this.palette;
    if (!mesh || !palette) return;
    const colour = new Color();
    for (let i = 0; i < this.nodes.length; i++) {
      const target = this.nodes[i]!.artifact ? this.artifactMesh : mesh;
      target?.setColorAt(this.slot[i]!, colour.set(this.colours[i] ?? palette.edge));
    }
    for (const m of [mesh, this.artifactMesh]) if (m?.instanceColor) m.instanceColor.needsUpdate = true;
    const background = new Color(palette.background);
    this.renderer.setClearColor(background);
    this.scene.fog = new Fog(background, 1, 2);
    this.edgeMaterial.color.set(palette.edge);
    this.activeMaterial.color.set(palette.edgeActive);
    this.signalMaterial.color.set(palette.signal);
    (this.ring.material as MeshBasicMaterial).color.set(palette.ring);
    this.updateFog();
  }

  // --- selection ----------------------------------------------------------

  setSelection(id: string | null): void {
    const node = id ? this.nodes[this.index.get(id) ?? -1] : undefined;
    this.selected = node ? node.id : null;
    this.activeLinks = node ? this.links.filter((l) => endpoint(l.source) === node.id || endpoint(l.target) === node.id) : [];
    this.activeGeometry.setAttribute("position", new BufferAttribute(new Float32Array(this.activeLinks.length * 6), 3).setUsage(DynamicDrawUsage));
    this.ring.visible = !!node;
    this.idle.setSelected(!!node);
    this.edgeMaterial.opacity = node ? EDGE_BEHIND_SELECTION : EDGE_REST;
    this.burst = null;
    this.signalGeometry.setAttribute("position", new BufferAttribute(new Float32Array(MAX_SIGNALS * 3), 3).setUsage(DynamicDrawUsage));
    this.signals.visible = false;
  }

  /** Signals for the current selection (planned by motion.planBursts). */
  sendSignals(plan: Signal<SceneLink>[]): void {
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
  project(node: SceneNode): { x: number; y: number } | null {
    const v = new Vector3(node.x ?? 0, node.y ?? 0, node.z ?? 0).project(this.camera);
    if (v.z < -1 || v.z > 1) return null;
    const w = this.canvas.clientWidth;
    const h = this.canvas.clientHeight;
    return { x: ((v.x + 1) / 2) * w, y: ((1 - v.y) / 2) * h };
  }

  radiusOf(node: SceneNode): number {
    return LEAF_RADIUS + (HUB_RADIUS - LEAF_RADIUS) * node.weight;
  }

  // --- motion and camera --------------------------------------------------

  setLive(live: boolean): void {
    this.live = live && !this.reducedMotion;
    this.idle.setLive(this.live);
    this.applyMotion();
    if (!this.live) {
      this.burst = null;
      this.signals.visible = false;
    }
  }

  /** Drift and the live simulation follow the motion toggle alone: a
   *  selection keeps the network breathing (the rest recedes instead). */
  private applyMotion(): void {
    const drifting = this.live;
    this.driftForce.enabled(drifting);
    const target = drifting ? LIVE_ALPHA : 0;
    this.sim?.alphaTarget(this.bloom ? Math.max(target, BLOOM_WARMTH) : target);
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
    this.animateTo(target, this.reducedMotion ? 0 : FOCUS_MS, shift);
    this.follow = new Vector3(node.x, node.y, node.z);
  }

  unfocus(pose: Pose | null): void {
    this.follow = null;
    if (pose) this.animateTo(() => pose, this.reducedMotion ? 0 : FOCUS_MS, 0);
    else this.setShift(0);
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
  frameAll(ms = this.reducedMotion ? 0 : FOCUS_MS): void {
    this.fitEverything(0, ms);
  }

  /** Frames every drawn node, outliers and loose ones included, in the
   *  canvas above `occludedBottom` pixels, from the current viewpoint: the
   *  growth replay keeps the whole graph in sight with it as it grows. */
  fitEverything(occludedBottom: number, ms = this.reducedMotion ? 0 : FOCUS_MS, minDistance = 0): void {
    const pose = this.fitPoseFor(occludedBottom);
    if (!pose) return;
    const along = {
      x: pose.position.x - pose.lookAt.x,
      y: pose.position.y - pose.lookAt.y,
      z: pose.position.z - pose.lookAt.z,
    };
    const distance = Math.hypot(along.x, along.y, along.z);
    if (distance > 0 && distance < minDistance) {
      const k = minDistance / distance;
      pose.position = { x: pose.lookAt.x + along.x * k, y: pose.lookAt.y + along.y * k, z: pose.lookAt.z + along.z * k };
    }
    this.radius = boundingRadius(this.nodes);
    const limits = zoomLimits(this.radius);
    this.controls.minDistance = limits.min;
    this.controls.maxDistance = Math.max(limits.max, Math.max(distance, minDistance) * 1.5);
    this.animateTo(() => pose, ms);
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

  private fitPoseFor(occludedBottom: number): Pose | null {
    const bodies = this.nodes
      .filter((n) => Number.isFinite(n.x) && Number.isFinite(n.y) && Number.isFinite(n.z))
      .map((n) => ({ x: n.x!, y: n.y!, z: n.z!, radius: this.radiusOf(n) }));
    const { width, height } = this.viewSize();
    // The view stays centred on the canvas, so a band at the bottom is kept
    // clear by fitting the height left once it is taken off both edges.
    const usable = Math.max(1, height - 2 * Math.max(0, occludedBottom));
    const fov = (2 * Math.atan(Math.tan((this.camera.fov * Math.PI) / 360) * (usable / Math.max(1, height))) * 180) / Math.PI;
    const pose = fitPose(bodies, this.camera.position, this.controls.target, {
      width,
      height: usable,
      fov,
      occludedRight: 0,
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
    this.animateTo(() => to, this.reducedMotion ? 0 : 220);
  }

  private animateTo(to: () => Pose, ms: number, shift = this.shift): void {
    const from = this.pose();
    if (ms <= 0) {
      this.tween = null;
      this.setShift(shift);
      this.applyPose(to());
      return;
    }
    this.tween = { from, to, start: this.clock(), ms, shiftFrom: this.shift, shiftTo: shift };
    this.idle.setFocusing(true);
  }

  private setShift(px: number): void {
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
    if (this.frame === null && !this.offscreen) this.frame = requestAnimationFrame(this.tick);
  }

  private stop(): void {
    if (this.frame !== null) cancelAnimationFrame(this.frame);
    this.frame = null;
  }

  private ticks = 0;
  private readonly tick = (): void => {
    this.frame = requestAnimationFrame(this.tick);
    this.step(this.clock());
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
    this.step(nowMs);
  }

  /** Where the nodes start, by id (the visible scene's layout): the offline
   *  scene draws the video from the picture on screen. */
  seedPositions(positions: Map<string, { x: number; y: number; z: number }>): void {
    for (const n of this.nodes) {
      const p = positions.get(n.id);
      if (p) Object.assign(n, { x: p.x, y: p.y, z: p.z, vx: 0, vy: 0, vz: 0 });
    }
    this.sync();
  }

  private step(now: number): void {
    const sim = this.sim;
    // Still mode lets the simulation cool and then stops ticking it: at rest
    // the frame costs only the draw.
    if (sim && (this.live || sim.alpha() > 0.002)) {
      sim.tick();
      if (++this.ticks % 30 === 0) sanitize(this.nodes, this.neighbours);
    }
    if (this.tween) {
      const t = Math.min(1, (now - this.tween.start) / this.tween.ms);
      const k = t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2;
      const to = this.tween.to();
      const f = this.tween.from;
      const lerp = (a: number, b: number) => a + (b - a) * k;
      this.setShift(lerp(this.tween.shiftFrom, this.tween.shiftTo));
      this.applyPose({
        position: { x: lerp(f.position.x, to.position.x), y: lerp(f.position.y, to.position.y), z: lerp(f.position.z, to.position.z) },
        lookAt: { x: lerp(f.lookAt.x, to.lookAt.x), y: lerp(f.lookAt.y, to.lookAt.y), z: lerp(f.lookAt.z, to.lookAt.z) },
      });
      if (t >= 1) this.cancelTween();
    } else if (this.follow && this.selected) {
      // Follow the selection as the physics moves it.
      const node = this.nodeById(this.selected);
      if (node && Number.isFinite(node.x)) {
        const d = new Vector3(node.x, node.y, node.z).sub(this.follow);
        this.camera.position.add(d);
        this.controls.target.add(d);
        this.follow.set(node.x!, node.y!, node.z!);
      }
    } else if (now < this.settleUntil && !this.touched && !this.selected) {
      // A graph still settling: ease a little of the way to its fit every
      // frame, so the camera follows it without a jump and arrives as it
      // comes to rest.
      const pose = this.fitPoseFor(0);
      if (pose) {
        const p = this.camera.position;
        const t = this.controls.target;
        p.set(p.x + (pose.position.x - p.x) * SETTLE_EASE, p.y + (pose.position.y - p.y) * SETTLE_EASE, p.z + (pose.position.z - p.z) * SETTLE_EASE);
        t.set(t.x + (pose.lookAt.x - t.x) * SETTLE_EASE, t.y + (pose.lookAt.y - t.y) * SETTLE_EASE, t.z + (pose.lookAt.z - t.z) * SETTLE_EASE);
        this.controls.maxDistance = Math.max(this.controls.maxDistance, p.distanceTo(t) * 1.5);
      }
    }
    this.controls.autoRotate = !this.tween && this.idle.shouldRotate();
    this.controls.update();
    this.updateFog();
    this.sync(now);
    this.renderer.render(this.scene, this.camera);
    for (const listener of this.frameListeners) listener();
  }

  /** Called after each rendered frame (the component places its labels). */
  readonly frameListeners = new Set<() => void>();

  private readonly dummy = new Object3D();
  /** A new graph opening out of its centre: drawn positions run from the
   *  centre to the laid-out ones over BLOOM_MS. Only the drawing moves; the
   *  simulation runs on the true positions throughout. */
  private bloom: {
    start: number;
    centre: { x: number; y: number; z: number };
    flight: Map<SceneNode, { delay: number; span: number }>;
  } | null = null;
  /** Until when a new graph is followed by the camera as it settles. */
  private settleUntil = 0;

  private sync(now = this.clock()): void {
    const mesh = this.nodeMesh;
    if (!mesh) return;
    const d = this.dummy;
    const diamonds = this.artifactMesh;
    // The burst: every node flies out of the seed along its own line,
    // overshoots its place a little and springs back. The panorama keeps
    // turning at its own pace throughout; the burst adds no turn of its own.
    const bloom = this.bloom;
    let t = 1;
    if (bloom) {
      t = Math.min(1, (now - bloom.start) / BLOOM_MS);
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
      d.position.set(px(n), py(n), pz(n));
      d.scale.setScalar(this.radiusOf(n) * (n.artifact ? 1.5 : 1) * (0.2 + 0.8 * Math.min(1, kOf(n))));
      d.updateMatrix();
      (n.artifact ? diamonds : mesh)?.setMatrixAt(this.slot[i]!, d.matrix);
    }
    mesh.instanceMatrix.needsUpdate = true;
    if (diamonds) diamonds.instanceMatrix.needsUpdate = true;

    const write = (attr: BufferAttribute, i: number, l: SceneLink) => {
      const a = l.source as SceneNode;
      const b = l.target as SceneNode;
      const arr = attr.array as Float32Array;
      arr[i * 6] = px(a);
      arr[i * 6 + 1] = py(a);
      arr[i * 6 + 2] = pz(a);
      arr[i * 6 + 3] = px(b);
      arr[i * 6 + 4] = py(b);
      arr[i * 6 + 5] = pz(b);
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

  private readonly raycaster = new Raycaster();
  private readonly ndc = new Vector2();

  /** The node under a screen point: the ray first, then the nearest node
   *  within HIT_RADIUS_PX, so a small or distant node is still easy to take. */
  pick(clientX: number, clientY: number): SceneNode | null {
    const mesh = this.nodeMesh;
    if (!mesh || this.nodes.length === 0) return null;
    const r = this.canvas.getBoundingClientRect();
    this.ndc.set(((clientX - r.left) / r.width) * 2 - 1, -((clientY - r.top) / r.height) * 2 + 1);
    this.raycaster.setFromCamera(this.ndc, this.camera);
    const meshes = this.artifactMesh ? [mesh, this.artifactMesh] : [mesh];
    const hits = this.raycaster.intersectObjects(meshes, false);
    const hit = hits[0];
    if (hit && hit.instanceId !== undefined) {
      const owner = hit.object === this.artifactMesh ? this.diamondNodes : this.sphereNodes;
      return owner[hit.instanceId] ?? null;
    }
    let best: SceneNode | null = null;
    let bestD = HIT_RADIUS_PX * HIT_RADIUS_PX;
    const v = new Vector3();
    for (const n of this.nodes) {
      v.set(n.x ?? 0, n.y ?? 0, n.z ?? 0).project(this.camera);
      if (v.z < -1 || v.z > 1) continue;
      const dx = ((v.x + 1) / 2) * r.width - (clientX - r.left);
      const dy = ((1 - v.y) / 2) * r.height - (clientY - r.top);
      const d = dx * dx + dy * dy;
      if (d < bestD) {
        bestD = d;
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

    const onDown = (e: PointerEvent) => {
      this.touched = true;
      this.idle.input();
      this.cancelTween();
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
    const w = Math.max(1, this.container.clientWidth);
    const h = Math.max(1, this.container.clientHeight);
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
    this.renderer.setSize(w, h);
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
    this.setShift(this.shift);
  }

  dispose(): void {
    this.stop();
    this.sim?.stop();
    this.cleanups.forEach((fn) => fn());
    this.controls.dispose();
    this.nodeMesh?.dispose();
    this.artifactMesh?.dispose();
    for (const g of [this.sphere, this.diamond, this.edgeGeometry, this.activeGeometry, this.signalGeometry, this.ring.geometry]) g.dispose();
    for (const m of [this.nodeMaterial, this.edgeMaterial, this.activeMaterial, this.signalMaterial, this.ring.material as MeshBasicMaterial]) m.dispose();
    this.renderer.dispose();
    // An offline scene is one of two contexts alive at once (the visible one
    // is the other): release it now, not when the collector gets to it.
    if (this.offscreen) this.renderer.forceContextLoss();
    this.canvas.remove();
  }
}
