// The launch film: one page, one pure function of time. frame(n) sets every overlay from t = n / fps,
// fires the graph events whose time has passed, then lets the real Atlas engine draw the frame.
import "../../docs/brand/cartographer.css";
import "../../web/src/styles/tokens.css";
import logoUrl from "../../docs/brand/coordinate-pine.svg";
import { LivingScene, type SceneLink, type SceneNode, type ScenePalette } from "../../web/src/lib/graph3d/scene";
import { focusPose } from "../../web/src/lib/graph3d/camera";
import { beside } from "../../web/src/lib/graph3d/physics";
import { planBursts } from "../../web/src/lib/graph3d/motion";
import { resolveSlots } from "../../web/src/lib/palette";
import { buildKb } from "./data";

const FPS = 30;
const DURATION = 28;
const W = 1920;
const H = 1080;
const GW = 1000; // the graph's canvas: the right part of the frame; the left column (x 120-940) is for cards and captions
const GX = W - GW;
const RECORD = new URLSearchParams(location.search).has("record");
const TUESDAY_ZOOM = 1.25; // how much wider than focus() the Tuesday camera frames the neighbourhood
const COL = 120; // left margin of the text column
const CARD_W = 820;
const NEW_ID = "team/staging-deploys";

// --- easing ---------------------------------------------------------------
const clamp01 = (x: number) => Math.min(1, Math.max(0, x));
const prog = (t: number, t0: number, dur: number) => clamp01((t - t0) / dur);
const easeOut = (p: number) => 1 - Math.pow(1 - p, 3);
const easeInOut = (p: number) => (p < 0.5 ? 4 * p * p * p : 1 - Math.pow(-2 * p + 2, 3) / 2);
const lerp = (a: number, b: number, p: number) => a + (b - a) * p;

// --- DOM ------------------------------------------------------------------
const stage = document.getElementById("stage")!;
function mk<K extends keyof HTMLElementTagNameMap>(tag: K, cls = "", html = "", style = ""): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag);
  el.className = cls;
  el.innerHTML = html;
  el.style.cssText = style;
  stage.appendChild(el);
  return el;
}
/** Opacity + a rise (px, 0 = none), composed with a base transform. */
function vis(el: HTMLElement, opacity: number, rise = 0, extra = "") {
  const o = clamp01(opacity);
  el.style.opacity = String(o);
  el.style.visibility = o <= 0.001 ? "hidden" : "visible";
  el.style.transform = `${extra} translateY(${rise * (1 - clamp01(opacity))}px)`.trim();
}
/** Reveal: easeOutCubic over 600 ms, 16 px rise. */
const reveal = (t: number, t0: number, dur = 0.6) => easeOut(prog(t, t0, dur));
const fade = (t: number, t0: number, dur = 0.4) => 1 - prog(t, t0, dur);

const graph = mk("canvas", "", "", `left:${GX}px;top:0;width:${GW}px;height:${H}px`);
graph.id = "graph";
graph.width = GW;
graph.height = H;
const gctx = graph.getContext("2d")!;

const SVG = "http://www.w3.org/2000/svg";
const contours = document.createElementNS(SVG, "svg");
contours.id = "contours";
contours.setAttribute("viewBox", `0 0 ${W} ${H}`);
stage.appendChild(contours);
const CONTOURS = [
  { y: 210, amp: 56, k: 0.0021, ph: 0.4, v: 5 },
  { y: 420, amp: 84, k: 0.0014, ph: 2.1, v: -4 },
  { y: 700, amp: 66, k: 0.0026, ph: 4.0, v: 6 },
  { y: 920, amp: 48, k: 0.0017, ph: 1.2, v: -5 },
].map((c) => {
  const p = document.createElementNS(SVG, "path");
  p.setAttribute("fill", "none");
  p.setAttribute("stroke", "var(--carto-border)");
  p.setAttribute("stroke-width", "1.5");
  p.setAttribute("opacity", "0.8");
  contours.appendChild(p);
  return { ...c, p };
});

