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

test("the growth replay exports a real video, in the background, with the tab hidden halfway", async ({ page }) => {
  // Offline rendering at twice the export size on a software GL context is
  // the slow part: ~13 s of video at 60 fps is 780 frames.
  test.setTimeout(480_000);
  await open3D(page);
  await page.getByRole("button", { name: "Replay growth" }).click();
  // The button lives in the replay's control bar, not in the camera controls.
  await expect(page.getByRole("group", { name: "Graph camera" }).getByRole("button", { name: "Export video" })).toHaveCount(0);
  await page.getByRole("group", { name: "Growth replay" }).getByRole("button", { name: "Export video" }).click();
  await page.getByRole("radio", { name: /1:1/ }).check();
  // H.264 is covered by the unit test of pickCodec: the Chromium Playwright
  // drives may ship no H.264 encoder, so here it is VP9 in a WebM by
  // construction. The expectation asks the browser, so a branded Chrome passes
  // with .mp4.
  const mp4 = await page.evaluate(
    async () =>
      (
        await VideoEncoder.isConfigSupported({
          codec: "avc1.64002A",
          width: 1080,
          height: 1080,
          framerate: 60,
          avc: { format: "avc" },
        })
      ).supported === true,
  );
  const download = page.waitForEvent("download", { timeout: 450_000 });
  await page.getByRole("button", { name: "Export", exact: true }).click();
  const pill = page.getByText(/Exporting video \d+ %/);
  await expect(pill).toBeVisible();
  // The Atlas stays usable while it renders: the replay is still scrubbable.
  await expect(page.getByRole("slider", { name: "Replay position" })).toBeVisible();

  // Hide the page halfway through. The render loop waits on events and
  // messages, never timers, so a hidden tab must not slow or stop it.
  await expect
    .poll(async () => Number(/(\d+) %/.exec((await pill.textContent()) ?? "")?.[1] ?? 0), { timeout: 300_000 })
    .toBeGreaterThanOrEqual(40);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  const file = await download;
  expect(file.suggestedFilename()).toMatch(new RegExp(`-growth-1x1\\.${mp4 ? "mp4" : "webm"}$`));
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
    Object.defineProperty(document, "hidden", { configurable: true, get: () => false });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  const { readFileSync } = await import("node:fs");
  const bytes = readFileSync((await file.path())!);
  expect(bytes.length).toBeGreaterThan(10_000);

  // The proof the videos are not black (the regression this replaces): decode
  // the downloaded file in the browser and look at it.
  // In a blank page: the Atlas's own CSP does not allow a blob as media.
  const viewer = await page.context().newPage();
  const probe = await viewer.evaluate(
    async ({ data, type }) => {
      const blob = new Blob([Uint8Array.from(atob(data), (c) => c.charCodeAt(0))], { type });
      const video = document.createElement("video");
      video.muted = true;
      video.preload = "auto";
      video.src = URL.createObjectURL(blob);
      await new Promise<void>((resolve, reject) => {
        video.onloadeddata = () => resolve();
        video.onerror = () => reject(new Error(`the video does not decode: ${video.error?.message}`));
      });
      const canvas = document.createElement("canvas");
      canvas.width = 270;
      canvas.height = 270;
      const ctx = canvas.getContext("2d", { willReadFrequently: true })!;
      const look = async (t: number) => {
        video.currentTime = t;
        await new Promise<void>((resolve) => (video.onseeked = () => resolve()));
        ctx.drawImage(video, 0, 0, 270, 270);
        const px = ctx.getImageData(0, 0, 270, 270).data;
        const lum: number[] = [];
        for (let i = 0; i < px.length; i += 4) lum.push(0.2126 * px[i]! + 0.7152 * px[i + 1]! + 0.0722 * px[i + 2]!);
        const mean = lum.reduce((a, b) => a + b, 0) / lum.length;
        const sd = Math.sqrt(lum.reduce((a, b) => a + (b - mean) ** 2, 0) / lum.length);
        // pixels that stand out from the frame's own background (its corner)
        const bg = lum[0]!;
        const marked = lum.filter((l) => Math.abs(l - bg) > 30).length;
        return { mean, sd, max: Math.max(...lum), marked };
      };
      const first = await look(0);
      const mid = await look(video.duration / 2);
      // 60 fps: the presentation times of consecutive frames are 1/60 s apart.
      let step = 0;
      if ("requestVideoFrameCallback" in video) {
        const times: number[] = [];
        await new Promise<void>((resolve) => {
          const on = (_now: number, meta: { mediaTime: number }) => {
            times.push(meta.mediaTime);
            if (times.length > 20) resolve();
            else video.requestVideoFrameCallback(on);
          };
          video.requestVideoFrameCallback(on);
          video.currentTime = 2;
          void video.play();
        });
        video.pause();
        step = Math.min(...times.slice(1).map((t, i) => t - times[i]!).filter((d) => d > 0));
      }
      return { duration: video.duration, first, mid, step, w: video.videoWidth, h: video.videoHeight };
    },
    { data: bytes.toString("base64"), type: mp4 ? "video/mp4" : "video/webm" },
  );
  await viewer.close();
  expect([probe.w, probe.h]).toEqual([1080, 1080]);
  // 1 s opening card + the replay (8 s on a graph this small) + 1.5 s hold +
  // 2.5 s end card, at 60 fps: 780 frames.
  expect(probe.duration).toBeGreaterThan(12.7);
  expect(probe.duration).toBeLessThan(13.4);
  // Frame 0 is the opening card: the mark and the name stand out of the
  // background, so it is the thumbnail a messaging app shows.
  expect(probe.first.max).toBeGreaterThan(120);
  expect(probe.first.marked).toBeGreaterThan(300);
  // Mid-replay the graph is drawn: not black, not blank.
  expect(probe.mid.max).toBeGreaterThan(80);
  expect(probe.mid.marked).toBeGreaterThan(100);
  if (probe.step > 0) expect(probe.step).toBeLessThan(1 / 60 + 0.002);

  // The pill says it is done, and the view is the user's.
  await expect(page.getByText("Video saved (1:1)")).toBeVisible();
  await expect(page.getByRole("group", { name: "Growth replay" })).toBeVisible();
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
