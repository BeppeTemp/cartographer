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

const FPS = 60;
// Story time: every beat below is authored in the seconds of the first cut. The film plays them slower,
// so each line can be read at README size (the opening most), while the engine keeps real time.
const WARP: [number, number][] = [[0, 0], [11.4, 7], [53.4, 40], [60, 46.6]]; // [film s, story s]
const DURATION = WARP[WARP.length - 1]![0];
function story(T: number): number {
  for (let k = 1; k < WARP.length; k++) {
    const [f0, s0] = WARP[k - 1]!, [f1, s1] = WARP[k]!;
    if (T <= f1) return s0 + ((T - f0) * (s1 - s0)) / (f1 - f0);
  }
  return WARP[WARP.length - 1]![1];
}
const W = 1920;
const H = 1080;
const GW = 1000; // the graph's canvas: the right part of the frame; the left column (x 120-940) is for the roster, cards and captions
const GX = W - GW;
const RECORD = new URLSearchParams(location.search).has("record");
const TUESDAY_ZOOM = 1.25; // how much wider than focus() the Tuesday camera frames the neighbourhood
const COL = 120; // left margin of the text column
const CARD_W = 820;
const NEW_ID = "team/staging-deploys";
const ROLLBACK_ID = "runbooks/rollback";
const FLAGS_ID = "decisions/feature-flags";

// The cast: one person, one coding agent, one colour (avatar, title-bar dot, note border, connection line).
const CAST = [
  { name: "Alice", agents: ["Claude Code", "Codex"], color: "#365D50" },
  { name: "Marco", agents: ["Codex", "Antigravity"], color: "#B8785D" },
  { name: "Priya", agents: ["OpenCode", "Claude Code"], color: "#496B87" },
  { name: "Leo", agents: ["Kiro", "Copilot CLI"], color: "#805B18" },
];
const ROW0 = 320; // centre of the first roster row (x 120, 76 px apart), under the scene headline
const rowY = (i: number) => ROW0 + 76 * i;
const LX0 = 690; // where a connection line leaves its roster row
const TERM = { x: COL, y: 608, w: CARD_W }; // the agent's terminal card, under the roster

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


const SVGNS = SVG;
const lines = document.createElementNS(SVGNS, "svg");
lines.id = "lines";
lines.setAttribute("viewBox", `0 0 ${W} ${H}`);
lines.style.cssText = "position:absolute;left:0;top:0;width:1920px;height:1080px;pointer-events:none";
stage.appendChild(lines);
const connLines = CAST.map((p, i) => {
  const l = document.createElementNS(SVGNS, "line");
  for (const [k, v] of Object.entries({ x1: LX0, y1: rowY(i), x2: LX0, y2: rowY(i), stroke: p.color, "stroke-opacity": 0.5, "stroke-width": 1.5 })) l.setAttribute(k, String(v));
  lines.appendChild(l);
  return l;
});
// A dot that travels along a person's line: dir 1 = towards the graph (a write or a search), -1 = back to the row (a read).
const DOTS_RUN = [
  { i: 0, t0: 14.5, dir: 1 },
  { i: 1, t0: 24.5, dir: 1 },
  { i: 1, t0: 25.1, dir: -1 },
  { i: 2, t0: 33.5, dir: 1 },
  { i: 3, t0: 35.0, dir: 1 },
  { i: 3, t0: 36.5, dir: 1 },
].map((d) => {
  const c = document.createElementNS(SVGNS, "circle");
  c.setAttribute("r", "8");
  c.setAttribute("fill", CAST[d.i]!.color);
  c.setAttribute("opacity", "0");
  lines.appendChild(c);
  return { ...d, c };
});

const avatar = (p: (typeof CAST)[number], d: number) =>
  `<div style="flex:none;width:${d}px;height:${d}px;border-radius:50%;background:${p.color};color:#FCFAF6;font:${d / 2}px/${d}px Georgia,serif;text-align:center">${p.name[0]}</div>`;

