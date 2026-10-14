import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { Page, TestInfo } from "@playwright/test";
import { expect, LOCAL_URL, test } from "./support";

// The README's hero animation (D330, D790), as a flow the suite runs on every
// web change. In the suite it walks the fixture at full speed: a step the UI no
// longer offers fails here, which is how the recording goes stale loudly.
// scripts/record-hero.sh runs the same flow over the demo KB with HERO_RECORD
// set, and the tour is then filmed frame by frame on a virtual clock (see Film):
// the beats slow down to a watchable pace, and the output no longer depends on
// how fast the machine paints.

const RECORD = !!process.env.HERO_RECORD;
const KB = process.env.HERO_KB ?? "atlas";
const CONCEPT = process.env.HERO_CONCEPT ?? "infra/gateway";
const QUERY = process.env.HERO_QUERY ?? "gateway";
const ARTIFACT = process.env.HERO_ARTIFACT ?? "review";
const FPS = Math.max(1, Number(process.env.HERO_FPS ?? 30));

const VIEWPORT = { width: 1280, height: 760 };
// The recording is headless on the config's launch args (SwiftShader WebGL, so
// it runs on a runner with no GPU; it needs --enable-unsafe-swiftshader as well,
// since Chrome no longer falls back to a software WebGL context without it), at
// twice the viewport's pixels, scaled to the README's width at encode. The
// frames are screenshots of a clock this file advances, never what a compositor
// paints in real time, so a slow software renderer only makes the capture take
// longer, not look worse.
test.use({
  viewport: VIEWPORT,
  colorScheme: "dark",
  // No trace: thousands of calls (a clock step and a screenshot per frame) would
  // each be recorded, and nobody opens the trace of a capture.
  ...(RECORD ? { deviceScaleFactor: 2, trace: "off" as const } : {}),
});
// Real time: the whole tour is thousands of software-rendered frames. The
// per-step budgets below are in virtual seconds and are what catches a stuck tour.
if (RECORD) test.setTimeout(50 * 60_000);

/** The instant the page's clock starts at: any fixed one makes runs alike. */
const EPOCH = Date.UTC(2026, 0, 1, 12);
/** What the first draw may take, in virtual seconds, before the tour is called stuck. */
const WARMUP_BUDGET_S = 120;
/** What a step of the tour may wait for, in virtual seconds. */
const STEP_BUDGET_S = 30;
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
class Film {
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
  async until(what: string, check: (timeout: number) => Promise<unknown>, budgetS = STEP_BUDGET_S): Promise<void> {
    const from = this.virtualMs;
    for (;;) {
      try {
        await check(PROBE_MS);
        return;
      } catch (error) {
        const spent = (this.virtualMs - from) / 1000;
        if (spent > budgetS) {
          throw new Error(`hero: "${what}" did not hold after ${spent.toFixed(1)} virtual seconds: ${String(error)}`);
        }
      }
      await this.frame();
    }
  }

  /** Advances, with no shots, until the graph has drawn and opened: the film starts on it. */
  async untilDrawn(): Promise<void> {
    const drawn = this.page.locator('.graph__loading[data-drawn="true"]');
    const from = this.virtualMs;
    while ((await drawn.count()) === 0) {
      const spent = (this.virtualMs - from) / 1000;
      if (spent > WARMUP_BUDGET_S) {
        throw new Error(`hero: the graph was not drawn after ${spent.toFixed(1)} virtual seconds`);
      }
      await this.advance(1000 / FPS);
    }
    for (let left = SETTLE_MS; left > 0; left -= 1000 / FPS) await this.advance(1000 / FPS);
  }
}

