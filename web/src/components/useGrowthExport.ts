import { useEffect, useRef, useState } from "react";
import { GROWTH_MIN_ZOOM, growthOrder, growthPace, type GrowthStep } from "../lib/graph3d/growth";
import { beside } from "../lib/graph3d/physics";
import {
  ASPECTS,
  END_CARD_MS,
  FPS,
  FRAME_MS,
  GrowthEncoder,
  HOLD_MS,
  OPENING_MS,
  downloadBlob,
  exportFileName,
  isDark,
  loadMark,
  pickCodec,
  type Aspect,
  type Overlay,
} from "../lib/graph3d/record";
import type { SceneLink, SceneNode, ScenePalette } from "../lib/graph3d/scene";
import { cssVar } from "../lib/palette";
import { formatDay } from "./GrowthTimeline";

/** The picture on screen the video starts from: where every concept is, how
 *  heavy it is, what colour it wears. Read when the export starts, so the
 *  export owns its data from then on (it outlives a switch of KB or Map). */
export interface ExportLayout {
  positions: Map<string, { x: number; y: number; z: number }>;
  weights: Map<string, number>;
  colourOf(id: string): string;
  palette: ScenePalette;
}

interface Params {
  loadBirths?: () => Promise<Record<string, string>>;
  nodes: { id: string; pagerank?: number }[];
  edges: { source: string; target: string }[];
  kbName: string;
  /** The visible scene's layout and colours, as they are now. */
  layout(): ExportLayout;
  /** Motion on: the video drifts and turns like the live view. */
  live: boolean;
}

export interface ExportStatus {
  aspect: Aspect;
  /** 0..1 */
  progress: number;
  done: boolean;
}

/** The offline scene is drawn at twice the export size and downsampled. */
const PIXEL_RATIO = 2;
/** The camera refits every half second of video time, as the visible replay. */
const REFIT_FRAMES = FPS / 2;
const REFIT_MS = 900;
/** How long the pill says it is done. */
const DONE_PILL_MS = 4000;

/** One export at a time, across every graph view. */
let running = false;

const hasEncoder = () => typeof VideoEncoder !== "undefined" && typeof VideoFrame !== "undefined";

/** The date being replayed: the last birth day at or before the last concept
 *  out (a concept with no history is the newest, so it keeps the day before). */
function dayOf(order: GrowthStep[], shown: number): string {
  for (let i = Math.min(shown, order.length) - 1; i >= 0; i--) {
    const born = order[i]!.born;
    if (born) return formatDay(Date.parse(born));
  }
  return "";
}

/** How many concepts are out `ms` into the replay, at the replay's own pace
 *  (one step of `perTick` every `tickMs`, from the first concept). */
export function shownAt(ms: number, total: number, pace: { tickMs: number; perTick: number }): number {
  return Math.min(total, 1 + Math.floor(ms / pace.tickMs) * pace.perTick);
}

/** How many video frames the growth part lasts: the replay's own length. */
export function growthFrames(total: number, pace: { tickMs: number; perTick: number }): number {
  return Math.round((Math.ceil(total / pace.perTick) * pace.tickMs) / FRAME_MS);
}

interface Job {
  aspect: Aspect;
  p: Params;
  layout: ExportLayout;
  cancelled(): boolean;
  progress(done: number, total: number): void;
}

/**
 * Renders the growth replay offline (D691) and returns the file. Its own
 * LivingScene, in a detached container, is advanced by a virtual clock of
 * exactly 1000/60 ms a frame and drawn at twice the export size; each frame is
 * downsampled, overlaid and encoded. The visible scene is never touched.
 */
