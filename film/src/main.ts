// The launch film: one page, one pure function of time. frame(n) sets every overlay from t = n / fps,
// fires the graph events whose time has passed, then lets the real Atlas engine draw the frame.
import "../../docs/brand/cartographer.css";
import "../../web/src/styles/tokens.css";
import logoUrl from "../../docs/brand/coordinate-pine.svg";
import { LivingScene, type SceneLink, type SceneNode, type ScenePalette } from "../../web/src/lib/graph3d/scene";
import { beside } from "../../web/src/lib/graph3d/physics";
import { planBursts } from "../../web/src/lib/graph3d/motion";
import { resolveSlots } from "../../web/src/lib/palette";
import { buildKb } from "./data";

const FPS = 30;
const DURATION = 28;
const W = 1920;
const H = 1080;
const GW = 1152; // the graph's canvas: the right 60% of the frame
const GX = W - GW;
const RECORD = new URLSearchParams(location.search).has("record");
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
ring.innerHTML = `<ellipse cx="${GX + GW / 2}" cy="540" rx="470" ry="400" fill="none" stroke="var(--carto-clay-text)" stroke-width="3"/>`;
ring.style.cssText = "position:absolute;left:0;top:0;pointer-events:none;opacity:0;transform-origin:1344px 540px";
stage.appendChild(ring);

const title = mk("div", "title", "Every agent starts<br>from zero.", "left:160px;top:460px");
const dayMon = mk("div", "day", "Monday.", "left:160px;top:110px");
const dayTue = mk("div", "day", "Tuesday.", "left:160px;top:110px");
const cap = (s: string) => mk("div", "caption", s, "left:160px;top:880px");
const capA = cap("One wiki, in git.");
const capB = cap("None of them can break it.");
const capC = cap("Different machines. Different clients. One memory.");

function card(label: string, cls = "") {
  const c = mk("div", `card ${cls}`, `<div class="bar"><i class="live"></i><i></i><i></i><b>${label}</b></div><div class="body"></div>`);
  return { el: c, label: c.querySelector("b")!, body: c.querySelector(".body") as HTMLElement };
}
const small = [card("Claude Code"), card("Codex"), card("OpenCode")];
const SMALL = [
  { x: 1180, y: 230 },
  { x: 1270, y: 440 },
  { x: 1200, y: 650 },
];
const DOTS = [[40, 40], [110, 22], [170, 62], [240, 30], [300, 70], [90, 84], [350, 36]];
const EDGES = [[0, 1], [1, 2], [2, 3], [3, 4], [0, 5], [5, 2], [3, 6]];
small.forEach((c) => {
  c.body.style.cssText = "padding:0;position:relative";
  c.body.innerHTML = `<svg class="mem" width="420" height="132" viewBox="0 0 420 132" style="top:0;left:0">${
    EDGES.map(([a, b]) => `<line x1="${DOTS[a!]![0]! + 10}" y1="${DOTS[a!]![1]! + 20}" x2="${DOTS[b!]![0]! + 10}" y2="${DOTS[b!]![1]! + 20}" stroke="#3B473E" stroke-width="2"/>`).join("")
  }${DOTS.map(([x, y], i) => `<circle cx="${x! + 10}" cy="${y! + 20}" r="${i % 3 === 0 ? 9 : 7}" fill="${i % 2 ? "#E9E8DE" : "#91B9A0"}"/>`).join("")}</svg>`;
});
const bob = card("bob · Codex", "clay");
const BIG = { x: 96, y: 390, w: 760, h: 300 };
Object.assign(bob.el.style, { left: `${BIG.x}px`, top: `${BIG.y}px`, width: `${BIG.w}px`, height: `${BIG.h}px` });

