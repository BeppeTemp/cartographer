import type { Page, Response } from "@playwright/test";
import { HIDDEN_IN_MAP, HIDDEN_OUT_OF_MAP } from "./fixture.mjs";
import {
  ADMIN_TOKEN,
  AUTH_URL,
  NARROW_TOKEN,
  conceptRow,
  expect,
  listedConcepts,
  signIn,
  test,
  waitForAtlas,
  withPanelsOpen,
} from "./support";

test.use({ reducedMotion: "reduce" });
test.beforeEach(({ page }) => withPanelsOpen(page));

/** Every API body the page receives, for the leak and non-disclosure checks. */
function recordApi(page: Page): { url: string; status: number; body: string }[] {
  const seen: { url: string; status: number; body: string }[] = [];
  page.on("response", async (response: Response) => {
    if (!response.url().includes("/api/ui/v1/")) return;
    seen.push({ url: response.url(), status: response.status(), body: await response.text().catch(() => "") });
  });
  return seen;
}

test("the bearer prompt refuses a bad token and accepts a good one", async ({ page }) => {
  await page.goto(`${AUTH_URL}/ui/`);
  await expect(page.getByRole("heading", { name: "Cartographer Atlas" })).toBeVisible();

  await page.getByLabel("Bearer token").fill("not-a-token");
  await page.getByRole("button", { name: "Open the atlas" }).click();
  await expect(page.getByRole("alert")).toHaveText("That token was not accepted.");

  await page.getByLabel("Bearer token").fill(ADMIN_TOKEN);
  await page.getByRole("button", { name: "Open the atlas" }).click();
  await waitForAtlas(page);
});

test("a token is forgotten on reload unless remembered for the tab", async ({ page }) => {
  await signIn(page, ADMIN_TOKEN);
  await waitForAtlas(page);
  await page.reload();
  await expect(page.getByLabel("Bearer token")).toBeVisible();
});

test("'remember for this tab' survives a reload but not a new tab", async ({ page, context }) => {
  await signIn(page, ADMIN_TOKEN, true);
  await waitForAtlas(page);
  await page.reload();
  await waitForAtlas(page);
  await expect(page.getByLabel("Bearer token")).toHaveCount(0);

  const other = await context.newPage();
  await other.goto(`${AUTH_URL}/ui/`);
  await expect(other.getByLabel("Bearer token")).toBeVisible();
});

test("the token reaches no URL, no localStorage, no console and no error body", async ({ page, consoleLines }) => {
  const api = recordApi(page);
  const urls: string[] = [];
  page.on("request", (request) => urls.push(request.url()));

  // One rejected attempt first, so an error body is captured too.
  await page.goto(`${AUTH_URL}/ui/?kb=atlas`);
  await page.getByLabel("Bearer token").fill(`${ADMIN_TOKEN}-wrong`);
  await page.getByRole("button", { name: "Open the atlas" }).click();
  await expect(page.getByRole("alert")).toBeVisible();

  await page.getByLabel("Bearer token").fill(ADMIN_TOKEN);
  await page.getByLabel("Remember for this tab").check();
  await page.getByRole("button", { name: "Open the atlas" }).click();
  await waitForAtlas(page);
  await page.getByRole("button", { name: /Infrastructure/ }).click();
  await conceptRow(page, "infra/gateway").click();
  await expect(page.getByRole("complementary", { name: "Inspector for infra/gateway" })).toBeVisible();

  const local = await page.evaluate(() => JSON.stringify({ ...localStorage }));
  for (const token of [ADMIN_TOKEN, `${ADMIN_TOKEN}-wrong`]) {
    expect(page.url()).not.toContain(token);
    expect(urls.filter((url) => url.includes(token))).toEqual([]);
    expect(local).not.toContain(token);
    expect(consoleLines.filter((line) => line.includes(token))).toEqual([]);
    expect(api.filter((r) => r.body.includes(token))).toEqual([]);
  }
  expect(api.some((r) => r.status === 401)).toBe(true);
});

test.describe("a narrowed principal", () => {
  const HIDDEN = [HIDDEN_IN_MAP, HIDDEN_OUT_OF_MAP, "apps/backup", "incidents/2026-01-10-outage", "infra/missing-ruleset"];

  test("sees nothing outside its Map and type, anywhere", async ({ page }) => {
    const api = recordApi(page);
    await signIn(page, NARROW_TOKEN);
    await waitForAtlas(page);

    // Nodes and the KB list.
    await expect
      .poll(() => listedConcepts(page))
      .toEqual(["infra/cluster", "infra/cluster/nodes", "infra/dns", "infra/gateway", "infra/legacy-vpn"]);
    await expect(page.getByRole("combobox", { name: "Knowledge Base" }).locator("option")).toHaveText(["atlas"]);

    // Counts: no hidden type, status or collection shows up in a filter.
    const rail = page.getByRole("navigation", { name: "Atlas navigation" });
    await expect(rail.getByRole("button", { name: /^Runbook/ })).toHaveCount(0);
    await expect(rail.getByRole("button", { name: /^draft/ })).toHaveCount(0);
    await expect(rail.getByRole("button", { name: /Applications|Incidents/ })).toHaveCount(0);

    // Search.
    await page.keyboard.press("ControlOrMeta+k");
    const palette = page.getByRole("dialog", { name: "Search concepts" });
    await palette.getByRole("textbox").fill("firewall");
    await expect(palette.getByText(/No concept matches/)).toBeVisible();
    await page.keyboard.press("Escape");

    // Lint findings.
    await rail.getByRole("button", { name: /Observatory/ }).click();
    const observatory = page.getByRole("region", { name: "Observatory" });
    await expect(observatory).toBeVisible();
    await expect(observatory).not.toContainText("firewall");
    await expect(observatory).not.toContainText("infra/index.md");

    // And on the wire: not a node, not an edge endpoint, not a count, not a
    // finding, not a title.
    await expect.poll(() => api.length).toBeGreaterThan(3);
    for (const response of api) {
      for (const id of [...HIDDEN, "Firewall", "Proxy", "Runbook", "Incident"]) {
        expect(response.body, `${response.url} discloses ${id}`).not.toContain(id);
      }
    }
  });

  test("gets the not-found state, never a 403, for a concept it cannot see", async ({ page }) => {
    const api = recordApi(page);
    await signIn(page, NARROW_TOKEN, false, `/ui/?kb=atlas&concept=${encodeURIComponent(HIDDEN_IN_MAP)}`);
    const inspector = page.getByRole("complementary", { name: `Inspector for ${HIDDEN_IN_MAP}` });
    await expect(inspector.getByRole("alert")).toContainText("Not found");

    const concept = api.find((r) => r.url.includes("/concept?"));
    expect(concept?.status).toBe(404);
    expect(api.filter((r) => r.status === 403)).toEqual([]);
    // The same answer as for a concept that does not exist at all.
    const missing = await page.evaluate(async (token) => {
      const r = await fetch("/api/ui/v1/kbs/atlas/concept?id=infra/never-was", {
        headers: { Authorization: `Bearer ${token}` },
      });
      return { status: r.status, body: await r.text() };
    }, NARROW_TOKEN);
    expect(missing.status).toBe(404);
    expect(concept?.body).toBe(missing.body);
  });
});