async function render(job: Job): Promise<{ blob: Blob; ext: "mp4" | "webm" }> {
  const { aspect, p, layout } = job;
  const { width, height } = ASPECTS[aspect];
  const background = cssVar("--surface-0");
  const mark = await loadMark(`${import.meta.env.BASE_URL}brand/coordinate-micro-${isDark(background) ? "ivory" : "pine"}.svg`);
  const encoder = await GrowthEncoder.create({
    width,
    height,
    mark,
    theme: {
      background,
      text: cssVar("--text-primary"),
      muted: cssVar("--text-secondary"),
      font: getComputedStyle(document.body).fontFamily || "sans-serif",
    },
  });
  if (!encoder) throw new Error("This browser cannot encode video");
  let scene: import("../lib/graph3d/scene").LivingScene | null = null;
  try {
    const births = await p.loadBirths!();
    if (job.cancelled()) throw new Error("cancelled");
    const order = growthOrder(p.nodes, p.edges, births);
    const pace = growthPace(order.length);
    const total = order.length;
    const growth = growthFrames(total, pace);
    const opening = Math.round(OPENING_MS / FRAME_MS);
    const hold = Math.round(HOLD_MS / FRAME_MS);
    const endCard = Math.round(END_CARD_MS / FRAME_MS);
    const frames = opening + growth + hold + endCard;
    let done = 0;
    const tick = () => job.progress(++done, frames);

    // Frame 0 is the opening card: it is the thumbnail a messaging app shows.
    for (let f = 0; f < opening; f++) {
      if (job.cancelled()) throw new Error("cancelled");
      await encoder.drawOpening(p.kbName);
      tick();
    }

    const { LivingScene } = await import("../lib/graph3d/scene");
    const host = document.createElement("div");
    scene = new LivingScene(
      host,
      { onSelect() {}, onHover() {}, onLost() {} },
      { live: p.live, reducedMotion: false, offscreen: { width, height, pixelRatio: PIXEL_RATIO } },
    );
    const sc = scene;
    const byId = new Map<string, SceneNode>(
      p.nodes.map((n) => [n.id, { id: n.id, weight: layout.weights.get(n.id) ?? 0 }]),
    );
    const edgesOf = new Map<string, string[]>(p.nodes.map((n) => [n.id, []]));
    for (const e of p.edges) {
      edgesOf.get(e.source)?.push(e.target);
      edgesOf.get(e.target)?.push(e.source);
    }
    const link = (ids: Set<string>): SceneLink[] =>
      p.edges.filter((e) => ids.has(e.source) && ids.has(e.target)).map((e) => ({ source: e.source, target: e.target }));
    const colours = (nodes: SceneNode[]) => sc.setColours(nodes.map((n) => layout.colourOf(n.id)), layout.palette);

    // The whole graph first, from the layout on screen, to learn how far the
    // camera may come (the visible replay does the same before it hides it).
    const everyone = order.map((s) => byId.get(s.id)!);
    sc.setData(everyone, link(new Set(everyone.map((n) => n.id))), 0);
    sc.seedPositions(layout.positions);
    colours(everyone);
    const floor = sc.fitDistance(0) * GROWTH_MIN_ZOOM;

    let out = new Set<string>();
    let shown = 0;
    const show = (count: number) => {
      const nodes = order.slice(0, count).map((s) => byId.get(s.id)!);
      const ids = new Set(nodes.map((n) => n.id));
      // A concept born in the replay sprouts beside one already out, not
      // where the full layout had it, as in the visible replay.
      for (const n of nodes) {
        if (out.has(n.id)) continue;
        const anchor = (edgesOf.get(n.id) ?? []).find((id) => out.has(id));
        Object.assign(n, anchor ? beside(byId.get(anchor)!, n.id) : { x: 0, y: 0, z: 0 }, { vx: 0, vy: 0, vz: 0 });
      }
      out = ids;
      sc.setData(nodes, link(ids), 0);
      colours(nodes);
      shown = count;
    };

    // Replay time is virtual time: frame n is n * 1000/60 ms, whatever the
    // machine's speed. No timer paces this loop (a hidden tab throttles
    // timers, not promises): the encoder's queue is the only brake.
    const frame = async (f: number, overlay: Overlay) => {
      if (job.cancelled()) throw new Error("cancelled");
      sc.renderFrame(f * FRAME_MS);
      // Copy before awaiting anything: the canvas keeps its frame only for
      // the length of the task.
      await encoder.drawFrame(sc.canvas, overlay);
      tick();
    };
    let f = 0;
    for (let g = 0; g < growth; g++, f++) {
      const count = shownAt(g * FRAME_MS, total, pace);
      if (count !== shown) show(count);
      if (g === 0) sc.fitEverything(0, 0, floor);
      else if (g % REFIT_FRAMES === 0) sc.fitEverything(0, REFIT_MS, floor);
      await frame(f, { kb: p.kbName, day: dayOf(order, shown), count: shown });
    }
    show(total);
    for (let h = 0; h < hold; h++, f++) {
      if (h === 0) sc.fitEverything(0, REFIT_MS, floor);
      await frame(f, { kb: p.kbName, day: dayOf(order, total), count: total });
    }
    for (let e = 0; e < endCard; e++) {
      if (job.cancelled()) throw new Error("cancelled");
      await encoder.drawEndCard(p.kbName, total);
      tick();
    }
    return { blob: await encoder.finish(), ext: encoder.ext };
  } catch (err) {
    encoder.cancel();
    throw err;
  } finally {
    // Done, cancelled or failed: the offline context goes with the export.
    scene?.dispose();
  }
}

