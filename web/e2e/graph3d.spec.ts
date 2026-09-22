import type { Page } from "@playwright/test";
import { LOCAL_URL, conceptRow, expect, test } from "./support";

// The living 3D atlas (D234), against the shipped bundle on a real (software)
// WebGL context. The physics itself is measured in src/test/physics.test.ts;
// this spec holds the wiring: default view, fallback, motion policy,
// selection and the render loop's lifecycle.

const ATLAS = `${LOCAL_URL}/ui/?kb=atlas`;

async function open3D(page: Page, url = ATLAS): Promise<void> {
  await page.addInitScript(() => localStorage.setItem("cartographer.panel.list", "1"));
  await page.goto(url);
  await expect(page.locator("[data-testid=graph-3d] canvas").first()).toBeVisible();
}

const viewGroup = (page: Page) => page.getByRole("group", { name: "Graph view" });

test("a lost WebGL context hands over to the 2D atlas", async ({ page }) => {
  await open3D(page);
  await page.locator("[data-testid=graph-3d] canvas").first().evaluate((canvas) => {
    const gl = (canvas as HTMLCanvasElement).getContext("webgl2") ?? (canvas as HTMLCanvasElement).getContext("webgl");
    gl?.getExtension("WEBGL_lose_context")?.loseContext();
  });
  await expect(viewGroup(page).getByRole("button", { name: "2D" })).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("[data-testid=graph-canvas]")).toBeVisible();
});

test("3D is the first view on a wide screen, and 2D is remembered", async ({ page }) => {
  await open3D(page);
  await expect(viewGroup(page).getByRole("button", { name: "3D" })).toHaveAttribute("aria-pressed", "true");
  await viewGroup(page).getByRole("button", { name: "2D" }).click();
  await expect(page.locator("[data-testid=graph-canvas]")).toBeVisible();
  await page.reload();
  await expect(page.locator("[data-testid=graph-canvas]")).toBeVisible();
  await expect(page.locator("[data-testid=graph-3d]")).toHaveCount(0);
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
  // Neither view can draw -- Sigma needs WebGL too -- so the graph area says
  // so, and the shell does not go down with a renderer.
  await expect(page.getByText("This browser cannot draw the graph")).toBeVisible();
  await expect(viewGroup(page)).toHaveCount(0);
  await conceptRow(page, "infra/gateway").click();
  await expect(page.getByRole("complementary", { name: "Inspector for infra/gateway" })).toBeVisible();
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
  await conceptRow(page, "infra/gateway").click();
  await expect(page.getByRole("complementary", { name: "Inspector for infra/gateway" })).toBeVisible();
  await expect(page.locator(".graph3d__label--selected")).toHaveText("gateway");
  // gateway <-> dns is the fixture's backlink pair: the neighbour is named once.
  await expect(page.locator(".graph3d__label", { hasText: /^dns$/ })).toHaveCount(1);
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
    .locator("[data-testid=graph-3d] canvas")
    .first()
    .evaluate((el) => getComputedStyle(el).touchAction);
  expect(touchAction).toBe("none");
  expect(await page.evaluate(() => getComputedStyle(document.body).touchAction)).not.toBe("none");
});
