/**
 * What moves on its own in the 3D atlas, and when (D234).
 *
 * Three phenomena, kept apart: the camera's slow panoramic orbit, the nodes'
 * local motion (physics.ts), and signals running along a selected concept's
 * links. Pause and reduced motion stop everything that is autonomous; what the
 * reader does by hand -- orbit, zoom, drag -- always works.
 */

export type MotionMode = "live" | "still";

/** Reduced motion wins over the stored toggle; the reader can still flip it. */
export function initialMotion(stored: boolean, reducedMotion: boolean): boolean {
  return reducedMotion ? false : stored;
}

/** OrbitControls.autoRotateSpeed for ~1 deg/s: 2.0 is one orbit per 30 s. */
export const AUTO_ROTATE_SPEED = 2 * (1 / 12);
/** Quiet time after the last input before the panorama may turn again. */
export const IDLE_RESUME_MS = 10_000;

/**
 * IdleRotation decides whether the panorama turns. It stops on any input,
 * while a concept is selected (the reader is reading), while the camera is
 * focusing and while motion is off; it resumes only after IDLE_RESUME_MS of
 * quiet in the overview. A clock is injected so tests need no timers.
 */
export class IdleRotation {
  private lastInput = Number.NEGATIVE_INFINITY;
  private selected = false;
  private focusing = false;
  private live: boolean;

  constructor(live: boolean, private readonly now: () => number = () => performance.now()) {
    this.live = live;
  }

  input(): void {
    this.lastInput = this.now();
  }

  setSelected(selected: boolean): void {
    this.selected = selected;
    // Leaving a concept is input too: the panorama does not lurch into motion
    // the instant the reader closes the inspector.
    this.input();
  }

  setFocusing(focusing: boolean): void {
    this.focusing = focusing;
  }

  setLive(live: boolean): void {
    this.live = live;
  }

  shouldRotate(): boolean {
    if (!this.live || this.selected || this.focusing) return false;
    return this.now() - this.lastInput >= IDLE_RESUME_MS;
  }
}

/** Signals alive at once, at most. */
export const MAX_SIGNALS = 48;
/** A selection sends this many waves ... */
export const SIGNAL_WAVES = 3;
/** ... spread over this long, then stops. */
export const SIGNAL_SPAN_MS = 2500;
/** One pass of a signal along a link takes this long, whatever its length. */
export const SIGNAL_PASS_MS = 1100;

export interface BurstLink {
  source: string;
  target: string;
}

export interface Signal<L> {
  link: L;
  /** When to emit it, relative to the selection. */
  delayMs: number;
}

/**
 * planBursts schedules the signals for a selection: along the links incident
 * to `selected`, in SIGNAL_WAVES waves over SIGNAL_SPAN_MS, never more than
 * MAX_SIGNALS in total. A particle runs from a link's source to its target,
 * which is the direction of the wiki-link in the KB, so the direction shown is
 * the direction in the data. When a hub has more links than the budget allows,
 * the links to its best-connected neighbours win.
 *
 * Nothing is planned while motion is off: signals are autonomous motion.
 */
export function planBursts<L extends BurstLink>(
  selected: string | null,
  links: readonly L[],
  degree: (id: string) => number,
  live: boolean,
): Signal<L>[] {
  if (!selected || !live) return [];
  const incident = links.filter((l) => l.source === selected || l.target === selected);
  if (incident.length === 0) return [];
  const other = (l: L) => (l.source === selected ? l.target : l.source);
  const perWave = Math.max(1, Math.floor(MAX_SIGNALS / SIGNAL_WAVES));
  const chosen = [...incident]
    .sort((a, b) => degree(other(b)) - degree(other(a)) || (other(a) < other(b) ? -1 : 1))
    .slice(0, perWave);
  const gap = SIGNAL_WAVES > 1 ? (SIGNAL_SPAN_MS - SIGNAL_PASS_MS) / (SIGNAL_WAVES - 1) : 0;
  const signals: Signal<L>[] = [];
  for (let wave = 0; wave < SIGNAL_WAVES; wave++) {
    for (const link of chosen) signals.push({ link, delayMs: Math.round(wave * gap) });
  }
  return signals;
}

/**
 * particleSpeed is 3d-force-graph's particle speed -- the share of a link a
 * particle covers per frame -- for a pass of SIGNAL_PASS_MS at the measured
 * frame rate, so a long link and a short one take the same time.
 */
export function particleSpeed(fps = 60): number {
  return 1 / Math.max(1, (SIGNAL_PASS_MS / 1000) * fps);
}
