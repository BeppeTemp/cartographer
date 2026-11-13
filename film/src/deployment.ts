// The deployment film: "Deploy what you need." Four deployment profiles, the rows of the `Deployment scenarios` table in
// docs/deployment.md that a team picks. Each scene lights the components that profile runs on one fixed diagram (the four
// colours of scene G of the launch film), draws the edges between them, and types the real inputs of the profile into a
// terminal card. Every command line and line of output comes from film/transcripts/deployment.txt, which
// film/transcripts/deployment.sh wrote from the real binary and the real files, and the e2e scenario 31_film_transcripts
// keeps equal to it. The page never edits a line. Like every film it is a pure function of time: frame(n) sets every
// element from t = n / FPS.
import "../film.css";
import "../../docs/brand/cartographer.css";
import "../../web/src/styles/tokens.css";
import logoUrl from "../../docs/brand/coordinate-pine.svg";
import raw from "../transcripts/deployment.txt?raw";
import { clamp01, defineFilm, easeOut, fade, lerp, mk, prog, reveal, vis } from "./kit";

const FPS = 30;

// --- the profiles ---------------------------------------------------------
type Comp = "compass" | "meridian" | "atlas" | "surveyor";
interface Profile {
  id: string;
  /** The `#` cell of the row in docs/deployment.md §Deployment scenarios. */
  row: number;
  /** The start of the `Scenario` cell of that row, up to its colon. */
  title: string;
}
// One profile per line: internal/repodocs (TestFilmDeploymentProfilesAreScenarioRows) reads `row: N, title: "..."` from here
// and fails when the table of docs/deployment.md renumbers or renames the row.
export const PROFILES: Profile[] = [
  { id: "laptop", row: 1, title: "Local all-in-one" },
  { id: "vm", row: 3, title: "Single VM" },
  { id: "k8s", row: 5, title: "Kubernetes full" },
  { id: "compass", row: 13, title: "Compass on a workstation" },
];

/** What the diagram shows for a profile; the opacity of a component is how much that profile runs it. */
interface Look {
  lit: Record<Comp, number>;
  /** The label of each edge, absent when the profile has no such edge. */
  compassEdge?: string;
  surveyorEdge?: string;
  /** A line under Meridian and under the Surveyor. */
  meridianNote: string;
  surveyorNote: string;
  caption: string;
  sub: string;
}
const LOOKS: Look[] = [
  {
    lit: { compass: 1, meridian: 1, atlas: 1, surveyor: 0.18 },
    compassEdge: "loopback",
    meridianNote: "native service, one machine",
    surveyorNote: "",
    caption: "Laptop: everything on one machine.",
    sub: "one command sets it up; --dry-run prints the plan and changes nothing",
  },
  {
    lit: { compass: 1, meridian: 1, atlas: 1, surveyor: 1 },
    compassEdge: "HTTPS · bearer token",
    surveyorEdge: "MCP",
    meridianNote: "systemd unit or compose, TLS at a proxy",
    surveyorNote: "optional, a second service",
    caption: "Team VM: Meridian on a host, Compass on each laptop.",
    sub: "the component lines of the systemd unit and of the compose file",
  },
  {
    lit: { compass: 1, meridian: 1, atlas: 1, surveyor: 1 },
    compassEdge: "HTTPS · Ingress",
    surveyorEdge: "MCP",
    meridianNote: "the Helm chart, Atlas embedded",
    surveyorNote: "one per perimeter",
    caption: "Kubernetes: the chart runs the servers, not the client.",
    sub: "the component keys of the chart's own values files",
  },
  {
    lit: { compass: 1, meridian: 0.4, atlas: 0.4, surveyor: 0.12 },
    compassEdge: "HTTPS",
    meridianNote: "a Meridian somewhere else",
    surveyorNote: "",
    caption: "Compass alone: the client does not belong in a cluster.",
    sub: "connect writes the agent's entry and nothing else; --dry-run says what",
  },
];

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
    else throw new Error(`deployment.txt: a line before the first command: ${line}`);
  }
  return steps;
}
const STEPS = parse(raw);
// The commands of each profile, by their index in the transcript.
const SCENE_STEPS: number[][] = [[0], [1, 2], [3], [4]];
if (STEPS.length !== 5 || SCENE_STEPS.length !== PROFILES.length || SCENE_STEPS.length !== LOOKS.length)
  throw new Error(`deployment.txt holds ${STEPS.length} commands, the film tells 5 in ${PROFILES.length} scenes: regenerate it (make film-transcripts) and revise the scenes`);

interface Row {
  at: number;
  text: string;
  cls: string;
  /** characters per second; absent = shown whole */
  cps?: number;
}
const TYPE_CPS = 55;
const LINE_GAP = 0.09; // s between two lines of output
const AFTER_CMD = 0.45; // s between the end of typing and the first line of output
const BETWEEN = 0.9; // s between two commands
const HOLD = 3.5; // s a scene is held after its last line
const FIRST = 1.0; // s before the first command of a scene

