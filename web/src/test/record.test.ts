import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ASPECTS, GrowthRecorder, exportFileName, isDark, overlayLayout, pickMimeType } from "../lib/graph3d/record";

describe("pickMimeType", () => {
  it("prefers H.264 MP4, in the order of the list", () => {
    expect(pickMimeType(() => true)).toEqual({ mime: "video/mp4;codecs=avc1.640028", ext: "mp4" });
    expect(pickMimeType((m) => m === "video/mp4")).toEqual({ mime: "video/mp4", ext: "mp4" });
  });
  it("falls back to WebM where no MP4 is recorded (Firefox, Chromium without H.264)", () => {
    expect(pickMimeType((m) => m.startsWith("video/webm"))).toEqual({ mime: "video/webm;codecs=vp9", ext: "webm" });
    expect(pickMimeType((m) => m === "video/webm")).toEqual({ mime: "video/webm", ext: "webm" });
  });
  it("still answers when the browser claims nothing", () => {
    expect(pickMimeType(() => false)).toEqual({ mime: "video/webm", ext: "webm" });
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

describe("GrowthRecorder", () => {
  let created: { mimeType: string; options: MediaRecorderOptions } | null;
  let drawn: string[];
  const originalMR = globalThis.MediaRecorder;

  beforeEach(() => {
    created = null;
    drawn = [];
    class FakeRecorder {
      state: RecordingState = "inactive";
      mimeType: string;
      ondataavailable: ((e: { data: Blob }) => void) | null = null;
      onstop: (() => void) | null = null;
      onerror: ((e: Event) => void) | null = null;
      constructor(_stream: MediaStream, options: MediaRecorderOptions) {
        this.mimeType = options.mimeType ?? "";
        created = { mimeType: this.mimeType, options };
      }
      start() {
        this.state = "recording";
      }
      pause() {
        this.state = "paused";
      }
      resume() {
        this.state = "recording";
      }
      stop() {
        this.state = "inactive";
        this.ondataavailable?.({ data: new Blob(["frame"]) });
        this.onstop?.();
      }
    }
    globalThis.MediaRecorder = FakeRecorder as unknown as typeof MediaRecorder;
    const ctx = new Proxy(
      {},
      {
        get: (_t, prop) => {
          if (prop === "createLinearGradient") return () => ({ addColorStop() {} });
          if (prop === "measureText") return (t: string) => ({ width: t.length * 10 });
          return (...args: unknown[]) => {
            if (prop === "fillText") drawn.push(String(args[0]));
          };
        },
        set: () => true,
      },
    );
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(ctx as unknown as CanvasRenderingContext2D);
    HTMLCanvasElement.prototype.captureStream = vi.fn(() => ({}) as MediaStream);
  });
  afterEach(() => {
    globalThis.MediaRecorder = originalMR;
    vi.restoreAllMocks();
  });

  const theme = { background: "#131416", text: "#eeede5", muted: "#c4c5bd", font: "sans-serif" };

  it("records with the chosen mime and resolves a Blob of that type", async () => {
    const r = new GrowthRecorder({ width: 1080, height: 1080, mime: "video/webm", theme });
    expect(created?.mimeType).toBe("video/webm");
    expect(HTMLCanvasElement.prototype.captureStream).toHaveBeenCalledWith(30);
    r.drawFrame(document.createElement("canvas"), { kb: "kb-a", day: "1 Jan 2026", count: 3 });
    const blob = await r.stop();
    expect(blob.type).toBe("video/webm");
    expect(blob.size).toBeGreaterThan(0);
  });

  it("scales the bitrate with the frame", () => {
    new GrowthRecorder({ width: 1920, height: 1080, mime: "video/webm", theme });
    expect(created?.options.videoBitsPerSecond).toBe(8_000_000);
  });

  it("writes the KB, the day and the count over the frame, and the final count on the card", () => {
    const r = new GrowthRecorder({ width: 1080, height: 1920, mime: "video/webm", theme });
    r.drawFrame(document.createElement("canvas"), { kb: "kb-a", day: "1 Jan 2026", count: 1 });
    expect(drawn).toEqual(["kb-a", "1 Jan 2026", "1 concept"]);
    drawn.length = 0;
    r.drawEndCard("kb-a", 42);
    expect(drawn).toEqual(["kb-a", "42 concepts"]);
  });

  it("keeps nothing when cancelled", async () => {
    const r = new GrowthRecorder({ width: 1080, height: 1080, mime: "video/webm", theme });
    r.cancel();
    await expect(r.stop()).rejects.toThrow("cancelled");
  });
});
