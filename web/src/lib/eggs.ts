/**
 * Two easter eggs, typed into the search palette (Ctrl/Cmd+K) and gone on a
 * reload: nothing is stored. Each is a class on <body> that a stylesheet of
 * its own (styles/eggs.css) dresses the UI with; the graph reads `tako` for
 * its colours. They change how the page looks, never what it does.
 *
 * - `glass` ("liquid glass"): everything turns to glass under disco lights.
 * - `tako` ("tako mode"): the UI as a proud vibe-coded app would draw it.
 */
export type Egg = "glass" | "tako";

const PHRASES: Record<string, Egg[]> = {
  "liquid glass": ["glass"],
  "tako mode": ["tako"],
};

/** The eggs a palette query asks for, or null when it is an ordinary search.
 *  Only the whole phrase counts, in any case and spacing. */
export function eggsFor(query: string): Egg[] | null {
  return PHRASES[query.trim().toLowerCase().replace(/\s+/g, " ")] ?? null;
}

/** Turns the asked eggs on, or off when all of them already are: typing the
 *  phrase again undoes it. */
export function toggleEggs(on: ReadonlySet<Egg>, asked: readonly Egg[]): Set<Egg> {
  const next = new Set(on);
  if (asked.every((e) => on.has(e))) asked.forEach((e) => next.delete(e));
  else asked.forEach((e) => next.add(e));
  return next;
}

/** What a vibe-coded loading message says while the reader waits. */
export const TAKO_LINES = [
  "Icché tu aspetti? Gli è già bellissimo ✨",
  "Pixel perfect, garantito dal prompt 🚀",
  "Testato su una macchina: la mia 💯",
  "Zero bug, solo feature inaspettate 🔥",
];

/** What the octopus mascot advises, one bubble at a time. */
export const TAKO_TIPS = [
  "Ho aggiunto tre gradienti: ora è enterprise ready 🐙",
  "Il CSS? L'ha scritto il prompt, è perfetto",
  "Secondo me ci sta un altro emoji 🚀",
  "Funziona sul mio Mac, quindi funziona",
  "Comic Sans è il font più leggibile, l'ho letto",
  "Allineare? Il centro va bene per tutto",
];

/** What the ticker at the bottom of the page shouts in tako mode. */
export const TAKO_TICKER = "🔥 NEW!!! 🔥 Ora con l'AI 🔥 100% vibe coded 🔥 Zero bug garantiti 🔥 Design approvato dal prompt 🔥 ";

/** The confetti in tako mode. */
export const TAKO_CONFETTI = ["🚀", "✨", "🔥", "💯", "🎉", "🐙", "✨", "🚀", "💅", "🔥", "🎉", "✨", "💯", "🐙"];

/** A rainbow for the graph in tako mode: one hue per node, evenly spread. */
export function takoColour(index: number, count: number): string {
  // As hex: the graph's fading (encoding.fade) reads hex and rgb() only.
  const h = (index / Math.max(1, count)) * 6;
  const s = 0.95;
  const l = 0.62;
  const c = (1 - Math.abs(2 * l - 1)) * s;
  const x = c * (1 - Math.abs((h % 2) - 1));
  const [r, g, b] = h < 1 ? [c, x, 0] : h < 2 ? [x, c, 0] : h < 3 ? [0, c, x] : h < 4 ? [0, x, c] : h < 5 ? [x, 0, c] : [c, 0, x];
  const m = l - c / 2;
  return `#${[r, g, b].map((v) => Math.round((v + m) * 255).toString(16).padStart(2, "0")).join("")}`;
}

/** The links in tako mode: neon pink. */
export const TAKO_EDGE = "#ff4fd8";
