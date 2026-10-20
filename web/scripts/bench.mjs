// Benchmark of the Atlas at the scale it has to handle (#861) -- not part of CI.
//
// It serves the ten test KBs `make dev` serves (internal/testkbs, 5,000
// concepts down to 30) with bin/cartographer, opens the Atlas in Chromium and
// measures, for each KB:
//   - graph: navigation to the first drawn frame (cold, then warm), frame time
//     at rest and while orbiting, p50/p95 over 5 s, JS heap;
//   - map: the largest Map's graph, opened in the app;
//   - switch: from the previous KB's graph to this one, in the app;
//   - panels: Work, Health, Activity, Artifacts, Settings, opened in the app;
//     time until no skeleton is left, and the longest main-thread task;
//   - api: each UI API route the Atlas calls, warm server time.
// "In the app" means a history entry plus popstate, the path Back and Forward
// take: no reload, so the time is the view's own, not the bundle's.
// It prints the GL renderer string, so a software (SwiftShader) run is never
// mistaken for a GPU one. Report the numbers with the hardware in
// docs/testing.md §Atlas UI budgets.
//
// Usage (from web/, after `make build` at the repo root):
//   node scripts/bench.mjs [--data <dir>] [--kbs test-5000,test-800] [--gpu] [--skip-frames]
//   --data   a data directory (one KB per subdirectory), copied before serving;
//            without it the fleet is generated (about a minute)
//   --gpu    headed Chromium on the machine's GPU; without it the run is
//            headless, on whatever the browser falls back to

import { chromium } from "@playwright/test";
import { spawn, execFileSync } from "node:child_process";
import { cpSync, mkdtempSync, readdirSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const WEB = join(dirname(fileURLToPath(import.meta.url)), "..");
const ROOT = join(WEB, "..");
const BIN = join(ROOT, "bin", "cartographer");
const args = process.argv.slice(2);
const opt = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const gpu = args.includes("--gpu");
const skipFrames = args.includes("--skip-frames");

const freePort = () =>
  new Promise((resolve) => {
    const s = createServer().listen(0, "127.0.0.1", () => {
      const { port } = s.address();
      s.close(() => resolve(port));
    });
  });
const pct = (xs, p) => {
  const s = [...xs].sort((a, b) => a - b);
  return s.length ? s[Math.min(s.length - 1, Math.floor(s.length * p))] : NaN;
};
const round = (x) => Math.round(x * 10) / 10;
const log = (...m) => console.error(`[bench ${new Date().toISOString().slice(11, 19)}]`, ...m);

const work = mkdtempSync(join(tmpdir(), "cartographer-bench-"));
const data = join(work, "data");
if (opt("--data")) {
  cpSync(opt("--data"), data, { recursive: true });
} else {
  log("generating the test KBs");
  execFileSync("go", ["run", "./internal/testkbs/gen", data], { cwd: ROOT, stdio: "ignore" });
}
const kbs = (opt("--kbs")?.split(",") ?? readdirSync(data).filter((d) => !d.startsWith(".")))
  // Largest first: the fleet's names end in their size.
  .sort((a, b) => (Number(b.split("-").pop()) || 0) - (Number(a.split("-").pop()) || 0));

const port = await freePort();
const base = `http://127.0.0.1:${port}`;
const api = `${base}/api/ui/v1`;
const started = Date.now();
const server = spawn(
  BIN,
  ["serve", "--data", data, "--http", `127.0.0.1:${port}`, "--update-check=false"],
  { env: { ...process.env, CARTOGRAPHER_AUTH: "false", CARTOGRAPHER_TOKENS: "" }, stdio: "ignore" },
);
const cleanup = () => {
  server.kill();
  rmSync(work, { recursive: true, force: true });
};
process.on("exit", cleanup);
// Ctrl-C or a kill would skip "exit" and leave the server running, orphaned.
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => process.exit(130));

// /health answers before the KBs are indexed: wait for the UI API to report
// every one ready.
let readyMs = NaN;
for (let i = 0; i < 1200; i++) {
  try {
    const body = await (await fetch(`${api}/kbs`)).json();
    if (body.kbs?.length && body.kbs.every((k) => k.ready)) {
      readyMs = Date.now() - started;
      break;
    }
  } catch {}
  await new Promise((r) => setTimeout(r, 250));
}
log(`server up, ${kbs.length} KBs ready in ${readyMs} ms`);

/** Warm server time of each route the Atlas calls for one KB. */
async function apiTimes(kb) {
  const routes = [
    "overview",
    "graph",
    "lint?severity_min=info",
    "search?q=topic&limit=20",
    "changes?since=2000-01-01T00:00:00Z&limit=500",
    "work?limit=200",
    "maintenance/summary",
    "maintenance/review",
    "checks",
    "births",
    "status",
    "artifacts",
    "maps",
    "audit",
  ];
  const out = {};
  for (const r of routes) {
    const url = `${api}/kbs/${kb}/${r}`;
    await (await fetch(url)).arrayBuffer();
    const t = performance.now();
    const body = await (await fetch(url)).arrayBuffer();
    out[r.split("?")[0]] = { ms: Math.round(performance.now() - t), kib: Math.round(body.byteLength / 1024) };
  }
  return out;
}

const browser = await chromium.launch({
  headless: !gpu,
  args: gpu ? [] : ["--use-angle=swiftshader", "--enable-unsafe-swiftshader"],
});
// 2x on a GPU, as on a laptop screen; 1x in software, where 2x would only
// measure SwiftShader's fill rate.
const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: gpu ? 2 : 1 });
await context.addInitScript(() => {
  window.__longtasks = [];
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) window.__longtasks.push({ start: e.startTime, ms: e.duration });
  }).observe({ type: "longtask", buffered: true });
});
const page = await context.newPage();

