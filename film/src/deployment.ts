// The deployment film: "Deploy what you need." First the four components, each with its role (server, web interface,
// agent, client); then four ways to run them, each drawn as the machines it uses and the components on each, with the
// one command or artefact that sets it up. Concepts first, in the style of the launch film: no terminal output.
// Like every film it is a pure function of time: frame(n) sets every element from t = n / FPS.
import "../film.css";
import "../../docs/brand/cartographer.css";
import "../../web/src/styles/tokens.css";
import logoUrl from "../../docs/brand/coordinate-pine.svg";
import { clamp01, defineFilm, fade, mk, reveal, stage, vis } from "./kit";
import { COL, COLOR, type Comp, NAME, ROLE, comp, endCard, heading, how } from "./parts";

const FPS = 30;

// --- the profiles ---------------------------------------------------------
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
  { id: "remote", row: 16, title: "No client" },
  { id: "mixed", row: 9, title: "Same KB on two servers" },
];

// --- the components -------------------------------------------------------
const INTRO: Record<Comp, string> = {
  meridian: "Holds the wiki, a git repository, and is its only writer. Your agents reach it over MCP.",
  atlas: "Shows the wiki as a living map in the browser. Served by Meridian.",
  surveyor: "Works on a schedule: brings in your sources and keeps the wiki in order.",
  compass: "Runs on each laptop and connects every coding agent: Claude Code, Codex, Kiro and more.",
};

// --- the scenes -----------------------------------------------------------
interface Scene {
  dur: number;
  kicker: string;
  caption: string;
  sub: string;
  /** The elements of the scene; each fades in at its own `at` (s from the scene's start). */
  els: { el: HTMLElement | SVGElement; at: number }[];
}
const SCENES: Scene[] = [];
const { kicker, cap } = heading();
const SVG = "http://www.w3.org/2000/svg";
const lines = document.createElementNS(SVG, "svg");
lines.setAttribute("style", "position:absolute;left:0;top:0;width:1920px;height:1080px;overflow:visible;pointer-events:none");
stage.appendChild(lines);

/** A machine: a dashed frame with its name above it. */
function host(x: number, y: number, w: number, h: number, label: string): HTMLElement {
  return mk(
    "div",
    "",
    `<div style="position:absolute;left:0;top:-44px;font:26px var(--carto-font-code);color:var(--carto-text-muted);white-space:nowrap">${label}</div>`,
    `left:${x}px;top:${y}px;width:${w}px;height:${h}px;box-sizing:border-box;border:2px dashed #B9BBB1;border-radius:22px`,
  );
}
interface Line { g: SVGGElement; x1: number; y1: number; x2: number; y2: number }
/** A connection: a line and a label sitting above it, between the boxes and never on them. */
function link(x1: number, y1: number, x2: number, y2: number, label: string, color: string, dashed = false, layer: SVGSVGElement = lines): HTMLElement {
  const g = document.createElementNS(SVG, "g");
  g.innerHTML = `<line x1="${x1}" y1="${y1}" x2="${x2}" y2="${y2}" stroke="${color}" stroke-width="4" stroke-linecap="round"${dashed ? ' stroke-dasharray="14 10"' : ""}/>`;
  layer.appendChild(g);
  const mx = (x1 + x2) / 2, my = Math.min(y1, y2) - 30;
  const lab = mk("div", "", label, `left:${mx - 160}px;top:${my - 46}px;width:320px;text-align:center;font:22px var(--carto-font-code);color:var(--carto-text-muted);white-space:nowrap`);
  (lab as unknown as { _line: Line })._line = { g, x1, y1, x2, y2 };
  return lab;
}
// 1 — the four components, each with its role.
{
  const order: Comp[] = ["meridian", "atlas", "surveyor", "compass"];
  const els = order.map((c, k) => ({
    el: mk(
      "div",
      "comp",
      `<b style="width:300px">${NAME[c]}<br><span style="font:24px var(--carto-font-code);color:var(--carto-text-muted);padding:0;white-space:nowrap">${ROLE[c]}</span></b><span>${INTRO[c]}</span>`,
      `left:${COL}px;top:${330 + k * 150}px;width:1500px;border-left-color:${COLOR[c]}`,
    ),
    at: 0.8 + k * 0.9,
  }));
  SCENES.push({ dur: 7.5, kicker: "One binary, four components.", caption: "Deploy what you need.", sub: "", els });
}

