import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GraphView } from "../components/GraphView";
import { snapshotCommunities } from "../lib/communities";
import { graph } from "./fixtures";
import { sceneStub } from "./sceneStub";

// The Image of the end card never loads in jsdom.
vi.mock("../lib/graph3d/record", async (importActual) => ({
  ...(await importActual<typeof import("../lib/graph3d/record")>()),
  loadMark: async () => null,
}));

/** The export flow (D677) with the scene and the recorder stubbed. */
const births = { "infra/a": "2026-01-01T00:00:00Z", "infra/b": "2026-02-01T00:00:00Z", "notes/c": "2026-03-01T00:00:00Z" };
const originalMR = globalThis.MediaRecorder;

function installRecorder(mp4: boolean) {
  class Fake {
    static isTypeSupported = (m: string) => (mp4 ? m.startsWith("video/mp4") : m.startsWith("video/webm"));
    state = "inactive";
    mimeType = "";
    ondataavailable: unknown = null;
    onstop: unknown = null;
    onerror: unknown = null;
    start() {}
    stop() {}
    pause() {}
    resume() {}
  }
  globalThis.MediaRecorder = Fake as unknown as typeof MediaRecorder;
}

function view(loadBirths?: () => Promise<Record<string, string>>) {
  return render(
    <GraphView
      snapshot={graph}
      layoutKey="kb-a"
      loadBirths={loadBirths}
      kbName="kb-a"
      communities={snapshotCommunities(graph)}
      colorBy="community"
      selected={null}
      hiddenIds={new Set()}
      themeKey="dark"
      live={false}
      occludedRight={0}
      onSelect={() => {}}
    />,
  );
}

describe("Export video", () => {
  beforeEach(() => {
    sceneStub.reset();
    installRecorder(false);
    HTMLCanvasElement.prototype.captureStream = vi.fn(() => ({}) as MediaStream);
    const ctx = new Proxy({}, { get: () => () => ({ addColorStop() {}, width: 0 }), set: () => true });
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(ctx as unknown as CanvasRenderingContext2D);
  });
  afterEach(() => {
    globalThis.MediaRecorder = originalMR;
    vi.restoreAllMocks();
  });

  it("is offered only where the replay is and the browser can record", async () => {
    const { unmount } = view();
    expect(screen.queryByRole("button", { name: "Export video" })).toBeNull();
    unmount();

    // @ts-expect-error -- a browser without MediaRecorder
    delete globalThis.MediaRecorder;
    const second = view(async () => births);
    expect(screen.queryByRole("button", { name: "Export video" })).toBeNull();
    second.unmount();

    installRecorder(true);
    view(async () => births);
    expect(screen.getByRole("button", { name: "Export video" })).toBeInTheDocument();
  });

  it("defaults to 9:16 and says so when the browser can only record WebM", async () => {
    view(async () => births);
    await userEvent.click(screen.getByRole("button", { name: "Export video" }));
    expect(screen.getByRole("radio", { name: /9:16/ })).toBeChecked();
    expect(screen.getByRole("radio", { name: /16:9/ })).not.toBeChecked();
    expect(screen.getByText(/records WebM; Chrome, Edge or Safari record MP4/)).toBeInTheDocument();
  });

  it("does not mention WebM where MP4 is recorded", async () => {
    installRecorder(true);
    view(async () => births);
    await userEvent.click(screen.getByRole("button", { name: "Export video" }));
    expect(screen.queryByText(/records WebM/)).toBeNull();
  });

  it("cancelling mid-recording restores the view and leaves the replay closed", async () => {
    view(async () => births);
    await userEvent.click(screen.getByRole("button", { name: "Export video" }));
    await userEvent.click(screen.getByRole("radio", { name: /1:1/ }));
    await userEvent.click(screen.getByRole("button", { name: "Record" }));
    await screen.findByText(/Recording 1:1/);
    expect(sceneStub.calls.capture).toEqual(["begin 1080x1080"]);
    expect(screen.getByTestId("graph-view")).toHaveAttribute("data-growing");
    // The timeline is not in the way of the frame.
    expect(screen.queryByRole("slider")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByText(/Recording/)).toBeNull());
    expect(sceneStub.calls.capture).toEqual(["begin 1080x1080", "end"]);
    expect(screen.getByTestId("graph-view")).not.toHaveAttribute("data-growing");
    // Nothing was offered for download.
    expect(document.querySelector("a[download]")).toBeNull();
  });

  it("a recorder that cannot start ends the export, restores the view and says so", async () => {
    globalThis.MediaRecorder = class {
      static isTypeSupported = () => true;
      constructor() {
        throw new Error("boom");
      }
    } as unknown as typeof MediaRecorder;
    view(async () => births);
    await userEvent.click(screen.getByRole("button", { name: "Export video" }));
    await userEvent.click(screen.getByRole("button", { name: "Record" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("could not be recorded");
    expect(sceneStub.calls.capture).toEqual(["begin 1080x1920", "end"]);
    expect(screen.getByTestId("graph-view")).not.toHaveAttribute("data-growing");
  });
});
