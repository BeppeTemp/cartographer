import { appUrl, EXT_TOKEN, extUrl, LOCAL_URL, expect, signIn, test, waitForAtlas, withPanelsOpen } from "./support";

// The extensible Atlas (D1938), against the "ext" server of e2e/run.sh: auth
// on, and a pinned plugin `e2e-board` whose atlas side declares one view
// (Registry, a table over GET /api/v1/kbs) and one app (Board, a link to
// /board/ on the app origin, another loopback port). Every test also fails if the
// Atlas page contacts an origin other than the servers under test
// (support.ts); the app's origin is not among them, so the page that draws the
// link must never touch it.
test.use({ reducedMotion: "reduce" });
test.beforeEach(({ page }) => withPanelsOpen(page));

const VIEW_TITLE = "Registry";
const APP_TITLE = "Board";
const appPage = (): string => `${appUrl()}/board/`;

/** The request line and headers the app origin recorded (e2e/app-origin.mjs). */
type Recorded = { method: string; url: string; headers: Record<string, string> };

test("a plugin's view is in the rail and draws its route", async ({ page }) => {
  await signIn(page, EXT_TOKEN, false, "/ui/", extUrl());
  await waitForAtlas(page);

  const rail = page.getByRole("navigation", { name: "Atlas navigation" });
  await expect(rail.getByRole("heading", { name: "Extensions" })).toBeVisible();
  const entry = rail.getByRole("button", { name: VIEW_TITLE, exact: true });
  await entry.click();
  await expect(entry).toHaveAttribute("aria-current", "page");

  // The renderer draws the answer of the declared route as text: one row per
  // KB the server mounts, under the labels the plugin chose.
  await expect(page.getByText("Supplied by plugin e2e-board")).toBeVisible();
  const table = page.getByRole("table", { name: VIEW_TITLE });
  await expect(table.getByRole("columnheader", { name: "KB" })).toBeVisible();
  await expect(table.getByRole("columnheader", { name: "Status" })).toBeVisible();
  for (const kb of ["atlas", "annex", "void"]) {
    await expect(table.getByRole("cell", { name: kb, exact: true })).toBeVisible();
  }
});

test("an app opens in a new tab on its own origin, with no token and no Referer", async ({ page, request }) => {
  await signIn(page, EXT_TOKEN, false, "/ui/", extUrl());
  await waitForAtlas(page);

  const rail = page.getByRole("navigation", { name: "Atlas navigation" });
  const link = rail.getByRole("link", { name: `${APP_TITLE}, opens ${new URL(appUrl()).host} in a new tab` });
  await expect(link).toHaveAttribute("href", appPage());
  await expect(link).toHaveAttribute("target", "_blank");

  // The tab is a page of the context, whether or not the browser keeps an opener.
  const [app] = await Promise.all([page.context().waitForEvent("page"), link.click()]);
  await expect(app.getByRole("heading", { name: "The external board" })).toBeVisible();
  expect(new URL(app.url()).origin).toBe(new URL(appUrl()).origin);
  expect(new URL(app.url()).origin).not.toBe(new URL(extUrl()).origin);
  expect(await app.evaluate(() => ({ referrer: document.referrer, opener: window.opener === null }))).toEqual({
    referrer: "",
    opener: true,
  });

  // What the app's server received for that navigation.
  const response = await request.get(`${appUrl()}/__requests`);
  expect(response.ok()).toBe(true);
  const visits = ((await response.json()) as Recorded[]).filter((r) => r.url === "/board/");
  expect(visits.length, "the app origin saw no navigation to /board/").toBeGreaterThan(0);
  for (const visit of visits) {
    expect(visit.method).toBe("GET");
    // A header the browser did send, so an empty record cannot pass for "nothing sent".
    expect(visit.headers["user-agent"]).toBeTruthy();
    expect(Object.keys(visit.headers)).not.toContain("authorization");
    expect(Object.keys(visit.headers)).not.toContain("referer");
    expect(JSON.stringify(visit)).not.toContain(EXT_TOKEN);
  }
});

test("loading an extension adds no directive to the Content-Security-Policy", async ({ request }) => {
  // The extension is really loaded on this server: one view and one app.
  const listed = await request.get(`${extUrl()}/api/ui/v1/extensions`, { headers: { Authorization: `Bearer ${EXT_TOKEN}` } });
  expect(listed.ok()).toBe(true);
  const extensions = (await listed.json()) as { views: { plugin: string; name: string }[]; apps: { plugin: string; url: string }[] };
  expect(extensions.views.map((v) => `${v.plugin}/${v.name}`)).toEqual(["e2e-board/registry"]);
  expect(extensions.apps.map((a) => a.url)).toEqual([appPage()]);

  const policy = async (base: string): Promise<string> => {
    const response = await request.get(`${base}/ui/`);
    expect(response.ok()).toBe(true);
    return response.headers()["content-security-policy"] ?? "";
  };
  const plain = await policy(LOCAL_URL);
  expect(plain).toContain("script-src 'self'");
  expect(await policy(extUrl())).toBe(plain);
  expect(plain).not.toContain("frame-src");
  expect(plain).not.toContain(new URL(appUrl()).host);
});
