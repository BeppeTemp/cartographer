/**
 * The growth replay as a video (D677): the pure parts (which container, the
 * file name, where the text goes) and the recorder that composes each frame
 * -- the 3D canvas plus a drawn overlay -- into a 2D canvas and records that
 * canvas with MediaRecorder. Everything runs in the browser; the server is
 * not involved.
 */

export type Aspect = "16:9" | "9:16" | "1:1";

export const ASPECTS: Record<Aspect, { width: number; height: number; slug: string }> = {
  "16:9": { width: 1920, height: 1080, slug: "16x9" },
  "9:16": { width: 1080, height: 1920, slug: "9x16" },
  "1:1": { width: 1080, height: 1080, slug: "1x1" },
};

export const RECORD_FPS = 30;
/** Held on the full graph once the replay is over, then the end card. */
export const HOLD_MS = 1500;
export const END_CARD_MS = 2500;

/** MP4 first (H.264 in branded Chrome and Edge from 126, and Safari), then
 *  WebM. Chromium builds without a proprietary codec -- Playwright's, Firefox
 *  -- accept only WebM, so the fallback is real, not theoretical. */
const MIME_CANDIDATES: { mime: string; ext: "mp4" | "webm" }[] = [
  { mime: "video/mp4;codecs=avc1.640028", ext: "mp4" },
  { mime: "video/mp4;codecs=avc1", ext: "mp4" },
  { mime: "video/mp4", ext: "mp4" },
  { mime: "video/webm;codecs=vp9", ext: "webm" },
  { mime: "video/webm", ext: "webm" },
];

export function pickMimeType(isSupported: (mime: string) => boolean): { mime: string; ext: "mp4" | "webm" } {
  // A browser that claims nothing still records its own default: the blob
  // then carries whatever type the recorder reports.
  return MIME_CANDIDATES.find((c) => isSupported(c.mime)) ?? { mime: "video/webm", ext: "webm" };
}

export function exportFileName(kb: string, aspect: Aspect, ext: string): string {
  const slug =
    kb
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "kb";
  return `${slug}-growth-${ASPECTS[aspect].slug}.${ext}`;
}

export interface OverlayLayout {
  title: number;
  small: number;
  margin: number;
}

/** Font sizes and margin, proportional to the short side so the three
 *  aspects look alike. */
export function overlayLayout(width: number, height: number): OverlayLayout {
  const short = Math.min(width, height);
  return { title: Math.round(short * 0.045), small: Math.round(short * 0.03), margin: Math.round(short * 0.05) };
}

export interface Overlay {
  kb: string;
  /** The date being replayed, already formatted. */
  day: string;
  count: number;
}

export const countLabel = (n: number) => `${n.toLocaleString("en")} ${n === 1 ? "concept" : "concepts"}`;

export interface RecorderTheme {
  background: string;
  text: string;
  muted: string;
  font: string;
}

export interface RecorderOptions {
  width: number;
  height: number;
  mime: string;
  theme: RecorderTheme;
  /** The Cartographer mark for the end card, loaded before recording. */
  mark?: CanvasImageSource | null;
}

/**
 * Composes and records. The caller feeds it a frame per render
 * (`drawFrame`) from the scene's frame listener -- the one moment the WebGL
 * canvas can be copied, since the renderer keeps no drawing buffer -- and ends
 * with `stop()`, which resolves the recording.
 */
export class GrowthRecorder {
  private readonly canvas: HTMLCanvasElement;
  private readonly ctx: CanvasRenderingContext2D;
  private readonly recorder: MediaRecorder;
  private readonly chunks: Blob[] = [];
  private readonly done: Promise<Blob>;
  private failure: Error | null = null;
  /** Reports a recorder error that happens mid-recording. */
  onError: ((err: Error) => void) | null = null;

  constructor(private readonly opts: RecorderOptions) {
    this.canvas = document.createElement("canvas");
    this.canvas.width = opts.width;
    this.canvas.height = opts.height;
    const ctx = this.canvas.getContext("2d");
    if (!ctx) throw new Error("This browser cannot compose the video");
    this.ctx = ctx;
    const stream = this.canvas.captureStream(RECORD_FPS);
    this.recorder = new MediaRecorder(stream, {
      mimeType: opts.mime,
      videoBitsPerSecond: Math.round((8_000_000 * opts.width * opts.height) / (1920 * 1080)),
    });
    this.done = new Promise<Blob>((resolve, reject) => {
      this.recorder.ondataavailable = (e) => {
        if (e.data && e.data.size > 0) this.chunks.push(e.data);
      };
      this.recorder.onstop = () => {
        if (this.failure) reject(this.failure);
        else resolve(new Blob(this.chunks, { type: this.recorder.mimeType || opts.mime }));
      };
      this.recorder.onerror = (e) => {
        this.failure = new Error((e as Event & { error?: Error }).error?.message ?? "The recorder failed");
        this.onError?.(this.failure);
      };
    });
    // A rejection nobody awaited (a cancel) is not an unhandled one.
    this.done.catch(() => {});
    this.recorder.start();
  }