const ring = document.createElementNS(SVG, "svg");
ring.id = "ring";
ring.setAttribute("viewBox", `0 0 ${W} ${H}`);
const RING = { cx: 1470, cy: 540, rx: 400, ry: 380 };
ring.innerHTML = `<ellipse cx="${RING.cx}" cy="${RING.cy}" rx="${RING.rx}" ry="${RING.ry}" fill="none" stroke="var(--carto-clay-text)" stroke-width="3"/>`;
ring.style.cssText = `position:absolute;left:0;top:0;pointer-events:none;opacity:0;transform-origin:${RING.cx}px ${RING.cy}px`;
stage.appendChild(ring);

const title = mk("div", "title", "Every agent starts<br>from zero.", "left:120px;top:440px");
const dayMon = mk("div", "day", "Monday.", `left:${COL}px;top:96px`);
const dayTue = mk("div", "day", "Tuesday.", `left:${COL}px;top:96px`);
const cap = (s: string) => mk("div", "caption", s, `left:${COL}px;bottom:90px`);
const capA = cap("One wiki, in git.");
const capB = cap("None of them can break it.");
const capC = cap("Different machines, different clients.<br>One memory.");
capC.style.whiteSpace = "nowrap"; // two explicit lines, never a mid-sentence wrap

function card(label: string, cls = "") {
  const c = mk("div", `card ${cls}`, `<div class="bar"><i class="live"></i><i></i><i></i><b>${label}</b></div><div class="body"></div>`);
  return { el: c, label: c.querySelector("b")!, body: c.querySelector(".body") as HTMLElement };
}
const small = [card("Claude Code"), card("Codex"), card("OpenCode")];
const SM_W = 546; // the small cards are 420 x 180 scaled by 1.3 (bar included)
const SM_H = 244;
const SMALL = [
  { x: 1100, y: 160 },
  { x: 1190, y: 430 },
  { x: 1120, y: 700 },
];
const DOTS = [[40, 40], [110, 22], [170, 62], [240, 30], [300, 70], [90, 84], [350, 36]];
const EDGES = [[0, 1], [1, 2], [2, 3], [3, 4], [0, 5], [5, 2], [3, 6]];
small.forEach((c) => {
  c.body.style.cssText = "padding:0;position:relative";
  c.body.innerHTML = `<svg class="mem" width="546" height="172" viewBox="0 0 420 132" style="top:0;left:0">${
    EDGES.map(([a, b]) => `<line x1="${DOTS[a!]![0]! + 10}" y1="${DOTS[a!]![1]! + 20}" x2="${DOTS[b!]![0]! + 10}" y2="${DOTS[b!]![1]! + 20}" stroke="#3B473E" stroke-width="2"/>`).join("")
  }${DOTS.map(([x, y], i) => `<circle cx="${x! + 10}" cy="${y! + 20}" r="${i % 3 === 0 ? 9 : 7}" fill="${i % 2 ? "#E9E8DE" : "#91B9A0"}"/>`).join("")}</svg>`;
});
const bob = card("bob · Codex", "clay");
// The big cards sit in the left column; their heights are fitted to their final content in measureCards().
const BIG = { x: COL, y: 210, w: CARD_W, h: 400 };
Object.assign(bob.el.style, { left: `${BIG.x}px`, top: `${BIG.y}px`, width: `${BIG.w}px`, height: `${BIG.h}px` });

const noteGood = mk("div", "note", "staging-deploys", "left:0;top:0");
const noteBad = mk("div", "note bad", "stagin-deploy", "left:0;top:0");
const tag = mk("div", "tag", "✗ broken link [[infra/stagin]]", "left:0;top:0");
// Created before the veil, so the end card covers it.
const nodeLabel = mk("div", "", "staging-deploys",
  "left:0;top:0;font:26px Georgia,serif;color:#252824;background:#FCFAF6;border:1px solid #DCDDD5;border-radius:8px;padding:4px 10px;white-space:nowrap;visibility:hidden");
const veil = mk("div", "", "");
veil.id = "veil";
const logo = mk("img", "", "", "left:310px;top:392px;width:150px;height:150px") as HTMLImageElement;
logo.src = logoUrl;
const word = mk("div", "wordmark", "Cartographer", "left:500px;top:378px");
const sub = mk("div", "sub", "Shared memory for your team's AI agents", "left:504px;top:520px");
const url = mk("div", "url", "cartographer-demo.beppetemp.com/mcp &nbsp;·&nbsp; github.com/BeppeTemp/cartographer", "left:504px;top:596px");

