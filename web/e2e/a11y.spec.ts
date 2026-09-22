import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";
import { AUTH_URL, LOCAL_URL, conceptRow, expect, test, waitForAtlas, withPanelsOpen } from "./support";

const ATLAS = `${LOCAL_URL}/ui/?kb=atlas`;

// Panels open, so axe and the keyboard walk cover the rail and the node list
// too; the graph-only default is covered by the component tests.
test.beforeEach(({ page }) => withPanelsOpen(page));

/** Serious and critical axe violations, WCAG 2.2 A/AA rules. */
async function seriousViolations(page: Page): Promise<string[]> {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
    .analyze();
  return results.violations
    .filter((v) => v.impact === "serious" || v.impact === "critical")
    .map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
}

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 390, height: 844 },
]) {
  test.describe(`axe at ${viewport.width}x${viewport.height}`, () => {
    test.use({ viewport, reducedMotion: "reduce" });
    const narrow = viewport.width < 1024;

    test("shell and graph view", async ({ page }) => {
      await page.goto(ATLAS);
      await expect(page.locator("[data-testid=graph-canvas]")).toBeVisible();
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("inspector", async ({ page }) => {
      await page.goto(`${ATLAS}&scope=infra&concept=infra%2Fgateway`);
      if (narrow) await page.getByRole("button", { name: "Open inspector" }).click();
      await expect(page.getByRole("complementary", { name: "Inspector for infra/gateway" })).toBeVisible();
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("navigation", async ({ page }) => {
      await page.goto(ATLAS);
      if (narrow) await page.getByRole("button", { name: "Open navigation" }).click();
      await expect(page.getByRole("navigation", { name: "Atlas navigation" })).toBeVisible();
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("Observatory", async ({ page }) => {
      await page.goto(`${ATLAS}&panel=observatory`);
      await expect(page.getByRole("region", { name: "Observatory" })).toBeVisible();
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("auth prompt", async ({ page }) => {
      await page.goto(`${AUTH_URL}/ui/`);
      await expect(page.getByLabel("Bearer token")).toBeVisible();
      expect(await seriousViolations(page)).toEqual([]);
    });
  });
}

test.describe("keyboard only", () => {
  test.use({ reducedMotion: "reduce" });

  /** Presses Tab until the focused element matches, failing after a bound. */
  async function tabTo(page: Page, match: (el: { text: string; label: string; id: string }) => boolean) {
    for (let i = 0; i < 80; i++) {
      await page.keyboard.press("Tab");
      const focused = await page.evaluate(() => {
        const el = document.activeElement as HTMLElement | null;
        return {
          text: el?.textContent?.trim() ?? "",
          label: el?.getAttribute("aria-label") ?? "",
          id: el?.getAttribute("data-concept-id") ?? "",
        };
      });
      if (match(focused)) return;
    }
    throw new Error("Tab never reached the expected element");
  }

  test("top bar → filters → node list → inspector and back", async ({ page }) => {
    await page.goto(ATLAS);
    await waitForAtlas(page);
    await page.locator("body").focus();

    await tabTo(page, (el) => el.text.includes("Search concepts"));
    await tabTo(page, (el) => el.text.startsWith("Infrastructure"));
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/scope=infra/);

    await tabTo(page, (el) => el.text.startsWith("Runbook"));
    await page.keyboard.press("Enter");
    await tabTo(page, (el) => el.id === "infra/firewall");
    await page.keyboard.press("Enter");
    const inspector = page.getByRole("complementary", { name: "Inspector for infra/firewall" });
    await expect(inspector).toBeVisible();

    await tabTo(page, (el) => el.label === "Close inspector");
    await page.keyboard.press("Enter");
    await expect(conceptRow(page, "infra/firewall")).toBeFocused();
  });
});

test.describe("reduced motion", () => {
  // Every value data-entry takes, from the first frame on.
  const recordEntry = () => {
    const seen: string[] = [];
    (window as unknown as { __entry: string[] }).__entry = seen;
    new MutationObserver((records) => {
      for (const r of records) {
        const value = (r.target as HTMLElement).getAttribute("data-entry");
        if (value) seen.push(value);
      }
    }).observe(document, { attributes: true, attributeFilter: ["data-entry"], subtree: true });
    document.addEventListener("DOMContentLoaded", () => {
      const el = document.querySelector("[data-entry]");
      if (el) seen.push(el.getAttribute("data-entry")!);
    });
  };

  async function run(page: Page) {
    await withPanelsOpen(page);
    await page.addInitScript(recordEntry);
    await page.goto(`${ATLAS}&scope=infra`);
    await waitForAtlas(page);
    await expect(page.locator(".graph")).toHaveAttribute("data-entry", "settled");
    await conceptRow(page, "infra/gateway").click();
    await expect(page.getByRole("complementary", { name: "Inspector for infra/gateway" })).toBeVisible();
    return {
      motion: await page.locator(".graph").getAttribute("data-motion"),
      entries: await page.evaluate(() => (window as unknown as { __entry: string[] }).__entry),
      camera: await page.locator("[data-testid=graph-canvas]").getAttribute("data-camera"),
    };
  }

  test("skips the entry settle and jumps the camera", async ({ browser }) => {
    const context = await browser.newContext({ reducedMotion: "reduce" });
    const result = await run(await context.newPage());
    await context.close();
    expect(result.motion).toBe("reduced");
    expect(result.entries).not.toContain("settling");
    expect(result.camera).toBe("jump");
  });

  test("without the preference, the settle plays and the camera tweens", async ({ browser }) => {
    const context = await browser.newContext({ reducedMotion: "no-preference" });
    const result = await run(await context.newPage());
    await context.close();
    expect(result.motion).toBe("full");
    expect(result.entries).toContain("settling");
    expect(result.camera).toBe("tween");
  });
});