const title = mk("div", "title", "Meet the team.", "left:120px;top:76px");
// Scene A: four large person cards across the whole frame.
const PC = { x0: 140, y: 340, w: 380, h: 500, gap: 40 };
const CLUSTER = [[18, 80], [66, 24], [114, 92], [162, 38], [210, 100], [258, 30], [306, 84]]; // 1.6x the old dots, spread over the card width
const CLUSTER_EDGES = [[0, 1], [1, 2], [2, 3], [3, 4], [4, 5], [5, 6], [1, 3]];
const pcards = CAST.map((p, i) => {
  const el = mk("div", "pcard", `${avatar(p, 96)}<div class="pn" style="font-size:56px;margin-top:24px">${p.name}</div><div style="display:flex;flex-wrap:wrap;gap:10px;margin-top:18px">${p.agents.map((a) => `<div class="pill" style="margin-top:0">${a}</div>`).join("")}</div>`, `padding:28px;left:${PC.x0 + i * (PC.w + PC.gap)}px;top:${PC.y}px;width:${PC.w}px;height:${PC.h}px`);
  const svg = document.createElementNS(SVGNS, "svg");
  svg.setAttribute("class", "cl");
  svg.style.left = "28px";
  svg.style.bottom = "28px";
  svg.setAttribute("width", "324");
  svg.setAttribute("height", "120");
  svg.innerHTML =
    CLUSTER_EDGES.map(([a, b]) => `<line x1="${CLUSTER[a!]![0]}" y1="${CLUSTER[a!]![1]}" x2="${CLUSTER[b!]![0]}" y2="${CLUSTER[b!]![1]}" stroke="${p.color}" stroke-opacity="0.45" stroke-width="3"/>`).join("") +
    CLUSTER.map(([x, y], k) => `<circle cx="${x}" cy="${y}" r="${k % 3 === 0 ? 13 : 10}" fill="${p.color}" fill-opacity="${k % 2 ? 0.55 : 0.9}"/>`).join("");
  el.appendChild(svg);
  return { el, svg };
});
// The roster: avatar + name + agent, stacked in the left column from scene B on.
const rows = CAST.map((p, i) => {
  const el = mk("div", "row", `<i class="rb" style="background:${p.color}"></i>${avatar(p, 52)}<span class="rn" style="font-size:40px;width:140px">${p.name}</span><span class="ra" style="font-size:24px">${p.agents.join(" · ")}</span>`, `left:${COL}px;top:${rowY(i) - 26}px;width:560px;height:52px`);
  return { el, bar: el.querySelector(".rb") as HTMLElement };
});

const dayEls = ["Monday, 18:40", "Tuesday, 09:15", "Every day."].map((s) => mk("div", "day", s, `left:${COL}px;top:56px`));
// A caption is the headline of its scene: under the title or the day label, never left at the bottom.
const cap = (s: string, wide = false) => {
  const el = mk("div", wide ? "caption wide" : "caption", s, `left:${COL}px;top:${wide ? 196 : 112}px`);
  return el;
};
const capA1 = cap("Four people, each with their own coding agents.", true);
const capA2 = cap("What one agent learns stays on one laptop.", true);
const capB = cap('Cartographer gives them<br>one shared wiki.<div class="sub24">over MCP · Markdown in git</div>');
const capC = cap("Alice finds out why staging<br>breaks. Her agent writes it down.");
const capD = cap("Marco — another laptop, another agent —<br>picks it up the next morning.");
const capE1 = cap("Everyone’s agents add to it.");
const capE2 = cap("Nothing they write can break it.");

function card(label: string, color: string) {
  const c = mk("div", "card", `<div class="bar"><i class="live" style="background:${color}"></i><i></i><i></i><b>${label}</b></div><div class="body"></div>`);
  return { el: c, label: c.querySelector("b")!, body: c.querySelector(".body") as HTMLElement };
}
const alice = card("alice · Claude Code", CAST[0]!.color);
const marco = card("marco · Codex", CAST[1]!.color);
for (const c of [alice, marco]) Object.assign(c.el.style, { left: `${TERM.x}px`, top: `${TERM.y}px`, width: `${TERM.w}px`, height: "400px" });

