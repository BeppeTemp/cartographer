// The plugins film: "Extend Cartographer with plugins." What a plugin is (a folder or a git repository whose parts go
// to each component), one plugin for a company, then installing it: a preview, the approval, and the digest that stops
// it when one byte changes. Same parts as the deployment film (./parts).
// Like every film it is a pure function of time: frame(n) sets every element from t = n / FPS.
import "../film.css";
import "../../docs/brand/cartographer.css";
import "../../web/src/styles/tokens.css";
import logoUrl from "../../docs/brand/coordinate-pine.svg";
import { clamp01, defineFilm, fade, mk, reveal, stage, vis } from "./kit";
import { CODE, COL, COLOR, type Comp, NAME, PAPER, ROLE, SERIF, endCard, heading, how, pill } from "./parts";

const FPS = 30;
const IN = 0.5, OUT = 0.45;

/** An element on screen from `from` to `to` (s, absolute). */
interface Shot { el: HTMLElement; from: number; to: number }
const SHOTS: Shot[] = [];
const on = (el: HTMLElement, from: number, to: number) => { SHOTS.push({ el, from, to }); return el; };

/** A line that draws itself from (x1, y1) to (x2, y2) from `from`, and fades at `to`. */
interface Wire { ln: SVGLineElement; x1: number; y1: number; x2: number; y2: number; from: number; to: number }
const WIRES: Wire[] = [];
const SVG = "http://www.w3.org/2000/svg";
const layer = document.createElementNS(SVG, "svg");
layer.setAttribute("style", "position:absolute;left:0;top:0;width:1920px;height:1080px;overflow:visible;pointer-events:none");
stage.appendChild(layer);
function wire(x1: number, y1: number, x2: number, y2: number, color: string, from: number, to: number) {
  const ln = document.createElementNS(SVG, "line");
  ln.setAttribute("stroke", color);
  ln.setAttribute("stroke-width", "4");
  ln.setAttribute("stroke-linecap", "round");
  ln.setAttribute("x1", String(x1));
  ln.setAttribute("y1", String(y1));
  layer.appendChild(ln);
  WIRES.push({ ln, x1, y1, x2, y2, from, to });
}

/** The kicker and caption of each stretch of the film. */
const BEATS: { from: number; to: number; kicker: string; caption: string }[] = [];
const { kicker, cap } = heading();
const muted = (s: string) => `<span style="color:var(--carto-text-muted)">${s}</span>`;

/** A component box with what the plugin adds to it, one `+` line each. */
function adds(c: Comp, x: number, y: number, w: number, h: number, items: string[], size = 28): HTMLElement {
  return mk(
    "div",
    "",
    `<div style="display:flex;align-items:baseline;gap:18px"><span style="font:36px ${SERIF}">${NAME[c]}</span><span style="font:22px ${CODE};color:var(--carto-text-muted)">${ROLE[c]}</span></div>` +
      items.map((i) => `<div style="font:${size}px/1.5 ${SERIF};margin-top:4px">+ ${i}</div>`).join(""),
    `left:${x}px;top:${y}px;width:${w}px;height:${h}px;${PAPER};border:4px solid ${COLOR[c]};border-left-width:12px;padding:16px 24px`,
  );
}

