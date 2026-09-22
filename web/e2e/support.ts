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
  await expect(page.locator("[data-testid=graph-canvas]")).toBeVisible();
}

/** The ids the node list offers: the accessible mirror of the canvas. */
export async function listedConcepts(page: Page): Promise<string[]> {
  const list = page.getByRole("region", { name: "Concepts in this view" });
  return (await list.locator("[data-concept-id]").evaluateAll((els) =>
    els.map((el) => el.getAttribute("data-concept-id") ?? ""),
  )).sort();
}

export function conceptRow(page: Page, id: string) {
  return page.locator(`[data-concept-id="${id}"]`);
}

/** Opens the atlas on the auth-on server and signs in with token. */
export async function signIn(page: Page, token: string, remember = false, path = "/ui/"): Promise<void> {
  await page.goto(AUTH_URL + path);
  await page.getByLabel("Bearer token").fill(token);
  if (remember) await page.getByLabel("Remember for this tab").check();
  await page.getByRole("button", { name: "Open the atlas" }).click();
}
