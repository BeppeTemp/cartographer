import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { linkWikiLinks } from "../components/Markdown";
import { stubApi } from "./fixtures";
import { hasWebGL } from "../lib/webgl";
import { sceneStub } from "./sceneStub";


/**
 * The graph is the page: navigation and the node list start folded away and
 * the inspector exists only while something is selected. Each panel opens on
 * demand, and the choice is remembered.
 */
describe("graph-first layout", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/");
    localStorage.clear();
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("says so instead of leaving a dead canvas when the scene cannot start", async () => {
    // There is no other view to hand over to (D235): the page names the
    // problem and keeps the list and the search.
    sceneStub.fail = true;
    try {
      stubApi();
      render(<App />);
      expect(await screen.findByText("This browser cannot draw the graph", {}, { timeout: 5000 })).toBeInTheDocument();
      expect(document.querySelector("[data-testid=graph-view]")).toBeNull();
    } finally {
      sceneStub.fail = false;
    }
  });

  it("without WebGL keeps the list and names why there is no graph", async () => {
    // Sigma needs WebGL as much as three.js does, and throws on its first
    // draw: without the up-front check the whole page went to the boundary.
    vi.mocked(hasWebGL).mockReturnValue(false);
    try {
      stubApi();
      const user = userEvent.setup();
      render(<App />);
      expect(await screen.findByText("This browser cannot draw the graph")).toBeInTheDocument();
      expect(document.querySelector("[data-testid=graph-view]")).toBeNull();
      await user.click(screen.getByRole("button", { name: /^Concepts/ }));
      expect(await screen.findByRole("region", { name: /concepts in this view/i })).toBeInTheDocument();
    } finally {
      vi.mocked(hasWebGL).mockReturnValue(true);
    }
  });

  it("starts with only the graph, and opens panels on demand", async () => {
    stubApi();
    const user = userEvent.setup();
    render(<App />);

    const toggle = await screen.findByRole("button", { name: /^Concepts/ });
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    expect(screen.queryByRole("region", { name: /concepts in this view/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("complementary")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /expand navigation/i })).toBeInTheDocument();

    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("region", { name: /concepts in this view/i })).toBeInTheDocument();
    expect(localStorage.getItem("cartographer.panel.list")).toBe("1");

    await user.click(screen.getByRole("button", { name: /infra\/a/ }));
    expect(await screen.findByRole("complementary", { name: /inspector for infra\/a/i })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /close inspector/i }));
    await waitFor(() => expect(screen.queryByRole("complementary")).not.toBeInTheDocument());
  });

  it("keeps the folded rail's buttons named", async () => {
    stubApi();
    render(<App />);
    expect(await screen.findByRole("button", { name: "Atlas" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Observatory/ })).toBeInTheDocument();
  });
});

describe("wiki-links in a concept body", () => {
  it("become concept links, with their label or their id", () => {
    expect(linkWikiLinks("See [[infra/dns]] and [[infra/gateway|the edge]]."))
      .toBe("See [infra/dns](concept-link/infra%2Fdns) and [the edge](concept-link/infra%2Fgateway).");
    expect(linkWikiLinks("Section [[infra/dns#records]]")).toBe("Section [infra/dns](concept-link/infra%2Fdns)");
  });

  it("leave code alone", () => {
    const body = "Use `[[not/a-link]]` or\n```\n[[also/not]]\n```\nbut [[yes/this]]";
    expect(linkWikiLinks(body)).toBe(
      "Use `[[not/a-link]]` or\n```\n[[also/not]]\n```\nbut [yes/this](concept-link/yes%2Fthis)",
    );
  });

  it("navigate inside the atlas when followed", async () => {
    window.history.replaceState(null, "", "/ui/?kb=homelab&concept=infra%2Fa");
    localStorage.clear();
    stubApi({
      "/concept": () =>
        new Response(
          JSON.stringify({
            id: "infra/a",
            title: "Alpha",
            collection: "infra",
            frontmatter: { type: "Service" },
            body: "Depends on [[infra/b|Beta]].",
            body_bytes: 30,
            outline: [],
            content_hash: "h",
            outbound: ["infra/b"],
            inbound: [],
            broken: [],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
    });
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole("link", { name: "Beta" }));
    await waitFor(() => expect(window.location.search).toContain("concept=infra%2Fb"));
    vi.unstubAllGlobals();
  });
});

/**
 * A filter that hides the selection keeps it selected, but the graph draws
 * the filtered set as if nothing were (D240): no receded canvas, no names
 * floating over nodes that are no longer drawn.
 */
describe("a selection hidden by a filter", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=homelab&concept=infra%2Fa");
    localStorage.clear();
    sessionStorage.clear();
    sceneStub.reset();
  });
  afterEach(() => vi.unstubAllGlobals());

  const labels = () => [...document.querySelectorAll(".graph3d__label:not(.graph3d__tooltip)")].map((el) => el.textContent);

  async function openRail() {
    stubApi();
    const user = userEvent.setup();
    render(<App />);
    await waitFor(() => expect(sceneStub.calls.focus).toContain("infra/a"));
    await user.click(screen.getByRole("button", { name: /expand navigation/i }));
    return user;
  }

  it("draws the filtered set at full colour, unfocused and unnamed", async () => {
    const user = await openRail();
    expect(labels().length).toBeGreaterThan(0);

    const unfocused = sceneStub.calls.unfocus;
    await user.click(await screen.findByRole("button", { name: /^Note/ }));
    await waitFor(() => expect(sceneStub.calls.unfocus).toBeGreaterThan(unfocused));
    expect(labels()).toEqual([]);
    // Without a canvas token the stub's receded colour is an rgba() fade.
    expect(sceneStub.calls.colours.at(-1)!.some((c) => c.startsWith("rgba"))).toBe(false);
    expect(window.location.search).toContain("concept=infra%2Fa");
    expect(screen.getByRole("complementary", { name: /inspector for infra\/a/i })).toBeInTheDocument();

    const focused = sceneStub.calls.focus.length;
    await user.click(screen.getByRole("button", { name: /^Note/ }));
    await waitFor(() => expect(sceneStub.calls.focus.length).toBeGreaterThan(focused));
    expect(sceneStub.calls.focus.at(-1)).toBe("infra/a");
  });

  it("names only the neighbours still drawn", async () => {
    const user = await openRail();
    await waitFor(() => expect(labels()).toContain("c"));

    await user.click(await screen.findByRole("button", { name: /^Service/ }));
    await waitFor(() => expect(labels()).not.toContain("c"));
    expect(labels()).toContain("b");
  });
});