// --- terminal scripts -----------------------------------------------------
interface Line {
  start: number;
  text: string;
  cls?: string;
  /** characters per second; absent = the line is revealed whole */
  cps?: number;
}
const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;");
// Prompts the agent types fast (64 cps) so the commit lands before the note lifts at 7.4 s; the answer is 44 cps.
const ALICE: Line[] = [
  { start: 4.6, cps: 64, text: "> Staging fails: migrations run before the secret rotates. Remember it for the team." },
  { start: 6.1, cls: "tool", text: "● concept_write  team/staging-deploys" },
  { start: 6.8, cls: "tool", text: "✓ committed 4f2a9c1" },
  { start: 11.0, cls: "err", text: "✗ rejected: broken link [[infra/stagin]]" },
];
const BOB: Line[] = [
  { start: 15.0, cps: 32, text: "> Why does staging fail?" },
  { start: 16.1, cls: "tool", text: '● search  "staging"' },
  { start: 16.6, cls: "tool", text: "● concept_read  team/staging-deploys" },
  { start: 17.6, cps: 44, text: "Migrations run before the secret rotates — alice's agent wrote it on Monday." },
];
const cache = new WeakMap<HTMLElement, string>();
function renderLines(body: HTMLElement, lines: Line[], t: number) {
  let html = "";
  for (const l of lines) {
    if (t < l.start) continue;
    let text = l.text;
    let o = 1;
    let cursor = false;
    if (l.cps) {
      const n = Math.min(text.length, Math.floor((t - l.start) * l.cps));
      cursor = n < text.length;
      text = text.slice(0, n);
    } else o = reveal(t, l.start);
    const rise = Math.round(16 * (1 - o));
    html += `<div class="${l.cls ?? ""}" style="opacity:${o.toFixed(3)};transform:translateY(${rise}px)">${esc(text)}${cursor ? '<span class="cur"></span>' : ""}</div>`;
  }
  if (cache.get(body) !== html) {
    body.innerHTML = html;
    cache.set(body, html);
  }
}
/** Fits each big card to its final content, once, so it never resizes while the lines type in. */
let aliceH = BIG.h;
let bobH = BIG.h;
function fitHeight(c: { el: HTMLElement; body: HTMLElement }, lines: Line[]): number {
  const keep = { css: c.body.style.cssText, html: c.body.innerHTML, w: c.el.style.width, h: c.el.style.height };
  c.body.style.cssText = "";
  c.body.innerHTML = lines.map((l) => `<div>${esc(l.text)}</div>`).join("");
  c.el.style.width = `${CARD_W}px`;
  c.el.style.height = "auto";
  const h = c.el.offsetHeight; // layout height, unaffected by the preview's scale transform
  c.body.style.cssText = keep.css;
  c.body.innerHTML = keep.html;
  c.el.style.width = keep.w;
  c.el.style.height = keep.h;
  cache.delete(c.body);
  return h;
}
function measureCards() {
  aliceH = fitHeight(small[0]!, ALICE);
  bobH = fitHeight(bob, BOB);
  bob.el.style.height = `${bobH}px`;
}
function setRect(el: HTMLElement, x: number, y: number, w: number, h: number) {
  el.style.left = `${x}px`;
  el.style.top = `${y}px`;
  el.style.width = `${w}px`;
  el.style.height = `${h}px`;
}
function setText(el: HTMLElement, s: string) {
  if (el.textContent !== s) el.textContent = s;
}