/** The graph has drawn its first frame and nothing is still loading. */
const graphDrawn = () =>
  page.waitForFunction(
    () => document.querySelector(".graph__loading")?.getAttribute("data-drawn") === "true",
    null,
    { timeout: 120_000, polling: 16 },
  );
/** No skeleton left in the page, for two frames in a row. */
const settled = () =>
  page.waitForFunction(
    () => {
      const w = window;
      const none = !document.querySelector(".skeleton, .state--loading");
      w.__settledFrames = none ? (w.__settledFrames ?? 0) + 1 : 0;
      return w.__settledFrames >= 2;
    },
    null,
    { timeout: 120_000, polling: "raf" },
  );
const twoFrames = () => page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
/** Navigates in the app, as Back/Forward do, and times until `until` holds,
 *  after the response whose URL contains `waitFor`, when given. */
async function inApp(search, until, waitFor) {
  const mark = await page.evaluate(() => performance.now());
  const response = waitFor
    ? page.waitForResponse((r) => r.url().includes(waitFor), { timeout: 120_000 }).catch(() => null)
    : null;
  const t = Date.now();
  await page.evaluate((s) => {
    history.pushState(null, "", `/ui/${s}`);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, search);
  await response;
  await until();
  const ms = Date.now() - t;
  const longest = await page.evaluate(
    (m) => Math.max(0, ...window.__longtasks.filter((e) => e.start >= m).map((e) => e.ms)),
    mark,
  );
  return { ms, longestTaskMs: Math.round(longest) };
}

async function frameTimes(ms, during) {
  await page.evaluate(() => {
    const w = window;
    w.__intervals = [];
    w.__last = performance.now();
    w.__measuring = true;
    const tick = (t) => {
      if (!w.__measuring) return;
      w.__intervals.push(t - w.__last);
      w.__last = t;
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });
  await (during ? during() : page.waitForTimeout(ms));
  return page.evaluate(() => {
    window.__measuring = false;
    return window.__intervals.slice(1);
  });
}

let renderer = "unknown";
let previous = null;
for (const kb of kbs) {
  const row = { kb, concepts: 0 };
  const overview = await (await fetch(`${api}/kbs/${kb}/overview`)).json();
  row.concepts = overview.concepts?.total ?? 0;

  // Graph: a full navigation, cold then warm.
  const load = async () => {
    const t = Date.now();
    await page.goto(`${base}/ui/?kb=${kb}`);
    await graphDrawn();
    await twoFrames();
    return Date.now() - t;
  };
  row.graphColdMs = await load();
  row.graphWarmMs = await load();
  if (renderer === "unknown") {
    renderer = await page.evaluate(() => {
      const gl = document.createElement("canvas").getContext("webgl2");
      const info = gl?.getExtension("WEBGL_debug_renderer_info");
      return info ? gl.getParameter(info.UNMASKED_RENDERER_WEBGL) : "unknown";
    });
    log("renderer", renderer);
  }
  await page.waitForTimeout(3000); // past the settle and the re-framing
  row.heapMB = round((await page.evaluate(() => performance.memory?.usedJSHeapSize ?? NaN)) / 2 ** 20);
  if (!skipFrames) {
    const rest = await frameTimes(5000);
    const box = await page.locator("[data-testid=graph-view] canvas").first().boundingBox();
    const cx = box.x + box.width / 2;
    const cy = box.y + box.height / 2;
    await page.mouse.move(cx, cy);
    await page.mouse.down();
    const orbit = await frameTimes(0, async () => {
      const start = Date.now();
      while (Date.now() - start < 5000) {
        const s = (Date.now() - start) / 1000;
        await page.mouse.move(cx + 140 * Math.cos(s * 2), cy + 90 * Math.sin(s * 2));
        await page.waitForTimeout(16);
      }
    });
    await page.mouse.up();
    Object.assign(row, {
      restP50: round(pct(rest, 0.5)),
      restP95: round(pct(rest, 0.95)),
      orbitP50: round(pct(orbit, 0.5)),
      orbitP95: round(pct(orbit, 0.95)),
    });
  }

  // The largest Map, then back to the whole KB.
  const biggest = [...(overview.collections ?? [])].sort((a, b) => b.concepts - a.concepts)[0];
  if (biggest) {
    row.map = {
      name: biggest.name,
      concepts: biggest.concepts,
      ...(await inApp(`?kb=${kb}&scope=${biggest.name}`, graphDrawn, `/kbs/${kb}/graph?`)),
    };
    await inApp(`?kb=${kb}`, graphDrawn);
  }

  // Panels, in the app.
  row.panels = {};
  for (const panel of ["work", "health", "activity", "artifacts", "settings"]) {
    row.panels[panel] = await inApp(`?kb=${kb}&panel=${panel}`, settled);
  }

  // Switching KB: from the previous KB's graph to this one's.
  if (previous) {
    await inApp(`?kb=${previous}`, graphDrawn);
    await page.waitForTimeout(500);
    row.switchFrom = previous;
    // The tab has drawn this KB before (the graph loads above): the switch
    // is to a KB whose last view the app keeps, refreshed in place.
    row.switch = await inApp(`?kb=${kb}`, graphDrawn, `/kbs/${kb}/graph`);
  }
  row.api = await apiTimes(kb);
  previous = kb;
  console.log(JSON.stringify(row));
  log(kb, "done");
}

console.log(JSON.stringify({ renderer, readyMs, kbs: kbs.length }));
await browser.close();
process.exit(0); // the child server keeps the event loop alive: the exit handler stops it