// 2 — all-in-one: everyone with their own installation.
{
  const people = ["Alice", "Marco", "Sara"];
  const els: { el: HTMLElement; at: number }[] = [];
  people.forEach((p, k) => {
    const x = 100 + k * 590;
    els.push({ el: host(x, 330, 540, 520, `${p}'s laptop`), at: 0.4 + k * 0.9 });
    els.push({ el: comp("compass", x + 30, 380, 480, 120), at: 0.7 + k * 0.9 });
    els.push({ el: link(x + 270, 500, x + 270, 560, "", COLOR.compass), at: 1.3 + k * 0.9 });
    els.push({ el: comp("meridian", x + 30, 560, 480, 275, "<br>their own KBs"), at: 1.0 + k * 0.9 });
    els.push({ el: comp("atlas", x + 60, 692, 420, 122), at: 1.2 + k * 0.9 });
  });
  els.push({ el: how("cartographer setup", "one command each: the server as a service, the agents connected"), at: 3.6 });
  SCENES.push({ dur: 7.5, kicker: "All-in-one", caption: "Everyone with their own installation.", sub: "", els });
}

// 3 — client-server: many people, one server.
{
  const people = ["Alice", "Marco", "Sara"];
  const laptops = people.map((p, k) => host(100, 330 + k * 190, 420, 130, `${p}'s laptop`));
  const cs = people.map((_, k) => comp("compass", 120, 340 + k * 190, 380, 110));
  const S = host(1000, 330, 820, 510, "one server: a VM or a Kubernetes cluster");
  const M = comp("meridian", 1030, 420, 470, 330);
  const A = comp("atlas", 1060, 590, 410, 130);
  const V = comp("surveyor", 1530, 420, 260, 330, "<br>optional");
  const ls = people.map((_, k) => link(500, 395 + k * 190, 1030, 585, k === 0 ? "HTTPS · a token each" : "", COLOR.compass));
  SCENES.push({
    dur: 7.5,
    kicker: "Client and server",
    caption: "Many people, one server.",
    sub: "",
    els: [
      ...laptops.map((el, k) => ({ el, at: 0.4 + k * 0.2 })),
      ...cs.map((el, k) => ({ el, at: 0.6 + k * 0.2 })),
      { el: S, at: 1.2 }, { el: M, at: 1.5 }, { el: A, at: 1.8 }, { el: V, at: 2.2 },
      ...ls.map((el, k) => ({ el, at: 2.6 + k * 0.3 })),
      { el: how("cartographer serve · helm install", "the server on a VM or in Kubernetes; on each laptop, cartographer connect"), at: 3.2 },
    ],
  });
}