// --- the graph ------------------------------------------------------------
const kb = buildKb();
const slots = resolveSlots();
const css = (n: string) => getComputedStyle(document.documentElement).getPropertyValue(n).trim();
const palette: ScenePalette = {
  background: css("--surface-0"),
  edge: css("--graph-edge-3d"),
  edgeActive: css("--graph-edge-3d-active"),
  signal: css("--graph-signal"),
  ring: css("--graph-ring"),
};
const host = document.createElement("div");
host.style.cssText = "position:absolute;left:-9999px;top:0";
document.body.appendChild(host);
const sc = new LivingScene(host, { onSelect() {}, onHover() {}, onLost() {} }, {
  live: true,
  reducedMotion: false,
  offscreen: { width: GW, height: H, pixelRatio: 1 },
});
// project() and focus() read clientWidth/Height, which a canvas gets from its CSS size, not its buffer.
sc.canvas.style.cssText = `display:block;width:${GW}px;height:${H}px`;

const nodes: SceneNode[] = kb.nodes.map((n) => ({ id: n.id, weight: n.weight }));
const colourOf = new Map<string, string>(kb.nodes.map((n) => [n.id, slots[n.community + 1]!]));
colourOf.set(NEW_ID, slots[6]!);
let links: SceneLink[] = kb.links.map((l) => ({ ...l }));
let selected: string | null = null;

const mix = (hex: string, into: string, amount: number) => {
  const p = (h: string) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16));
  const a = p(hex.length === 7 ? hex : "#8a988a");
  const b = p(into);
  return "#" + a.map((v, i) => Math.round(lerp(v, b[i]!, amount)).toString(16).padStart(2, "0")).join("");
};
function recolour() {
  const near = new Set<string>();
  if (selected) {
    near.add(selected);
    for (const n of sc.neighboursOf(selected)) near.add(n.id);
  }
  sc.setColours(nodes.map((n) => (selected && !near.has(n.id) ? mix(colourOf.get(n.id)!, palette.background, 0.72) : colourOf.get(n.id)!)), palette);
}
sc.setData(nodes, links, 300);
recolour();
sc.fitEverything(0, 0);

function addNote() {
  const anchor = nodes.find((n) => n.id === "infra/ci")!;
  nodes.push({ id: NEW_ID, weight: 0.35, ...beside(anchor, NEW_ID), vx: 0, vy: 0, vz: 0 } as SceneNode);
  links = [...links, ...["infra/ci", "services/api", "runbooks/deploy"].map((target) => ({ source: NEW_ID, target }))];
  sc.setData(nodes, links, 0);
  recolour();
  sc.fitEverything(0, 1500);
}
function select() {
  selected = NEW_ID;
  const deg = new Map<string, number>();
  for (const l of links) for (const e of [l.source, l.target] as string[]) deg.set(e, (deg.get(e) ?? 0) + 1);
  sc.setSelection(NEW_ID);
  recolour();
  sc.sendSignals(planBursts(NEW_ID, links as { source: string; target: string }[], (id) => deg.get(id) ?? 0, true));
  sc.focus(NEW_ID, { left: 0, right: 0 }, () => {}); // selects the follow target; its own 750 ms ease is replaced just below
  // focus() frames the neighbourhood so tightly that the faded nodes swell into pale discs. Ease to the same pose with a
  // wider radius instead, over 2 s: animateTo/neighbourhoodRadius/camera are private in TypeScript only.
  // Private on purpose: zoomBy is a fixed 220 ms tween and would read as a jump, so this couples the film to scene.ts's private animateTo.
  const internals = sc as unknown as {
    animateTo(to: () => ReturnType<typeof focusPose>, ms: number): void;
    neighbourhoodRadius(n: SceneNode): number;
    camera: { position: { clone(): { x: number; y: number; z: number } }; fov: number };
  };
  const node = sc.nodeById(NEW_ID)!;
  const from = internals.camera.position.clone();
  internals.animateTo(
    () => focusPose(node as Required<SceneNode>, from, internals.neighbourhoodRadius(node) * TUESDAY_ZOOM, { width: GW, height: H, fov: internals.camera.fov, occludedRight: 0, occludedLeft: 0 }),
    2000,
  );
}
const EVENTS: { t: number; run: () => void; done?: boolean }[] = [
  { t: 3.5, run: () => sc.fitEverything(0, 1500) },
  { t: 8.5, run: addNote },
  { t: 17.0, run: select },
];

