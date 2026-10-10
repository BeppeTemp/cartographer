import type { Page } from "@playwright/test";
import { expect, LOCAL_URL, openConcept, test } from "./support";

// The living 3D atlas (D234), against the shipped bundle on a real (software)
// WebGL context. The physics itself is measured in src/test/physics.test.ts;
// this spec holds the wiring: the lost-context state, motion policy,
// selection and the render loop's lifecycle.

const ATLAS = `${LOCAL_URL}/ui/?kb=atlas`;

async function open3D(page: Page, url = ATLAS): Promise<void> {
  await page.addInitScript(() => localStorage.setItem("cartographer.panel.list", "1"));
  await page.goto(url);
  await expect(page.locator('[data-testid=graph-view] canvas').first()).toBeVisible();
}

test("a lost WebGL context says so and keeps the list", async ({ page }) => {
  await open3D(page);
  await page.locator("[data-testid=graph-view] canvas").first().evaluate((canvas) => {
    const gl = (canvas as HTMLCanvasElement).getContext("webgl2") ?? (canvas as HTMLCanvasElement).getContext("webgl");
    gl?.getExtension("WEBGL_lose_context")?.loseContext();
  });
  // There is no other view to hand over to (D235).
  await expect(page.getByText("This browser cannot draw the graph")).toBeVisible();
  await expect(page.locator("[data-testid=graph-view]")).toHaveCount(0);
});

test("without WebGL the list, search and inspector still work", async ({ page }) => {
  // No context of any WebGL flavour: three.js throws on its first call.
  await page.addInitScript(() => {
    const original = HTMLCanvasElement.prototype.getContext;
    HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement, type: string, ...rest: unknown[]) {
      if (type.startsWith("webgl") || type === "experimental-webgl") return null;
      return (original as (...a: unknown[]) => unknown).call(this, type, ...rest);
    } as typeof original;
    localStorage.setItem("cartographer.panel.list", "1");
  });
  await page.goto(ATLAS);
  // The graph cannot draw, so the graph area says so, and the shell does not
  // go down with the renderer.
  await expect(page.getByText("This browser cannot draw the graph")).toBeVisible();
  await expect(page.locator("[data-testid=graph-view]")).toHaveCount(0);
  await openConcept(page, "infra/gateway");
  await expect(page.getByRole("complementary", { name: "Inspector for infra/gateway" })).toBeVisible();
});

test("the growth replay exports as a video download", async ({ page }) => {
  // The replay lasts 8 s at least, then the hold and the end card.
  test.setTimeout(90_000);
  await open3D(page);
  await page.getByRole("button", { name: "Export video" }).click();
  await page.getByRole("radio", { name: /1:1/ }).check();
  // MP4 is covered by the unit test of pickMimeType: the Chromium Playwright
  // drives ships no H.264 encoder, so here it is WebM by construction. The
  // expectation asks the browser, so a branded Chrome passes with .mp4.
  const mp4 = await page.evaluate(() => MediaRecorder.isTypeSupported("video/mp4;codecs=avc1"));
  const download = page.waitForEvent("download", { timeout: 60_000 });
  await page.getByRole("button", { name: "Record" }).click();
  await expect(page.getByText(/Recording 1:1/)).toBeVisible();
  const file = await download;
  expect(file.suggestedFilename()).toMatch(new RegExp(`-growth-1x1\\.${mp4 ? "mp4" : "webm"}$`));
  const path = await file.path();
  expect((await import("node:fs")).statSync(path).size).toBeGreaterThan(0);
  // The view is back as it was.
  await expect(page.getByRole("button", { name: "Export video" })).toBeVisible();
  await expect(page.locator("[data-testid=graph-view]")).not.toHaveAttribute("data-growing", /.*/);
});

test.describe("motion", () => {
  test("is live by default and the toggle is remembered", async ({ page }) => {
    await open3D(page);
    const toggle = page.getByRole("button", { name: "Motion" });
    await expect(toggle).toHaveAttribute("aria-pressed", "true");
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-pressed", "false");
    await page.reload();
    await expect(page.getByRole("button", { name: "Motion" })).toHaveAttribute("aria-pressed", "false");
  });

  test.describe("under reduced motion", () => {
    test.use({ reducedMotion: "reduce" });
    test("starts still, whatever was stored", async ({ page }) => {
      await page.addInitScript(() => localStorage.setItem("cartographer.panel.motion", "1"));
      await open3D(page);
      await expect(page.getByRole("button", { name: "Motion" })).toHaveAttribute("aria-pressed", "false");
    });
  });
});

test("a selection opens the inspector and names its neighbourhood", async ({ page }) => {
  await open3D(page);
  await openConcept(page, "infra/gateway");
  await expect(page.getByRole("complementary", { name: "Inspector for infra/gateway" })).toBeVisible();
  // Labels carry the concept's title, not its id.
  await expect(page.locator(".graph3d__label--selected")).toHaveText("Gateway");
  // gateway <-> dns is the fixture's backlink pair: the neighbour is named once.
  await expect(page.locator(".graph3d__label", { hasText: /^DNS$/ })).toHaveCount(1);
  await page.keyboard.press("Escape");
  await expect(page.locator(".graph3d__label--selected")).toHaveCount(0);
});

test("a hidden tab stops the render loop", async ({ page }) => {
  await page.addInitScript(() => {
    const w = window as unknown as { __frames: number };
    w.__frames = 0;
    const raf = window.requestAnimationFrame.bind(window);
    window.requestAnimationFrame = (cb) => raf((t) => (w.__frames++, cb(t)));
  });
  await open3D(page);
  // Past the one-off re-framing tween after the settle (Graph3D, ~1.8 s +
  // FOCUS_MS), which is its own short rAF loop.
  await page.waitForTimeout(3000);
  const frames = () => page.evaluate(() => (window as unknown as { __frames: number }).__frames);
  const during = async (ms: number) => {
    const before = await frames();
    await page.waitForTimeout(ms);
    return (await frames()) - before;
  };
  expect(await during(500)).toBeGreaterThan(5);
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  // At most the frame already scheduled when the tab was hidden.
  expect(await during(700)).toBeLessThanOrEqual(2);
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, get: () => false });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect(await during(500)).toBeGreaterThan(5);
});

test("the page stays scrollable around the canvas; gestures stay on it", async ({ page }) => {
  await open3D(page);
  const touchAction = await page
    .locator('[data-testid=graph-view] canvas')
    .first()
    .evaluate((el) => getComputedStyle(el).touchAction);
  expect(touchAction).toBe("none");
  expect(await page.evaluate(() => getComputedStyle(document.body).touchAction)).not.toBe("none");
});
