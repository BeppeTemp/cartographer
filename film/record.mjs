// Renders the film frame by frame. The vite server must already be running (see README.md).
// --no-mp4 skips the MP4 (CI only needs the WebP); a page error or a failed frame exits non-zero (D1068).
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const { chromium } = createRequire(path.join(here, "../web/package.json"))("playwright");

const args = process.argv.slice(2);
const opt = (name, dflt) => { const i = args.indexOf(`--${name}`); return i < 0 ? dflt : args[i + 1]; };
const noMp4 = args.includes("--no-mp4"); // CI publishes the WebP only
const stills = new Set((opt("stills", "") || "").split(",").filter(Boolean).map(Number));
const fpsOpt = Number(opt("fps", 0)) || 0;
const url = opt("url", "http://localhost:5180/?record");
const out = path.join(here, "out");
const framesDir = path.join(out, "frames");
// A full render starts from an empty frames dir: stale frames from a longer run would end up in the video and the WebP.
if (!stills.size) fs.rmSync(framesDir, { recursive: true, force: true });
fs.mkdirSync(framesDir, { recursive: true });

const browser = await chromium.launch({
  headless: true,
  args: ["--use-angle=swiftshader", "--use-gl=angle", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"],
});
const page = await browser.newPage({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
// A page error or a failed frame is a broken render, not a log line: the process exits non-zero (D1068), which
// is how a camera change in the engine that breaks the film turns the CI job red.
const pageErrors = [];
page.on("pageerror", (e) => { pageErrors.push(e.message); console.error("page error:", e.message); });
const fail = async (why) => { console.error(`record: ${why}`); await browser.close().catch(() => {}); process.exit(1); };
// The first load of a cold vite server pre-bundles three and the engine: well over the default 30 s on a runner.
await page.goto(url, { timeout: 180000 });
await page.waitForFunction(() => window.film, null, { timeout: 60000 });
const { fps, frames } = await page.evaluate(() => ({ fps: window.film.fps, frames: window.film.frames }));
if (fpsOpt && fpsOpt !== fps) console.warn(`the page runs at ${fps} fps; --fps ${fpsOpt} only changes the encode`);
const rate = fpsOpt || fps;
const stillFrames = new Map([...stills].map((s) => [Math.round(s * fps), s]));
const last = stills.size ? Math.max(...stillFrames.keys()) : frames - 1;

for (let n = 0; n <= last; n++) {
  try { await page.evaluate((i) => window.film.frame(i), n); } catch (e) { await fail(`frame ${n} failed: ${e.message}`); }
  if (pageErrors.length) await fail(`page error at frame ${n}: ${pageErrors[0]}`);
  if (stills.size) {
    if (stillFrames.has(n)) await page.screenshot({ path: path.join(out, `still-${stillFrames.get(n)}.png`) });
  } else await page.screenshot({ path: path.join(framesDir, `${String(n).padStart(5, "0")}.png`) });
  if (n % 60 === 0) console.log(`frame ${n}/${stills.size ? last : frames - 1}`);
}
await browser.close();
if (pageErrors.length) { console.error(`record: page error: ${pageErrors[0]}`); process.exit(1); }
if (!stills.size) {
  const rendered = fs.readdirSync(framesDir).filter((f) => f.endsWith(".png")).length;
  if (rendered !== frames) { console.error(`record: rendered ${rendered} frames, the film has ${frames}`); process.exit(1); }
}

if (!stills.size) {
  const input = ["-y", "-framerate", String(rate), "-i", path.join(framesDir, "%05d.png")];
  if (!noMp4) execFileSync("ffmpeg", [...input, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18", "-r", String(rate), path.join(out, "film.mp4")], { stdio: "inherit" });
  // The README's WebP: 1120 px wide (the page shows it at ~900) and 30 fps, since browsers do not honour
  // animated-image frame delays much under 20 ms. A key frame every 15 frames (-kmin/-kmax) and -exact: between
  // key frames the lossy encoder treats near-identical pixels as unchanged, so whatever fades slowly (a card, its
  // shadow) stays on screen as ghost stripes until the next key frame, which by default never comes. Homebrew's ffmpeg has no libwebp encoder: ffmpeg only
  // picks and scales the frames, img2webp (from the webp package) assembles the animation.
  const webpDir = path.join(out, "webp-frames");
  fs.rmSync(webpDir, { recursive: true, force: true });
  fs.mkdirSync(webpDir, { recursive: true });
  const step = Math.max(1, Math.round(rate / 30));
  execFileSync("ffmpeg", ["-y", "-loglevel", "error", "-i", path.join(framesDir, "%05d.png"), "-vf", `select='not(mod(n\\,${step}))',scale=1120:-1:flags=lanczos`, "-fps_mode", "vfr", path.join(webpDir, "%05d.png")], { stdio: "inherit" });
  const pngs = fs.readdirSync(webpDir).filter((f) => f.endsWith(".png")).sort().map((f) => path.join(webpDir, f));
  execFileSync("img2webp", ["-loop", "0", "-d", String(Math.round((1000 * step) / rate)), "-lossy", "-q", "85", "-m", "4", "-exact", "-kmin", "14", "-kmax", "15", ...pngs, "-o", path.join(out, "film.webp")], { stdio: "inherit" });
}
