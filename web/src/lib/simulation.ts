import type Graph from "graphology";
import FA2Layout from "graphology-layout-forceatlas2/worker";
import { layoutSettings, seededUnit } from "./layout";

/**
 * What makes the graph feel alive rather than printed.
 *
 * Two independent mechanisms, because they answer two different needs:
 *
 *  - the **supervisor** is the real force simulation, run in a worker. It
 *    settles a new graph and re-settles the neighbourhood while a node is
 *    being dragged, then stops. Leaving it running forever would burn a core
 *    for a picture that has already converged.
 *
 *  - the **drift** is a per-node sine offset applied at render time only. It
 *    is what gives the resting graph its slow breathing. Crucially it never
 *    touches the stored coordinates, so the cached layout, the fingerprint and
 *    the "same KB state renders the same picture" property all survive: the
 *    graph breathes around its deterministic position instead of wandering
 *    away from it.
 *
 * Both are off under prefers-reduced-motion, where a constantly moving
 * interface is not a delight but a barrier.
 */

/** Amplitude as a share of the layout's own extent, so the breath is the same
 *  size relative to the picture whatever scale ForceAtlas2 settled on. An
 *  absolute value looks right on one graph and invisible or seasick on the
 *  next. */
const DRIFT_AMPLITUDE_RATIO = 0.012;
/** Radians per millisecond. One full cycle is roughly twelve seconds. */
const DRIFT_SPEED = 0.00052;
/** Above this many nodes the drift is switched off: writing a coordinate per
 *  node per frame stops being free, and a dense graph reads as noise when it
 *  moves. */
export const DRIFT_NODE_LIMIT = 400;
/** The drift runs at 30fps, not 60: it is a twelve-second cycle, so the extra
 *  frames buy nothing and cost a coordinate write per node. */
const DRIFT_FRAME_MS = 33;

export interface DriftOffset {
  dx: number;
  dy: number;
}

/**
 * driftOffset is a pure function of the node id and the clock, so two tabs
 * showing the same graph at the same moment agree, and a test can assert it
 * without a renderer.
 */
export function driftOffset(
  id: string,
  elapsedMs: number,
  amplitude = 1,
  group?: string,
): DriftOffset {
  const own = ellipse(id, elapsedMs);
  if (group === undefined) {
    return { dx: own.dx * amplitude, dy: own.dy * amplitude };
  }
  // Most of the motion is shared with the node's community, a little is its
  // own. Independent per-node wobble is what made the first pass read as
  // stiff: every node jittering on its own is noise, while a cluster that
  // sways as one body -- with its members shifting slightly inside it -- reads
  // as mass held together by its links.
  const shared = ellipse(`community:${group}`, elapsedMs);
  return {
    dx: (shared.dx * GROUP_SHARE + own.dx * (1 - GROUP_SHARE)) * amplitude,
    dy: (shared.dy * GROUP_SHARE + own.dy * (1 - GROUP_SHARE)) * amplitude,
  };
}

/** The share of a node's drift it takes from its community. */
const GROUP_SHARE = 0.7;

function ellipse(key: string, elapsedMs: number): DriftOffset {
  const phaseX = seededUnit(key, 11) * Math.PI * 2;
  const phaseY = seededUnit(key, 12) * Math.PI * 2;
  // Slightly different rates per axis, so nodes trace small ellipses rather
  // than sliding back and forth along one line.
  const rate = 0.75 + seededUnit(key, 13) * 0.5;
  return {
    dx: Math.sin(elapsedMs * DRIFT_SPEED * rate + phaseX),
    dy: Math.cos(elapsedMs * DRIFT_SPEED * rate * 0.82 + phaseY),
  };
}

export interface Simulation {
  /** Run the force layout for a while, then stop on its own. */
  nudge(durationMs?: number): void;
  /** Keep it running until release() -- used for the length of a drag. */
  hold(): void;
  release(): void;
  stop(): void;
  kill(): void;
}

/**
 * createSimulation wires a ForceAtlas2 supervisor to the graph. The worker
 * keeps the main thread free, which is what lets a drag stay at frame rate
 * while several hundred nodes reorganise around it.
 *
 * Returns null when a worker cannot be spawned. The supervisor builds its
 * worker from a blob URL, which fails wherever `URL.createObjectURL` is absent
 * or a Content-Security-Policy forbids `worker-src blob:`. That must degrade to
 * a still graph, never to a dead page: the layout is already computed and
 * cached, so everything except the live re-settling keeps working.
 */
