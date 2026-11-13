// The plugins film: a plugin adds clients, commands and tools, and you approve exactly what runs. One terminal card
// types the commands of docs/plugins.md §Add a script: the example plugin; every command line and every line of output
// comes from film/transcripts/plugins.txt, which the real binary wrote (film/transcripts/plugins.sh) and the e2e
// scenario 26_film_transcripts keeps equal to it. The page only folds long outputs (a `…` line) and never edits a line.
// Like every film it is a pure function of time: frame(n) sets every element from t = n / FPS.
import "../film.css";
import "../../docs/brand/cartographer.css";
import "../../web/src/styles/tokens.css";
import logoUrl from "../../docs/brand/coordinate-pine.svg";
import raw from "../transcripts/plugins.txt?raw";
import { clamp01, defineFilm, fade, mk, prog, reveal, vis } from "./kit";

const FPS = 30;

// --- the transcript -------------------------------------------------------
interface Step {
  /** A note line (`>> ...`) written before the command, if any. */
  note?: string;
  cmd: string;
  out: string[];
}
function parse(text: string): Step[] {
  const steps: Step[] = [];
  let note: string | undefined;
  for (const line of text.replace(/\n+$/, "").split("\n")) {
    if (line.startsWith("$ ")) {
      steps.push({ note, cmd: line.slice(2), out: [] });
      note = undefined;
    } else if (line.startsWith(">> ")) note = line.slice(3);
    else if (steps.length) steps[steps.length - 1]!.out.push(line);
    else throw new Error(`plugins.txt: a line before the first command: ${line}`);
  }
  return steps;
}
const STEPS = parse(raw);
if (STEPS.length !== 9) throw new Error(`plugins.txt holds ${STEPS.length} commands, the film tells 9: regenerate it (make film-transcripts) and revise the scenes`);

/** Keeps the lines `keep` accepts; a run of dropped lines becomes one `  …` line. */
function fold(lines: string[], keep: RegExp): string[] {
  const out: string[] = [];
  for (const l of lines) {
    if (keep.test(l)) out.push(l);
    else if (out[out.length - 1] !== "  …") out.push("  …");
  }
  return out;
}
const ALL = /^/;
// What each command shows, by its index in the transcript. A folded line is the film's choice, never a changed one.
const KEEP: RegExp[] = [
  ALL, // validate
  /^(plugin |  source|  digest|permissions:|  may |settings:|pending approval)/, // install, answered n
  ALL, // the command before approval
  /^(plugin |permissions:|  may |settings:|scripts|  lib\/|  plugin\.star  |registers:|  command|  event|  tool)/, // show --files
  /^(plugin |Enabled)/, // approve
  ALL, // config
  ALL, // hello ada
  ALL, // list
  ALL, // hello ada, modified
];

// --- the scenes -----------------------------------------------------------
interface Scene {
  steps: number[];
  caption: string;
  sub: string;
}
const SCENES: Scene[] = [
  { steps: [0], caption: "A plugin adds clients,<br>commands and tools.", sub: "validate lists what it registers; nothing has run" },
  { steps: [1, 2], caption: "Nothing runs<br>until you approve it.", sub: "installed, not approved: the command exits 2" },
  { steps: [3, 4], caption: "You read exactly<br>what runs.", sub: "permissions, scripts and their digests, per version" },
  { steps: [5, 6], caption: "A setting is not code.", sub: "no second approval for a setting" },
  { steps: [7, 8], caption: "Pinned by digest.", sub: "one byte changed: modified, and it stops" },
];

interface Row {
  at: number;
  text: string;
  cls: string;
  /** characters per second; absent = shown whole */
  cps?: number;
}
const TYPE_CPS = 32;
const LINE_GAP = 0.07; // s between two lines of output
const AFTER_CMD = 0.45; // s between the end of typing and the first line of output
const BETWEEN = 0.9; // s between two commands
const HOLD = 1.5; // s a scene is held after its last line
const FIRST = 0.7; // s before the first command of a scene

function rowClass(l: string): string {
  if (l.startsWith("Error:") || l.startsWith("warning:") || l.startsWith("[exit")) return "err";
  if (l === "  …") return "dim";
  if (l.startsWith("pending approval") || l.startsWith("Enabled")) return "tool";
  return "";
}
interface Timed {
  start: number;
  end: number;
  scene: Scene;
  rows: Row[];
}
const TIMED: Timed[] = [];
{
  let cur = 0;
  SCENES.forEach((scene) => {
    const rows: Row[] = [];
    let tl = FIRST;
    scene.steps.forEach((k, j) => {
      const step = STEPS[k]!;
      if (step.note) {
        rows.push({ at: tl, text: step.note, cls: "note" });
        tl += 0.5;
      }
      rows.push({ at: tl, text: `$ ${step.cmd}`, cls: "cmd", cps: TYPE_CPS });
      tl += step.cmd.length / TYPE_CPS + AFTER_CMD;
      for (const l of fold(step.out, KEEP[k]!)) {
        rows.push({ at: tl, text: l, cls: rowClass(l) });
        tl += LINE_GAP;
      }
      if (j < scene.steps.length - 1) tl += BETWEEN;
    });
    TIMED.push({ start: cur, end: cur + tl + HOLD, scene, rows });
    cur += tl + HOLD;
  });
}
const SCENES_END = TIMED[TIMED.length - 1]!.end;
const END_CARD = 4.5; // s: the end card, from the last scene's end
const DURATION = SCENES_END + END_CARD;