// --- the frame ------------------------------------------------------------
const gctr = { x: GX + GW / 2, y: 540 };
const bez = (a: number[], c: number[], b: number[], u: number) => [0, 1].map((i) => (1 - u) ** 2 * a[i]! + 2 * (1 - u) * u * c[i]! + u * u * b[i]!);
const place = (el: HTMLElement, p: number[], o: number, extra = "") => {
  el.style.left = `${p[0]! - 140}px`;
  el.style.top = `${p[1]! - 38}px`;
  vis(el, o, 0, extra);
};

function overlays(tFilm: number) {
  const t = tFilm;
  // contours: drift a few px per second
  for (const c of CONTOURS) {
    const t = Math.min(tFilm, 26); // the end card holds still from 26 s
    let d = "";
    for (let x = -40; x <= W + 40; x += 40) d += `${x === -40 ? "M" : "L"}${x} ${(c.y + c.amp * Math.sin(c.k * (x + c.v * t) + c.ph) + c.amp * 0.35 * Math.sin(c.k * 2.3 * (x + c.v * t))).toFixed(1)} `;
    c.p.setAttribute("d", d);
  }
  // beat 1
  vis(title, reveal(t, 0.2) * fade(t, 3.3, 0.5), 16);
  small.forEach((c, i) => {
    const s = SMALL[i]!;
    let o = reveal(t, 0.3 + 0.3 * i);
    if (i > 0) o *= fade(t, 3.5, 0.6);
    if (i > 0 || t < 3.5) (c.body.firstElementChild as SVGElement).style.opacity = String(fade(t, 2.2 + 0.4 * i, 0.5));
    if (i === 0) {
      const e = easeInOut(prog(t, 3.5, 1.0));
      setRect(c.el, lerp(s.x, BIG.x, e), lerp(s.y, BIG.y, e), lerp(SM_W, BIG.w, e), lerp(SM_H, aliceH, e));
      setText(c.label, t >= 4.0 ? "alice · Claude Code" : "Claude Code");
      o *= fade(t, 14.0, 0.6);
      if (t >= 3.5) {
        c.body.style.cssText = "";
        renderLines(c.body, ALICE, t);
      }
      c.el.style.display = t > 14.7 ? "none" : "block";
    } else setRect(c.el, s.x, s.y, SM_W, SM_H);
    vis(c.el, o, 16);
  });
  vis(dayMon, reveal(t, 3.7) * fade(t, 13.8, 0.3), 16);
  vis(dayTue, reveal(t, 14.2), 16);
  vis(capA, reveal(t, 8.8) * fade(t, 11.7, 0.4), 16);
  vis(capB, reveal(t, 12.2) * fade(t, 13.8, 0.4), 16);
  vis(capC, reveal(t, 19.2) * fade(t, 21.6, 0.4), 16);
  vis(bob.el, reveal(t, 14.4), 16);
  renderLines(bob.body, BOB, t);
  // the graph appears with the Monday scene and leaves with the veil
  graph.style.opacity = String(easeOut(prog(t, 3.8, 1.2)));
  // Both notes leave from the card's right edge, vertically centred on the card.
  const cy = BIG.y + aliceH / 2;
  const start = [BIG.x + CARD_W, cy];
  // note 1: lifts out of the card, an eased arc to the centre of the graph, then a dot
  {
    const u = easeInOut(prog(t, 7.4, 1.1));
    const p = bez(start, [1150, cy - 230], [gctr.x, gctr.y], u);
    const shrink = easeInOut(prog(t, 8.5, 0.3));
    const o = clamp01(prog(t, 7.4, 0.3)) * (1 - prog(t, 8.8, 0.3));
    place(noteGood, p, o, `scale(${(1 - 0.92 * shrink).toFixed(3)})`);
  }
  // note 2: flies at the graph, bounces off its edge (the ring), upright all the way
  {
    const dy = (cy - RING.cy) / RING.ry;
    const hit = [RING.cx - RING.rx * Math.sqrt(Math.max(0, 1 - dy * dy)) + 40, cy];
    const goes = easeInOut(prog(t, 9.7, 1.3));
    const back = easeOut(prog(t, 11.0, 0.9));
    const away = [start[0]! + 150, cy - 90];
    const p = t < 11.0 ? bez(start, [(start[0]! + hit[0]!) / 2, cy - 70], hit, goes) : [lerp(hit[0]!, away[0]!, back), lerp(hit[1]!, away[1]!, back) - 50 * Math.sin(Math.PI * back)];
    place(noteBad, p, clamp01(prog(t, 9.7, 0.4)) * (1 - prog(t, 11.4, 0.9)));
    // the clay tag sits just under the bounce point, clear of the card
    tag.style.left = `${hit[0]! - 40}px`;
    tag.style.top = `${hit[1]! + 62}px`;
  }
  vis(tag, reveal(t, 11.1) * fade(t, 13.2, 0.6), 16);
  {
    const p = prog(t, 11.0, 0.9);
    ring.style.opacity = String(Math.sin(Math.PI * p) * 0.9);
    ring.style.transform = `scale(${(0.97 + 0.06 * p).toFixed(3)})`;
  }
  // the end card
  veil.style.opacity = String(easeInOut(prog(t, 22.0, 0.5)));
  vis(logo, reveal(t, 23.0, 0.8));
  vis(word, easeOut(prog(t, 23.8, 0.8)), 0, `translateX(${(-12 * (1 - easeOut(prog(t, 23.8, 0.8)))).toFixed(2)}px)`);
  vis(sub, reveal(t, 24.6), 16);
  vis(url, reveal(t, 25.2), 16);
}

