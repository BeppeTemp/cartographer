import { type ChildProcess, spawn } from "node:child_process";
import { resolve } from "node:path";
import { Film, filmUse, RECORD, RECORD_TIMEOUT_MS } from "./film";
import { expect, LOCAL_URL, test } from "./support";

// The Surveyor at work (the README's second Atlas animation, next to hero.spec.ts):
// the Atlas on a KB, Settings -> Surveyor with no Surveyor reported, then a REAL
// `cartographer surveyor` started over a feed campaign, and what the Atlas shows
// while it works: the run in Activity, its settings, the new concepts in the graph,
// and the feed that failed in Work. The model is scripted (scripts/stub-model.mjs)
// and one recipe needs none (exec kind); scripts/demo-surveyor.mjs builds all of it.
//
// In the suite it runs at full speed over a KB of its own (e2e/run.sh); a step the
// UI no longer offers fails here, and the script the stub serves must be consumed to
// the last turn, which pins the Surveyor's prompts and order. scripts/record-hero.sh
// films it (HERO_SPEC=surveyor.spec.ts) on the same virtual clock as the tour.
//
// The Surveyor's reports reach the Atlas on its heartbeat (60 s), so the wait for the
// run is real time: the film cuts it (nothing is shot while time passes).

test.use(filmUse);
test.setTimeout(RECORD ? RECORD_TIMEOUT_MS : 5 * 60_000);

