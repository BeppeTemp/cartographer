import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GraphView } from "../components/GraphView";
import { snapshotCommunities } from "../lib/communities";
import { graph } from "./fixtures";
import { sceneStub } from "./sceneStub";

// The Image of the cards never loads in jsdom.
vi.mock("../lib/graph3d/record", async (importActual) => ({
  ...(await importActual<typeof import("../lib/graph3d/record")>()),
  loadMark: async () => null,
}));
// The muxer is not under test here (record.test.ts holds it).
vi.mock("mediabunny", () => ({
  BufferTarget: class {
    buffer = new ArrayBuffer(8);
  },
  Mp4OutputFormat: class {},
  WebMOutputFormat: class {},
  Output: class {
    addVideoTrack() {}
    async start() {}
    async finalize() {}
    async cancel() {}
  },
  EncodedVideoPacketSource: class {
    async add() {}
  },
  EncodedPacket: { fromEncodedChunk: (c: unknown) => c },
}));

/** The export flow (D691) with the scene and the encoder stubbed. */
const births = { "infra/a": "2026-01-01T00:00:00Z", "infra/b": "2026-02-01T00:00:00Z", "notes/c": "2026-03-01T00:00:00Z" };
const original = { VideoEncoder: globalThis.VideoEncoder, VideoFrame: globalThis.VideoFrame };

let encoded = 0;
let failEncode = false;
function installEncoder(codecs: "both" | "vp9") {
  encoded = 0;
  class FakeEncoder extends EventTarget {
    static isConfigSupported = async (c: VideoEncoderConfig) => ({
      supported: codecs === "both" || c.codec.startsWith("vp09"),
    });
    state = "configured";
    encodeQueueSize = 0;
    constructor(private readonly init: { output(chunk: unknown, meta?: unknown): void }) {
      super();
    }
    configure() {}
    encode() {
      if (failEncode) throw new Error("boom");
      encoded++;
      this.init.output({}, {});
    }
    async flush() {}
    close() {
      this.state = "closed";
    }
  }
  globalThis.VideoEncoder = FakeEncoder as unknown as typeof VideoEncoder;
  globalThis.VideoFrame = class {
    close() {}
  } as unknown as typeof VideoFrame;
}

function props(loadBirths?: () => Promise<Record<string, string>>, layoutKey = "kb-a") {
  return {
    snapshot: graph,
    layoutKey,
    loadBirths,
    kbName: "kb-a",
    communities: snapshotCommunities(graph),
    colorBy: "community" as const,
    selected: null,
    hiddenIds: new Set<string>(),
    themeKey: "dark",
    live: false,
    occludedRight: 0,
    onSelect: () => {},
  };
}
const view = (loadBirths?: () => Promise<Record<string, string>>) => render(<GraphView {...props(loadBirths)} />);

async function openReplay() {
  await userEvent.click(screen.getByRole("button", { name: "Replay growth" }));
  await screen.findByRole("group", { name: "Growth replay" });
}
async function startExport(aspect: RegExp = /1:1/) {
  await userEvent.click(screen.getByRole("button", { name: "Export video" }));
  await userEvent.click(screen.getByRole("radio", { name: aspect }));
  await userEvent.click(screen.getByRole("button", { name: "Export" }));
}