// --- 1. what a plugin is (0–7.5 s) ---------------------------------------------
const S1 = 0, S2 = 7.5, S3 = 15.5, S4 = 21.5, S5 = 27, END = 33;
BEATS.push({ from: S1, to: S2, kicker: "Plugins", caption: "Extend Cartographer with plugins." });
{
  // the plugin: a folder or a git repository, one part per component
  const TREE: [string, string][] = [
    ["cartographer-plugin.yaml", "the manifest"],
    ["client/", "skills · hooks · plugin.star"],
    ["server/", "roles · presets"],
    ["surveyor/", "recipes · connectors"],
    ["atlas/", "branding · views · apps"],
  ];
  on(
    mk(
      "div",
      "",
      `<div style="font:22px ${CODE};color:var(--carto-text-muted)">a folder or a git repository</div><div style="font:48px/1.3 ${SERIF};margin-bottom:18px">a plugin</div>` +
        TREE.map(([f, d]) => `<div style="height:96px;box-sizing:border-box;border-top:1px solid #DCDDD5;padding-top:14px"><div style="font:28px ${CODE}">${f}</div><div style="font:22px ${CODE};color:var(--carto-text-muted);margin-top:6px">${d}</div></div>`).join(""),
      `left:${COL}px;top:290px;width:620px;height:640px;${PAPER};border:2px solid #DCDDD5;padding:26px 34px`,
    ),
    S1 + 0.5, S2,
  );
  // each part goes to its component
  const items: Record<Comp, string> = {
    compass: "a new agent client, skills, hooks, commands and tools",
    meridian: "roles and presets for the server",
    surveyor: "recipes and connectors to your sources",
    atlas: "your branding, extra views and apps",
  };
  (["compass", "meridian", "surveyor", "atlas"] as Comp[]).forEach((c, k) => {
    const y = 330 + k * 150;
    on(adds(c, 1060, y, 760, 130, [items[c]]), S1 + 1.6 + k * 0.6, S2);
    const ry = 290 + 26 + 22 + 62 + 18 + (k + 1) * 96 + 40; // the row of the tree that part comes from
    wire(720, ry, 1060, y + 65, COLOR[c], S1 + 1.9 + k * 0.6, S2);
  });
}

// --- 2. one plugin for a company (7.5–15.5 s) --------------------------------------
BEATS.push({ from: S2, to: S3, kicker: "An example", caption: "One plugin, the same for the whole company." });
{
  on(
    mk(
      "div",
      "",
      `<div style="font:22px ${CODE};color:var(--carto-text-muted)">a plugin</div><div style="font:60px/1.2 ${SERIF}">acme-tools</div><div style="font:22px ${CODE};color:var(--carto-text-muted)">gitlab.example.com/acme</div>`,
      `left:760px;top:490px;width:400px;height:200px;box-sizing:border-box;border:4px solid var(--carto-text);border-radius:20px;background:#FFFFFF;padding:24px 30px;box-shadow:0 26px 52px rgba(37,40,36,.24)`,
    ),
    S2 + 0.4, S3,
  );
  const C: [Comp, number, number, string[]][] = [
    ["atlas", 100, 290, ["the Acme logo and colours", "a view of open incidents"]],
    ["surveyor", 1260, 290, ["a connector to the ticket system", "a recipe that files the runbooks"]],
    ["compass", 100, 710, ["acme-agent, the in-house agent", "a deploy command for every agent"]],
    ["meridian", 1260, 710, ["an on-call role that may only read", "a preset for new team KBs"]],
  ];
  C.forEach(([c, x, y, it], k) => {
    on(adds(c, x, y, 560, 190, it, 28), S2 + 1.4 + k * 0.7, S3);
    const left = x < 760;
    wire(left ? 760 : 1160, y < 500 ? 520 : 660, left ? 660 : 1260, y + 95, COLOR[c], S2 + 1.7 + k * 0.7, S3);
  });
}

