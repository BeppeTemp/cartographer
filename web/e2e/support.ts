import { test as base, expect, type Page } from "@playwright/test";

const LOCAL = process.env.E2E_LOCAL_URL;
const AUTH = process.env.E2E_AUTH_URL;
if (!LOCAL || !AUTH) {
  throw new Error("E2E_LOCAL_URL / E2E_AUTH_URL are unset: run the suite through `make e2e-web`.");
}

/** The auth-off server and the auth-on one (see e2e/run.sh). */
export const LOCAL_URL: string = LOCAL;
export const AUTH_URL: string = AUTH;
export const ADMIN_TOKEN = "e2e-admin-token";
export const NARROW_TOKEN = "e2e-narrow-token";

/**
 * Every test gets a page whose traffic is watched: any request to an origin
 * other than the server under test fails the test. It fails rather than
 * allow-lists, so a font, a CDN or a telemetry beacon added later is caught
 * the day it appears (D228). Console messages are kept for the token-leak
 * assertions.
 */
export const test = base.extend<{ foreign: string[]; consoleLines: string[] }>({
  foreign: [
    async ({ page }, use) => {
      const foreign: string[] = [];
      const allowed = new Set([new URL(LOCAL_URL).origin, new URL(AUTH_URL).origin]);
      page.on("request", (request) => {
        const url = request.url();
        if (url.startsWith("data:") || url.startsWith("blob:")) return;
        if (!allowed.has(new URL(url).origin)) foreign.push(url);
      });
      await use(foreign);
      expect(foreign, "requests left the Cartographer origin").toEqual([]);
    },
    { auto: true },
  ],
  consoleLines: [
    async ({ page }, use) => {
      const lines: string[] = [];
      page.on("console", (message) => lines.push(message.text()));
      page.on("pageerror", (error) => lines.push(String(error)));
      await use(lines);
    },
    { auto: true },
  ],
});

export { expect };

/** The shell is up and the graph for the current view has rendered. */
export async function waitForAtlas(page: Page): Promise<void> {
  await expect(page.getByRole("navigation", { name: "Atlas navigation" })).toBeVisible();
  await expect(page.locator("[data-testid=graph-view] canvas").first()).toBeVisible();
}

/** The ids the search offers with an empty query: every concept the current
 *  view draws (the fixture is far below the palette's row cap). */
export async function listedConcepts(page: Page): Promise<string[]> {
  await page.keyboard.press("ControlOrMeta+k");
  const dialog = page.getByRole("dialog", { name: "Search concepts" });
  await expect(dialog).toBeVisible();
  const ids = await dialog.locator("[data-concept-id]").evaluateAll((els) =>
    els.map((el) => el.getAttribute("data-concept-id") ?? ""),
  );
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  return ids.sort();
}

/** Opens a concept the way a keyboard user does: Ctrl/Cmd+K, its id, Enter. */
export async function openConcept(page: Page, id: string): Promise<void> {
  await page.keyboard.press("ControlOrMeta+k");
  const dialog = page.getByRole("dialog", { name: "Search concepts" });
  await expect(dialog).toBeVisible();
  await page.keyboard.type(id);
  await expect(dialog.locator(`[data-concept-id="${id}"]`).first()).toBeVisible();
  await dialog.locator(`[data-concept-id="${id}"]`).first().click();
}

/** The URL names the selected concept. */
export function selectedIn(id: string): RegExp {
  return new RegExp("concept=" + encodeURIComponent(id).replace(/[.*()]/g, "\\$&"));
}

/** Opens the atlas on the auth-on server and signs in with token. */
export async function signIn(page: Page, token: string, remember = false, path = "/ui/"): Promise<void> {
  await page.goto(AUTH_URL + path);
  await page.getByLabel("Bearer token").fill(token);
  if (remember) await page.getByLabel("Remember for this tab").check();
  await page.getByRole("button", { name: "Open the atlas" }).click();
}

/**
 * The atlas starts graph-only: navigation folded. Flows that
 * walk those panels open them the way a returning viewer has them -- through
 * the remembered preference -- so each test does not re-click its way there.
 */
export async function withPanelsOpen(page: Page): Promise<void> {
  await page.addInitScript(() => {
    localStorage.setItem("cartographer.panel.rail", "0");
  });
}
