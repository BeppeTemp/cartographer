// The parts the concept films (deployment, plugins) share, so they read as one series: the four components with their
// colour, name and role, the kicker and caption of a scene, the one-command chip, a status pill and the end card.
import { mk, prog, reveal, vis } from "./kit";

export type Comp = "meridian" | "atlas" | "surveyor" | "compass";
/** The brand's colour of each component, as in scene G of the launch film. */
export const COLOR: Record<Comp, string> = {
  meridian: "var(--carto-accent)",
  surveyor: "var(--carto-clay-text)",
  atlas: "var(--carto-info)",
  compass: "var(--carto-warning)",
};
export const NAME: Record<Comp, string> = { meridian: "Meridian", atlas: "Atlas", surveyor: "Surveyor", compass: "Compass" };
/** The role, always beside the name: what the component is, in one word or two. */
export const ROLE: Record<Comp, string> = { meridian: "the server", atlas: "the web interface", surveyor: "the agent", compass: "the client" };

/** The left margin every scene aligns to. */
export const COL = 100;
export const CODE = "var(--carto-font-code)";
export const SERIF = "var(--carto-font-editorial)";
export const PAPER = "box-sizing:border-box;background:#FBF8F1;border-radius:16px;box-shadow:0 18px 36px rgba(37,40,36,.12)";

/** The kicker (italic, small) and the caption (the scene's sentence) at the top left. */
export function heading(): { kicker: HTMLElement; cap: HTMLElement } {
  return {
    kicker: mk("div", "day", "", `left:${COL}px;top:48px`),
    cap: mk("div", "caption wide", "", `left:${COL}px;top:104px`),
  };
}

/** A component box: its colour, its name and, always, its role; `extra` follows the role, `body` goes below. */
export function comp(c: Comp, x: number, y: number, w: number, h: number, extra = "", body = ""): HTMLElement {
  return mk(
    "div",
    "",
    `<div style="font:38px/1.1 ${SERIF}">${NAME[c]}</div><div style="font:22px/1.3 ${CODE};color:var(--carto-text-muted);margin-top:8px">${ROLE[c]}${extra}</div>${body}`,
    `left:${x}px;top:${y}px;width:${w}px;height:${h}px;${PAPER};border:4px solid ${COLOR[c]};border-left-width:12px;padding:18px 22px`,
  );
}

/** The one command a scene shows, with what it does, on the bottom line. */
export function how(text: string, note: string): HTMLElement {
  return mk(
    "div",
    "",
    `<span style="font:26px ${CODE};background:#191F1C;color:#EEEDE5;padding:12px 22px;border-radius:12px">${text}</span><span style="font:26px ${SERIF};color:var(--carto-text-muted);margin-left:24px">${note}</span>`,
    `left:${COL}px;top:960px;white-space:nowrap`,
  );
}

/** A status pill. */
export function pill(text: string, bg: string, x: number, y: number): HTMLElement {
  return mk("div", "", text, `left:${x}px;top:${y}px;font:26px ${CODE};background:${bg};color:#FCFAF6;padding:10px 20px;border-radius:12px;white-space:nowrap`);
}

/** The end card: the logo, two lines of wordmark and the URL. Create it last, so it covers the scenes. */
export function endCard(logoUrl: string, line1: string, line2: string): (t: number, start: number) => void {
  const veil = mk("div", "", "");
  veil.id = "veil";
  const logo = mk("img", "", "", "left:310px;top:340px;width:150px;height:150px") as HTMLImageElement;
  logo.src = logoUrl;
  const word = mk("div", "wordmark", line1, "left:500px;top:326px;font-size:88px");
  const word2 = mk("div", "wordmark", line2, "left:500px;top:432px;font-size:88px");
  const url = mk("div", "url", "github.com/BeppeTemp/cartographer", "left:504px;top:590px");
  return (t, start) => {
    const e = prog(t, start, 0.6);
    vis(veil, e);
    veil.style.visibility = e <= 0.001 ? "hidden" : "visible";
    vis(logo, reveal(t, start + 0.3), 16);
    vis(word, reveal(t, start + 0.5), 16);
    vis(word2, reveal(t, start + 1.1), 16);
    vis(url, reveal(t, start + 2.0), 16);
  };
}