// The label follows the node's projected position, ~18 px right of its on-screen radius.
type Cam = { camera: { position: { x: number; y: number; z: number }; fov: number } };
function placeLabel(t: number) {
  const node = sc.nodeById(NEW_ID);
  const p = node && t >= 8.5 ? sc.project(node) : null;
  const monday = reveal(t, 8.5, 0.4) * fade(t, 10.8, 0.6) * (t < 12 ? 1 : 0);
  const tuesday = reveal(t, 17.0, 0.4) * fade(t, 22.0, 0.5) * (t >= 17 ? 1 : 0);
  const o = p ? Math.max(monday, tuesday) : 0;
  vis(nodeLabel, o);
  if (!p || o <= 0) return;
  const cam = (sc as unknown as Cam).camera;
  const dist = Math.hypot(cam.position.x - (node!.x ?? 0), cam.position.y - (node!.y ?? 0), cam.position.z - (node!.z ?? 0));
  const rpx = (sc.radiusOf(node!) * (H / 2)) / (Math.tan((cam.fov * Math.PI) / 360) * dist);
  nodeLabel.style.left = `${GX + p.x + rpx + 18}px`;
  nodeLabel.style.top = `${p.y - nodeLabel.offsetHeight / 2}px`;
}

let last = -1;
async function frame(n: number) {
  if (n === 0) {
    await document.fonts.ready;
    measureCards();
  }
  if (n !== last + 1) throw new Error(`frames must be rendered in order: asked ${n} after ${last}`);
  last = n;
  const t = n / FPS;
  for (const ev of EVENTS) if (!ev.done && t >= ev.t) { ev.done = true; ev.run(); }
  overlays(t);
  sc.renderFrame(t * 1000);
  placeLabel(t);
  // Copy before awaiting: the WebGL canvas holds its frame only for the length of the task.
  gctx.drawImage(sc.canvas, 0, 0);
}

const film = { fps: FPS, frames: DURATION * FPS, frame };
(window as unknown as { film: typeof film }).film = film;

if (!RECORD) {
  const fit = () => { stage.style.transform = `scale(${Math.min(innerWidth / W, innerHeight / H)})`; };
  fit();
  addEventListener("resize", fit);
  addEventListener("keydown", (e) => { if (e.key === "r" || e.key === "R") location.reload(); });
  const t0 = performance.now();
  const tick = async () => {
    const want = Math.min(film.frames, Math.floor(((performance.now() - t0) / 1000) * FPS));
    for (let k = 0; last + 1 < want && k < 3; k++) await frame(last + 1);
    if (last + 1 >= film.frames) setTimeout(() => location.reload(), 1500);
    else requestAnimationFrame(tick);
  };
  void document.fonts.ready.then(() => requestAnimationFrame(tick));
}