function rowClass(l: string): string {
  if (l.startsWith("Error:") || l.startsWith("warning:") || l.startsWith("[exit")) return "err";
  if (l.startsWith("dry run") || l.startsWith("[dry-run]")) return "tool";
  return "";
}
interface Timed {
  start: number;
  end: number;
  rows: Row[];
}
const TIMED: Timed[] = [];
{
  let cur = 0;
  SCENE_STEPS.forEach((steps) => {
    const rows: Row[] = [];
    let tl = FIRST;
    steps.forEach((k, j) => {
      const step = STEPS[k]!;
      if (step.note) {
        rows.push({ at: tl, text: step.note, cls: "note" });
        tl += 0.5;
      }
      rows.push({ at: tl, text: `$ ${step.cmd}`, cls: "cmd", cps: TYPE_CPS });
      tl += step.cmd.length / TYPE_CPS + AFTER_CMD;
      for (const l of step.out) {
        rows.push({ at: tl, text: l, cls: rowClass(l) });
        tl += LINE_GAP;
      }
      if (j < steps.length - 1) tl += BETWEEN;
    });
    TIMED.push({ start: cur, end: cur + tl + HOLD, rows });
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

// The diagram: Compass on the left, Meridian (Atlas inside it) in the middle, the Surveyor on the right.
const COLOURS: Record<Comp, string> = { meridian: "var(--carto-accent)", surveyor: "var(--carto-clay-text)", atlas: "var(--carto-info)", compass: "var(--carto-warning)" };
const BOX: Record<Comp, { x: number; y: number; w: number; h: number }> = {
  compass: { x: COL, y: 270, w: 380, h: 190 },
  meridian: { x: 700, y: 250, w: 520, h: 230 },
  atlas: { x: 740, y: 340, w: 440, h: 100 },
  surveyor: { x: 1440, y: 270, w: 380, h: 190 },
};
const blocks = {} as Record<Comp, HTMLElement>;
(["meridian", "compass", "surveyor", "atlas"] as Comp[]).forEach((c) => {
  const b = BOX[c];
  const name = c[0]!.toUpperCase() + c.slice(1);
  blocks[c] = mk(
    "div",
    "",
    `<b style="font-weight:normal">${name}</b>`,
    `left:${b.x}px;top:${b.y}px;width:${b.w}px;height:${b.h}px;box-sizing:border-box;border:4px solid ${COLOURS[c]};border-radius:14px;background:${c === "atlas" ? "var(--carto-background)" : "transparent"};font:40px/1 var(--carto-font-editorial);color:var(--carto-text);padding:${c === "atlas" ? "30px 26px" : "18px 24px"}`,
  );
});
// Meridian's own name sits in its top left; Atlas, inside it, is drawn after it so it is on top.
const notes = {
  meridian: mk("div", "url", "", `left:${BOX.meridian.x}px;top:${BOX.meridian.y + BOX.meridian.h + 14}px;width:${BOX.meridian.w}px;font-size:24px;white-space:normal`),
  surveyor: mk("div", "url", "", `left:${BOX.surveyor.x}px;top:${BOX.surveyor.y + BOX.surveyor.h + 14}px;font-size:24px`),
};
const EDGE_Y = 365;
const edges = {
  compass: { x1: BOX.compass.x + BOX.compass.w, x2: BOX.meridian.x, y: EDGE_Y },
  surveyor: { x1: BOX.surveyor.x, x2: BOX.meridian.x + BOX.meridian.w, y: EDGE_Y },
};
const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
svg.setAttribute("style", "position:absolute;left:0;top:0;width:1920px;height:1080px;overflow:visible");
svg.innerHTML =
  `<line id="e-compass" x1="${edges.compass.x1}" y1="${EDGE_Y}" x2="${edges.compass.x2}" y2="${EDGE_Y}" stroke="${COLOURS.compass}" stroke-width="4"/>` +
  `<line id="e-surveyor" x1="${edges.surveyor.x1}" y1="${EDGE_Y}" x2="${edges.surveyor.x2}" y2="${EDGE_Y}" stroke="${COLOURS.surveyor}" stroke-width="4"/>`;
document.getElementById("stage")!.appendChild(svg);
const edgeLine = { compass: svg.querySelector("#e-compass") as SVGLineElement, surveyor: svg.querySelector("#e-surveyor") as SVGLineElement };
const edgeLabel = {
  compass: mk("div", "url", "", `left:${edges.compass.x1}px;top:${EDGE_Y - 44}px;width:${edges.compass.x2 - edges.compass.x1}px;text-align:center;font-size:22px`),
  surveyor: mk("div", "url", "", `left:${edges.surveyor.x2}px;top:${EDGE_Y - 44}px;width:${edges.surveyor.x1 - edges.surveyor.x2}px;text-align:center;font-size:22px`),
};

const CARD = { x: COL, y: 560, w: 1720, rows: 12, lh: 34 };
const card = mk("div", "card", "", `left:${CARD.x}px;top:${CARD.y}px;width:${CARD.w}px`);
card.innerHTML = '<div class="bar"><i class="live"></i><i></i><i></i><b>~ · deploy</b></div>';
const body = document.createElement("div");
body.className = "body";
body.style.cssText = `font:21px/${CARD.lh}px var(--carto-font-code);height:${CARD.rows * CARD.lh}px;white-space:pre;overflow:hidden`;
card.appendChild(body);

// The end card covers the scenes; created last so it is on top.
const veil = mk("div", "", "");
veil.id = "veil";
const logo = mk("img", "", "", "left:310px;top:340px;width:150px;height:150px") as HTMLImageElement;
logo.src = logoUrl;
const word = mk("div", "wordmark", "Deploy what you need.", "left:500px;top:326px;font-size:88px");
const word2 = mk("div", "wordmark", "Laptop, VM or cluster.", "left:500px;top:432px;font-size:88px");
const url = mk("div", "url", "github.com/BeppeTemp/cartographer", "left:504px;top:590px");

const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;");
const last = { html: "" };
const PLATE = 0.7; // s a scene takes to move the diagram from the previous profile's look to its own

function frame(n: number) {
  const t = n / FPS;
  const si = Math.max(0, TIMED.findIndex((s) => t >= s.start && t < s.end));
  const inScenes = t < SCENES_END;
  const cur = inScenes ? TIMED[si]! : TIMED[TIMED.length - 1]!;
  const tl = t - cur.start;
  const look = LOOKS[si]!;
  const prev = LOOKS[Math.max(0, si - 1)]!;
  const mix = si === 0 ? 1 : easeOut(prog(tl, 0, PLATE));

  // the caption and the counter of the scene, crossfading at its edges
  const edge = inScenes ? reveal(tl, 0.2) * fade(tl, cur.end - cur.start - 0.4, 0.4) : 0;
  day.textContent = `row ${PROFILES[si]!.row} of the deployment scenarios`;
  cap.innerHTML = `${look.caption}<div class="sub24">${esc(look.sub)}</div>`;
  vis(day, edge, 12);
  vis(cap, edge, 12);

  // the diagram: the components this profile runs are lit, the others dimmed
  const shell = inScenes ? reveal(t, 0.2, 0.6) * (1 - prog(t, SCENES_END, 0.5)) : 0;
  (Object.keys(BOX) as Comp[]).forEach((c) => vis(blocks[c], shell * lerp(prev.lit[c], look.lit[c], mix)));
  const edgeOp = (key: "compass" | "surveyor") => {
    const was = prev[key === "compass" ? "compassEdge" : "surveyorEdge"] ? 1 : 0;
    const is = look[key === "compass" ? "compassEdge" : "surveyorEdge"] ? 1 : 0;
    return shell * lerp(was, is, mix);
  };
  (["compass", "surveyor"] as const).forEach((k) => {
    const o = edgeOp(k);
    edgeLine[k].style.opacity = String(clamp01(o));
    // Compass is dashed when the Meridian it reaches is not part of the profile.
    edgeLine[k].setAttribute("stroke-dasharray", k === "compass" && si === 3 ? "14 10" : "");
    const label = (k === "compass" ? look.compassEdge : look.surveyorEdge) ?? (k === "compass" ? prev.compassEdge : prev.surveyorEdge) ?? "";
    edgeLabel[k].textContent = label;
    vis(edgeLabel[k], o);
  });
  notes.meridian.textContent = look.meridianNote;
  notes.surveyor.textContent = look.surveyorNote;
  vis(notes.meridian, shell * Math.max(look.lit.meridian, 0.6));
  vis(notes.surveyor, look.surveyorNote ? shell : 0);

  // the terminal: the rows whose time has come, the last `rows` of them (it follows its tail)
  vis(card, inScenes ? reveal(t, 0, 0.5) * (1 - prog(t, SCENES_END, 0.5)) : 0, 16);
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
    const style = r.cls === "note" ? "color:#AFB9AE;font-style:italic;" : "";
    html += `<div class="${r.cls === "err" ? "err" : r.cls === "tool" ? "tool" : ""}" style="${style}opacity:${clamp01(o).toFixed(3)}">${inner}${cursor}</div>`;
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
  vis(url, reveal(t, SCENES_END + 2.0), 16);
}

defineFilm({ fps: FPS, duration: DURATION, frame });