// --- the page -------------------------------------------------------------
const COL = 100;
const day = mk("div", "day", "", `left:${COL}px;top:40px`);
const cap = mk("div", "caption", "", `left:${COL}px;top:92px`);
const CARD = { x: COL, y: 250, w: 1720, rows: 18, lh: 34 };
const card = mk("div", "card", "", `left:${CARD.x}px;top:${CARD.y}px;width:${CARD.w}px`);
card.innerHTML = '<div class="bar"><i class="live"></i><i></i><i></i><b>~ · example-forks</b></div>';
const body = document.createElement("div");
body.className = "body";
body.style.cssText = `font:23px/${CARD.lh}px var(--carto-font-code);height:${CARD.rows * CARD.lh}px;white-space:pre;overflow:hidden`;
card.appendChild(body);

// The end card covers the scenes; created last so it is on top.
const veil = mk("div", "", "");
veil.id = "veil";
const logo = mk("img", "", "", "left:310px;top:300px;width:150px;height:150px") as HTMLImageElement;
logo.src = logoUrl;
const word = mk("div", "wordmark", "Approved per version.", "left:500px;top:286px;font-size:88px");
const word2 = mk("div", "wordmark", "Pinned by digest.", "left:500px;top:392px;font-size:88px");
// The contribution points of docs/plugins.md §Contribution points that exist in this build, by side.
const pts = mk(
  "div",
  "url",
  "client &nbsp;skills · agents · hooks · providers · scripts (commands, tools, sync handlers)<br>server &nbsp;roles · presets<br>atlas &nbsp;&nbsp;branding · views · apps",
  "left:504px;top:540px;font-size:28px;line-height:1.6;white-space:nowrap",
);
const url = mk("div", "url", "github.com/BeppeTemp/cartographer", "left:504px;top:700px");

const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;");
const last = { html: "" };

function frame(n: number) {
  const t = n / FPS;
  const si = Math.max(0, TIMED.findIndex((s) => t >= s.start && t < s.end));
  const inScenes = t < SCENES_END;
  const cur = inScenes ? TIMED[si]! : TIMED[TIMED.length - 1]!;
  const tl = t - cur.start;

  // the caption and the counter of the scene, crossfading at its edges
  const edge = inScenes ? reveal(tl, 0.2) * fade(tl, cur.end - cur.start - 0.4, 0.4) : 0;
  day.textContent = `${si + 1} / ${SCENES.length}`;
  cap.innerHTML = `${cur.scene.caption}<div class="sub24">${esc(cur.scene.sub)}</div>`;
  vis(day, edge, 12);
  vis(cap, edge, 12);
  vis(card, inScenes ? reveal(t, 0, 0.5) * (1 - prog(t, SCENES_END, 0.5)) : 0, 16);

  // the terminal: the rows whose time has come, the last `rows` of them (it follows its tail)
  const shown = cur.rows.filter((r) => tl >= r.at);
  const view = shown.slice(-CARD.rows);
  let html = "";
  view.forEach((r, k) => {
    let text = r.text;
    let typing = false;
    if (r.cps) {
      const c = Math.min(text.length, Math.floor((tl - r.at) * r.cps));
      typing = c < text.length;
      text = text.slice(0, c);
    }
    const o = r.cps ? 1 : reveal(tl, r.at, 0.18);
    const isLast = k === view.length - 1;
    const cursor = isLast && (typing || Math.floor(t * 2) % 2 === 0) ? '<span class="cur"></span>' : "";
    let inner = esc(text);
    if (r.cls === "cmd") inner = `<span class="tool">$</span>${esc(text.slice(1))}`;
    html += `<div class="${r.cls === "err" ? "err" : r.cls === "tool" ? "tool" : ""}" style="${rowStyle(r.cls)}opacity:${clamp01(o).toFixed(3)}">${inner}${cursor}</div>`;
  });
  if (last.html !== html) {
    body.innerHTML = html;
    last.html = html;
  }

  // the end card
  const e = prog(t, SCENES_END, 0.6);
  vis(veil, e);
  veil.style.visibility = e <= 0.001 ? "hidden" : "visible";
  vis(logo, reveal(t, SCENES_END + 0.3), 16);
  vis(word, reveal(t, SCENES_END + 0.5), 16);
  vis(word2, reveal(t, SCENES_END + 1.1), 16);
  vis(pts, reveal(t, SCENES_END + 1.9), 16);
  vis(url, reveal(t, SCENES_END + 2.6), 16);
}
function rowStyle(cls: string): string {
  if (cls === "note" || cls === "dim") return "color:#AFB9AE;font-style:italic;";
  return "";
}

defineFilm({ fps: FPS, duration: DURATION, frame });
