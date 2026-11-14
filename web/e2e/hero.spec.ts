import { Film, filmUse, RECORD, RECORD_TIMEOUT_MS } from "./film";
import { expect, LOCAL_URL, test } from "./support";

// The README's hero animation (D330, D790), as a flow the suite runs on every
// web change. In the suite it walks the fixture at full speed: a step the UI no
// longer offers fails here, which is how the recording goes stale loudly.
// scripts/record-hero.sh runs the same flow over the demo KB with HERO_RECORD
// set, and the tour is then filmed frame by frame on a virtual clock (see Film):
// the beats slow down to a watchable pace, and the output no longer depends on
// how fast the machine paints.

test.use(filmUse);
if (RECORD) test.setTimeout(RECORD_TIMEOUT_MS);

const KB = process.env.HERO_KB ?? "atlas";
const CONCEPT = process.env.HERO_CONCEPT ?? "infra/gateway";
const QUERY = process.env.HERO_QUERY ?? "gateway";
const ARTIFACT = process.env.HERO_ARTIFACT ?? "review";

test("the hero tour: graph, search, a concept's links, artifacts, health", async ({ page }, testInfo) => {
  const film = RECORD ? new Film(page, testInfo) : null;
  await film?.start();

  /** A pause the viewer needs and the check does not. */
  const beat = (ms: number) => (film ? film.hold(ms) : Promise.resolve());
  /** A state the tour waits for: the plain assertion in the suite, frames until it holds in a recording. */
  const until = (what: string, check: (timeout: number | undefined) => Promise<unknown>) =>
    film ? film.until(what, check) : check(undefined);

  /** Orbits the camera with a slow drag across the canvas. */
  async function orbit(dx: number, steps: number): Promise<void> {
    const box = await page.locator("[data-testid=graph-view] canvas").first().boundingBox();
    if (!box) throw new Error("the graph has no canvas");
    const x = box.x + box.width / 2;
    const y = box.y + box.height / 2;
    await page.mouse.move(x, y);
    await page.mouse.down();
    if (!film) {
      await page.mouse.move(x + dx, y + dx / 8, { steps: 2 });
    } else {
      // One step per frame, so the turn takes as long as it looks like it does.
      for (let i = 1; i <= steps; i++) {
        await page.mouse.move(x + (dx * i) / steps, y + (dx / 8) * (i / steps));
        await film.frame();
      }
    }
    await page.mouse.up();
  }

  await page.addInitScript(() => {
    localStorage.setItem("cartographer.theme", "dark");
    localStorage.setItem("cartographer.colorBy", "community");
  });
  await page.goto(`${LOCAL_URL}/ui/?kb=${KB}`);
  const canvas = page.locator("[data-testid=graph-view] canvas").first();
  // A software renderer charts the demo KB slowly: the recording waits, and
  // filming starts once the graph is drawn.
  // The page loads on timers a paused clock holds back: filming advances it from the start.
  if (film) await film.untilDrawn();
  else {
    await expect(canvas).toBeVisible();
    await expect(page.locator(".graph__loading")).toHaveAttribute("data-drawn", "true");
  }

  // 1. The graph settles and turns.
  await beat(800);
  await orbit(320, 120);
  await beat(400);

  // 2. Search: the palette, a query typed, the camera flies to the concept.
  await page.keyboard.press("ControlOrMeta+k");
  const palette = page.getByRole("dialog", { name: "Search concepts" });
  await until("the search palette opens", (timeout) => expect(palette).toBeVisible({ timeout }));
  if (film) {
    for (const key of QUERY) {
      await page.keyboard.type(key);
      await film.hold(110);
    }
  } else {
    await page.keyboard.type(QUERY);
  }
  const hit = palette.locator(`[data-concept-id="${CONCEPT}"]`).first();
  await until(`the search finds ${CONCEPT}`, (timeout) => expect(hit).toBeVisible({ timeout }));
  await beat(600);
  await hit.click();
  const inspector = page.getByRole("complementary", { name: `Inspector for ${CONCEPT}` });
  await until("the inspector opens", (timeout) => expect(inspector).toBeVisible({ timeout }));
  await beat(1400);

  // 3. Its links, named both ways.
  await inspector.getByRole("tab", { name: /Links/ }).click();
  await beat(1300);
  await inspector.getByRole("button", { name: "Close inspector" }).click();

  // 4. The same graph, coloured by Map (the legend keys the Maps; there is no switch).
  await until("the legend keys the Maps", (timeout) => expect(page.getByRole("region", { name: "Graph legend" })).toBeVisible({ timeout }));
  await orbit(-260, 100);
  await beat(600);

  // 5. What the KB ships to agents: a skill and the concepts it reads.
  await page.getByRole("button", { name: "Artifacts", exact: true }).click();
  await beat(700);
  await page.getByRole("button", { name: new RegExp(`^${ARTIFACT}\\b`) }).first().click();
  const skill = page.getByText(`skills/${ARTIFACT}/SKILL.md`).first();
  await until("the skill opens", (timeout) => expect(skill).toBeVisible({ timeout }));
  await beat(1600);

  // 6. What needs attention.
  await page.getByRole("button", { name: /^Health/ }).click();
  const findings = page.getByRole("heading", { level: 1, name: /found/i });
  await until("the health findings show", (timeout) => expect(findings).toBeVisible({ timeout }));
  await beat(1400);

  // 7. The Surveyor's tab, always there: its Sources by ingest status. No Surveyor
  // reports on the demo server, so the tab shows the template and the Sources block.
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  const surveyorTab = page.getByRole("tab", { name: /^Surveyor\b/ });
  await until("the Settings tabs show", (timeout) => expect(surveyorTab).toBeVisible({ timeout }));
  await beat(500);
  await surveyorTab.click();
  const sourcesBlock = page.getByRole("list", { name: "Sources by status" });
  await until("the Surveyor tab shows its Sources", (timeout) => expect(sourcesBlock).toBeVisible({ timeout }));
  await beat(1800);

  // Back where the loop starts.
  await page.getByRole("button", { name: "Atlas", exact: true }).click();
  await until("the graph is back", (timeout) => expect(canvas).toBeVisible({ timeout }));
  await beat(600);
  if (film) console.log(`hero: ${film.frames} frames, ${film.seconds.toFixed(1)} virtual seconds`);
});