// --- 3–5. installing it: preview, approval, digest (15.5–33 s) ---------------------
BEATS.push({ from: S3, to: S4, kicker: "Install", caption: "Install from a folder or a git repository." });
BEATS.push({ from: S4, to: S5, kicker: "Approval", caption: "Nothing runs until you approve it." });
BEATS.push({ from: S5, to: END, kicker: "Integrity", caption: "What runs is pinned by digest." });
{
  // the left column: where it comes from, then what each beat means
  on(
    mk("div", "", `<div style="font:22px ${CODE};color:var(--carto-text-muted)">the source</div><div style="font:30px/1.4 ${CODE};margin-top:8px">https://gitlab.example.com/<br>acme/acme-tools.git<br>${muted("@ v1.2")}</div>`,
      `left:${COL}px;top:300px;width:600px;height:200px;${PAPER};border:2px solid #DCDDD5;padding:24px 30px`),
    S3 + 0.4, END,
  );
  wire(700, 400, 820, 400, "var(--carto-text-muted)", S3 + 1.0, END);
  const NOTE = `left:${COL}px;top:560px;width:600px;font:40px/1.3 ${SERIF}`;
  on(mk("div", "", "Pinned to one commit. You see everything before anything runs.", NOTE), S3 + 2.2, S4);
  on(mk("div", "", "You read what it may do, then you say yes. A new version asks again.", NOTE), S4 + 1.6, S5);
  on(mk("div", "", `One byte of a script changes on disk. ${muted("The digest no longer matches: it stops until you approve again.")}`, NOTE), S5 + 1.4, END);

  // the preview, on screen from the install to the end: an illustration of what `plugin install` shows, its "may"
  // lines worded as internal/plugin/preview.go words them
  const row = (k: string, v: string) => `<div style="display:flex;gap:20px;font:26px/1.75 ${CODE}"><span style="width:140px;color:var(--carto-text-muted)">${k}</span><span>${v}</span></div>`;
  on(
    mk(
      "div",
      "",
      `<div style="font:46px ${SERIF}">acme-tools ${muted(`<span style="font:26px ${CODE}">v1.2 · commit 4f9c2e1</span>`)}</div>` +
        `<div style="margin-top:18px;border-top:1px solid #DCDDD5;padding-top:12px">` +
        row("adds", "client acme-agent · command deploy") +
        row("", "connector tickets · Acme branding") +
        row("may", "reach hosts: tickets.example.com") +
        row("", "read environment variables: TICKETS_TOKEN") +
        row("settings", "tickets_url · team") +
        `</div><div style="margin-top:12px;border-top:1px solid #DCDDD5;padding-top:12px">` +
        row("runs", `plugin.star&nbsp;&nbsp; ${muted("sha256:9f2c…41ab")}`) +
        row("", `surveyor.star <span id="pv-digest"></span>`) +
        `</div>`,
      `left:820px;top:300px;width:1000px;height:560px;${PAPER};border:2px solid #DCDDD5;border-left:12px solid var(--carto-text);padding:28px 36px`,
    ),
    S3 + 0.9, END,
  );
  // the state, right-aligned in the preview's top corner
  const state = (text: string, bg: string) => {
    const el = pill(text, bg, 0, 322);
    el.style.left = "auto";
    el.style.right = "130px";
    return el;
  };
  on(state("pending approval", COLOR.surveyor), S3 + 1.6, S4 + 1.0);
  on(state("approved · v1.2", COLOR.meridian), S4 + 1.2, S5 + 1.8);
  on(state("modified · stopped", COLOR.surveyor), S5 + 2.0, END);

  on(how("cartographer plugin install https://gitlab.example.com/acme/acme-tools.git@v1.2", "a preview, nothing enabled"), S3 + 2.6, S4);
  on(how("cartographer plugin approve acme-tools", "once per version"), S4 + 2.2, S5);
}
const digest = document.getElementById("pv-digest")!;
const DIGEST_CHANGED = S5 + 1.2;

// --- the end card -----------------------------------------------------------
const end = endCard(logoUrl, "Approved per version.", "Pinned by digest.");
const DURATION = END + 4.5;

function frame(n: number) {
  const t = n / FPS;
  for (const s of SHOTS) vis(s.el, t < s.to ? reveal(t, s.from, IN) * fade(t, s.to - OUT, OUT) : 0);
  for (const w of WIRES) {
    const p = t < w.to ? reveal(t, w.from, 0.45) : 0;
    w.ln.setAttribute("x2", String(w.x1 + (w.x2 - w.x1) * p));
    w.ln.setAttribute("y2", String(w.y1 + (w.y2 - w.y1) * p));
    w.ln.style.opacity = String(clamp01(p > 0 ? fade(t, w.to - OUT, OUT) : 0));
  }
  const d = t < DIGEST_CHANGED ? muted("sha256:03de…77c0") : `<span style="color:${COLOR.surveyor}">sha256:b71a…e902 ≠ approved</span>`;
  if (digest.innerHTML !== d) digest.innerHTML = d;
  const beat = BEATS.find((b) => t >= b.from && t < b.to);
  if (beat) {
    const e = reveal(t, beat.from, IN) * fade(t, beat.to - OUT, OUT);
    kicker.textContent = beat.kicker;
    cap.textContent = beat.caption;
    vis(kicker, e);
    vis(cap, e);
  } else { vis(kicker, 0); vis(cap, 0); }
  end(t, END);
}

defineFilm({ fps: FPS, duration: DURATION, frame });