const need = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is unset: run the spec through make e2e-web or make hero SPEC=surveyor.`);
  return v;
};
const URL = process.env.SURVEYOR_URL ?? LOCAL_URL;
const KB = process.env.SURVEYOR_KB ?? process.env.HERO_KB ?? "atlas";
const CONFIG = need("SURVEYOR_CONFIG");
const STUB = need("SURVEYOR_STUB");
const NOTES = need("SURVEYOR_NOTES").split(",");
const BIN = resolve(process.cwd(), "..", "bin", "cartographer");
/** The real time the Surveyor may take to work and report (one heartbeat, 60 s, and its tasks). */
const REPORT_BUDGET_MS = 150_000;

test("the Surveyor at work: sources, a run, new concepts, a failed feed", async ({ page }, testInfo) => {
  const film = RECORD ? new Film(page, testInfo) : null;
  await film?.start();
  const beat = (ms: number) => (film ? film.hold(ms) : Promise.resolve());
  const until = (what: string, check: (timeout: number | undefined) => Promise<unknown>) =>
    film ? film.until(what, check) : check(undefined);

  let surveyor: ChildProcess | null = null;
  try {
    await page.addInitScript(() => {
      localStorage.setItem("cartographer.theme", "dark");
      localStorage.setItem("cartographer.colorBy", "community");
    });
    await page.goto(`${URL}/ui/?kb=${KB}`);
    const canvas = page.locator("[data-testid=graph-view] canvas").first();
    // The page loads on timers a paused clock holds back: filming advances it from the start.
    if (film) await film.untilDrawn();
    else {
      await expect(canvas).toBeVisible();
      await expect(page.locator(".graph__loading")).toHaveAttribute("data-drawn", "true");
    }
    await beat(800);

    // 1. Settings -> Surveyor: no Surveyor yet, and the Sources the KB declares.
    const surveyorTab = page.getByRole("tab", { name: "Surveyor", exact: true });
    const sources = page.getByRole("list", { name: "Sources by status" });
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await until("the Settings tabs show", (timeout) => expect(surveyorTab).toBeVisible({ timeout }));
    await surveyorTab.click();
    await until("the Surveyor tab shows its Sources", (timeout) => expect(sources).toContainText("pending", { timeout }));
    await expect(page.getByText(/No Surveyor has reported/)).toBeVisible();
    await beat(2200);

    // 2. A real Surveyor starts. Its token and key are placeholders the stub and the
    // server (auth off) accept; they are long enough that the settings report, which
    // refuses to carry the request's token, cannot hold them by chance.
    surveyor = spawn(BIN, ["surveyor", "--config", CONFIG], {
      env: { ...process.env, SURVEYOR_TOKEN: "film-surveyor-token-7f3a", STUB_API_KEY: "film-model-key-9c1d" },
      stdio: ["ignore", "ignore", "inherit"],
    });
    const exited = new Promise<number | null>((done) => surveyor!.once("exit", done));
    void exited.then((code) => {
      if (surveyor) console.error(`surveyor: exited early (${code})`);
    });

    // 3. Activity: its run. A heartbeat carries the report, so this waits in real
    // time (a cut in the film) and reopens the page, which refetches, now and then.
    const activity = page.getByRole("button", { name: /^Activity/ });
    const work = page.getByRole("button", { name: /^Work/ });
    const runs = page.getByRole("list", { name: "Surveyor runs" });
    await activity.click();
    const from = Date.now();
    for (let probe = 0; ; probe++) {
      try {
        await expect(runs).toBeVisible({ timeout: 1000 });
        break;
      } catch (error) {
        if (Date.now() - from > REPORT_BUDGET_MS) throw new Error(`the Surveyor's runs never reached Activity: ${String(error)}`);
      }
      if (film) await film.advance(2000);
      if (probe % 3 === 2) {
        await work.click();
        await activity.click();
      }
    }
    await expect(runs).toContainText("field-notes");
    await beat(2800);

    // 4. Settings -> Surveyor again: now it has reported, and one feed failed.
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await surveyorTab.click();
    await until("the Surveyor has reported", (timeout) => expect(page.getByText(/replica/).first()).toBeVisible({ timeout }));
    await expect(page.getByRole("list", { name: "Sources whose last poll failed" })).toBeVisible();
    await beat(2800);

    // 5. The graph gained what the run wrote: the search finds the new concepts.
    await page.getByRole("button", { name: "Atlas", exact: true }).click();
    await until("the graph is back", (timeout) => expect(canvas).toBeVisible({ timeout }));
    await page.keyboard.press("ControlOrMeta+k");
    const palette = page.getByRole("dialog", { name: "Search concepts" });
    await until("the search palette opens", (timeout) => expect(palette).toBeVisible({ timeout }));
    const query = NOTES[0].split("/")[1];
    if (film) {
      for (const key of query) {
        await page.keyboard.type(key);
        await film.hold(110);
      }
    } else {
      await page.keyboard.type(query);
    }
    const hit = palette.locator(`[data-concept-id="${NOTES[0]}"]`).first();
    await until(`the search finds ${NOTES[0]}`, (timeout) => expect(hit).toBeVisible({ timeout }));
    await beat(1200);
    await page.keyboard.press("Escape");
    await beat(300);

    // 6. Work: the feed that failed, beside the Surveyor that reported it. The film ends here.
    await work.click();
    const feeds = page.getByRole("list", { name: "Feeds in error" });
    await until("the failed feed shows in Work", (timeout) => expect(feeds).toBeVisible({ timeout }));
    await expect(feeds).toContainText("503");
    await beat(3200);

    // Every turn of the script was used, and none was refused.
    const state = (await (await fetch(`${STUB}/state`)).json()) as { served: number; total: number; failed: number };
    expect(state, "the scripted model served its whole script").toEqual({ served: state.total, total: state.total, failed: 0 });
    if (film) console.log(`surveyor: ${film.frames} frames, ${film.seconds.toFixed(1)} virtual seconds`);
  } finally {
    const child = surveyor as ChildProcess | null;
    surveyor = null; // the early-exit note is for a Surveyor that was not stopped by us
    if (child && child.exitCode === null) {
      child.kill("SIGTERM");
      await new Promise((done) => {
        child.once("exit", done);
        setTimeout(done, 10_000);
      });
    }
  }
});
