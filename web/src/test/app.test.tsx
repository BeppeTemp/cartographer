import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { graph, json, stubApi } from "./fixtures";

/**
 * Boot tests for the whole shell.
 *
 * They exist because the failure they catch is the worst one this UI has: a
 * render-time throw unmounts the tree, and against a dark background that is a
 * black page with nothing in it -- no message, no console breadcrumb the
 * operator would think to look for.
 *
 * Sigma is stubbed: jsdom has no WebGL, and what is under test here is the
 * shell, the data flow and the states, not the renderer.
 *
 * The force-layout worker is deliberately NOT stubbed. jsdom has no
 * URL.createObjectURL, so the supervisor genuinely fails to spawn here -- which
 * is the same thing that happens in a browser whose CSP forbids blob workers.
 * These tests therefore prove the graph degrades to a still picture instead of
 * taking the page down with it.
 */
vi.mock("sigma", () => import("./sigmaStub"));

describe("the shell boots", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/");
    localStorage.clear();
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("renders the atlas without throwing", async () => {
    stubApi();
    render(<App />);

    // If this never appears, the tree threw during render -- the black-page
    // failure this test exists for.
    expect(await screen.findByRole("banner")).toBeInTheDocument();
    expect(await screen.findByRole("navigation", { name: /atlas navigation/i })).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("region", { name: /concepts in this view/i })).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: /infra\/a/ })).toBeInTheDocument();
  });

  it("lists the visible collections with their counts", async () => {
    stubApi();
    render(<App />);
    expect(await screen.findByRole("button", { name: /Infrastructure/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Whole atlas/ })).toBeInTheDocument();
  });

  it("shows the auth prompt on a 401 instead of a blank page", async () => {
    stubApi({ "/kbs": () => json({ error: { code: "unauthorized", message: "no" } }, 401) });
    render(<App />);
    expect(await screen.findByLabelText(/bearer token/i)).toBeInTheDocument();
  });

  it("names an unreachable server rather than rendering nothing", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.reject(new TypeError("Failed to fetch"))),
    );
    render(<App />);
    expect(await screen.findByText(/server unreachable/i)).toBeInTheDocument();
  });

  it("says a KB is empty rather than showing an empty canvas", async () => {
    stubApi({
      "/graph": () => json({ nodes: [], edges: [], total_nodes: 0, total_edges: 0, limit: 2000 }),
    });
    render(<App />);
    expect(await screen.findByText(/no concepts yet/i)).toBeInTheDocument();
  });

  it("explains a truncated graph instead of presenting it as complete", async () => {
    stubApi({
      "/graph": () => json({ ...graph, truncated: true, total_nodes: 9000 }),
    });
    render(<App />);
    expect(await screen.findByText(/truncated/i)).toBeInTheDocument();
    expect(screen.getByText(/of 9000 concepts/i)).toBeInTheDocument();
  });
});
