// Benchmark of the living 3D atlas (D234) -- not part of CI.
//
// For each size it generates a demo KB (scripts/demo-kb.mjs), serves it with
// bin/cartographer, opens the 3D view in Chromium and measures:
//   - cold load: navigation to the first rendered 3D frame;
//   - warm load: the same, on a reload with the cache primed;
//   - frame time at rest, live mode, p50/p95 over 5 s;
//   - frame time while a node is dragged in circles for 5 s, p50/p95;
//   - JS heap after load (Chromium's performance.memory).
// It prints the GL renderer string, so a software (SwiftShader) run is never
// mistaken for a GPU one. Report the numbers with the hardware in
// docs/testing.md §Atlas UI budgets.
//
// Usage (from web/, after `make build` at the repo root):
//   node scripts/bench3d.mjs [sizes=1000,2000] [--gpu]
//   --gpu runs headed Chromium on the machine's GPU; without it the run is
//   headless, on whatever the browser falls back to.

import { chromium } from "@playwright/test";
import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const WEB = join(dirname(fileURLToPath(import.meta.url)), "..");
const BIN = join(WEB, "..", "bin", "cartographer");
const args = process.argv.slice(2);
const gpu = args.includes("--gpu");
const sizes = (args.find((a) => !a.startsWith("--")) ?? "1000,2000").split(",").map(Number);

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
const log = (...m) => console.error(`[bench3d ${new Date().toISOString().slice(11, 19)}]`, ...m);

/** Frame intervals over `ms`, measured in the page with rAF. */
async function frameTimes(page, ms, during) {
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

/** Finds a node on screen by hovering until 3d-force-graph shows a tooltip. */
async function findNode(page) {
  const box = await page.locator("[data-testid=graph-3d] canvas").first().boundingBox();
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  for (let r = 0; r < 260; r += 12) {
    for (let a = 0; a < 360; a += r === 0 ? 360 : 30) {
      const x = cx + r * Math.cos((a * Math.PI) / 180);
      const y = cy + r * Math.sin((a * Math.PI) / 180);
      await page.mouse.move(x, y);
      await page.waitForTimeout(60);
      const text = await page.evaluate(
        () => [...document.querySelectorAll(".float-tooltip-kap")].map((e) => e.textContent).join(""),
      );
      if (text.trim()) return { x, y };
    }
  }
  return null;
}

async function run(size) {
  const dir = mkdtempSync(join(tmpdir(), "cartographer-bench3d-"));
  execFileSync("node", [join(WEB, "scripts", "demo-kb.mjs"), dir, String(size)], { stdio: "ignore" });
  const port = await freePort();
  const server = spawn(BIN, ["serve", "--init", "--http", `127.0.0.1:${port}`, "--kb", join(dir, "demo")], {
    env: { ...process.env, CARTOGRAPHER_AUTH: "false" },
    stdio: "ignore",
  });
  const base = `http://127.0.0.1:${port}`;
  // /health answers before a fresh KB is indexed: wait for the UI API to
  // report it ready.
  for (let i = 0; i < 240; i++) {
    try {
      const body = await (await fetch(`${base}/api/ui/v1/kbs`)).text();
      if (body.includes('"ready":true')) break;
    } catch {}
    await new Promise((r) => setTimeout(r, 250));
  }

  const browser = await chromium.launch({
    headless: !gpu,
    args: gpu ? [] : ["--use-angle=swiftshader", "--enable-unsafe-swiftshader"],
  });
  // 2x on a GPU, as on a laptop screen; 1x in software, where 2x would only
  // measure SwiftShader's fill rate.
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: gpu ? 2 : 1 });
  await context.addInitScript(() => localStorage.setItem("cartographer.panel.3d", "1"));
  const page = await context.newPage();
  const url = `${base}/ui/?kb=demo`;

  const load = async () => {
    const start = Date.now();
    await page.goto(url);
    try {
      await page.locator("[data-testid=graph-3d] canvas").first().waitFor({ timeout: 60_000 });
    } catch (err) {
      const shot = join(tmpdir(), `bench3d-${size}.png`);
      await page.screenshot({ path: shot });
      log(size, "no 3D canvas; screenshot at", shot);
      throw err;
    }
    await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
    return Date.now() - start;
  };
  log(size, "server up, loading");
  const cold = await load();
  log(size, "cold", cold);
  const warm = await load();
  const renderer = await page.evaluate(() => {
    const gl = document.createElement("canvas").getContext("webgl2");
    const info = gl?.getExtension("WEBGL_debug_renderer_info");
    return info ? gl.getParameter(info.UNMASKED_RENDERER_WEBGL) : "unknown";
  });
  await page.waitForTimeout(3000); // past the settle and the re-framing
  const heap = await page.evaluate(() => performance.memory?.usedJSHeapSize ?? NaN);
  const rest = await frameTimes(page, 5000);
  log(size, "rest frames", rest.length);

  let drag = [];
  const node = await findNode(page);
  log(size, "node", node);
  if (node) {
    await page.mouse.move(node.x, node.y);
    await page.mouse.down();
    drag = await frameTimes(page, 0, async () => {
      const start = Date.now();
      while (Date.now() - start < 5000) {
        const t = (Date.now() - start) / 1000;
        await page.mouse.move(node.x + 140 * Math.cos(t * 2), node.y + 90 * Math.sin(t * 2));
        await page.waitForTimeout(16);
      }
    });
    await page.mouse.up();
  }
  const finite = await page.evaluate(() => !document.querySelector(".state__title"));

  await browser.close();
  server.kill();
  rmSync(dir, { recursive: true, force: true });
  return {
    concepts: size,
    renderer,
    coldMs: cold,
    warmMs: warm,
    heapMB: round(heap / 2 ** 20),
    restP50: round(pct(rest, 0.5)),
    restP95: round(pct(rest, 0.95)),
    dragP50: node ? round(pct(drag, 0.5)) : "no node found",
    dragP95: node ? round(pct(drag, 0.95)) : "no node found",
    viewAlive: finite,
  };
}

for (const size of sizes) console.log(JSON.stringify(await run(size)));