/**
 * Exports the growth replay as a video, in the background (D691): the export
 * runs on its own scene and its own data, so the replay, the graph and the
 * rest of the Atlas stay usable, a hidden tab does not slow it, and switching
 * KB or Map does not end it. Only leaving the view, or Cancel, does.
 */
export function useGrowthExport(p: Params) {
  const [dialog, setDialog] = useState(false);
  const [status, setStatus] = useState<ExportStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  // null: not asked yet (or no encoder at all)
  const [ext, setExt] = useState<"mp4" | "webm" | null>(null);
  const abort = useRef<(() => void) | null>(null);
  const alive = useRef(true);
  const params = useRef(p);
  params.current = p;

  const supported = !!p.loadBirths && hasEncoder();
  useEffect(() => {
    if (!supported) return;
    let stale = false;
    void pickCodec((c) => VideoEncoder.isConfigSupported(c)).then((c) => !stale && setExt(c?.ext ?? null));
    return () => {
      stale = true;
    };
  }, [supported]);
  // Known to be WebM only: the dialog says so. While the browser has not
  // answered yet, it says nothing.
  const webm = supported && ext === "webm";

  const start = async (aspect: Aspect) => {
    const job = params.current;
    setDialog(false);
    if (!job.loadBirths || running) return;
    running = true;
    setError(null);
    let cancelled = false;
    let doneTimer = 0;
    abort.current = () => {
      cancelled = true;
    };
    setStatus({ aspect, progress: 0, done: false });
    try {
      const { blob, ext: kind } = await render({
        aspect,
        p: job,
        layout: job.layout(),
        cancelled: () => cancelled,
        progress: (done, total) => {
          if (alive.current && !cancelled) setStatus((s) => (s ? { ...s, progress: done / total } : s));
        },
      });
      if (cancelled) return;
      downloadBlob(blob, exportFileName(job.kbName, aspect, kind));
      if (alive.current) {
        setStatus({ aspect, progress: 1, done: true });
        doneTimer = window.setTimeout(() => alive.current && setStatus(null), DONE_PILL_MS);
      }
    } catch (err) {
      if (!cancelled) {
        console.warn("Atlas: the video export failed", err);
        if (alive.current) {
          setStatus(null);
          setError("The video could not be rendered. Nothing was saved.");
        }
      }
    } finally {
      running = false;
      abort.current = null;
      if (cancelled && alive.current) setStatus(null);
      void doneTimer;
    }
  };

  // Leaving the view ends an export in progress.
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      abort.current?.();
    };
  }, []);

  return {
    supported,
    webm,
    dialog,
    openDialog: () => setDialog(true),
    closeDialog: () => setDialog(false),
    start,
    cancel: () => {
      abort.current?.();
      setStatus(null);
    },
    status,
    error,
    dismissError: () => setError(null),
  };
}
