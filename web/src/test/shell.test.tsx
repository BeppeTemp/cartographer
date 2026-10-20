import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { stubApi, openPanels, graph, json } from "./fixtures";


/**
 * The shell as a keyboard user and a narrow screen meet it (WP1 acceptance).
 * The canvas is opaque to both, so these paths are the only way in: if one of
 * them breaks, part of the audience simply has no atlas.
 */

const realMatchMedia = window.matchMedia;

function viewport(narrow: boolean) {
  window.matchMedia = ((query: string) => ({
    matches: narrow && query.includes("max-width: 1023px"),
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
}

beforeEach(() => {
  window.history.replaceState(null, "", "/ui/");
  localStorage.clear();
  openPanels();
  sessionStorage.clear();
});

afterEach(() => {
  window.matchMedia = realMatchMedia;
  vi.unstubAllGlobals();
});

/** Presses Tab (or Shift+Tab) until `found` holds for the focused element,
 *  failing after `limit` presses: an unreachable control is the defect. */
async function tabUntil(
  user: ReturnType<typeof userEvent.setup>,
  found: (el: Element) => boolean,
  { shift = false, limit = 80 } = {},
): Promise<Element> {
  for (let i = 0; i < limit; i++) {
    await user.tab({ shift });
    const el = document.activeElement;
    if (el && found(el)) return el;
  }
  throw new Error(`not reached within ${limit} ${shift ? "Shift+Tab" : "Tab"} presses`);
}

describe("keyboard traversal", () => {
  it("goes shell -> filters -> search -> inspector and back", async () => {
    viewport(false);
    stubApi();
    const user = userEvent.setup();
    render(<App />);
    await screen.findByTestId("graph-view");
    await screen.findByRole("button", { name: /^Service(?!s)/ });

    // Shell first: the skip link and the top bar come before anything else.
    const first = await tabUntil(user, () => true);
    expect(first).toHaveTextContent(/skip to content/i);
    await tabUntil(user, (el) => /search concepts/i.test(el.textContent ?? ""));

    // Then the filters in the rail.
    const chip = await tabUntil(user, (el) => el.classList.contains("chip"));
    await user.keyboard("{Enter}");
    expect(chip).toHaveAttribute("aria-pressed", "true");
    await user.keyboard("{Enter}");
    expect(chip).toHaveAttribute("aria-pressed", "false");

    // Then the search, which exposes the same selection as the canvas: the
    // keyboard path to every concept.
    await user.keyboard("{Control>}k{/Control}");
    expect(screen.getByRole("dialog", { name: /search concepts/i })).toBeInTheDocument();
    await user.keyboard("infra/a{Enter}");
    const inspector = await screen.findByRole("complementary", { name: /inspector for infra\/a/i });

    // Onward into the inspector.
    const inside = await tabUntil(user, (el) => inspector.contains(el));
    expect(inside).toBeInTheDocument();

    // Closing the inspector leaves focus on the main region, not on <body>.
    screen.getByRole("button", { name: /close inspector/i }).focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(document.activeElement).toBe(document.getElementById("main")));
  });
});

describe("narrow layout", () => {
  it("puts navigation in a modal sheet that traps and returns focus", async () => {
    viewport(true);
    stubApi();
    const user = userEvent.setup();
    render(<App />);

    const open = await screen.findByRole("button", { name: /open navigation/i });
    // Not rendered beside the graph: there is no room for it.
    expect(screen.queryByRole("navigation", { name: /atlas navigation/i })).not.toBeInTheDocument();

    await user.click(open);
    const sheet = screen.getByRole("dialog", { name: /navigation/i });
    expect(sheet).toHaveAttribute("aria-modal", "true");
    expect(within(sheet).getByRole("navigation", { name: /atlas navigation/i })).toBeInTheDocument();
    expect(sheet.contains(document.activeElement)).toBe(true);

    // Tab never leaves the sheet.
    for (let i = 0; i < 30; i++) {
      await user.tab();
      expect(sheet.contains(document.activeElement)).toBe(true);
    }

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(document.activeElement).toBe(open);
  });

  it("opens the inspector as a sheet on selection, and closing it keeps the selection", async () => {
    viewport(true);
    stubApi();
    const user = userEvent.setup();
    render(<App />);

    await screen.findByTestId("graph-view");
    await user.keyboard("{Control>}k{/Control}");
    await user.keyboard("infra/a{Enter}");

    const sheet = await screen.findByRole("dialog", { name: /inspector/i });
    expect(
      await within(sheet).findByRole("complementary", { name: /inspector for infra\/a/i }),
    ).toBeInTheDocument();

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    // Escape closed the sheet only: the concept is still selected, and the
    // top bar offers the way back to it.
    expect(new URLSearchParams(window.location.search).toString()).toContain("infra");
    await user.click(screen.getByRole("button", { name: /open inspector/i }));
    expect(screen.getByRole("dialog", { name: /inspector/i })).toBeInTheDocument();
  });
});

describe("colour legend", () => {
  it("is a card over the canvas: what is drawn, the colour switch, the key and the replay", async () => {
    viewport(false);
    stubApi();
    const user = userEvent.setup();
    render(<App />);

    const legend = await screen.findByRole("region", { name: /graph legend/i });
    // Over the canvas, not above it and not in the rail.
    expect(screen.getByTestId("graph-view").contains(legend)).toBe(true);
    expect(screen.queryByRole("region", { name: "Graph context" })).toBeNull();
    expect(within(legend).getByText("3 concepts · 2 links")).toBeInTheDocument();
    const community = within(legend).getByRole("button", { name: "Community" });
    const map = within(legend).getByRole("button", { name: "Map" });
    expect(community).toHaveAttribute("aria-pressed", "true");
    expect(within(legend).getByRole("button", { name: "Replay growth" })).toBeInTheDocument();
    // The rail no longer repeats the community key.
    const rail = screen.getByRole("navigation", { name: /atlas navigation/i });
    expect(within(rail).queryByRole("heading", { name: "Communities" })).toBeNull();

    await user.click(map);
    expect(map).toHaveAttribute("aria-pressed", "true");
    expect(within(legend).getByText("infra")).toBeInTheDocument();
    expect(localStorage.getItem("cartographer.colorBy")).toBe("map");

    // Lint problems are not a colour mode (D307): the Health panel reads them.
    expect(within(legend).queryByRole("button", { name: "Health" })).not.toBeInTheDocument();
  });

  it("steps aside while a concept is open and comes back when the reading panel closes", async () => {
    viewport(false);
    window.history.replaceState(null, "", "/ui/?kb=kb-a&concept=infra%2Fa");
    stubApi();
    const user = userEvent.setup();
    render(<App />);

    const close = await screen.findByRole("button", { name: "Close inspector" });
    expect(screen.queryByRole("region", { name: /graph legend/i })).toBeNull();
    // Nothing above the canvas either: the graph is the page.
    expect(screen.queryByRole("region", { name: "Graph context" })).toBeNull();

    await user.click(close);
    expect(await screen.findByRole("region", { name: /graph legend/i })).toBeInTheDocument();
  });
});

describe("camera controls", () => {
  it("sit over the canvas with zoom, fit and the motion switch", async () => {
    viewport(false);
    stubApi();
    const user = userEvent.setup();
    render(<App />);

    const camera = await screen.findByRole("group", { name: "Graph camera" });
    expect(screen.getByTestId("graph-view").contains(camera)).toBe(true);
    expect(within(camera).getByRole("button", { name: "Zoom in" })).toBeInTheDocument();
    expect(within(camera).getByRole("button", { name: "Zoom out" })).toBeInTheDocument();
    expect(within(camera).getByRole("button", { name: "Fit graph to view" })).toBeInTheDocument();
    // The replay lives in the legend card, not here.
    expect(within(camera).queryByRole("button", { name: "Replay growth" })).toBeNull();

    const motion = within(camera).getByRole("button", { name: "Motion" });
    expect(motion).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("graph-view")).toHaveAttribute("data-motion", "live");
    await user.click(motion);
    expect(motion).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByTestId("graph-view")).toHaveAttribute("data-motion", "still");
    await user.click(motion);
    expect(motion).toHaveAttribute("aria-pressed", "true");
  });

  it("stay with a Map selected and with a concept open", async () => {
    viewport(false);
    window.history.replaceState(null, "", "/ui/?kb=kb-a&scope=infra&concept=infra%2Fa");
    stubApi();
    render(<App />);

    await screen.findByRole("button", { name: "Close inspector" });
    const camera = await screen.findByRole("group", { name: "Graph camera" });
    expect(within(camera).getByRole("button", { name: "Fit graph to view" })).toBeInTheDocument();
  });
});

