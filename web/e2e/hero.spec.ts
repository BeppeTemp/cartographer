import { writeFileSync } from "node:fs";
import type { Page } from "@playwright/test";
import { expect, LOCAL_URL, test } from "./support";

// The README's hero animation (D330), as a flow the suite runs on every web
// change. In the suite it walks the fixture at full speed: a step the UI no
// longer offers fails here, which is how the recording goes stale loudly.
// scripts/record-hero.sh runs the same flow over the demo KB with HERO_RECORD
// set: the beats slow down to a watchable pace and Playwright records it.

const RECORD = !!process.env.HERO_RECORD;
const KB = process.env.HERO_KB ?? "atlas";
const CONCEPT = process.env.HERO_CONCEPT ?? "infra/gateway";
const QUERY = process.env.HERO_QUERY ?? "gateway";
const ARTIFACT = process.env.HERO_ARTIFACT ?? "review";

const VIEWPORT = { width: 1280, height: 760 };
test.use({
  viewport: VIEWPORT,
  colorScheme: "dark",
  video: RECORD ? { mode: "on", size: VIEWPORT } : "off",
  // The recording draws on the machine's GPU: SwiftShader charts a 400-node
  // KB too slowly to film.
  ...(RECORD ? { launchOptions: { args: [] } } : {}),
});
if (RECORD) test.setTimeout(120_000);

/** A pause the viewer needs and the check does not. */
const beat = (page: Page, ms: number) => (RECORD ? page.waitForTimeout(ms) : Promise.resolve());

/** Orbits the camera with a slow drag across the canvas. */
async function orbit(page: Page, dx: number, steps: number): Promise<void> {
  const box = await page.locator("[data-testid=graph-view] canvas").first().boundingBox();
  if (!box) throw new Error("the graph has no canvas");
  const x = box.x + box.width / 2;
  const y = box.y + box.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + dx, y + dx / 8, { steps: RECORD ? steps : 2 });
  await page.mouse.up();
}

test("the hero tour: graph, search, a concept's links, artifacts, observatory", async ({ page }, testInfo) => {
  const opened = Date.now();
  await page.addInitScript(() => {
    localStorage.setItem("cartographer.theme", "dark");
    localStorage.setItem("cartographer.colorBy", "community");
  });
  await page.goto(`${LOCAL_URL}/ui/?kb=${KB}`);
  const canvas = page.locator("[data-testid=graph-view] canvas").first();
  await expect(canvas).toBeVisible();
  // A software renderer charts the demo KB slowly: the recording waits, and
  // record-hero.sh cuts the wait off the front using tour-start.txt.
  await expect(page.locator(".graph__loading")).toHaveAttribute("data-drawn", "true", {
    timeout: RECORD ? 90_000 : undefined,
  });
  if (RECORD) writeFileSync(testInfo.outputPath("tour-start.txt"), String((Date.now() - opened) / 1000));

  // 1. The graph settles and turns.
  await beat(page, 800);
  await orbit(page, 260, 40);
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
  await orbit(page, -220, 35);
  await beat(page, 600);

  // 5. What the KB ships to agents: a skill and the concepts it reads.
  await page.getByRole("button", { name: /^Artifacts, / }).click();
  await beat(page, 700);
  await page.getByRole("button", { name: new RegExp(`^${ARTIFACT}\\b`) }).first().click();
  await expect(page.getByText(`skills/${ARTIFACT}/SKILL.md`).first()).toBeVisible();
  await beat(page, 1600);

  // 6. What needs attention.
  await page.getByRole("button", { name: /^Observatory/ }).click();
  await expect(page.getByRole("heading", { name: /need attention|nothing to fix/i })).toBeVisible();
  await beat(page, 1400);

  // Back where the loop starts.
  await page.getByRole("button", { name: "Atlas", exact: true }).click();
  await expect(canvas).toBeVisible();
  await beat(page, 600);
});