// 4 — no client: any MCP agent straight to the server.
{
  const AG = ["a chat app on the web", "an agent in the cloud", "your own agent"];
  const agents = AG.map((a, k) =>
    mk("div", "", `<div style="font:34px/1.1 var(--carto-font-editorial)">${a}</div><div style="font:22px var(--carto-font-code);color:var(--carto-text-muted);margin-top:8px">any MCP client</div>`,
      `left:100px;top:${340 + k * 190}px;width:420px;height:120px;box-sizing:border-box;border:2px solid #B9BBB1;border-radius:16px;background:#FBF8F1;padding:18px 22px;box-shadow:0 18px 36px rgba(37,40,36,.12)`));
  const S = host(1000, 330, 820, 510, "your server");
  const M = comp("meridian", 1030, 420, 760, 330, "<br>the MCP tools: search, read, write, review");
  const A = comp("atlas", 1060, 590, 410, 130);
  const ls = AG.map((_, k) => link(520, 400 + k * 190, 1030, 585, k === 0 ? "MCP over HTTPS · a token" : "", COLOR.meridian));
  SCENES.push({
    dur: 7,
    kicker: "No client",
    caption: "Any MCP agent, straight to the server.",
    sub: "",
    els: [
      ...agents.map((el, k) => ({ el, at: 0.4 + k * 0.2 })),
      { el: S, at: 1.1 }, { el: M, at: 1.4 }, { el: A, at: 1.7 },
      ...ls.map((el, k) => ({ el, at: 2.2 + k * 0.3 })),
      { el: how("https://wiki.example.com/mcp/routed", "add it as a remote MCP server: no skills or hooks, just the tools"), at: 2.8 },
    ],
  });
}

// 5 — all together: the KB is a git repository on its own; every server keeps a clone of it, and people and agents
// reach whichever server suits them. Built group by group: the KB, then each server with its git line, then the rest.
{
  const box = (x: number, y: number, w: number, h: number, title: string, sub: string) =>
    mk("div", "", `<div style="font:32px/1.1 var(--carto-font-editorial)">${title}</div><div style="font:22px var(--carto-font-code);color:var(--carto-text-muted);margin-top:8px">${sub}</div>`,
      `left:${x}px;top:${y}px;width:${w}px;height:${h}px;box-sizing:border-box;border:2px solid #B9BBB1;border-radius:16px;background:#FBF8F1;padding:16px 20px;box-shadow:0 18px 36px rgba(37,40,36,.12)`);
  const GIT = "var(--carto-accent)";
  const KB = mk("div", "", `<div style="font:22px var(--carto-font-code);color:var(--carto-text-muted)">the knowledge base</div><div style="font:56px/1.2 var(--carto-font-editorial)">team-kb</div><div style="font:22px var(--carto-font-code);color:var(--carto-text-muted)">a git repository<br>on GitHub or GitLab</div>`,
    `left:800px;top:470px;width:320px;height:210px;box-sizing:border-box;border:4px solid var(--carto-text);border-radius:20px;background:#FFFFFF;padding:22px 26px;box-shadow:0 26px 52px rgba(37,40,36,.24)`);
  // Alice: everything on her laptop, her own KB beside the team's
  const AH = host(100, 290, 580, 220, "Alice's laptop");
  const AC = comp("compass", 120, 330, 250, 130);
  const AM = comp("meridian", 395, 310, 270, 180, "<br>+ alice-notes");
  const AL = link(370, 395, 395, 395, "", COLOR.compass);
  const AG = link(665, 450, 800, 530, "", GIT, true);
  // a Kubernetes cluster: the Atlas and one Surveyor per team
  const KH = host(1220, 290, 620, 380, "a Kubernetes cluster");
  const KM = comp("meridian", 1240, 320, 330, 330);
  const KA = comp("atlas", 1260, 505, 300, 125);
  const KV1 = comp("surveyor", 1600, 320, 220, 160, "<br>team A");
  const KV2 = comp("surveyor", 1600, 500, 220, 150, "<br>team B");
  const KG = link(1240, 470, 1120, 560, "", GIT, true);
  // a VM in another office
  const VH = host(420, 620, 260, 230, "a VM");
  const VM = comp("meridian", 440, 650, 220, 180);
  const VG = link(660, 700, 800, 640, "", GIT, true);
  // a Surveyor on a GPU box, with a local model, working for the cluster's server
  const GH = host(790, 290, 340, 140, "a GPU box, local model");
  const GV = comp("surveyor", 810, 305, 300, 110);
  const GL = link(1110, 360, 1240, 400, "", COLOR.surveyor);
  // the people and agents, each on the server that suits them
  const sara = comp("compass", 100, 600, 280, 140, "<br>Sara");
  const saraL = link(380, 672, 440, 690, "", COLOR.compass);
  const ci = comp("compass", 100, 760, 280, 140, "<br>in a CI job");
  const ciL = link(380, 822, 440, 790, "", COLOR.compass);
  const marco = comp("compass", 1220, 740, 280, 140, "<br>Marco");
  const marcoL = link(1360, 740, 1380, 650, "", COLOR.compass);
  const giulia = comp("compass", 1540, 740, 280, 140, "<br>Giulia");
  const giuliaL = link(1640, 740, 1500, 650, "", COLOR.compass);
  const chat = box(790, 740, 340, 140, "a chat app", "over MCP, no client");
  const chatL = link(1130, 790, 1240, 620, "", COLOR.meridian);
  const g = (t: number, ...el: HTMLElement[]) => el.map((e, k) => ({ el: e, at: t + k * 0.2 }));
  SCENES.push({
    dur: 10.5,
    kicker: "All together",
    caption: "One KB. Any mix of servers, people and agents.",
    sub: "",
    els: [
      ...g(0.4, KB),
      ...g(1.0, AH, AM, AC, AL, AG),
      ...g(1.9, KH, KM, KA, KV1, KV2, KG),
      ...g(2.9, VH, VM, VG),
      ...g(3.6, GH, GV, GL),
      ...g(4.3, sara, saraL), ...g(4.6, ci, ciL), ...g(4.9, marco, marcoL), ...g(5.2, giulia, giuliaL), ...g(5.5, chat, chatL),
      { el: how("cartographer kb clone", "every server keeps its own clone of the KB; their writes meet through git"), at: 6.2 },
    ],
  });
}