test("the hero tour: graph, search, a concept's links, artifacts, health", async ({ page }, testInfo) => {
  const film = RECORD ? new Film(page, testInfo) : null;
  await film?.start();

  /** A pause the viewer needs and the check does not. */
  const beat = (ms: number) => (film ? film.hold(ms) : Promise.resolve());
  /** A state the tour waits for: the plain assertion in the suite, frames until it holds in a recording. */
  const until = (what: string, check: (timeout: number | undefined) => Promise<unknown>) =>
    film ? film.until(what, check) : check(undefined);

  /** Orbits the camera with a slow drag across the canvas. */
  async function orbit(dx: number, steps: number): Promise<void> {
    const box = await page.locator("[data-testid=graph-view] canvas").first().boundingBox();
    if (!box) throw new Error("the graph has no canvas");
    const x = box.x + box.width / 2;
    const y = box.y + box.height / 2;
    await page.mouse.move(x, y);
    await page.mouse.down();
    if (!film) {
      await page.mouse.move(x + dx, y + dx / 8, { steps: 2 });
    } else {
      // One step per frame, so the turn takes as long as it looks like it does.
      for (let i = 1; i <= steps; i++) {
        await page.mouse.move(x + (dx * i) / steps, y + (dx / 8) * (i / steps));
        await film.frame();
      }
    }
    await page.mouse.up();
  }

  await page.addInitScript(() => {
    localStorage.setItem("cartographer.theme", "dark");
    localStorage.setItem("cartographer.colorBy", "community");
  });
  await page.goto(`${LOCAL_URL}/ui/?kb=${KB}`);
  const canvas = page.locator("[data-testid=graph-view] canvas").first();
  await expect(canvas).toBeVisible();
  // A software renderer charts the demo KB slowly: the recording waits, and
  // filming starts once the graph is drawn.
  if (film) await film.untilDrawn();
  else await expect(page.locator(".graph__loading")).toHaveAttribute("data-drawn", "true");

  // 1. The graph settles and turns.
  await beat(800);
  await orbit(320, 120);
  await beat(400);

  // 2. Search: the palette, a query typed, the camera flies to the concept.
  await page.keyboard.press("ControlOrMeta+k");
  const palette = page.getByRole("dialog", { name: "Search concepts" });
  await until("the search palette opens", (timeout) => expect(palette).toBeVisible({ timeout }));
  if (film) {
    for (const key of QUERY) {
      await page.keyboard.type(key);
      await film.hold(110);
    }
  } else {
    await page.keyboard.type(QUERY);
  }
  const hit = palette.locator(`[data-concept-id="${CONCEPT}"]`).first();
  await until(`the search finds ${CONCEPT}`, (timeout) => expect(hit).toBeVisible({ timeout }));
  await beat(600);
  await hit.click();
  const inspector = page.getByRole("complementary", { name: `Inspector for ${CONCEPT}` });
  await until("the inspector opens", (timeout) => expect(inspector).toBeVisible({ timeout }));
  await beat(1400);

  // 3. Its links, named both ways.
  await inspector.getByRole("tab", { name: /Links/ }).click();
  await beat(1300);
  await inspector.getByRole("button", { name: "Close inspector" }).click();

  // 4. The same graph, coloured by Map.
  const byMap = page.getByRole("button", { name: "Map", exact: true });
  await byMap.click();
  await until("the graph is coloured by Map", (timeout) => expect(byMap).toHaveAttribute("aria-pressed", "true", { timeout }));
  await orbit(-260, 100);
  await beat(600);

  // 5. What the KB ships to agents: a skill and the concepts it reads.
  await page.getByRole("button", { name: "Artifacts", exact: true }).click();
  await beat(700);
  await page.getByRole("button", { name: new RegExp(`^${ARTIFACT}\\b`) }).first().click();
  const skill = page.getByText(`skills/${ARTIFACT}/SKILL.md`).first();
  await until("the skill opens", (timeout) => expect(skill).toBeVisible({ timeout }));
  await beat(1600);

  // 6. What needs attention.
  await page.getByRole("button", { name: /^Health/ }).click();
  const findings = page.getByRole("heading", { level: 1, name: /found/i });
  await until("the health findings show", (timeout) => expect(findings).toBeVisible({ timeout }));
  await beat(1400);

  // Back where the loop starts.
  await page.getByRole("button", { name: "Atlas", exact: true }).click();
  await until("the graph is back", (timeout) => expect(canvas).toBeVisible({ timeout }));
  await beat(600);
  if (film) console.log(`hero: ${film.frames} frames, ${film.seconds.toFixed(1)} virtual seconds`);
});