const mkNote = (text: string, color: string) => {
  const n = mk("div", "note", text, "left:0;top:0");
  n.style.border = `2px solid ${color}`;
  return n;
};
const noteAlice = mkNote("staging-deploys", CAST[0]!.color);
const notePriya = mkNote("runbooks/rollback", CAST[2]!.color);
const noteLeo = mkNote("decisions/feature-flags", CAST[3]!.color);
const noteBad = mk("div", "note bad", "stagin-deploy", "left:0;top:0");
noteBad.style.borderColor = CAST[3]!.color;
noteBad.style.color = CAST[3]!.color;
const tag = mk("div", "tag", "✗ broken link [[infra/stagin]]", "left:0;top:0");
// Created before the veil, so the end card covers them.
const LABEL_CSS = "left:0;top:0;font:26px Georgia,serif;color:#252824;background:#FCFAF6;border:1px solid #DCDDD5;border-radius:8px;padding:4px 10px;white-space:nowrap;visibility:hidden";
const LABELS = [NEW_ID, ROLLBACK_ID, FLAGS_ID].map((id) => ({ id, el: mk("div", "", id.split("/")[1]!, LABEL_CSS) }));
const veil = mk("div", "", "");
veil.id = "veil";
const logo = mk("img", "", "", "left:310px;top:392px;width:150px;height:150px") as HTMLImageElement;
logo.src = logoUrl;
const word = mk("div", "wordmark", "Cartographer", "left:500px;top:378px");
const sub = mk("div", "sub", "Shared memory for your team's AI agents", "left:504px;top:520px");
const url = mk("div", "url", "cartographer-demo.beppetemp.com/mcp &nbsp;·&nbsp; github.com/BeppeTemp/cartographer", "left:504px;top:596px");
const agents = mk("div", "url", "Claude Code · Codex · OpenCode · Kiro · Antigravity · Copilot CLI · any MCP client", "left:504px;top:650px;font-size:22px");