/**
 * The reading panel's splitter (D239). jsdom lays nothing out, so the shell's
 * body and main report a 1600px window with the open rail (264px): the panel
 * may then grow to 1600 - 264 - 280 = 1056px.
 */
describe("the reading panel splitter", () => {
  // jsdom defines clientWidth on Element.prototype: shadow it on
  // HTMLElement.prototype and delete the shadow afterwards.
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&concept=infra%2Fa");
    Object.defineProperty(HTMLElement.prototype, "clientWidth", {
      configurable: true,
      get(this: HTMLElement) {
        if (this.classList.contains("shell__body")) return 1600;
        if (this.id === "main") return 1336;
        return 0;
      },
    });
  });
  afterEach(() => {
    delete (HTMLElement.prototype as { clientWidth?: number }).clientWidth;
  });

  const panel = () => document.getElementById("reading-panel")!;

  it("resizes from the keyboard, persists the result and resets on Enter", async () => {
    stubApi();
    const user = userEvent.setup();
    render(<App />);
    const handle = await screen.findByRole("separator", { name: "Resize reading panel" });
    // The shell is measured in the effect after its first render.
    await waitFor(() => expect(handle).toHaveAttribute("aria-valuemax", "1056"));
    // No preference yet: half the room beside the rail, (1600 - 264) / 2.
    expect(handle).toHaveAttribute("aria-valuenow", "668");
    expect(handle).toHaveAttribute("aria-valuemin", "320");
    expect(handle).toHaveAttribute("aria-controls", "reading-panel");

    handle.focus();
    await user.keyboard("{ArrowLeft}");
    expect(handle).toHaveAttribute("aria-valuenow", "684");
    expect(panel().style.getPropertyValue("--inspector-width")).toBe("684px");
    expect(localStorage.getItem("cartographer.inspector.width")).toBe("684");

    await user.keyboard("{Shift>}{ArrowRight}{/Shift}");
    expect(handle).toHaveAttribute("aria-valuenow", "620");
    await user.keyboard("{Home}");
    expect(handle).toHaveAttribute("aria-valuenow", "1056");
    await user.keyboard("{End}");
    expect(handle).toHaveAttribute("aria-valuenow", "320");

    await user.keyboard("{Enter}");
    expect(handle).toHaveAttribute("aria-valuenow", "668");
    expect(localStorage.getItem("cartographer.inspector.width")).toBe("668");
  });

  it("restores the stored width, clamped to the window without rewriting it", async () => {
    localStorage.setItem("cartographer.inspector.width", "600");
    stubApi();
    const { unmount } = render(<App />);
    expect(await screen.findByRole("separator", { name: "Resize reading panel" })).toHaveAttribute(
      "aria-valuenow",
      "600",
    );
    unmount();

    localStorage.setItem("cartographer.inspector.width", "5000");
    render(<App />);
    const handle = await screen.findByRole("separator", { name: "Resize reading panel" });
    await waitFor(() => expect(handle).toHaveAttribute("aria-valuenow", "1056"));
    expect(panel().style.getPropertyValue("--inspector-width")).toBe("1056px");
    expect(localStorage.getItem("cartographer.inspector.width")).toBe("5000");
  });

  it("is absent from the narrow layout", async () => {
    viewport(true);
    stubApi();
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole("button", { name: /open inspector/i }));
    expect(await screen.findByRole("dialog", { name: "Inspector" })).toBeInTheDocument();
    expect(screen.queryByRole("separator", { name: "Resize reading panel" })).not.toBeInTheDocument();
  });
});

