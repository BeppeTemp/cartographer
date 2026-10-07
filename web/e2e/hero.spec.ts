import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { Page, TestInfo } from "@playwright/test";
import { expect, LOCAL_URL, test } from "./support";

// The README's hero animation (D330), as a flow the suite runs on every web
// change. In the suite it walks the fixture at full speed: a step the UI no
// longer offers fails here, which is how the recording goes stale loudly.
// scripts/record-hero.sh runs the same flow over the demo KB with HERO_RECORD
// set: the beats slow down to a watchable pace and the browser's own screencast
// films it, frame by frame (see film()).

const RECORD = !!process.env.HERO_RECORD;
const KB = process.env.HERO_KB ?? "atlas";
const CONCEPT = process.env.HERO_CONCEPT ?? "infra/gateway";
const QUERY = process.env.HERO_QUERY ?? "gateway";
const ARTIFACT = process.env.HERO_ARTIFACT ?? "review";

const VIEWPORT = { width: 1280, height: 760 };
test.use({
  viewport: VIEWPORT,
  colorScheme: "dark",
  // The recording runs in a headed window on the machine's GPU. SwiftShader
  // charts a 400-node KB too slowly to film, and headless Chromium hands the
  // screencast fewer than 20 frames/s against ~45 headed. Do not emulate a
  // deviceScaleFactor there: in a headed window it crops the screencast to a
  // corner. A Retina display already paints at 2x.
  ...(RECORD ? { launchOptions: { args: [], headless: false } } : {}),
});
if (RECORD) test.setTimeout(120_000);

/** A pause the viewer needs and the check does not. */
const beat = (page: Page, ms: number) => (RECORD ? page.waitForTimeout(ms) : Promise.resolve());

/**
 * Films the page through the DevTools screencast: near-lossless frames
 * straight from the compositor, each with its timestamp, instead of
 * Playwright's video, whose ~1 Mbit/s VP8 smears a dense graph. Frames arrive
 * only when something changes, so stop() writes an ffconcat list that holds
 * each one for as long as it was on screen.
 */
async function film(page: Page, testInfo: TestInfo): Promise<{ stop(): Promise<void> }> {
  const dir = testInfo.outputPath("frames");
  mkdirSync(dir, { recursive: true });
  const cdp = await page.context().newCDPSession(page);
  const frames: { file: string; at: number }[] = [];
  cdp.on("Page.screencastFrame", ({ data, sessionId }) => {
    const file = `${String(frames.length).padStart(5, "0")}.jpg`;
    writeFileSync(join(dir, file), Buffer.from(data, "base64"));
    // Arrival time, not metadata.timestamp: stop() closes the last frame with
    // Date.now(), and the two clocks are not the same one.
    frames.push({ file, at: Date.now() / 1000 });
    void cdp.send("Page.screencastFrameAck", { sessionId }).catch(() => {});
  });
  // JPEG at 95 is cheaper to hand over than PNG, and the WebP pass loses far
  // more than it does. maxWidth/maxHeight must be explicit: without them a
  // headed window's screencast is a cropped corner of the page.
  await cdp.send("Page.startScreencast", {
    format: "jpeg",
    quality: 95,
    everyNthFrame: 1,
    maxWidth: VIEWPORT.width * 2,
    maxHeight: VIEWPORT.height * 2,
  });
  return {
    async stop() {
      await cdp.send("Page.stopScreencast");
      const end = Date.now() / 1000;
      const lines = ["ffconcat version 1.0"];
      frames.forEach((frame, i) => {
        const next = i + 1 < frames.length ? frames[i + 1].at : end;
        lines.push(`file ${frame.file}`, `duration ${Math.max(next - frame.at, 0.001).toFixed(4)}`);
      });
      // The concat demuxer drops the last duration unless the file repeats.
      if (frames.length) lines.push(`file ${frames[frames.length - 1].file}`);
      writeFileSync(join(dir, "frames.ffconcat"), lines.join("\n") + "\n");
    },
  };
}

/** Orbits the camera with a slow drag across the canvas. */
async function orbit(page: Page, dx: number, steps: number): Promise<void> {
  const box = await page.locator("[data-testid=graph-view] canvas").first().boundingBox();
  if (!box) throw new Error("the graph has no canvas");
  const x = box.x + box.width / 2;
  const y = box.y + box.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  if (!RECORD) {
    await page.mouse.move(x + dx, y + dx / 8, { steps: 2 });
  } else {
    // One step per frame, so the turn takes as long as it looks like it does.
    for (let i = 1; i <= steps; i++) {
      await page.mouse.move(x + (dx * i) / steps, y + (dx / 8) * (i / steps));
      await page.waitForTimeout(16);
    }
  }
  await page.mouse.up();
}

test("the hero tour: graph, search, a concept's links, artifacts, health", async ({ page }, testInfo) => {
  await page.addInitScript(() => {
    localStorage.setItem("cartographer.theme", "dark");
    localStorage.setItem("cartographer.colorBy", "community");
  });
  await page.goto(`${LOCAL_URL}/ui/?kb=${KB}`);
  const canvas = page.locator("[data-testid=graph-view] canvas").first();
  await expect(canvas).toBeVisible();
  // A software renderer charts the demo KB slowly: the recording waits, and
  // filming starts once the graph is drawn.
  await expect(page.locator(".graph__loading")).toHaveAttribute("data-drawn", "true", {
    timeout: RECORD ? 90_000 : undefined,
  });
  const camera = RECORD ? await film(page, testInfo) : null;

  // 1. The graph settles and turns.
  await beat(page, 800);
  await orbit(page, 320, 120);
  await beat(page, 400);

  // 2. Search: the palette, a query typed, the camera flies to the concept.
  await page.keyboard.press("ControlOrMeta+k");
  const palette = page.getByRole("dialog", { name: "Search concepts" });
  await expect(palette).toBeVisible();
  await page.keyboard.type(QUERY, { delay: RECORD ? 110 : 0 });
  const hit = palette.locator(`[data-concept-id="${CONCEPT}"]`).first();
  await expect(hit).toBeVisible();
  await beat(page, 600);
  await hit.click();
  const inspector = page.getByRole("complementary", { name: `Inspector for ${CONCEPT}` });
  await expect(inspector).toBeVisible();
  await beat(page, 1400);

  // 3. Its links, named both ways.
  await inspector.getByRole("tab", { name: /Links/ }).click();
  await beat(page, 1300);
  await inspector.getByRole("button", { name: "Close inspector" }).click();

  // 4. The same graph, coloured by Map.
  await page.getByRole("button", { name: "Map", exact: true }).click();
  await expect(page.getByRole("button", { name: "Map", exact: true })).toHaveAttribute("aria-pressed", "true");
  await orbit(page, -260, 100);
  await beat(page, 600);

  // 5. What the KB ships to agents: a skill and the concepts it reads.
  await page.getByRole("button", { name: "Artifacts", exact: true }).click();
  await beat(page, 700);
  await page.getByRole("button", { name: new RegExp(`^${ARTIFACT}\\b`) }).first().click();
  await expect(page.getByText(`skills/${ARTIFACT}/SKILL.md`).first()).toBeVisible();
  await beat(page, 1600);

  // 6. What needs attention.
  await page.getByRole("button", { name: /^Health/ }).click();
  await expect(page.getByRole("heading", { name: /broken|attention|wait|all clear/i })).toBeVisible();
  await beat(page, 1400);

  // Back where the loop starts.
  await page.getByRole("button", { name: "Atlas", exact: true }).click();
  await expect(canvas).toBeVisible();
  await beat(page, 600);
  await camera?.stop();
});
