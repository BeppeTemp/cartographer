/**
 * The growth replay as a video (D691, superseding D677's recorder): the pure
 * parts (which codec, the file name, where the text goes) and the encoder that
 * composes each frame -- the offline 3D canvas plus a drawn overlay -- into a
 * 2D canvas and encodes it with WebCodecs, 60 frames a second, into an MP4
 * (H.264) or a WebM (VP9) muxed by mediabunny. Everything runs in the
 * browser; the server is not involved.
 */

export type Aspect = "16:9" | "9:16" | "1:1";

export const ASPECTS: Record<Aspect, { width: number; height: number; slug: string }> = {
  "16:9": { width: 1920, height: 1080, slug: "16x9" },
  "9:16": { width: 1080, height: 1920, slug: "9x16" },
  "1:1": { width: 1080, height: 1080, slug: "1x1" },
};

export const FPS = 60;
export const FRAME_MS = 1000 / FPS;
/** The opening card (frame 0 is its first frame, so it is the thumbnail), the
 *  hold on the full graph once the replay is over, and the end card. */
export const OPENING_MS = 1000;
export const HOLD_MS = 1500;
export const END_CARD_MS = 2500;
/** A keyframe every two seconds. */
export const KEYFRAME_EVERY = 2 * FPS;
/** How many frames may wait in the encoder before the renderer waits for it. */
const MAX_QUEUE = 8;

export type Codec = "avc" | "vp9";
export interface CodecChoice {
  config: VideoEncoderConfig;
  codec: Codec;
  ext: "mp4" | "webm";
}

/** 8 Mbit/s at 1920x1080, scaled by pixel count. */
export const bitrateFor = (width: number, height: number) => Math.round((8_000_000 * width * height) / (1920 * 1080));

/**
 * H.264 High level 4.2 into an MP4 where the browser encodes it (branded
 * Chrome and Edge, Safari), else VP9 into a WebM (Chromium builds without a
 * proprietary codec -- Playwright's -- and Firefox), else null. Asks the
 * browser per codec, which is why it is async.
 */