describe("transitions", () => {
  it("boots as a skeleton of the shell, with the top bar and rail already in place", async () => {
    viewport(false);
    const fetchMock = stubApi();
    // The first answer never comes: the page stays in its booting state.
    fetchMock.mockImplementation(() => new Promise<Response>(() => {}));
    render(<App />);

    expect(screen.getByText("Connecting to Cartographer")).toBeInTheDocument();
    expect(screen.getByRole("banner")).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: /loading navigation/i })).toBeInTheDocument();
    expect(screen.getByRole("main")).toBeInTheDocument();
    expect(document.querySelector(".shell[aria-busy='true']")).not.toBeNull();
  });

  it("keeps the snapshot's title in the inspector while the concept loads, and focuses it after the palette", async () => {
    viewport(false);
    const titled = { ...graph, nodes: graph.nodes.map((n) => (n.id === "infra/a" ? { ...n, title: "Alpha Name" } : n)) };
    const fetchMock = stubApi({ "/graph": () => json(titled) });
    const base = fetchMock.getMockImplementation()!;
    let release!: () => void;
    const gate = new Promise<void>((resolve) => (release = resolve));
    fetchMock.mockImplementation(async (url: string) => {
      if (url.includes("/concept")) await gate;
      return base(url);
    });
    const user = userEvent.setup();
    render(<App />);
    await screen.findByTestId("graph-view");
    await screen.findByRole("button", { name: /^Service(?!s)/ });

    await user.keyboard("{Control>}k{/Control}");
    await user.keyboard("infra/a{Enter}");
    const inspector = await screen.findByRole("complementary", { name: /inspector for infra\/a/i });
    expect(within(inspector).getByRole("heading", { level: 2 })).toHaveTextContent("Alpha Name");

    release();
    await waitFor(() => expect(within(inspector).getByRole("heading", { level: 2 })).toHaveTextContent("Alpha"));
    await waitFor(() => expect(inspector.contains(document.activeElement)).toBe(true));
  });
});
