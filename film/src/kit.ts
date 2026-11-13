// The page kit every film shares: the 1920x1080 stage, the easing and DOM helpers, and defineFilm(), which installs
// the `window.film` record.mjs drives and runs the real-time preview when the page is opened without `?record`.
// A film is a pure function of time: its `frame(n)` sets every element from n / fps and draws; it never reads a clock.

export const W = 1920;
export const H = 1080;

/** The page record.mjs renders sets `?record`: no preview loop, the recorder asks for each frame itself. */
const RECORD = new URLSearchParams(location.search).has("record");

// --- easing ---------------------------------------------------------------
export const clamp01 = (x: number) => Math.min(1, Math.max(0, x));
export const prog = (t: number, t0: number, dur: number) => clamp01((t - t0) / dur);
export const easeOut = (p: number) => 1 - Math.pow(1 - p, 3);
export const easeInOut = (p: number) => (p < 0.5 ? 4 * p * p * p : 1 - Math.pow(-2 * p + 2, 3) / 2);
export const lerp = (a: number, b: number, p: number) => a + (b - a) * p;

// --- DOM ------------------------------------------------------------------
export const stage = document.getElementById("stage")!;
export function mk<K extends keyof HTMLElementTagNameMap>(tag: K, cls = "", html = "", style = ""): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag);
  el.className = cls;
  el.innerHTML = html;
  el.style.cssText = style;
  stage.appendChild(el);
  return el;
}
/** Opacity + a rise (px, 0 = none), composed with a base transform. */
export function vis(el: HTMLElement, opacity: number, rise = 0, extra = "") {
  const o = clamp01(opacity);
  el.style.opacity = String(o);
  el.style.visibility = o <= 0.001 ? "hidden" : "visible";
  el.style.transform = `${extra} translateY(${rise * (1 - clamp01(opacity))}px)`.trim();
}
/** Reveal: easeOutCubic over 600 ms, 16 px rise. */
export const reveal = (t: number, t0: number, dur = 0.6) => easeOut(prog(t, t0, dur));
export const fade = (t: number, t0: number, dur = 0.4) => 1 - prog(t, t0, dur);

// --- the film -------------------------------------------------------------
export interface FilmSpec {
  fps: number;
  /** Seconds of film; the page has Math.round(duration * fps) frames. */
  duration: number;
  /** Draws frame n. Called in order from 0, once each, after the fonts are loaded; a rejection fails the render. */
  frame: (n: number) => void | Promise<void>;
}
export interface Film {
  fps: number;
  frames: number;
  frame: (n: number) => Promise<void>;
  rendered: () => number;
}

/**
 * Installs `window.film` {fps, frames, frame, rendered}, which film/record.mjs reads. `frame` refuses a frame out of
 * order (the physics of a film is stateful, so a skipped or repeated frame would render something else), waits for the
 * fonts before frame 0, then hands the frame to the film. Without `?record` it also scales the stage to the window and
 * plays the film in real time, looping (`R` restarts).
 */
export function defineFilm(spec: FilmSpec): Film {
  let last = -1;
  const film: Film = {
    fps: spec.fps,
    frames: Math.round(spec.duration * spec.fps),
    async frame(n) {
      if (n !== last + 1) throw new Error(`frames must be rendered in order: asked ${n} after ${last}`);
      last = n;
      if (n === 0) await document.fonts.ready;
      await spec.frame(n);
    },
    rendered: () => last + 1,
  };
  (window as unknown as { film: Film }).film = film;

  if (!RECORD) {
    const fit = () => { stage.style.transform = `scale(${Math.min(innerWidth / W, innerHeight / H)})`; };
    fit();
    addEventListener("resize", fit);
    addEventListener("keydown", (e) => { if (e.key === "r" || e.key === "R") location.reload(); });
    const t0 = performance.now();
    const tick = async () => {
      const want = Math.min(film.frames, Math.floor(((performance.now() - t0) / 1000) * film.fps));
      for (let k = 0; film.rendered() < want && k < 3; k++) await film.frame(film.rendered());
      if (film.rendered() >= film.frames) setTimeout(() => location.reload(), 1500);
      else requestAnimationFrame(tick);
    };
    void document.fonts.ready.then(() => requestAnimationFrame(tick));
  }
  return film;
}