export async function pickCodec(
  isConfigSupported: (config: VideoEncoderConfig) => Promise<{ supported?: boolean }>,
  width = 1920,
  height = 1080,
): Promise<CodecChoice | null> {
  const common = {
    width,
    height,
    framerate: FPS,
    bitrate: bitrateFor(width, height),
    bitrateMode: "variable",
    latencyMode: "quality",
  } as const;
  const candidates: CodecChoice[] = [
    { codec: "avc", ext: "mp4", config: { ...common, codec: "avc1.64002A", avc: { format: "avc" } } },
    { codec: "vp9", ext: "webm", config: { ...common, codec: "vp09.00.41.08" } },
  ];
  for (const c of candidates) {
    try {
      if ((await isConfigSupported(c.config)).supported) return c;
    } catch {
      /* a codec string this browser rejects outright is just unsupported */
    }
  }
  return null;
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

export interface EncoderOptions {
  width: number;
  height: number;
  theme: RecorderTheme;
  /** The Cartographer mark for the cards, loaded before encoding. */
  mark?: CanvasImageSource | null;
}

/** What GrowthEncoder needs from the muxer (mediabunny, loaded on demand). */
interface Muxer {
  add(chunk: EncodedVideoChunk, meta?: EncodedVideoChunkMetadata): Promise<void>;
  finish(): Promise<Blob>;
  cancel(): Promise<void>;
}

async function createMuxer(choice: CodecChoice): Promise<Muxer> {
  const mb = await import("mediabunny");
  const target = new mb.BufferTarget();
  const mp4 = choice.ext === "mp4";
  const output = new mb.Output({ format: mp4 ? new mb.Mp4OutputFormat({ fastStart: "in-memory" }) : new mb.WebMOutputFormat(), target });
  const source = new mb.EncodedVideoPacketSource(choice.codec);
  output.addVideoTrack(source, { frameRate: FPS });
  await output.start();
  const type = mp4 ? "video/mp4" : "video/webm";
  return {
    add: (chunk, meta) => source.add(mb.EncodedPacket.fromEncodedChunk(chunk), meta),
    async finish() {
      await output.finalize();
      return new Blob([target.buffer!], { type });
    },
    cancel: () => output.cancel(),
  };
}

/** Lets the event loop run without a timer: timers are throttled to a crawl
 *  in a hidden tab, a message is not. */
const yieldTask = () =>
  new Promise<void>((resolve) => {
    const { port1, port2 } = new MessageChannel();
    port1.onmessage = () => {
      port1.close();
      resolve();
    };
    port2.postMessage(null);
    port2.close();
  });

/**
 * Composes and encodes. Each `draw*` call composes one frame on a 2D canvas
 * and encodes it with the next timestamp (frame n at n * 1e6 / 60 us), a
 * keyframe every KEYFRAME_EVERY frames; frame 0 is the opening card. It waits
 * for the encoder's queue to drain, so the renderer can run as fast as it can
 * without outrunning memory. `finish()` resolves the file.
 */
export class GrowthEncoder {
  private readonly canvas: HTMLCanvasElement;
  private readonly ctx: CanvasRenderingContext2D;
  private frames = 0;
  private cancelled = false;
  private failure: Error | null = null;
  private pending: Promise<void> = Promise.resolve();

  private constructor(
    private readonly opts: EncoderOptions,
    private readonly encoder: VideoEncoder,
    private readonly muxer: Muxer,
    readonly ext: "mp4" | "webm",
  ) {
    this.canvas = document.createElement("canvas");
    this.canvas.width = opts.width;
    this.canvas.height = opts.height;
    const ctx = this.canvas.getContext("2d");
    if (!ctx) throw new Error("This browser cannot compose the video");
    this.ctx = ctx;
  }

  /** Null where the browser can encode neither H.264 nor VP9. */
  static async create(opts: EncoderOptions): Promise<GrowthEncoder | null> {
    const choice = await pickCodec((c) => VideoEncoder.isConfigSupported(c), opts.width, opts.height);
    if (!choice) return null;
    const muxer = await createMuxer(choice);
    let self: GrowthEncoder | null = null;
    const encoder = new VideoEncoder({
      output: (chunk, meta) => {
        // The muxer is asynchronous; chunks are added in order.
        const done = self!.pending.then(() => muxer.add(chunk, meta));
        self!.pending = done.catch((err) => self!.fail(err));
      },
      error: (err) => self!.fail(err),
    });
    encoder.configure({ ...choice.config, width: opts.width, height: opts.height });
    self = new GrowthEncoder(opts, encoder, muxer, choice.ext);
    return self;
  }

  private fail(err: unknown): void {
    this.failure ??= err instanceof Error ? err : new Error(String(err));
  }

  /** The opening card: the mark and the KB name. */
  async drawOpening(kb: string): Promise<void> {
    this.card(kb, null);
    await this.push();
  }

  /** Copies the 3D canvas (any size: it is downsampled to the frame's), then
   *  draws the overlay at the frame's own size, so the text stays crisp. */
  async drawFrame(source: CanvasImageSource, overlay: Overlay): Promise<void> {
    const { width, height, theme } = this.opts;
    const ctx = this.ctx;
    ctx.fillStyle = theme.background;
    ctx.fillRect(0, 0, width, height);
    ctx.imageSmoothingEnabled = true;
    ctx.imageSmoothingQuality = "high";
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
    await this.push();
  }

  /** The closing card: the mark, the KB name and the final count. */
  async drawEndCard(kb: string, count: number): Promise<void> {
    this.card(kb, count);
    await this.push();
  }

  private card(kb: string, count: number | null): void {
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
    if (count === null) return;
    ctx.fillStyle = theme.muted;
    ctx.font = `500 ${Math.round(lay.small * 1.3)}px ${theme.font}`;
    ctx.fillText(countLabel(count), width / 2, cy + markSize * 0.2 + lay.title * 2);
  }

  /** Encodes what is on the canvas as the next frame, then waits for room. */
  private async push(): Promise<void> {
    if (this.cancelled) return;
    if (this.failure) throw this.failure;
    const n = this.frames++;
    const frame = new VideoFrame(this.canvas, {
      timestamp: Math.round((n * 1e6) / FPS),
      duration: Math.round(1e6 / FPS),
    });
    try {
      this.encoder.encode(frame, { keyFrame: n % KEYFRAME_EVERY === 0 });
    } finally {
      frame.close();
    }
    // Backpressure by events and messages, never setTimeout: a hidden tab
    // throttles timers (to once a second or worse) but not these.
    while (!this.cancelled && this.encoder.encodeQueueSize >= MAX_QUEUE) {
      await new Promise<void>((resolve) => this.encoder.addEventListener("dequeue", () => resolve(), { once: true }));
    }
    await yieldTask();
    if (this.failure) throw this.failure;
  }

  /** Flushes the encoder and resolves the finished file. */
  async finish(): Promise<Blob> {
    if (this.cancelled) throw new Error("cancelled");
    await this.encoder.flush();
    this.encoder.close();
    await this.pending;
    if (this.failure) throw this.failure;
    return this.muxer.finish();
  }

  /** Abandons the video: nothing more is encoded and nothing is kept. */
  cancel(): void {
    if (this.cancelled) return;
    this.cancelled = true;
    try {
      if (this.encoder.state !== "closed") this.encoder.close();
    } catch {
      /* already closed */
    }
    void this.muxer.cancel().catch(() => {});
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

/** Loads the brand mark for the cards; null if it cannot be had (the card
 *  then goes without it rather than failing the export). */
export function loadMark(url: string): Promise<HTMLImageElement | null> {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => resolve(null);
    img.src = url;
  });
}

/** Hands the video to the browser's download, then frees the URL. */
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
