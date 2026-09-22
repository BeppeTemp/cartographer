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
  MOUSE,
  Mesh,
  MeshBasicMaterial,
  Object3D,
  PerspectiveCamera,
  Points,
  PointsMaterial,
  Raycaster,
  Scene,
  Plane,
  Sphere,
  SphereGeometry,
  TOUCH,
  Vector2,
  Vector3,
  WebGLRenderer,
} from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import { focusPose, framePose, FOCUS_MS, zoomLimits, type Pose } from "./camera";
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
 * Two modes share it: "3d" (orbited; nodes are selected, not grabbed) and
 * "2d" (the same network flat, seen from above; nodes can be dragged). See
 * bindGestures and D234.
 */

export interface SceneNode extends PhysicsNode {
  /** Visual weight, 0..1: 0 a leaf, 1 the best-connected node. */
  weight: number;
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

/** "3d": a network in depth, orbited. "2d": the same network laid flat and
 *  seen from above -- panned, zoomed, and its nodes can be dragged. */
export type SceneMode = "2d" | "3d";

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
const LEAF_RADIUS = LINK_DISTANCE * 0.12;
const HUB_RADIUS = LEAF_RADIUS * 2.36;
/** A press that travels less than this is a click, not a drag or an orbit. */
const CLICK_SLOP_PX = 5;
/** Picking reaches this far around a node on screen: small nodes stay easy
 *  to hit (brand kit: 12-18 px hit area). */
const HIT_RADIUS_PX = 14;

const endpoint = (end: string | SceneNode) => (typeof end === "object" ? end.id : end);

export class LivingScene {
  readonly canvas: HTMLCanvasElement;
  private readonly renderer: WebGLRenderer;
  private readonly scene = new Scene();
  readonly camera = new PerspectiveCamera(44, 1, 1, 50_000);
  readonly controls: OrbitControls;
  private readonly idle: IdleRotation;
  private readonly driftForce: DriftForce<SceneNode>;
  readonly mode: SceneMode;
  private readonly dims: 2 | 3;
  private sim: Simulation<SceneNode> | null = null;

  private nodes: SceneNode[] = [];
  private links: SceneLink[] = [];
  private index = new Map<string, number>();
  private neighbours = new Map<string, SceneNode[]>();

  private readonly sphere = new SphereGeometry(1, 12, 9);
  private nodeMesh: InstancedMesh | null = null;
  private readonly nodeMaterial = new MeshBasicMaterial();
  private readonly edgeGeometry = new BufferGeometry();
  private readonly edgeMaterial = new LineBasicMaterial({ transparent: true, opacity: 0.28, depthWrite: false });
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
  private tween: { from: Pose; to: () => Pose; start: number; ms: number } | null = null;
  private follow: Vector3 | null = null;
  private touched = false;
  private radius = LINK_DISTANCE * 4;
  private readonly cleanups: (() => void)[] = [];

