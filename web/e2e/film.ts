import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { Page, TestInfo } from "@playwright/test";

// The virtual clock the films run on (hero.spec.ts, surveyor.spec.ts; D330, D790):
// in the suite a spec walks its flow at full speed, and scripts/record-hero.sh
// sets HERO_RECORD to film it frame by frame on a clock the spec advances (see
// Film), so a slow software renderer makes the capture take longer, not look worse.

export const RECORD = !!process.env.HERO_RECORD;
export const FPS = Math.max(1, Number(process.env.HERO_FPS ?? 30));
export const VIEWPORT = { width: 1280, height: 760 };

// The recording is headless on the config's launch args (SwiftShader WebGL, so
// it runs on a runner with no GPU; it needs --enable-unsafe-swiftshader as well,
// since Chrome no longer falls back to a software WebGL context without it), at
// twice the viewport's pixels, scaled to the README's width at encode. The
// frames are screenshots of a clock this file advances, never what a compositor
// paints in real time, so a slow software renderer only makes the capture take
// longer, not look worse.
/** `test.use` options of a film spec. */
export const filmUse = {
  viewport: VIEWPORT,
  colorScheme: "dark",
  // No trace: thousands of calls (a clock step and a screenshot per frame) would
  // each be recorded, and nobody opens the trace of a capture.
  ...(RECORD ? { deviceScaleFactor: 2, trace: "off" as const } : {}),
};
/** Real time a whole recording may take: thousands of software-rendered frames. The
 *  per-step budgets below are in virtual seconds and are what catches a stuck tour. */
export const RECORD_TIMEOUT_MS = 50 * 60_000;

/** The instant the page's clock starts at: any fixed one makes runs alike. */
const EPOCH = Date.UTC(2026, 0, 1, 12);
/** What the first draw may take, in virtual seconds, before the tour is called stuck. */
const WARMUP_BUDGET_S = 120;
/** What a step of the tour may wait for, in virtual seconds. */
export const STEP_BUDGET_S = 30;
/**
 * Past `data-drawn` the graph's opening burst (BLOOM_MS, 1.8 s, in
 * graph3d/scene.ts) is still running: it passes unfilmed, so the film opens on
 * the settled graph and not on an empty canvas.
 */
const SETTLE_MS = 2000;
/** How long one probe of a condition may take in real time. */
const PROBE_MS = 250;

/**
 * Moves every running CSS transition and animation on by `ms` of virtual time.
 * The fake clock drives timers, requestAnimationFrame, Date and performance,
 * but not the Web Animations timeline: left alone, a panel would slide at the
 * machine's speed, jumping between two frames when the capture is slower than
 * real time (D790). So each animation is paused and its currentTime set by hand.
 * One just created is reset to 0: it has been running in real time since the
 * browser's own frame started it, and that elapsed time must not leak in.
 */
function stepAnimations(ms: number): void {
  const w = window as unknown as { __heroSeen?: WeakSet<Animation> };
  const seen = (w.__heroSeen ??= new WeakSet());
  for (const a of document.getAnimations()) {
    if (a.playState === "finished") continue;
    if (!seen.has(a)) {
      seen.add(a);
      a.pause();
      a.currentTime = 0;
      continue;
    }
    const end = a.effect?.getComputedTiming().endTime;
    const next = Number(a.currentTime ?? 0) + ms;
    if (typeof end === "number" && Number.isFinite(end) && next >= end) a.finish();
    else a.currentTime = next;
  }
}

/** Virtual time: the page's clock, advanced one frame at a time, and the film of it. */
export class Film {
  private n = 0;
  private virtualMs = 0;
  private clockMs = 0;
  private readonly dir: string;

  constructor(
    private readonly page: Page,
    testInfo: TestInfo,
  ) {
    this.dir = testInfo.outputPath("frames");
    mkdirSync(this.dir, { recursive: true });
  }

  /** Installs the clock at a fixed instant and pauses it, before the page loads. */
  async start(): Promise<void> {
    await this.page.clock.install({ time: EPOCH });
    await this.page.clock.pauseAt(EPOCH + 1000);
  }

  get frames(): number {
    return this.n;
  }

  get seconds(): number {
    return this.virtualMs / 1000;
  }

  /**
   * Lets `ms` of virtual time pass, without a shot. Playwright keeps every
   * clock call as an init script to replay on a new document, so a recording
   * accumulates thousands of them: harmless while the tour never navigates
   * again after the first load, which it does not.
   */
  async advance(ms: number): Promise<void> {
    this.virtualMs += ms;
    // Whole milliseconds, carrying the remainder: 30 fps is 33.3 ms a frame.
    const whole = Math.round(this.virtualMs) - this.clockMs;
    this.clockMs += whole;
    await this.page.clock.runFor(whole);
    await this.page.evaluate(stepAnimations, whole);
  }

  /** One frame of the film: a frame's worth of time, then a shot. */
  async frame(): Promise<void> {
    await this.advance(1000 / FPS);
    await this.page.screenshot({
      path: join(this.dir, `${String(++this.n).padStart(5, "0")}.png`),
      type: "png",
    });
  }

  /** A pause the viewer needs and the check does not. */
  async hold(ms: number): Promise<void> {
    for (let i = Math.round((ms * FPS) / 1000); i > 0; i--) await this.frame();
  }

  /**
   * Waits for a condition the way the app does: frames pass until it holds
   * (the debounces, the simulation and the transitions only move with them),
   * and a budget of virtual seconds fails the tour with the step's name.
   */
  async until(what: string, check: (timeout: number) => Promise<unknown>, budgetS = STEP_BUDGET_S, quiet = false): Promise<void> {
    const from = this.virtualMs;
    for (;;) {
      try {
        await check(PROBE_MS);
        return;
      } catch (error) {
        const spent = (this.virtualMs - from) / 1000;
        if (spent > budgetS) {
          throw new Error(`film: "${what}" did not hold after ${spent.toFixed(1)} virtual seconds: ${String(error)}`);
        }
      }
      // A quiet wait is a cut: time passes (the app's timers run) and nothing is filmed.
      if (quiet) await this.advance(1000 / FPS);
      else await this.frame();
    }
  }

  /** Advances, with no shots, until the graph has drawn and opened: the film starts on it. */
  async untilDrawn(): Promise<void> {
    const drawn = this.page.locator('.graph__loading[data-drawn="true"]');
    const from = this.virtualMs;
    while ((await drawn.count()) === 0) {
      const spent = (this.virtualMs - from) / 1000;
      if (spent > WARMUP_BUDGET_S) {
        throw new Error(`film: the graph was not drawn after ${spent.toFixed(1)} virtual seconds`);
      }
      await this.advance(1000 / FPS);
    }
    for (let left = SETTLE_MS; left > 0; left -= 1000 / FPS) await this.advance(1000 / FPS);
  }
}