  /** Copies the 3D canvas, which must be the export's size, then the overlay. */
  drawFrame(source: CanvasImageSource, overlay: Overlay): void {
    const { width, height, theme } = this.opts;
    const ctx = this.ctx;
    ctx.fillStyle = theme.background;
    ctx.fillRect(0, 0, width, height);
    ctx.drawImage(source, 0, 0, width, height);
    const lay = overlayLayout(width, height);
    // A soft veil under the text, so it reads over a dense graph.
    const veil = ctx.createLinearGradient(0, 0, 0, lay.margin * 2 + lay.title * 2);
    veil.addColorStop(0, withAlpha(theme.background, 0.8));
    veil.addColorStop(1, withAlpha(theme.background, 0));
    ctx.fillStyle = veil;
    ctx.fillRect(0, 0, width, lay.margin * 2 + lay.title * 2);

    ctx.textBaseline = "top";
    ctx.fillStyle = theme.text;
    ctx.font = `600 ${lay.title}px ${theme.font}`;
    ctx.textAlign = "left";
    const count = countLabel(overlay.count);
    ctx.font = `500 ${lay.small}px ${theme.font}`;
    const countWidth = ctx.measureText(count).width;
    ctx.font = `600 ${lay.title}px ${theme.font}`;
    // The name never runs into the count.
    const titleRoom = width - lay.margin * 3 - countWidth;
    ctx.fillText(fit(ctx, overlay.kb, titleRoom), lay.margin, lay.margin);
    ctx.fillStyle = theme.muted;
    ctx.font = `500 ${lay.small}px ${theme.font}`;
    ctx.fillText(overlay.day, lay.margin, lay.margin + lay.title * 1.4);
    ctx.textAlign = "right";
    ctx.fillStyle = theme.text;
    ctx.fillText(count, width - lay.margin, lay.margin + (lay.title - lay.small) / 2);
  }

  /** The closing card: the mark, the KB name and the final count. */
  drawEndCard(kb: string, count: number): void {
    const { width, height, theme, mark } = this.opts;
    const ctx = this.ctx;
    const lay = overlayLayout(width, height);
    ctx.fillStyle = theme.background;
    ctx.fillRect(0, 0, width, height);
    const markSize = Math.round(Math.min(width, height) * 0.16);
    const cy = height / 2;
    if (mark) ctx.drawImage(mark, (width - markSize) / 2, cy - markSize * 1.1, markSize, markSize);
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    ctx.fillStyle = theme.text;
    ctx.font = `600 ${Math.round(lay.title * 1.4)}px ${theme.font}`;
    ctx.fillText(fit(ctx, kb, width - lay.margin * 2), width / 2, cy + markSize * 0.2);
    ctx.fillStyle = theme.muted;
    ctx.font = `500 ${Math.round(lay.small * 1.3)}px ${theme.font}`;
    ctx.fillText(countLabel(count), width / 2, cy + markSize * 0.2 + lay.title * 2);
  }

  pause(): void {
    if (this.recorder.state === "recording") this.recorder.pause();
  }

  resume(): void {
    if (this.recorder.state === "paused") this.recorder.resume();
  }

  stop(): Promise<Blob> {
    if (this.recorder.state !== "inactive") this.recorder.stop();
    return this.done;
  }

  /** Abandons the recording: nothing is kept. */
  cancel(): void {
    this.failure = new Error("cancelled");
    if (this.recorder.state !== "inactive") this.recorder.stop();
  }
}

/** `text` shortened with an ellipsis until it fits `room` pixels. */
function fit(ctx: CanvasRenderingContext2D, text: string, room: number): string {
  if (ctx.measureText(text).width <= room) return text;
  let out = text;
  while (out.length > 1 && ctx.measureText(`${out}…`).width > room) out = out.slice(0, -1);
  return `${out}…`;
}

/** `colour` (#rgb, #rrggbb or any CSS colour) at `alpha`. */
function withAlpha(colour: string, alpha: number): string {
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(colour.trim());
  if (!hex) return colour;
  const h = hex[1]!.length === 3 ? [...hex[1]!].map((c) => c + c).join("") : hex[1]!;
  const n = parseInt(h, 16);
  return `rgba(${n >> 16},${(n >> 8) & 255},${n & 255},${alpha})`;
}

/** Whether a `#rrggbb` colour is dark: picks the ivory or the pine mark. */
export function isDark(colour: string): boolean {
  const hex = /^#([0-9a-f]{6})$/i.exec(colour.trim());
  if (!hex) return true;
  const n = parseInt(hex[1]!, 16);
  return (0.2126 * (n >> 16) + 0.7152 * ((n >> 8) & 255) + 0.0722 * (n & 255)) / 255 < 0.5;
}

/** Loads the brand mark for the end card; null if it cannot be had (the card
 *  then goes without it rather than failing the export). */
export function loadMark(url: string): Promise<HTMLImageElement | null> {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => resolve(null);
    img.src = url;
  });
}

/** Hands the recording to the browser's download, then frees the URL. */
export function downloadBlob(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
