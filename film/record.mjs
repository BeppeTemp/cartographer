// Renders the film frame by frame. The vite server must already be running (see README.md), or use film/render.sh.
// Exits non-zero when the page raised an error or fewer frames than the film has were rendered (D1068): that is
// what turns a broken import or camera in web/src/lib/graph3d into a red CI job.
// --no-mp4 skips the MP4 (CI publishes only the WebP).
// --frames N renders only frames 0..N-1 and skips the "the film has <frames>" count check; the encode is unchanged
// (film-check.yml runs it on every pull request that touches film/, to try the tools of the runner).
import { createRequire } from "node:module";
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const { chromium } = createRequire(path.join(here, "../web/package.json"))("playwright");

const args = process.argv.slice(2);
const opt = (name, dflt) => { const i = args.indexOf(`--${name}`); return i < 0 ? dflt : args[i + 1]; };
const stills = new Set((opt("stills", "") || "").split(",").filter(Boolean).map(Number));
const noMp4 = args.includes("--no-mp4");
const framesOpt = Math.floor(Number(opt("frames", 0))) || 0;
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
const pageErrors = [];
page.on("pageerror", (e) => { pageErrors.push(e.message); console.error("page error:", e.message); });
await page.goto(url);
await page.waitForFunction(() => window.film, null, { timeout: 60000 });
const { fps, frames } = await page.evaluate(() => ({ fps: window.film.fps, frames: window.film.frames }));
if (fpsOpt && fpsOpt !== fps) console.warn(`the page runs at ${fps} fps; --fps ${fpsOpt} only changes the encode`);
const rate = fpsOpt || fps;
const stillFrames = new Map([...stills].map((s) => [Math.round(s * fps), s]));
const last = stills.size ? Math.max(...stillFrames.keys()) : Math.min(framesOpt || frames, frames) - 1;

const fail = async (msg) => { console.error(`record: ${msg}`); await browser.close(); process.exit(1); };
for (let n = 0; n <= last; n++) {
  try {
    await page.evaluate((i) => window.film.frame(i), n);
  } catch (e) {
    await fail(`frame ${n} failed: ${e.message}`);
  }
  if (pageErrors.length) await fail(`the page raised ${pageErrors.length} error(s) at frame ${n}, the first: ${pageErrors[0]}`);
  if (stills.size) {
    if (stillFrames.has(n)) await page.screenshot({ path: path.join(out, `still-${stillFrames.get(n)}.png`) });
  } else await page.screenshot({ path: path.join(framesDir, `${String(n).padStart(5, "0")}.png`) });
  if (n % 60 === 0) console.log(`frame ${n}/${stills.size ? last : frames - 1}`);
}
if (pageErrors.length) await fail(`the page raised ${pageErrors.length} error(s), the first: ${pageErrors[0]}`);
const rendered = await page.evaluate(() => window.film.rendered());
if (rendered !== last + 1) await fail(`the page rendered ${rendered} frames, expected ${last + 1}`);
if (!stills.size && !framesOpt && rendered !== frames) await fail(`rendered ${rendered} frames, the film has ${frames}`);
await browser.close();

if (!stills.size) {
  if (!noMp4) {
    const input = ["-y", "-framerate", String(rate), "-i", path.join(framesDir, "%05d.png")];
    execFileSync("ffmpeg", [...input, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18", "-r", String(rate), path.join(out, "film.mp4")], { stdio: "inherit" });
  }
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
  // -exact is img2webp 1.4+: the CI runner's Ubuntu ships 1.3, which refuses it. Without it the key frames still bound
  // a ghost stripe to half a second.
  const exact = img2webpHas("-exact") ? ["-exact"] : [];
  execFileSync("img2webp", ["-loop", "0", "-d", String(Math.round((1000 * step) / rate)), "-lossy", "-q", "85", "-m", "4", ...exact, "-kmin", "14", "-kmax", "15", ...pngs, "-o", path.join(out, "film.webp")], { stdio: "inherit" });
}

// img2webpHas reports whether this img2webp lists an option in its help, where it reads " -exact, -noexact ...".
function img2webpHas(option) {
  const r = spawnSync("img2webp", ["-h"], { encoding: "utf8" });
  return `${r.stdout}${r.stderr}`.split(/[\s,]+/).includes(option);
}
