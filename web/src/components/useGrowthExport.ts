import { useEffect, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction } from "react";
import { growthOrder, type GrowthStep } from "../lib/graph3d/growth";
import {
  ASPECTS,
  END_CARD_MS,
  GrowthRecorder,
  HOLD_MS,
  downloadBlob,
  exportFileName,
  isDark,
  loadMark,
  pickMimeType,
  type Aspect,
  type Overlay,
} from "../lib/graph3d/record";
import type { LivingScene } from "../lib/graph3d/scene";
import { cssVar } from "../lib/palette";
import { formatDay } from "./GrowthTimeline";

export interface GrowthState {
  order: GrowthStep[];
  shown: number;
  playing: boolean;
}

interface Params {
  sceneRef: MutableRefObject<LivingScene | null>;
  loadBirths?: () => Promise<Record<string, string>>;
  nodes: { id: string; pagerank?: number }[];
  edges: { source: string; target: string }[];
  kbName: string;
  growth: GrowthState | null;
  setGrowth: Dispatch<SetStateAction<GrowthState | null>>;
  /** Where the replay's camera floor is set, once the scene has the export's
   *  frame (the fit depends on its aspect). */
  setFloor(scene: LivingScene): void;
  /** A new layout (another KB or Map) ends an export, like it ends a replay. */
  layoutKey: string;
}

const hasRecorder = () => typeof MediaRecorder !== "undefined" && typeof HTMLCanvasElement.prototype.captureStream === "function";

/** The date being replayed: the last birth day at or before the last concept
 *  out (a concept with no history is the newest, so it keeps the day before). */
function dayOf(g: GrowthState): string {
  for (let i = Math.min(g.shown, g.order.length) - 1; i >= 0; i--) {
    const born = g.order[i]!.born;
    if (born) return formatDay(Date.parse(born));
  }
  return "";
}

/**
 * Exports the growth replay as a video (D677): records the replay from its
 * first concept at one of three aspects, in the browser, and downloads it.
 * The scene is put in capture mode for the length of the export and restored
 * whatever happens -- done, cancelled, failed or unmounted.
 */
export function useGrowthExport(p: Params) {
  const [dialog, setDialog] = useState(false);
  const [recording, setRecording] = useState<{ aspect: Aspect; seconds: number; paused: boolean } | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Ends the export in progress, restoring the view; null when none runs.
  const abort = useRef<(() => void) | null>(null);
  // The latest replay state, read by the frame listener without re-binding it.
  const growthRef = useRef(p.growth);
  growthRef.current = p.growth;
  const params = useRef(p);
  params.current = p;

  const supported = !!p.loadBirths && hasRecorder();
  const webm = supported && !pickMimeType((m) => MediaRecorder.isTypeSupported(m)).mime.startsWith("video/mp4");

  const start = async (aspect: Aspect) => {
    const { sceneRef, loadBirths, nodes, edges, kbName, setGrowth, setFloor } = params.current;
    const scene = sceneRef.current;
    if (!scene || !loadBirths || abort.current) return;
    setDialog(false);
    setError(null);
    let recorder: GrowthRecorder | null = null;
    let capturing = false;
    let cancelled = false;
    const undo: (() => void)[] = [];
    const end = () => {
      cancelled = true;
      abort.current = null;
      for (const f of undo.splice(0).reverse()) f();
      recorder?.cancel();
      // The scene may already be gone (a lost context unmounts the view).
      if (capturing) {
        try {
          scene.endCapture();
        } catch {
          /* disposed */
        }
      }
      setGrowth(null);
      setRecording(null);
    };
    abort.current = end;
    try {
      const births = await loadBirths();
      if (cancelled) return;
      const order = growthOrder(nodes, edges, births);
      const { width, height } = ASPECTS[aspect];
      const { mime, ext } = pickMimeType((m) => MediaRecorder.isTypeSupported(m));
      const background = cssVar("--surface-0");
      const mark = await loadMark(`${import.meta.env.BASE_URL}brand/coordinate-micro-${isDark(background) ? "ivory" : "pine"}.svg`);
      if (cancelled) return;

      scene.beginCapture(width, height);
      capturing = true;
      setFloor(scene);
      recorder = new GrowthRecorder({
        width,
        height,
        mime,
        mark,
        theme: {
          background,
          text: cssVar("--text-primary"),
          muted: cssVar("--text-secondary"),
          font: getComputedStyle(document.body).fontFamily || "sans-serif",
        },
      });
      const rec = recorder;
      const fail = (err: unknown) => {
        console.warn("Atlas: the video export failed", err);
        setError("The video could not be recorded. Nothing was saved.");
        end();
      };
      rec.onError = fail;

      let finishing = false;
      let held = 0;
      let last = performance.now();
      const finish = async () => {
        try {
          const blob = await rec.stop();
          if (cancelled) return;
          downloadBlob(blob, exportFileName(kbName, aspect, ext));
          end();
        } catch (err) {
          if (!cancelled) fail(err);
        }
      };
      // The one moment the WebGL canvas can be copied is right after a render
      // (the renderer keeps no drawing buffer), so each frame is composed
      // here. Time is counted in frames' deltas, capped, so the minutes a
      // hidden tab sleeps never count towards the hold or the end card.
      const onFrame = () => {
        const now = performance.now();
        const dt = Math.min(100, now - last);
        last = now;
        const g = growthRef.current;
        if (!g || finishing) return;
        if (g.shown >= g.order.length && !g.playing) held += dt;
        if (held >= HOLD_MS) {
          rec.drawEndCard(kbName, g.order.length);
          if (held >= HOLD_MS + END_CARD_MS) {
            finishing = true;
            void finish();
          }
          return;
        }
        const overlay: Overlay = { kb: kbName, day: dayOf(g), count: g.shown };
        rec.drawFrame(scene.canvas, overlay);
      };
      scene.frameListeners.add(onFrame);
      undo.push(() => scene.frameListeners.delete(onFrame));

      // A hidden tab stops the render loop: pause the recording and the
      // replay with it, rather than record frozen frames.
      const onVisibility = () => {
        const hidden = document.hidden;
        if (hidden) rec.pause();
        else rec.resume();
        last = performance.now();
        setRecording((r) => (r ? { ...r, paused: hidden } : r));
        setGrowth((g) => (g ? { ...g, playing: !hidden && g.shown < g.order.length } : g));
      };
      document.addEventListener("visibilitychange", onVisibility);
      undo.push(() => document.removeEventListener("visibilitychange", onVisibility));

      const clock = window.setInterval(
        () => setRecording((r) => (r && !r.paused ? { ...r, seconds: r.seconds + 1 } : r)),
        1000,
      );
      undo.push(() => window.clearInterval(clock));

      setRecording({ aspect, seconds: 0, paused: false });
      setGrowth({ order, shown: 1, playing: true });
    } catch (err) {
      if (cancelled) return;
      console.warn("Atlas: the video export failed", err);
      setError("The video could not be recorded. Nothing was saved.");
      end();
    }
  };

  // A new layout, or leaving the view, ends an export in progress.
  useEffect(() => () => abort.current?.(), [p.layoutKey]);

  return {
    supported,
    webm,
    dialog,
    openDialog: () => setDialog(true),
    closeDialog: () => setDialog(false),
    start,
    cancel: () => abort.current?.(),
    recording,
    error,
    dismissError: () => setError(null),
  };
}
