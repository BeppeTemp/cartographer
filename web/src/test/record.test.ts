import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import {
  ASPECTS,
  GrowthEncoder,
  KEYFRAME_EVERY,
  bitrateFor,
  exportFileName,
  isDark,
  overlayLayout,
  pickCodec,
} from "../lib/graph3d/record";

// mediabunny is the muxer: here a recorder of what reaches it.
const muxed: { chunks: unknown[]; formats: string[]; cancelled: boolean; finalized: boolean } = vi.hoisted(() => ({
  chunks: [],
  formats: [],
  cancelled: false,
  finalized: false,
}));
vi.mock("mediabunny", () => ({
  BufferTarget: class {
    buffer = new ArrayBuffer(8);
  },
  Mp4OutputFormat: class {
    constructor() {
      muxed.formats.push("mp4");
    }
  },
  WebMOutputFormat: class {
    constructor() {
      muxed.formats.push("webm");
    }
  },
  Output: class {
    addVideoTrack() {}
    async start() {}
    async finalize() {
      muxed.finalized = true;
    }
    async cancel() {
      muxed.cancelled = true;
    }
  },
  EncodedVideoPacketSource: class {
    async add(packet: unknown) {
      muxed.chunks.push(packet);
    }
  },
  EncodedPacket: { fromEncodedChunk: (c: unknown) => c },
}));

describe("pickCodec", () => {
  const only = (prefix: string) => async (c: VideoEncoderConfig) => ({ supported: c.codec.startsWith(prefix) });

  it("prefers H.264 High 4.2 in an MP4, variable rate, quality latency", async () => {
    const asked: string[] = [];
    const choice = await pickCodec(async (c) => (asked.push(c.codec), { supported: true }));
    expect(asked).toEqual(["avc1.64002A"]);
    expect(choice).toMatchObject({ codec: "avc", ext: "mp4" });
    expect(choice!.config).toMatchObject({
      codec: "avc1.64002A",
      width: 1920,
      height: 1080,
      framerate: 60,
      bitrate: 8_000_000,
      bitrateMode: "variable",
      latencyMode: "quality",
    });
  });
  it("falls back to VP9 in a WebM where H.264 is not encoded (Firefox, Chromium without it)", async () => {
    expect(await pickCodec(only("vp09"))).toMatchObject({ codec: "vp9", ext: "webm" });
  });
  it("is null where neither is", async () => {
    expect(await pickCodec(async () => ({ supported: false }))).toBeNull();
  });
  it("treats a codec string the browser rejects outright as unsupported", async () => {
    const choice = await pickCodec(async (c) => {
      if (c.codec.startsWith("avc")) throw new TypeError("bad codec");
      return { supported: true };
    });
    expect(choice?.ext).toBe("webm");
  });
  it("scales the bitrate with the frame", async () => {
    expect(bitrateFor(1920, 1080)).toBe(8_000_000);
    expect(bitrateFor(1080, 1080)).toBe(4_500_000);
    expect((await pickCodec(only("avc"), 1080, 1920))!.config.bitrate).toBe(8_000_000);
  });
});

describe("exportFileName", () => {
  it("slugs the KB name and names the aspect", () => {
    expect(exportFileName("Work KB / Notes", "9:16", "mp4")).toBe("work-kb-notes-growth-9x16.mp4");
    expect(exportFileName("kb-a", "16:9", "webm")).toBe("kb-a-growth-16x9.webm");
    expect(exportFileName("kb-a", "1:1", "webm")).toBe("kb-a-growth-1x1.webm");
  });
  it("never produces an empty name", () => {
    expect(exportFileName("???", "1:1", "mp4")).toBe("kb-growth-1x1.mp4");
  });
});

describe("overlayLayout", () => {
  it.each(Object.entries(ASPECTS))("scales with the short side at %s and fits the width", (_, { width, height }) => {
    const l = overlayLayout(width, height);
    const short = Math.min(width, height);
    expect(l.title).toBe(Math.round(short * 0.045));
    expect(l.small).toBe(Math.round(short * 0.03));
    expect(l.margin).toBe(Math.round(short * 0.05));
    // A 24-character KB name at ~0.6 em per glyph, with room for the margins.
    expect(24 * 0.6 * l.title + 2 * l.margin).toBeLessThan(width);
  });
  it("is the same for the same short side", () => {
    expect(overlayLayout(1080, 1920)).toEqual(overlayLayout(1920, 1080));
  });
});

describe("isDark", () => {
  it("tells the dark surface from the light one", () => {
    expect(isDark("#131416")).toBe(true);
    expect(isDark("#fcfaf6")).toBe(false);
  });
});