  constructor(
    private readonly container: HTMLElement,
    private readonly callbacks: SceneCallbacks,
    options: { live: boolean; reducedMotion: boolean; mode?: SceneMode },
  ) {
    this.mode = options.mode ?? "3d";
    this.dims = this.mode === "2d" ? 2 : 3;
    this.driftForce = drift<SceneNode>(this.dims);
    this.live = options.live && !options.reducedMotion;
    this.reducedMotion = options.reducedMotion;
    // The panorama is a 3D thing: a flat map does not turn on its own.
    this.idle = new IdleRotation(this.live && this.mode === "3d");

    this.renderer = new WebGLRenderer({ antialias: true, powerPreference: "high-performance" });
    this.canvas = this.renderer.domElement;
    container.appendChild(this.canvas);
    this.controls = new OrbitControls(this.camera, this.canvas);
    this.controls.enableDamping = true;
    this.controls.dampingFactor = 0.08;
    this.controls.rotateSpeed = 0.6;
    this.controls.zoomSpeed = 0.8;
    this.controls.autoRotateSpeed = AUTO_ROTATE_SPEED;
    this.camera.position.set(0, 0, 600);
    if (this.mode === "2d") {
      // Seen from above, always: drag pans, wheel and pinch zoom.
      this.controls.enableRotate = false;
      this.controls.screenSpacePanning = true;
      this.controls.mouseButtons = { LEFT: MOUSE.PAN, MIDDLE: MOUSE.DOLLY, RIGHT: MOUSE.PAN };
      this.controls.touches = { ONE: TOUCH.PAN, TWO: TOUCH.DOLLY_PAN };
    }

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
  setData(nodes: SceneNode[], links: SceneLink[], warmupTicks: number): void {
    const first = this.sim === null;
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
    const sim = forceSimulation<SceneNode>(nodes, this.dims)
      .force("link", forceLink<SceneNode, SceneLink>(links).id((n) => n.id))
      .force("charge", forceManyBody<SceneNode>())
      .velocityDecay(VELOCITY_DECAY)
      .stop();
    configureForces(
      (name, ...rest: unknown[]) => (rest.length ? sim.force(name, rest[0] as never) : sim.force(name)),
      this.driftForce,
      this.dims,
    );
    sim.alpha(first ? 1 : 0.25);
    sim.tick(first ? warmupTicks : Math.round(warmupTicks / 4));
    sim.alphaTarget(this.live ? LIVE_ALPHA : 0);
    this.sim = sim;
    this.driftForce.enabled(this.live);

    this.nodeMesh?.removeFromParent();
    this.nodeMesh?.dispose();
    const mesh = new InstancedMesh(this.sphere, this.nodeMaterial, Math.max(1, nodes.length));
    mesh.count = nodes.length;
    mesh.instanceMatrix.setUsage(DynamicDrawUsage);
    mesh.frustumCulled = false;
    // Nodes move every frame: a bounding sphere that holds everything, so the
    // raycaster never skips the mesh on a stale one.
    mesh.boundingSphere = new Sphere(new Vector3(), Number.POSITIVE_INFINITY);
    this.nodeMesh = mesh;
    this.scene.add(mesh);
    this.edgeGeometry.setAttribute("position", new BufferAttribute(new Float32Array(links.length * 6), 3).setUsage(DynamicDrawUsage));
    this.applyColours();
    this.setSelection(this.selected);
    this.sync();
    if (first) this.frameAll(0);
    else this.radius = boundingRadius(this.linkedNodes());
  }

  /** Colours per node, in node order, and the scene's own colours. */
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
    for (let i = 0; i < this.nodes.length; i++) mesh.setColorAt(i, colour.set(this.colours[i] ?? palette.edge));
    if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
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
    this.burst = null;
    this.signalGeometry.setAttribute("position", new BufferAttribute(new Float32Array(MAX_SIGNALS * 3), 3).setUsage(DynamicDrawUsage));
    this.signals.visible = false;
  }

  /** Signals for the current selection (planned by motion.planBursts). */
  sendSignals(plan: Signal<SceneLink>[]): void {
    this.burst = plan.length ? { start: performance.now(), plan: plan.slice(0, MAX_SIGNALS * 3) } : null;
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
    this.idle.setLive(this.live && this.mode === "3d");
    this.driftForce.enabled(this.live);
    this.sim?.alphaTarget(this.live ? LIVE_ALPHA : 0);
    if (!this.live) {
      this.burst = null;
      this.signals.visible = false;
    }
  }

  /** Eases the camera onto a node's neighbourhood beside `occludedRight`
   *  pixels of panel, then follows it while it moves. */
  focus(id: string, occludedRight: number, savePose: () => void): void {
    const node = this.nodeById(id);
    if (!node) return;
    savePose();
    const from = this.camera.position.clone();
    const target = () =>
      focusPose(node as Required<SceneNode>, from, this.neighbourhoodRadius(node), {
        width: this.canvas.clientWidth,
        height: this.canvas.clientHeight,
        fov: this.camera.fov,
        occludedRight,
      });
    this.animateTo(target, this.reducedMotion ? 0 : FOCUS_MS);
    this.follow = new Vector3(node.x, node.y, node.z);
  }