const noteGood = mk("div", "note", "staging-deploys", "left:0;top:0");
const noteBad = mk("div", "note bad", "stagin-deploy", "left:0;top:0");
const tag = mk("div", "tag", "✗ broken link [[infra/stagin]]", "left:880px;top:590px");
const veil = mk("div", "", "");
veil.id = "veil";
const logo = mk("img", "", "", "left:580px;top:372px;width:120px;height:120px") as HTMLImageElement;
logo.src = logoUrl;
const word = mk("div", "wordmark", "Cartographer", "left:740px;top:366px");
const sub = mk("div", "sub", "Shared memory for your team's AI agents", "left:744px;top:506px");
const url = mk("div", "url", "cartographer-demo.beppetemp.com/mcp &nbsp;·&nbsp; github.com/BeppeTemp/cartographer", "left:744px;top:584px");

// --- terminal scripts -----------------------------------------------------
interface Line {
  start: number;
  text: string;
  cls?: string;
  /** characters per second; absent = the line is revealed whole */
  cps?: number;
}
const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;");
// Prompts the agent types fast (64 cps) so the commit lands before the note lifts at 7.4 s; the answer is 32 cps.
const ALICE: Line[] = [
  { start: 4.6, cps: 64, text: "> Staging deploys fail: the migration job runs before the secret rotates. Save it for the team." },
  { start: 6.1, cls: "tool", text: "● concept_write  team/staging-deploys" },
  { start: 6.8, cls: "tool", text: "✓ committed 4f2a9c1" },
  { start: 11.0, cls: "err", text: "✗ rejected: broken link [[infra/stagin]] — nothing written" },
];
const BOB: Line[] = [
  { start: 15.0, cps: 32, text: "> Why do staging deploys fail?" },
  { start: 16.1, cls: "tool", text: '● search  "staging deploy"' },
  { start: 16.6, cls: "tool", text: "● concept_read  team/staging-deploys" },
  { start: 17.6, cps: 32, text: "The migration job runs before the secret rotates — written Monday by alice's agent." },
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
  sc.focus(NEW_ID, { left: 0, right: 0 }, () => {});
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
  el.style.left = `${p[0]! - 100}px`;
  el.style.top = `${p[1]! - 28}px`;
  vis(el, o, 0, extra);
};

function overlays(t: number) {
  // contours: drift a few px per second
  for (const c of CONTOURS) {
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
      setRect(c.el, lerp(s.x, BIG.x, e), lerp(s.y, BIG.y, e), lerp(420, BIG.w, e), lerp(180, BIG.h, e));
      setText(c.label, t >= 4.0 ? "alice · Claude Code" : "Claude Code");
      o *= fade(t, 14.0, 0.6);
      if (t >= 3.5) {
        c.body.style.cssText = "";
        renderLines(c.body, ALICE, t);
      }
      c.el.style.display = t > 14.7 ? "none" : "block";
    } else setRect(c.el, s.x, s.y, 420, 180);
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
  // note 1: lifts out of the terminal, an eased arc to the graph centre, then a dot
  {
    const u = easeInOut(prog(t, 7.4, 1.1));
    const p = bez([640, 440], [900, 230], [gctr.x, gctr.y], u);
    const shrink = easeInOut(prog(t, 8.5, 0.3));
    const o = clamp01(prog(t, 7.4, 0.3)) * (1 - prog(t, 8.8, 0.3));
    place(noteGood, p, o, `scale(${(1 - 0.92 * shrink).toFixed(3)})`);
  }
  // note 2: bounces off the edge of the graph
  {
    const goes = easeInOut(prog(t, 9.7, 1.3));
    const back = easeOut(prog(t, 11.0, 0.9));
    const hit = [900, 540];
    const p = t < 11.0 ? bez([700, 420], [860, 300], hit, goes) : [lerp(hit[0]!, 800, back), lerp(hit[1]!, 410, back) - 40 * Math.sin(Math.PI * back)];
    place(noteBad, p, clamp01(prog(t, 9.7, 0.4)) * (1 - prog(t, 11.4, 0.9)), `rotate(${(-8 * back).toFixed(2)}deg)`);
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

let last = -1;
async function frame(n: number) {
  if (n !== last + 1) throw new Error(`frames must be rendered in order: asked ${n} after ${last}`);
  last = n;
  const t = n / FPS;
  for (const ev of EVENTS) if (!ev.done && t >= ev.t) { ev.done = true; ev.run(); }
  overlays(t);
  sc.renderFrame(t * 1000);
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
