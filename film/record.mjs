// Renders the film frame by frame. The vite server must already be running (see README.md).
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const { chromium } = createRequire(path.join(here, "../web/package.json"))("playwright");

const args = process.argv.slice(2);
const opt = (name, dflt) => { const i = args.indexOf(`--${name}`); return i < 0 ? dflt : args[i + 1]; };
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
page.on("pageerror", (e) => console.error("page error:", e.message));
await page.goto(url);
await page.waitForFunction(() => window.film, null, { timeout: 60000 });
const { fps, frames } = await page.evaluate(() => ({ fps: window.film.fps, frames: window.film.frames }));
if (fpsOpt && fpsOpt !== fps) console.warn(`the page runs at ${fps} fps; --fps ${fpsOpt} only changes the encode`);
const rate = fpsOpt || fps;
const stillFrames = new Map([...stills].map((s) => [Math.round(s * fps), s]));
const last = stills.size ? Math.max(...stillFrames.keys()) : frames - 1;

for (let n = 0; n <= last; n++) {
  await page.evaluate((i) => window.film.frame(i), n);
  if (stills.size) {
    if (stillFrames.has(n)) await page.screenshot({ path: path.join(out, `still-${stillFrames.get(n)}.png`) });
  } else await page.screenshot({ path: path.join(framesDir, `${String(n).padStart(5, "0")}.png`) });
  if (n % 60 === 0) console.log(`frame ${n}/${stills.size ? last : frames - 1}`);
}
await browser.close();

if (!stills.size) {
  const input = ["-y", "-framerate", String(rate), "-i", path.join(framesDir, "%05d.png")];
  execFileSync("ffmpeg", [...input, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18", "-r", String(rate), path.join(out, "film.mp4")], { stdio: "inherit" });
  // The README's WebP: 1280 px wide (the page shows it at ~900) and 30 fps, since browsers do not honour
  // animated-image frame delays much under 20 ms. Homebrew's ffmpeg has no libwebp encoder: ffmpeg only
  // picks and scales the frames, img2webp (from the webp package) assembles the animation.
  const webpDir = path.join(out, "webp-frames");
  fs.rmSync(webpDir, { recursive: true, force: true });
  fs.mkdirSync(webpDir, { recursive: true });
  const step = Math.max(1, Math.round(rate / 30));
  execFileSync("ffmpeg", ["-y", "-loglevel", "error", "-i", path.join(framesDir, "%05d.png"), "-vf", `select='not(mod(n\\,${step}))',scale=1280:-1:flags=lanczos`, "-fps_mode", "vfr", path.join(webpDir, "%05d.png")], { stdio: "inherit" });
  const pngs = fs.readdirSync(webpDir).filter((f) => f.endsWith(".png")).sort().map((f) => path.join(webpDir, f));
  execFileSync("img2webp", ["-loop", "0", "-d", String(Math.round((1000 * step) / rate)), "-lossy", "-q", "75", "-m", "3", ...pngs, "-o", path.join(out, "film.webp")], { stdio: "inherit" });
}