describe("Export video", () => {
  beforeEach(() => {
    sceneStub.reset();
    failEncode = false;
    installEncoder("both");
    const ctx = new Proxy({}, { get: () => () => ({ addColorStop() {}, width: 0 }), set: () => true });
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(ctx as unknown as CanvasRenderingContext2D);
  });
  afterEach(() => {
    globalThis.VideoEncoder = original.VideoEncoder;
    globalThis.VideoFrame = original.VideoFrame;
    vi.restoreAllMocks();
  });

  it("is a button of the replay's control bar, not of the camera controls", async () => {
    view(async () => births);
    expect(within(screen.getByRole("group", { name: "Graph camera" })).queryByRole("button", { name: "Export video" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Export video" })).toBeNull();
    await openReplay();
    const bar = screen.getByRole("group", { name: "Growth replay" });
    expect(within(bar).getByRole("button", { name: "Export video" })).toBeInTheDocument();
    expect(within(screen.getByRole("group", { name: "Graph camera" })).queryByRole("button", { name: "Export video" })).toBeNull();
  });

  it("is hidden where the browser has no video encoder", async () => {
    // @ts-expect-error -- a browser without WebCodecs
    delete globalThis.VideoEncoder;
    view(async () => births);
    await openReplay();
    expect(screen.queryByRole("button", { name: "Export video" })).toBeNull();
  });

  it("defaults to 9:16, no longer asks to keep the tab visible, and says so when the browser can only encode WebM", async () => {
    installEncoder("vp9");
    view(async () => births);
    await openReplay();
    await userEvent.click(screen.getByRole("button", { name: "Export video" }));
    expect(screen.getByRole("radio", { name: /9:16/ })).toBeChecked();
    expect(screen.getByRole("radio", { name: /16:9/ })).not.toBeChecked();
    expect(await screen.findByText(/encodes WebM; Chrome, Edge or Safari encode MP4/)).toBeInTheDocument();
    expect(screen.queryByText(/visible/i)).toBeNull();
  });

  it("does not mention WebM where MP4 is encoded", async () => {
    view(async () => births);
    await openReplay();
    await userEvent.click(screen.getByRole("button", { name: "Export video" }));
    // let the browser's answer in
    await new Promise((r) => setTimeout(r));
    expect(screen.queryByText(/encodes WebM/)).toBeNull();
  });

  it("renders in the background: progress moves, the replay and the graph stay as they were, and the file downloads", async () => {
    view(async () => births);
    await openReplay();
    const camera = sceneStub.calls.focus.length;
    await startExport();
    expect(await screen.findByText(/Exporting video \d+ %/)).toBeInTheDocument();
    // The replay is still there and still the user's.
    expect(screen.getByRole("group", { name: "Growth replay" })).toBeInTheDocument();
    expect(screen.getByRole("slider", { name: "Replay position" })).toBeInTheDocument();
    expect(await screen.findByText(/Video saved/, {}, { timeout: 20_000 })).toBeInTheDocument();
    const link = document.querySelector<HTMLAnchorElement>("a[download]") ?? null;
    // downloadBlob removes its anchor at once: the proof is the pill and the frames
    expect(link).toBeNull();
    // 60 opening + the replay (3 steps: 8 s) + 90 hold + 150 end card
    expect(encoded).toBe(60 + 480 + 90 + 150);
    expect(sceneStub.offline.created).toBe(1);
    expect(sceneStub.offline.disposed).toBe(1);
    // the offline scene was driven on a virtual clock of exactly 1000/60 ms
    const f = sceneStub.offline.frames;
    expect(f).toHaveLength(480 + 90);
    f.forEach((t, i) => expect(t).toBeCloseTo((i * 1000) / 60, 6));
    // The visible scene was not touched by it.
    expect(sceneStub.calls.focus.length).toBe(camera);
    expect(sceneStub.offline.seeded).toBe(1);
  }, 30_000);

  it("cancelling disposes the offline scene, downloads nothing and leaves the replay open", async () => {
    view(async () => births);
    await openReplay();
    await startExport();
    await waitFor(() => expect(sceneStub.offline.created).toBe(1));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(sceneStub.offline.disposed).toBe(1));
    expect(screen.queryByText(/Exporting video/)).toBeNull();
    expect(document.querySelector("a[download]")).toBeNull();
    expect(screen.getByRole("group", { name: "Growth replay" })).toBeInTheDocument();
    // and no more frames are rendered afterwards
    const frames = sceneStub.offline.frames.length;
    await new Promise((r) => setTimeout(r, 50));
    expect(sceneStub.offline.frames.length).toBe(frames);
  });

  it("refuses a second export while one runs", async () => {
    view(async () => births);
    await openReplay();
    await startExport();
    await waitFor(() => expect(sceneStub.offline.created).toBe(1));
    await startExport();
    await new Promise((r) => setTimeout(r, 50));
    expect(sceneStub.offline.created).toBe(1);
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(sceneStub.offline.disposed).toBe(1));
  });

  it("switching KB or Map does not cancel it", async () => {
    const { rerender } = view(async () => births);
    await openReplay();
    await startExport();
    await waitFor(() => expect(sceneStub.offline.created).toBe(1));
    rerender(<GraphView {...props(async () => births, "kb-b")} />);
    await new Promise((r) => setTimeout(r, 50));
    expect(sceneStub.offline.disposed).toBe(0);
    expect(screen.getByText(/Exporting video/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(sceneStub.offline.disposed).toBe(1));
  });

  it("leaving the view ends it and frees the offline scene", async () => {
    const { unmount } = view(async () => births);
    await openReplay();
    await startExport();
    await waitFor(() => expect(sceneStub.offline.created).toBe(1));
    unmount();
    await waitFor(() => expect(sceneStub.offline.disposed).toBe(1));
  });

  it("an encoder that fails says so, downloads nothing and frees the offline scene", async () => {
    failEncode = true;
    view(async () => births);
    await openReplay();
    await startExport();
    expect(await screen.findByRole("alert")).toHaveTextContent("could not be rendered");
    expect(document.querySelector("a[download]")).toBeNull();
    expect(sceneStub.offline.disposed).toBe(sceneStub.offline.created);
    // a new export is possible again
    expect(screen.getByRole("button", { name: "Export video" })).toBeInTheDocument();
  });
});
