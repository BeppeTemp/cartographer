import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { stubApi, openPanels } from "./fixtures";


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
  it("goes shell -> filters -> node list -> inspector and back", async () => {
    viewport(false);
    stubApi();
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole("button", { name: /infra\/a/ });
    await screen.findByRole("button", { name: /^Service/ });

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

    // Then the node list, which exposes the same selection as the canvas.
    const row = await tabUntil(user, (el) => el.getAttribute("data-concept-id") === "infra/a");
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("complementary", { name: /inspector for infra\/a/i })).toBeInTheDocument();
    expect(row).toHaveAttribute("aria-current", "true");

    // Onward into the inspector...
    const inspector = screen.getByRole("complementary", { name: /inspector for infra\/a/i });
    const inside = await tabUntil(user, (el) => inspector.contains(el));
    expect(inside).toBeInTheDocument();

    // ...and back to the node list.
    await tabUntil(user, (el) => el.hasAttribute("data-concept-id"), { shift: true });

    // Closing the inspector returns focus to the concept's row, not to <body>.
    screen.getByRole("button", { name: /close inspector/i }).focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(document.activeElement).toBe(row));
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
    // The node list travels with it, so every node stays reachable.
    await waitFor(() =>
      expect(within(sheet).getByRole("button", { name: /infra\/a/ })).toBeInTheDocument(),
    );
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

    await user.click(await screen.findByRole("button", { name: /open navigation/i }));
    const nav = screen.getByRole("dialog", { name: /navigation/i });
    await user.click(await within(nav).findByRole("button", { name: /infra\/a/ }));

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
  it("switches between community and Map colouring and remembers it", async () => {
    viewport(false);
    stubApi();
    const user = userEvent.setup();
    render(<App />);

    const legend = await screen.findByRole("region", { name: /graph legend/i });
    const community = within(legend).getByRole("button", { name: "Community" });
    const map = within(legend).getByRole("button", { name: "Map" });
    expect(community).toHaveAttribute("aria-pressed", "true");

    await user.click(map);
    expect(map).toHaveAttribute("aria-pressed", "true");
    expect(within(legend).getByText("infra")).toBeInTheDocument();
    expect(localStorage.getItem("cartographer.colorBy")).toBe("map");
  });
});
