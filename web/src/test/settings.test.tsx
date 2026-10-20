import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { formatValue } from "../components/Settings";
import { json, stubApi } from "./fixtures";

const SETTINGS = {
  version: "v1.2.3",
  latest_version: "v1.3.0",
  update_channel: "homebrew",
  server: [
    {
      key: "git.sync",
      value: false,
      origin: "env",
      description: "When a remote is configured, fetch and pull before and push after each write.",
      default: "true",
      env: "CARTOGRAPHER_GIT_SYNC",
      restart: true,
    },
    { key: "git.autocommit", value: true, origin: "default", description: "Create a git commit.", default: "true" },
    { key: "auth.tokens", value: "(set)", origin: "file", description: "Bearer tokens.", default: "[]", secret: true },
  ],
  kbs: [
    {
      name: "kb-a",
      entries: [{ key: "kbs[].usage_stale_days", value: 30, origin: "file", description: "Days before an unused skill is reported.", default: "42" }],
      yaml: "usage_stale_days: 30 # from the server's kbs[] entry\n\n# --- available settings ---\n",
      git: { has_remote: true, sync: true, push_state: "idle" },
    },
  ],
  server_yaml: "git:\n  sync: false # from CARTOGRAPHER_GIT_SYNC\n  # --- available settings ---\n  # autocommit: true\n",
};

describe("the Settings panel", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=settings");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("lists each key with its value, origin, override and restart marker, grouped, and never a secret", async () => {
    stubApi({ "/settings": () => json(SETTINGS) });
    const user = userEvent.setup();
    render(<App />);
    const git = await screen.findByRole("list", { name: "Git settings" });
    // Every group starts folded.
    expect(git.closest("details")).not.toHaveAttribute("open");
    await user.click(screen.getByRole("heading", { name: "Git" }));
    const sync = within(git).getByText("git.sync").closest("li")!;
    expect(within(sync).getByText(/^set by environment · default true$/)).toBeVisible();
    expect(within(sync).getByLabelText("Value of git.sync")).toHaveTextContent("false");
    // What it does is on the row; the default, the override and the restart open with it.
    expect(within(sync).getByText(/fetch and pull before/)).toBeVisible();
    expect(within(sync).getByText("CARTOGRAPHER_GIT_SYNC")).not.toBeVisible();
    await user.click(within(sync).getByText("git.sync"));
    expect(within(sync).getByText("CARTOGRAPHER_GIT_SYNC")).toBeVisible();
    expect(within(sync).getByText(/after the server restarts/)).toBeVisible();
    const auto = within(git).getByText("git.autocommit").closest("li")!;
    expect(within(auto).getByText("default")).toBeInTheDocument();
    const tokens = within(screen.getByRole("list", { name: "Access settings" })).getByText("auth.tokens").closest("li")!;
    expect(within(tokens).getByLabelText("Value of auth.tokens")).toHaveTextContent("(set)");
    expect(within(tokens).getByText(/^set by config file · default/)).toBeInTheDocument();
    expect(screen.getByText(/v1\.3\.0 available/)).toBeInTheDocument();
  });

  it("finds a key by name, description, value or override, opening the groups that hold it", async () => {
    stubApi({ "/settings": () => json(SETTINGS) });
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole("list", { name: "Git settings" });
    await user.type(screen.getByRole("searchbox", { name: "Search settings" }), "bearer");
    const access = screen.getByRole("list", { name: "Access settings" });
    expect(access.closest("details")).toHaveAttribute("open");
    expect(within(access).getByText("auth.tokens")).toBeVisible();
    expect(screen.queryByRole("list", { name: "Git settings" })).toBeNull();
    await user.clear(screen.getByRole("searchbox", { name: "Search settings" }));
    await user.type(screen.getByRole("searchbox", { name: "Search settings" }), "nothing like it");
    expect(screen.getByText(/No setting matches/)).toBeInTheDocument();
  });

  it("has a tab for the server and one for the selected KB only, each readable or as its YAML file", async () => {
    stubApi({ "/settings": () => json(SETTINGS) });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    // Each tab says how many keys it holds.
    expect(within(tabs).getAllByRole("tab").map((t) => t.textContent)).toEqual(["Client", "Server3", "Knowledge Base1"]);
    await user.click(within(tabs).getByRole("tab", { name: /Knowledge Base/ }));
    const writes = screen.getByRole("list", { name: "KB kb-a Maintenance settings" });
    expect(within(writes).getByText("usage_stale_days")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "YAML" }));
    const file = screen.getByLabelText("kb-a/settings.yaml");
    expect(file).toHaveTextContent("usage_stale_days: 30 # from the server's kbs[] entry");
    await user.click(within(tabs).getByRole("tab", { name: /^Server/ }));
    const server = screen.getByLabelText("config.yaml");
    expect(server).toHaveTextContent("sync: false # from CARTOGRAPHER_GIT_SYNC");
    // Select-all with the pointer over the file selects the file, not the page.
    await user.hover(server);
    await user.keyboard("{Control>}a{/Control}");
    expect(window.getSelection()?.toString()).toBe(server.textContent);
  });

  it("shows the viewer's own client as its sync reported it, or says it has not reported yet", async () => {
    stubApi({
      "/settings": () =>
        json({
          ...SETTINGS,
          clients: [
            {
              machine: "laptop",
              version: "v-client",
              server_url: "http://example.com/mcp",
              received_at: "2026-10-16T10:00:00Z",
              yaml: "server_url: http://example.com/mcp\n",
              entries: [
                { key: "server_url", value: "http://example.com/mcp", origin: "file", description: "MCP endpoint.", default: "x" },
                { key: "update_check", value: true, origin: "default", description: "Look up releases.", default: "true" },
              ],
            },
          ],
        }),
    });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: /^Client/ }));
    expect(screen.getByText(/laptop · client v-client · reported/)).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Client Connection settings" })).toHaveTextContent("server_url");
    expect(screen.getByRole("list", { name: "Client Updates & sync settings" })).toHaveTextContent("update_check");
    await user.click(screen.getByRole("button", { name: "YAML" }));
    expect(screen.getByLabelText(".cartographer.yaml")).toHaveTextContent("server_url: http://example.com/mcp");
  });

  it("says when the viewer's client has not reported yet", async () => {
    stubApi({ "/settings": () => json(SETTINGS) });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: "Client" }));
    expect(screen.getByText(/has not reported its settings/)).toBeInTheDocument();
  });

  it("reaches the panel from the rail", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a");
    stubApi({ "/settings": () => json(SETTINGS) });
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole("button", { name: "Settings" }));
    expect(await screen.findByRole("list", { name: "Git settings" })).toBeInTheDocument();
    expect(window.location.search).toContain("panel=settings");
  });

  it("says so when the settings cannot be read", async () => {
    stubApi({ "/settings": () => json({ error: { code: "internal", message: "internal error" } }, 500) });
    render(<App />);
    expect(await screen.findByRole("button", { name: /retry|try again/i })).toBeInTheDocument();
  });

  it("formats lists and empty values", () => {
    expect(formatValue([])).toBe("(none)");
    expect(formatValue(["a", "b"])).toBe("a, b");
    expect(formatValue("")).toBe("(empty)");
    expect(formatValue(false)).toBe("false");
  });
});