describe("GrowthEncoder", () => {
  const log: string[] = [];
  const encoded: { timestamp: number; keyFrame: boolean | undefined }[] = [];
  let queue = 0;
  let encoderInstance: FakeEncoder;
  const original = { VideoEncoder: globalThis.VideoEncoder, VideoFrame: globalThis.VideoFrame };

  class FakeEncoder extends EventTarget {
    static isConfigSupported = async (c: VideoEncoderConfig) => ({ supported: c.codec.startsWith("avc") });
    state = "configured";
    config: VideoEncoderConfig | null = null;
    closed = 0;
    constructor(private readonly init: { output(chunk: unknown, meta?: unknown): void }) {
      super();
      encoderInstance = this;
    }
    get encodeQueueSize() {
      return queue;
    }
    configure(c: VideoEncoderConfig) {
      this.config = c;
    }
    encode(frame: { timestamp: number }, opts?: { keyFrame?: boolean }) {
      log.push(`encode:${frame.timestamp}`);
      encoded.push({ timestamp: frame.timestamp, keyFrame: opts?.keyFrame });
      this.init.output({ timestamp: frame.timestamp }, {});
    }
    async flush() {}
    close() {
      this.state = "closed";
      this.closed++;
    }
  }
  class FakeFrame {
    constructor(
      _source: unknown,
      readonly init: { timestamp: number; duration: number },
    ) {}
    get timestamp() {
      return this.init.timestamp;
    }
    close() {}
  }
  const theme = { background: "#131416", text: "#eeede5", muted: "#c4c5bd", font: "sans-serif" };
  const create = (width = 1080, height = 1080) => GrowthEncoder.create({ width, height, theme });

  beforeEach(() => {
    log.length = 0;
    encoded.length = 0;
    queue = 0;
    muxed.chunks.length = 0;
    muxed.formats.length = 0;
    muxed.cancelled = false;
    muxed.finalized = false;
    globalThis.VideoEncoder = FakeEncoder as unknown as typeof VideoEncoder;
    globalThis.VideoFrame = FakeFrame as unknown as typeof VideoFrame;
    const ctx = new Proxy(
      {},
      {
        get: (_t, prop) => {
          if (prop === "createLinearGradient") return () => ({ addColorStop() {} });
          if (prop === "measureText") return (t: string) => ({ width: t.length * 10 });
          return (...args: unknown[]) => {
            if (prop === "fillText") log.push(`text:${args[0]}`);
          };
        },
        set: () => true,
      },
    );
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(ctx as unknown as CanvasRenderingContext2D);
  });
  afterEach(() => {
    globalThis.VideoEncoder = original.VideoEncoder;
    globalThis.VideoFrame = original.VideoFrame;
    vi.restoreAllMocks();
  });

  it("configures the encoder for the frame and muxes into an MP4 when H.264 is encoded", async () => {
    const enc = (await create(1920, 1080))!;
    expect(enc.ext).toBe("mp4");
    expect(encoderInstance.config).toMatchObject({ codec: "avc1.64002A", width: 1920, height: 1080, framerate: 60 });
    expect(muxed.formats).toEqual(["mp4"]);
    const blob = await (async () => {
      await enc.drawOpening("kb-a");
      return enc.finish();
    })();
    expect(blob.type).toBe("video/mp4");
    expect(muxed.finalized).toBe(true);
  });

  it("is null where the browser encodes neither codec", async () => {
    FakeEncoder.isConfigSupported = async () => ({ supported: false });
    expect(await create()).toBeNull();
    FakeEncoder.isConfigSupported = async (c) => ({ supported: c.codec.startsWith("avc") });
  });

  it("stamps frame n at exactly n * 1e6/60 us, with a keyframe every two seconds", async () => {
    const enc = (await create())!;
    const overlay = { kb: "kb-a", day: "1 Jan 2026", count: 1 };
    await enc.drawOpening("kb-a");
    for (let n = 1; n <= KEYFRAME_EVERY; n++) await enc.drawFrame(document.createElement("canvas"), overlay);
    await enc.drawEndCard("kb-a", 1);
    expect(encoded).toHaveLength(KEYFRAME_EVERY + 2);
    encoded.forEach((e, n) => expect(e.timestamp).toBe(Math.round((n * 1e6) / 60)));
    expect(encoded.map((e, n) => (e.keyFrame ? n : -1)).filter((n) => n >= 0)).toEqual([0, KEYFRAME_EVERY]);
    // every one reached the muxer, in order
    expect(muxed.chunks).toHaveLength(encoded.length);
  });

  it("encodes the opening card first: the mark and the name, before any overlay", async () => {
    const enc = (await create())!;
    await enc.drawOpening("kb-a");
    await enc.drawFrame(document.createElement("canvas"), { kb: "kb-a", day: "1 Jan 2026", count: 3 });
    expect(log.slice(0, 2)).toEqual(["text:kb-a", "encode:0"]);
    expect(log.indexOf("text:1 Jan 2026")).toBeGreaterThan(log.indexOf("encode:0"));
  });

  it("writes the KB, the day and the count over a frame, and the final count on the end card", async () => {
    const enc = (await create(1080, 1920))!;
    await enc.drawFrame(document.createElement("canvas"), { kb: "kb-a", day: "1 Jan 2026", count: 1 });
    expect(log.filter((l) => l.startsWith("text:"))).toEqual(["text:kb-a", "text:1 Jan 2026", "text:1 concept"]);
    log.length = 0;
    await enc.drawEndCard("kb-a", 42);
    expect(log.filter((l) => l.startsWith("text:"))).toEqual(["text:kb-a", "text:42 concepts"]);
  });

  it("waits for the encoder's queue to drain before the next frame", async () => {
    const enc = (await create())!;
    queue = 8;
    let resolved = false;
    const drawn = enc.drawOpening("kb-a").then(() => (resolved = true));
    await new Promise((r) => setTimeout(r, 20));
    expect(resolved).toBe(false);
    queue = 0;
    encoderInstance.dispatchEvent(new Event("dequeue"));
    await drawn;
    expect(resolved).toBe(true);
  });

  it("encodes nothing more once cancelled, keeps nothing and tells the muxer", async () => {
    const enc = (await create())!;
    await enc.drawOpening("kb-a");
    enc.cancel();
    await enc.drawOpening("kb-a");
    await enc.drawFrame(document.createElement("canvas"), { kb: "kb-a", day: "", count: 1 });
    expect(encoded).toHaveLength(1);
    expect(encoderInstance.state).toBe("closed");
    expect(muxed.cancelled).toBe(true);
    await expect(enc.finish()).rejects.toThrow("cancelled");
  });
});
