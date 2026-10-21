// What an open, untouched Atlas tab costs the machine it runs on (#861) -- not
// part of CI.
//
// It serves the eleven test KBs `make dev` serves (internal/testkbs) with
// bin/cartographer, opens the Atlas in Chromium and leaves it alone. For each
// scenario it reports, over the window, the CPU each Chromium process type
// spent (percent of one core), their resident memory, the page's JS heap, the
// frames the page drew per second and the server's CPU:
//   - graph-live:  the whole-KB graph, motion on (the default), after the
//                  panorama has started turning;
//   - graph-still: the same with motion off;
//   - work:        the Work panel open over the graph, motion on;
//   - hidden:      the tab behind another one.
// --soak <min> then leaves graph-live open that long and samples heap and
// resident memory every 30 s, to see whether they grow.
//
// Run it headed on the GPU (`--gpu`): headless Chromium draws WebGL in
// software (SwiftShader), which measures the CPU drawing the scene, not the
// Atlas.
//
// Usage (from web/, after `make build` at the repo root):
//   node scripts/idle.mjs [--data <dir>] [--kbs test-5000,test-30] [--window 30] [--soak 10] [--gpu]

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
const windowS = Number(opt("--window") ?? 30);
const soakMin = Number(opt("--soak") ?? 0);
const log = (...m) => console.error(`[idle ${new Date().toISOString().slice(11, 19)}]`, ...m);
const round = (x) => Math.round(x * 10) / 10;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const freePort = () =>
  new Promise((resolve) => {
    const s = createServer().listen(0, "127.0.0.1", () => {
      const { port } = s.address();
      s.close(() => resolve(port));
    });
  });

/** CPU seconds and resident KiB of `root` and its descendants, by type. */
function sample(root) {
  const rows = execFileSync("ps", ["-A", "-o", "pid=,ppid=,time=,rss=,command="], { encoding: "utf8" })
    .split("\n")
    .filter(Boolean)
    .map((l) => {
      const m = l.trim().match(/^(\d+)\s+(\d+)\s+(\S+)\s+(\d+)\s+(.*)$/);
      return m && { pid: +m[1], ppid: +m[2], time: m[3], rss: +m[4], cmd: m[5] };
    })
    .filter(Boolean);
  const kids = new Map();
  for (const r of rows) kids.set(r.ppid, [...(kids.get(r.ppid) ?? []), r]);
  const secs = (t) => {
    // [[dd-]hh:]mm:ss.cc
    const [d, rest] = t.includes("-") ? t.split("-") : ["0", t];
    return rest.split(":").reduce((a, p) => a * 60 + Number(p), 0) + Number(d) * 86400;
  };
  const out = {};
  const add = (r) => {
    const type = r.cmd.match(/--type=([\w-]+)/)?.[1] ?? "browser";
    const kind = type === "gpu-process" ? "gpu" : type === "renderer" ? "renderer" : type === "browser" ? "browser" : "other";
    out[kind] ??= { cpu: 0, rssKiB: 0 };
    out[kind].cpu += secs(r.time);
    out[kind].rssKiB += r.rss;
    for (const k of kids.get(r.pid) ?? []) add(k);
  };
  const top = rows.find((r) => r.pid === root);
  if (top) add(top);
  return out;
}

const work = mkdtempSync(join(tmpdir(), "cartographer-idle-"));
const data = join(work, "data");
if (opt("--data")) {
  cpSync(opt("--data"), data, { recursive: true });
} else {
  log("generating the test KBs");
  execFileSync("go", ["run", "./internal/testkbs/gen", data], { cwd: ROOT, stdio: "ignore" });
}
const kbs = (opt("--kbs")?.split(",") ?? ["test-5000", "ferry-line", "test-30"]);

const port = await freePort();
const base = `http://127.0.0.1:${port}`;
const server = spawn(
  BIN,
  ["serve", "--data", data, "--http", `127.0.0.1:${port}`, "--update-check=false"],
  { env: { ...process.env, CARTOGRAPHER_AUTH: "false", CARTOGRAPHER_TOKENS: "" }, stdio: "ignore" },
);
process.on("exit", () => {
  server.kill();
  rmSync(work, { recursive: true, force: true });
});
// Ctrl-C or a kill would skip "exit" and leave the server running, orphaned.
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => process.exit(130));
for (let i = 0; i < 1200; i++) {
  try {
    const body = await (await fetch(`${base}/api/ui/v1/kbs`)).json();
    if (body.kbs?.length && body.kbs.every((k) => k.ready)) break;
  } catch {}
  await sleep(250);
}
log(`server up: ${readdirSync(data).length} KBs`);