// --- terminal scripts -----------------------------------------------------
interface Line {
  start: number;
  text: string;
  cls?: string;
  /** characters per second; absent = the line is revealed whole */
  cps?: number;
}
const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;");
// Alice's prompt is typed fast (64 cps) so the commit lands before the note lifts at 15.8 s; Marco's answer is 44 cps.
const ALICE: Line[] = [
  { start: 12.9, cps: 64, text: "> Staging fails: migrations run before the secret rotates. Remember it for the team." },
  { start: 14.5, cls: "tool", text: "● concept_write  team/staging-deploys" },
  { start: 15.2, cls: "tool", text: "✓ committed 4f2a9c1" },
];
const MARCO: Line[] = [
  { start: 23.0, cps: 38, text: "> Staging deploy failed again. Any idea why?" },
  { start: 24.5, cls: "tool", text: '● search  "staging"' },
  { start: 25.1, cls: "tool", text: "● concept_read  team/staging-deploys" },
  { start: 26.4, cps: 44, text: "Alice found it yesterday: rotate the secret first, then migrate." },
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
/** Fits each terminal card to its final content, once, so it never resizes while the lines type in. */
let aliceH = 400;
let marcoH = 400;
function fitHeight(c: { el: HTMLElement; body: HTMLElement }, lines: Line[]): number {
  const keep = { html: c.body.innerHTML, w: c.el.style.width, h: c.el.style.height };
  c.body.innerHTML = lines.map((l) => `<div>${esc(l.text)}</div>`).join("");
  c.el.style.width = `${CARD_W}px`;
  c.el.style.height = "auto";
  const h = c.el.offsetHeight; // layout height, unaffected by the preview's scale transform
  c.body.innerHTML = keep.html;
  c.el.style.width = keep.w;
  c.el.style.height = keep.h;
  cache.delete(c.body);
  return h;
}
function measureCards() {
  aliceH = fitHeight(alice, ALICE);
  marcoH = fitHeight(marco, MARCO);
  alice.el.style.height = `${aliceH}px`;
  marco.el.style.height = `${marcoH}px`;
}

function setRect(el: HTMLElement, x: number, y: number, w: number, h: number) {
  el.style.left = `${x}px`;
  el.style.top = `${y}px`;
  el.style.width = `${w}px`;
  el.style.height = `${h}px`;
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

function addNode(id: string, anchorId: string, targets: string[], colour: string) {
  const anchor = nodes.find((n) => n.id === anchorId)!;
  nodes.push({ id, weight: 0.35, ...beside(anchor, id), vx: 0, vy: 0, vz: 0 } as SceneNode);
  links = [...links, ...targets.map((target) => ({ source: id, target }))];
  colourOf.set(id, colour);
  sc.setData(nodes, links, 0);
  recolour();
  sc.fitEverything(0, 1500);
}
const addStaging = () => addNode(NEW_ID, "infra/ci", ["infra/ci", "services/api", "runbooks/deploy"], slots[6]!);
const addRollback = () => addNode(ROLLBACK_ID, "runbooks/deploy", ["runbooks/deploy", "services/api", "infra/k8s"], colourOf.get("runbooks/deploy")!);
const addFlags = () => addNode(FLAGS_ID, "decisions/adopt-k8s", ["decisions/adopt-k8s", "services/gateway", "infra/k8s"], colourOf.get("decisions/adopt-k8s")!);
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
/** Back to the whole graph once Tuesday is over: drops the selection and the follow target, then frames everything again. */
function release() {
  selected = null;
  sc.setSelection(null);
  sc.unfocus(null);
  recolour();
  sc.fitEverything(0, 1500);
}
const EVENTS: { t: number; run: () => void; done?: boolean }[] = [
  { t: 8.0, run: () => sc.fitEverything(0, 1500) },
  { t: 16.9, run: addStaging },
  { t: 25.5, run: select },
  { t: 31.8, run: release },
  { t: 34.6, run: addRollback },
  { t: 36.1, run: addFlags },
];

// --- the frame ------------------------------------------------------------
const gctr = { x: GX + GW / 2, y: 540 };
const bez = (a: number[], c: number[], b: number[], u: number) => [0, 1].map((i) => (1 - u) ** 2 * a[i]! + 2 * (1 - u) * u * c[i]! + u * u * b[i]!);
const place = (el: HTMLElement, p: number[], o: number, extra = "") => {
  el.style.left = `${p[0]! - 140}px`;
  el.style.top = `${p[1]! - 38}px`;
  vis(el, o, 0, extra);
};
/** A note lifts off, flies an eased arc to the graph (1.1 s), shrinks to a dot and is gone. */
function flight(el: HTMLElement, t: number, t0: number, start: number[], ctrl: number[], end: number[]) {
  const p = bez(start, ctrl, end, easeInOut(prog(t, t0, 1.1)));
  const shrink = easeInOut(prog(t, t0 + 1.1, 0.3));
  place(el, p, clamp01(prog(t, t0, 0.3)) * (1 - prog(t, t0 + 1.4, 0.3)), `scale(${(1 - 0.92 * shrink).toFixed(3)})`);
}
/** 0.4 s ramps in and out of each [from, to] window; the max over the windows. */
const windows = (t: number, ws: number[][]) => Math.max(...ws.map(([a, b]) => clamp01((t - a!) / 0.4) * (1 - clamp01((t - b!) / 0.4))));
const ACTIVE = [
  [[0, 22.0]], // Alice: the whole of Monday
  [[0, 12.0], [22.0, 32.0]], // Marco: Tuesday
  [[0, 12.0], [32.4, 35.2]], // Priya
  [[0, 12.0], [34.4, 39.0]], // Leo
];
const FLY = 7.0; // when the person cards fly into the roster

function overlays(tFilm: number) {
  const t = tFilm;
  // contours: drift a few px per second
  for (const c of CONTOURS) {
    const t = Math.min(tFilm, 44); // the end card holds still from 44 s
    let d = "";
    for (let x = -40; x <= W + 40; x += 40) d += `${x === -40 ? "M" : "L"}${x} ${(c.y + c.amp * Math.sin(c.k * (x + c.v * t) + c.ph) + c.amp * 0.35 * Math.sin(c.k * 2.3 * (x + c.v * t))).toFixed(1)} `;
    c.p.setAttribute("d", d);
  }
  // A: the team
  vis(title, reveal(t, 0.2) * fade(t, 6.8, 0.4), 16);
  pcards.forEach((c, i) => {
    const s0 = FLY + 0.12 * i;
    const p = easeInOut(prog(t, s0, 1.0));
    const dx = (300 - (PC.x0 + i * (PC.w + PC.gap) + PC.w / 2)) * p;
    const dy = (rowY(i) - (PC.y + PC.h / 2)) * p;
    vis(c.el, reveal(t, 0.8 + 0.3 * i) * (1 - prog(p, 0.6, 0.4)), 16, `translate(${dx.toFixed(1)}px,${dy.toFixed(1)}px) scale(${lerp(1, 0.16, p).toFixed(3)})`);
    c.svg.style.opacity = String(prog(t, 4.6, 0.4) * fade(t, 5.4 + 0.2 * i, 0.6));
  });
  vis(capA1, reveal(t, 3.0) * fade(t, 5.2, 0.4), 16);
  vis(capA2, reveal(t, 5.5) * fade(t, 6.7, 0.4), 16);
  // B: one wiki
  vis(capB, reveal(t, 7.8) * fade(t, 11.7, 0.4), 16);
  rows.forEach((r, i) => {
    const present = prog(t, FLY + 0.12 * i + 0.6, 0.4);
    const act = windows(t, ACTIVE[i]!);
    vis(r.el, present * (0.35 + 0.65 * act));
    r.bar.style.opacity = String(act * prog(t, 12.0, 0.4));
    const l = connLines[i]!;
    l.setAttribute("x2", String(lerp(LX0, GX, easeInOut(prog(t, 9.0 + 0.25 * i, 0.7)))));
  });
  for (const d of DOTS_RUN) {
    const p = prog(t, d.t0, 0.6);
    const e = easeInOut(p);
    d.c.setAttribute("cx", String(d.dir > 0 ? lerp(LX0, GX, e) : lerp(GX, LX0, e)));
    d.c.setAttribute("cy", String(rowY(d.i)));
    d.c.setAttribute("opacity", p > 0 && p < 1 ? "1" : "0");
  }
  // the graph appears with scene B and leaves with the veil
  graph.style.opacity = String(easeOut(prog(t, 8.2, 1.2)));
  // C: Monday
  vis(dayEls[0]!, reveal(t, 12.0) * fade(t, 21.8, 0.3), 16);
  vis(capC, reveal(t, 13.0) * fade(t, 21.6, 0.4), 16);
  setRect(alice.el, TERM.x, TERM.y, TERM.w, aliceH);
  vis(alice.el, reveal(t, 12.4) * fade(t, 21.6, 0.6), 16);
  alice.el.style.display = t > 22.3 ? "none" : "block";
  renderLines(alice.body, ALICE, t);
  const ac = TERM.y + aliceH / 2;
  flight(noteAlice, t, 15.8, [TERM.x + TERM.w, ac], [1150, ac - 230], [gctr.x, gctr.y]);
  // D: Tuesday
  vis(dayEls[1]!, reveal(t, 22.2) * fade(t, 31.5, 0.3), 16);
  vis(capD, reveal(t, 22.9) * fade(t, 31.4, 0.4), 16);
  setRect(marco.el, TERM.x, TERM.y, TERM.w, marcoH);
  vis(marco.el, reveal(t, 22.4) * fade(t, 31.2, 0.6), 16);
  marco.el.style.display = t < 22.3 || t > 31.9 ? "none" : "block";
  renderLines(marco.body, MARCO, t);
  // E: every day. The notes leave their rows along the connection lines.
  vis(dayEls[2]!, reveal(t, 31.9) * fade(t, 39.6, 0.3), 16);
  vis(capE1, reveal(t, 32.6) * fade(t, 36.3, 0.4), 16);
  vis(capE2, reveal(t, 36.6) * fade(t, 39.5, 0.4), 16);
  flight(notePriya, t, 33.5, [LX0 + 150, rowY(2)], [GX, rowY(2) - 130], [gctr.x, gctr.y - 40]);
  flight(noteLeo, t, 35.0, [LX0 + 150, rowY(3)], [GX, rowY(3) - 100], [gctr.x, gctr.y + 40]);
  // Leo's second note flies at the graph, bounces off its edge (the ring), upright all the way
  {
    const cy = rowY(3);
    const start = [LX0 + 150, cy];
    const dy = (cy - RING.cy) / RING.ry;
    const hit = [RING.cx - RING.rx * Math.sqrt(Math.max(0, 1 - dy * dy)) + 40, cy];
    const goes = easeInOut(prog(t, 36.5, 1.3));
    const back = easeOut(prog(t, 37.8, 0.9));
    const away = [start[0]! + 150, cy - 90];
    const p = t < 37.8 ? bez(start, [(start[0]! + hit[0]!) / 2, cy - 70], hit, goes) : [lerp(hit[0]!, away[0]!, back), lerp(hit[1]!, away[1]!, back) - 50 * Math.sin(Math.PI * back)];
    place(noteBad, p, clamp01(prog(t, 36.5, 0.4)) * (1 - prog(t, 38.2, 0.9)));
    // the clay tag sits just under the bounce point
    tag.style.left = `${hit[0]! - 40}px`;
    tag.style.top = `${hit[1]! + 62}px`;
  }
  vis(tag, reveal(t, 37.9) * fade(t, 39.2, 0.5), 16);
  {
    const p = prog(t, 37.8, 0.9);
    ring.style.opacity = String(Math.sin(Math.PI * p) * 0.9);
    ring.style.transform = `scale(${(0.97 + 0.06 * p).toFixed(3)})`;
  }
  // F: the end card
  veil.style.opacity = String(easeInOut(prog(t, 40.0, 0.5)));
  vis(logo, reveal(t, 40.9, 0.8));
  vis(word, easeOut(prog(t, 41.6, 0.8)), 0, `translateX(${(-12 * (1 - easeOut(prog(t, 41.6, 0.8)))).toFixed(2)}px)`);
  vis(sub, reveal(t, 42.3), 16);
  vis(url, reveal(t, 42.9), 16);
  vis(agents, reveal(t, 43.4), 16);
}

// Labels follow their node's projected position, ~18 px right of its on-screen radius.
type Cam = { camera: { position: { x: number; y: number; z: number }; fov: number } };
const labelOpacity: Record<string, (t: number) => number> = {
  [NEW_ID]: (t) => Math.max(reveal(t, 18.0, 0.4) * fade(t, 19.6, 0.6) * (t < 22 ? 1 : 0), reveal(t, 25.5, 0.4) * fade(t, 31.4, 0.5) * (t >= 25.5 ? 1 : 0)),
  [ROLLBACK_ID]: (t) => reveal(t, 34.6, 0.4) * fade(t, 35.4, 0.4),
  [FLAGS_ID]: (t) => reveal(t, 36.0, 0.4) * fade(t, 38.0, 0.5),
};
function placeLabels(t: number) {
  const shown: DOMRect[] = [];
  for (const { id, el } of LABELS) {
    const node = sc.nodeById(id);
    const p = node ? sc.project(node) : null;
    const o = p ? labelOpacity[id]!(t) : 0;
    vis(el, o);
    if (!p || o <= 0) continue;
    const cam = (sc as unknown as Cam).camera;
    const dist = Math.hypot(cam.position.x - (node!.x ?? 0), cam.position.y - (node!.y ?? 0), cam.position.z - (node!.z ?? 0));
    const rpx = (sc.radiusOf(node!) * (H / 2)) / (Math.tan((cam.fov * Math.PI) / 360) * dist);
    const w = el.offsetWidth;
    const h = el.offsetHeight;
    const top = p.y - h / 2;
    let left = GX + p.x + rpx + 18;
    // a label that would sit on one still visible goes to the node's left side instead
    if (shown.some((r) => left < r.right && left + w > r.left && top < r.bottom && top + h > r.top)) left = GX + p.x - rpx - 18 - w;
    el.style.left = `${left}px`;
    el.style.top = `${top}px`;
    shown.push(new DOMRect(left, top, w, h));
  }
}

let last = -1;
async function frame(n: number) {
  if (n === 0) {
    await document.fonts.ready;
    measureCards();
  }
  if (n !== last + 1) throw new Error(`frames must be rendered in order: asked ${n} after ${last}`);
  last = n;
  const T = n / FPS;
  const t = story(T);
  for (const ev of EVENTS) if (!ev.done && t >= ev.t) { ev.done = true; ev.run(); }
  overlays(t);
  sc.renderFrame(T * 1000);
  placeLabels(t);
  // Copy before awaiting: the WebGL canvas holds its frame only for the length of the task.
  gctx.drawImage(sc.canvas, 0, 0);
}

const film = { fps: FPS, frames: Math.round(DURATION * FPS), frame };
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