// --- timing -----------------------------------------------------------------
const STARTS: number[] = [];
{
  let t = 0;
  for (const s of SCENES) { STARTS.push(t); t += s.dur; }
}
const SCENES_END = STARTS[STARTS.length - 1]! + SCENES[SCENES.length - 1]!.dur;
const END_CARD = 4.5;
const DURATION = SCENES_END + END_CARD;
const OUT = 0.45; // s a scene takes to fade out

// Created last, so it covers the scenes.
const end = endCard(logoUrl, "Deploy what you need.", "Alone, as a team, or both.");

function frame(n: number) {
  const t = n / FPS;
  let si = SCENES.length - 1;
  for (let k = 0; k < SCENES.length; k++) if (t < STARTS[k]! + SCENES[k]!.dur) { si = k; break; }
  const inScenes = t < SCENES_END;

  SCENES.forEach((s, k) => {
    const tl = t - STARTS[k]!;
    const out = fade(tl, s.dur - OUT, OUT);
    const active = inScenes && k === si;
    for (const { el, at } of s.els) {
      const o = active ? reveal(tl, at) * out : 0;
      vis(el as HTMLElement, o);
      // a line draws itself from its start to its end while it fades in, instead of rising like a box
      const line = (el as unknown as { _line?: Line })._line;
      if (line) {
        const p = active ? reveal(tl, at, 0.45) : 0;
        const ln = line.g.firstElementChild!;
        ln.setAttribute("x2", String(line.x1 + (line.x2 - line.x1) * p));
        ln.setAttribute("y2", String(line.y1 + (line.y2 - line.y1) * p));
        line.g.style.opacity = String(clamp01(o));
      }
    }
    if (active) {
      const e = reveal(tl, 0.1) * out;
      kicker.textContent = s.kicker;
      cap.innerHTML = s.caption + (s.sub ? `<div class="sub24">${s.sub}</div>` : "");
      vis(kicker, e, 12);
      vis(cap, e, 12);
    }
  });
  if (!inScenes) { vis(kicker, 0); vis(cap, 0); }

  end(t, SCENES_END);
}

defineFilm({ fps: FPS, duration: DURATION, frame });
