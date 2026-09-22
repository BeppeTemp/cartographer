import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { linkWikiLinks } from "../components/Markdown";
import { stubApi, use2D } from "./fixtures";
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
    use2D();
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("opens in 3D and falls back to the 2D atlas when 3D cannot start", async () => {
    // The 3D scene fails to get a context; the view hands over to 2D instead
    // of leaving a dead canvas.
    localStorage.removeItem("cartographer.panel.3d");
    sceneStub.failModes.add("3d");
    try {
      stubApi();
      render(<App />);
      const view = await screen.findByRole("group", { name: "Graph view" });
      const [two, three] = within(view).getAllByRole("button");
      await waitFor(() => expect(two).toHaveAttribute("aria-pressed", "true"), { timeout: 5000 });
      expect(three).toHaveAttribute("aria-pressed", "false");
      await waitFor(() => expect(document.querySelector("[data-testid=graph-view]")).toHaveAttribute("data-mode", "2d"));
    } finally {
      sceneStub.failModes.clear();
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
      expect(screen.queryByRole("group", { name: "Graph view" })).not.toBeInTheDocument();
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
    use2D();
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