const browser = await chromium.launch({
  headless: !gpu,
  args: gpu ? [] : ["--use-angle=swiftshader", "--enable-unsafe-swiftshader"],
});
// launch() hides the browser's pid: it is this process's Chromium child, the
// root of the tree sample() walks.
const browserPid = Number(
  execFileSync("ps", ["-A", "-o", "pid=,ppid=,command="], { encoding: "utf8" })
    .split("\n")
    .map((l) => l.trim().split(/\s+/, 3))
    .find(([, ppid, cmd]) => Number(ppid) === process.pid && /chrom/i.test(cmd ?? ""))?.[0],
);
if (!browserPid) throw new Error("cannot find the Chromium process");
const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: gpu ? 2 : 1 });
await context.addInitScript(() => {
  // Frames the page drew: one rAF callback per frame while it is visible.
  window.__frames = 0;
  const count = () => {
    window.__frames++;
    requestAnimationFrame(count);
  };
  requestAnimationFrame(count);
});
const page = await context.newPage();
const graphDrawn = () =>
  page
    .waitForFunction(() => document.querySelector(".graph__loading")?.getAttribute("data-drawn") === "true", null, {
      timeout: 60_000,
    })
    .catch(async (e) => {
      log("not drawn:", page.url(), await page.evaluate(() => document.body.innerText.slice(0, 400)));
      throw e;
    });
const heapMB = () => page.evaluate(() => performance.memory?.usedJSHeapSize ?? NaN).then((b) => round(b / 2 ** 20));

/** Measures `windowS` seconds of whatever the page is doing now. */
async function measure() {
  const b0 = sample(browserPid);
  const s0 = sample(server.pid);
  const f0 = await page.evaluate(() => window.__frames);
  await sleep(windowS * 1000);
  const b1 = sample(browserPid);
  const s1 = sample(server.pid);
  const f1 = await page.evaluate(() => window.__frames);
  const pct = (k) => round((((b1[k]?.cpu ?? 0) - (b0[k]?.cpu ?? 0)) / windowS) * 100);
  const total = Object.values(b1).reduce((a, v) => a + v.rssKiB, 0);
  return {
    cpu: { renderer: pct("renderer"), gpu: pct("gpu"), browser: pct("browser"), other: pct("other") },
    cpuTotal: round(["renderer", "gpu", "browser", "other"].reduce((a, k) => a + pct(k), 0)),
    rssMB: round(total / 1024),
    heapMB: await heapMB(),
    fps: round((f1 - f0) / windowS),
    serverCpu: round((((s1.browser?.cpu ?? 0) - (s0.browser?.cpu ?? 0)) / windowS) * 100),
  };
}

async function open(kb, { motion = true, panel } = {}) {
  await page.goto(`${base}/ui/`);
  await page.evaluate((m) => localStorage.setItem("cartographer.panel.motion", m ? "1" : "0"), motion);
  await page.goto(`${base}/ui/?kb=${kb}${panel ? `&panel=${panel}` : ""}`);
  // A panel replaces the graph: wait for its content instead.
  if (panel)
    await page.waitForFunction(() => !document.querySelector(".skeleton, .state--loading"), null, { timeout: 60_000 });
  else await graphDrawn();
  // Past the opening burst, the settle and the 10 s of quiet before the
  // panorama turns: the steady state a tab left open reaches.
  await sleep(15_000);
}

let renderer = "unknown";
for (const kb of kbs) {
  const row = { kb };
  await open(kb);
  if (renderer === "unknown") {
    renderer = await page.evaluate(() => {
      const gl = document.createElement("canvas").getContext("webgl2");
      const info = gl?.getExtension("WEBGL_debug_renderer_info");
      return info ? gl.getParameter(info.UNMASKED_RENDERER_WEBGL) : "unknown";
    });
    log("renderer", renderer);
  }
  row["graph-live"] = await measure();
  log(kb, "graph-live", JSON.stringify(row["graph-live"]));

  const other = await context.newPage();
  await other.goto("about:blank");
  await other.bringToFront();
  await sleep(2000);
  row.hidden = await measure();
  log(kb, "hidden", JSON.stringify(row.hidden));
  await other.close();
  await page.bringToFront();

  await open(kb, { motion: false });
  row["graph-still"] = await measure();
  log(kb, "graph-still", JSON.stringify(row["graph-still"]));

  await open(kb, { panel: "work" });
  row.work = await measure();
  log(kb, "work", JSON.stringify(row.work));
  console.log(JSON.stringify(row));
}

if (soakMin > 0) {
  const kb = kbs[0];
  await open(kb);
  const points = [];
  const t0 = Date.now();
  while (Date.now() - t0 < soakMin * 60_000) {
    const b = sample(browserPid);
    points.push({
      min: round((Date.now() - t0) / 60_000),
      heapMB: await heapMB(),
      rssMB: round(Object.values(b).reduce((a, v) => a + v.rssKiB, 0) / 1024),
    });
    log("soak", JSON.stringify(points.at(-1)));
    await sleep(30_000);
  }
  console.log(JSON.stringify({ soak: kb, points }));
}

console.log(JSON.stringify({ renderer, windowS, gpu }));
await browser.close();
process.exit(0); // the exit handler stops the server