export function createSimulation(graph: Graph): Simulation | null {
  let layout: FA2Layout;
  try {
    // The same physics as the deterministic layout, with a higher slowDown:
    // that is what turns a force layout from a spring that snaps into one
    // that settles -- the same forces, applied gently enough to watch.
    layout = new FA2Layout(graph, { settings: layoutSettings(graph, 14) });
  } catch (err) {
    console.warn("Atlas: the force layout worker is unavailable, the graph will not re-settle", err);
    return null;
  }

  let timer: number | undefined;
  let held = 0;

  const clearTimer = () => {
    if (timer !== undefined) {
      window.clearTimeout(timer);
      timer = undefined;
    }
  };

  const stop = () => {
    clearTimer();
    if (layout.isRunning()) layout.stop();
  };

  return {
    nudge(durationMs = 1500) {
      clearTimer();
      if (!layout.isRunning()) layout.start();
      if (held > 0) return;
      timer = window.setTimeout(() => {
        timer = undefined;
        if (held === 0) layout.stop();
      }, durationMs);
    },
    hold() {
      held++;
      clearTimer();
      if (!layout.isRunning()) layout.start();
    },
    release() {
      held = Math.max(0, held - 1);
      if (held === 0) this.nudge(900);
    },
    stop,
    kill() {
      clearTimer();
      layout.kill();
    },
  };
}

/**
 * driftAmplitude sizes the breath from the graph's own bounding box.
 */
export function driftAmplitude(graph: Graph): number {
  if (graph.order === 0) return 0;
  let minX = Infinity;
  let maxX = -Infinity;
  let minY = Infinity;
  let maxY = -Infinity;
  graph.forEachNode((_, attrs) => {
    const x = attrs.x as number;
    const y = attrs.y as number;
    if (x < minX) minX = x;
    if (x > maxX) maxX = x;
    if (y < minY) minY = y;
    if (y > maxY) maxY = y;
  });
  const extent = Math.max(maxX - minX, maxY - minY);
  return Number.isFinite(extent) && extent > 0 ? extent * DRIFT_AMPLITUDE_RATIO : 1;
}

export interface DriftController {
  stop(): void;
  /** The deterministic coordinates the drift oscillates around, which are what
   *  gets cached -- never the drifted ones. */
  basePositions(): Record<string, { x: number; y: number }>;
  /** Re-reads the current coordinates as the new resting place. Called after a
   *  drag or a re-settle, where the graph genuinely moved. */
  rebase(): void;
  /** While a node is held, it must not also breathe. */
  exclude(id: string | null): void;
}

/**
 * startDrift writes the breathing offset onto the graph's own coordinates.
 *
 * Writing to the graph rather than adjusting x/y inside Sigma's node reducer is
 * deliberate: the reducer runs as part of Sigma's indexation pipeline, so
 * whether a coordinate changed there reaches the screen depends on when it is
 * applied relative to normalisation. Graphology's attribute events are the
 * documented path, and Sigma re-renders from them on its own -- no per-frame
 * refresh() call, and no dependency on an ordering that is not ours.
 *
 * The deterministic positions are kept aside, so the cache and the "same KB
 * renders the same picture" property survive a graph that is always moving.
 */
export function startDrift(graph: Graph): DriftController {
  let base: Record<string, { x: number; y: number }> = {};
  const rebase = () => {
    base = {};
    graph.forEachNode((id, attrs) => {
      base[id] = { x: attrs.x as number, y: attrs.y as number };
    });
  };
  rebase();

  let amplitude = driftAmplitude(graph);
  let excluded: string | null = null;
  let frame = 0;
  let last = 0;
  const started = performance.now();

  const step = (now: number) => {
    frame = requestAnimationFrame(step);
    if (now - last < DRIFT_FRAME_MS) return;
    last = now;
    const elapsed = now - started;
    for (const id of Object.keys(base)) {
      if (id === excluded || !graph.hasNode(id)) continue;
      const community = graph.getNodeAttribute(id, "community") as number | undefined;
      const group = community !== undefined && community >= 0 ? String(community) : undefined;
      const { dx, dy } = driftOffset(id, elapsed, amplitude, group);
      graph.setNodeAttribute(id, "x", base[id]!.x + dx);
      graph.setNodeAttribute(id, "y", base[id]!.y + dy);
    }
  };
  frame = requestAnimationFrame(step);

  return {
    stop() {
      cancelAnimationFrame(frame);
    },
    basePositions: () => ({ ...base }),
    rebase() {
      rebase();
      amplitude = driftAmplitude(graph);
    },
    exclude(id) {
      excluded = id;
    },
  };
}
