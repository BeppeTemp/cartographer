/**
 * A DEV-ONLY trace of the scene's frames, for analysing a jitter in the real
 * browser (the scene builds one only when `import.meta.env.DEV`, so the
 * production bundle carries none of it):
 *
 *   __atlasTrace.start();           // before the KB change
 *   ... change KB, wait ...
 *   const frames = __atlasTrace.stop();   // one entry per animation frame
 *   __atlasTrace.events;            // setData / bloom / morph / transition / camera calls, labelled
 */
export interface TraceFrame {
  /** Milliseconds since start(). */
  t: number;
  drawn: boolean;
  camera: [number, number, number];
  target: [number, number, number];
  alpha: number;
  /** Mean and largest node displacement since the previous traced frame, scene units. */
  meanMove: number;
  maxMove: number;
}

export interface TraceEvent {
  t: number;
  label: string;
  detail?: unknown;
}

export class FrameTrace {
  events: TraceEvent[] = [];
  frames: TraceFrame[] = [];
  private running = false;
  private t0 = 0;
  private previous: Float32Array | null = null;

  start = (): void => {
    this.events = [];
    this.frames = [];
    this.previous = null;
    this.t0 = performance.now();
    this.running = true;
  };

  stop = (): TraceFrame[] => {
    this.running = false;
    return this.frames;
  };

  event(label: string, detail?: unknown): void {
    // Calls made before start() are kept: the trace is usually started after the page loaded.
    this.events.push({ t: performance.now() - this.t0, label, detail });
  }

  /** `positions` are the nodes as drawn (x, y, z per node), not the simulation's: the burst's flight shows. */
  frame(
    drawn: boolean,
    camera: { x: number; y: number; z: number },
    target: { x: number; y: number; z: number },
    alpha: number,
    positions: Float32Array,
  ): void {
    if (!this.running) return;
    const count = positions.length / 3;
    const prev = this.previous && this.previous.length === positions.length ? this.previous : null;
    let sum = 0;
    let max = 0;
    if (prev) {
      for (let i = 0; i < count; i++) {
        const d = Math.hypot(positions[i * 3]! - prev[i * 3]!, positions[i * 3 + 1]! - prev[i * 3 + 1]!, positions[i * 3 + 2]! - prev[i * 3 + 2]!);
        sum += d;
        if (d > max) max = d;
      }
    }
    this.previous = positions.slice();
    this.frames.push({
      t: performance.now() - this.t0,
      drawn,
      camera: [camera.x, camera.y, camera.z],
      target: [target.x, target.y, target.z],
      alpha,
      meanMove: count ? sum / count : 0,
      maxMove: max,
    });
  }
}