  unfocus(pose: Pose | null): void {
    this.follow = null;
    if (pose) this.animateTo(() => pose, this.reducedMotion ? 0 : FOCUS_MS);
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

  /** Frames the whole visible graph, from the current viewpoint. */
  frameAll(ms = this.reducedMotion ? 0 : FOCUS_MS): void {
    const body = this.linkedNodes();
    this.radius = boundingRadius(body);
    const c = body.reduce(
      (s, n) => ({ x: s.x + (n.x ?? 0) / body.length, y: s.y + (n.y ?? 0) / body.length, z: s.z + (n.z ?? 0) / body.length }),
      { x: 0, y: 0, z: 0 },
    );
    const limits = zoomLimits(this.radius);
    this.controls.minDistance = limits.min;
    this.controls.maxDistance = limits.max;
    const pose = framePose(c, this.radius, this.camera.position, this.controls.target, {
      width: this.canvas.clientWidth,
      height: this.canvas.clientHeight,
      fov: this.camera.fov,
      occludedRight: 0,
    });
    this.animateTo(() => pose, ms);
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

  /** Shakes the layout loose and lets it settle again. */
  relax(): void {
    this.sim?.alpha(0.6);
  }

  /** Re-frame once the layout has settled, unless the reader took the camera. */
  refitIfUntouched(): void {
    if (!this.touched && !this.selected) this.frameAll();
  }

  private animateTo(to: () => Pose, ms: number): void {
    const from = this.pose();
    if (ms <= 0) {
      this.tween = null;
      this.applyPose(to());
      return;
    }
    this.tween = { from, to, start: performance.now(), ms };
    this.idle.setFocusing(true);
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
    if (this.mode === "2d") {
      // A flat map has no far side to fade.
      fog.near = 1e9;
      fog.far = 2e9;
      return;
    }
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
    if (this.frame === null) this.frame = requestAnimationFrame(this.tick);
  }

  private stop(): void {
    if (this.frame !== null) cancelAnimationFrame(this.frame);
    this.frame = null;
  }

  private ticks = 0;
  private readonly tick = (now: number): void => {
    this.frame = requestAnimationFrame(this.tick);
    const sim = this.sim;
    // Still mode lets the simulation cool and then stops ticking it: at rest
    // the frame costs only the draw.
    if (sim && (this.live || this.dragging || sim.alpha() > 0.002)) {
      sim.tick();
      if (++this.ticks % 30 === 0) sanitize(this.nodes, this.neighbours);
    }
    if (this.tween) {
      const t = Math.min(1, (now - this.tween.start) / this.tween.ms);
      const k = t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2;
      const to = this.tween.to();
      const f = this.tween.from;
      const lerp = (a: number, b: number) => a + (b - a) * k;
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
    }
    this.controls.autoRotate = !this.tween && this.idle.shouldRotate();
    this.controls.update();
    this.updateFog();
    this.sync(now);
    this.renderer.render(this.scene, this.camera);
    for (const listener of this.frameListeners) listener();
  };

  /** Called after each rendered frame (the component places its labels). */
  readonly frameListeners = new Set<() => void>();

  private readonly dummy = new Object3D();
  private sync(now = performance.now()): void {
    const mesh = this.nodeMesh;
    if (!mesh) return;
    const d = this.dummy;
    for (let i = 0; i < this.nodes.length; i++) {
      const n = this.nodes[i]!;
      d.position.set(n.x ?? 0, n.y ?? 0, n.z ?? 0);
      d.scale.setScalar(this.radiusOf(n));
      d.updateMatrix();
      mesh.setMatrixAt(i, d.matrix);
    }
    mesh.instanceMatrix.needsUpdate = true;

    const write = (attr: BufferAttribute, i: number, l: SceneLink) => {
      const a = l.source as SceneNode;
      const b = l.target as SceneNode;
      const arr = attr.array as Float32Array;
      arr[i * 6] = a.x ?? 0;
      arr[i * 6 + 1] = a.y ?? 0;
      arr[i * 6 + 2] = a.z ?? 0;
      arr[i * 6 + 3] = b.x ?? 0;
      arr[i * 6 + 4] = b.y ?? 0;
      arr[i * 6 + 5] = b.z ?? 0;
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
    const hits = this.raycaster.intersectObject(mesh);
    if (hits.length && hits[0]!.instanceId !== undefined) return this.nodes[hits[0]!.instanceId] ?? null;
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
   * 3D: nodes are selected, never grabbed -- every drag orbits, the wheel and
   * a pinch zoom. 2D: a drag that starts on a node moves that node (pinned in
   * the simulation, so its links pull the rest and let go it settles); any
   * other drag pans. A press that does not travel is a click: on a node it
   * selects it, on the background it clears the selection. Picking runs on
   * press, click and a throttled hover, never per frame.
   */
  private dragging: SceneNode | null = null;
  private readonly plane = new Plane(new Vector3(0, 0, 1), 0);
  private readonly hit = new Vector3();

  private bindGestures(): void {
    const canvas = this.canvas;
    const pointers = new Set<number>();
    let press: { x: number; y: number; moved: boolean; node: SceneNode | null } | null = null;
    let lastHover = 0;

    const release = () => {
      const node = this.dragging;
      if (!node) return;
      node.fx = node.fy = node.fz = undefined;
      this.dragging = null;
      this.sim?.alphaTarget(this.live ? LIVE_ALPHA : 0);
      this.controls.enabled = true;
      canvas.style.cursor = "";
    };

    const onDown = (e: PointerEvent) => {
      this.touched = true;
      this.idle.input();
      this.cancelTween();
      pointers.add(e.pointerId);
      if (pointers.size > 1) {
        // A second contact is a pinch: it ends a node drag in place.
        release();
        press = null;
        return;
      }
      const node = this.mode === "2d" ? this.pick(e.clientX, e.clientY) : null;
      press = { x: e.clientX, y: e.clientY, moved: false, node };
      if (node) {
        // Claim the gesture before OrbitControls sees it (capture phase).
        this.controls.enabled = false;
        this.dragging = node;
        this.follow = null;
        canvas.setPointerCapture(e.pointerId);
      }
    };

    const onMove = (e: PointerEvent) => {
      if (press && Math.hypot(e.clientX - press.x, e.clientY - press.y) > CLICK_SLOP_PX) press.moved = true;
      const node = this.dragging;
      if (node && press?.moved) {
        const r = canvas.getBoundingClientRect();
        this.ndc.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
        this.raycaster.setFromCamera(this.ndc, this.camera);
        if (this.raycaster.ray.intersectPlane(this.plane, this.hit) && Number.isFinite(this.hit.x)) {
          node.fx = node.x = this.hit.x;
          node.fy = node.y = this.hit.y;
          this.sim?.alphaTarget(0.3);
          if (this.sim && this.sim.alpha() < 0.1) this.sim.alpha(0.1);
        }
        canvas.style.cursor = "grabbing";
        return;
      }
      if (pointers.size > 0) {
        this.idle.input();
        return;
      }
      if (e.timeStamp - lastHover > 50) {
        lastHover = e.timeStamp;
        const hover = this.pick(e.clientX, e.clientY);
        canvas.style.cursor = hover ? (this.mode === "2d" ? "grab" : "pointer") : "";
        const r = canvas.getBoundingClientRect();
        this.callbacks.onHover(hover?.id ?? null, e.clientX - r.left, e.clientY - r.top);
      }
    };

    const onUp = (e: PointerEvent) => {
      pointers.delete(e.pointerId);
      const p = press;
      press = null;
      release();
      if (p && !p.moved && pointers.size === 0) this.callbacks.onSelect((p.node ?? this.pick(p.x, p.y))?.id ?? null);
    };
    const onCancel = (e: PointerEvent) => {
      pointers.delete(e.pointerId);
      press = null;
      release();
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

  private resize(): void {
    const w = Math.max(1, this.container.clientWidth);
    const h = Math.max(1, this.container.clientHeight);
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
    this.renderer.setSize(w, h);
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
  }

  dispose(): void {
    this.stop();
    this.sim?.stop();
    this.cleanups.forEach((fn) => fn());
    this.controls.dispose();
    this.nodeMesh?.dispose();
    for (const g of [this.sphere, this.edgeGeometry, this.activeGeometry, this.signalGeometry, this.ring.geometry]) g.dispose();
    for (const m of [this.nodeMaterial, this.edgeMaterial, this.activeMaterial, this.signalMaterial, this.ring.material as MeshBasicMaterial]) m.dispose();
    this.renderer.dispose();
    this.canvas.remove();
  }
}
