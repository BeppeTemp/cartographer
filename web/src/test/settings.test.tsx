import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { formatValue, humanBytes } from "../components/Settings";
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

/** The page opens on the viewer's client: the server's keys are one tab away. */
async function openServer(user: ReturnType<typeof userEvent.setup>) {
  const tabs = await screen.findByRole("tablist", { name: "Settings of" });
  await user.click(within(tabs).getByRole("tab", { name: /^Server/ }));
}

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
    await openServer(user);
    // Every group is open: its keys are read without a click.
    const git = await screen.findByRole("list", { name: "Git settings" });
    expect(screen.getByRole("heading", { name: "Git" })).toBeVisible();
    const sync = within(git).getByText("git.sync").closest("li")!;
    // A boolean reads as a switch.
    expect(within(sync).getByLabelText("Value of git.sync")).toHaveTextContent("off");
    // What it does is on the row; the default, the override and the restart open with it.
    expect(within(sync).getByText(/fetch and pull before/)).toBeVisible();
    expect(within(sync).queryByText("CARTOGRAPHER_GIT_SYNC")).toBeNull();
    await user.click(within(sync).getByText("git.sync"));
    // The table says the default and who set it.
    expect(within(sync).getByText("environment")).toBeVisible();
    expect(within(sync).getByText("CARTOGRAPHER_GIT_SYNC")).toBeVisible();
    expect(within(sync).getByText(/after the server restarts/)).toBeVisible();
    const tokens = within(screen.getByRole("list", { name: "Access settings" })).getByText("auth.tokens").closest("li")!;
    expect(within(tokens).getByLabelText("Value of auth.tokens")).toHaveTextContent("(set)");
  });

  it("finds a key by name, description, value or override, opening the groups that hold it", async () => {
    stubApi({ "/settings": () => json(SETTINGS) });
    const user = userEvent.setup();
    render(<App />);
    await openServer(user);
    await screen.findByRole("list", { name: "Git settings" });
    await user.type(screen.getByRole("searchbox", { name: "Search settings" }), "bearer");
    const access = screen.getByRole("list", { name: "Access settings" });
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
    // The page opens on the viewer's client; a tab names what it holds, no count.
    expect(within(tabs).getAllByRole("tab").map((t) => t.textContent)).toEqual(["Client", "Server", "Knowledge Base", "Worker"]);
    expect(within(tabs).getByRole("tab", { name: "Client" })).toHaveAttribute("aria-selected", "true");
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
              providers: [
                {
                  name: "claude",
                  scope: "workspace",
                  isolation: "workspace",
                  workspaces: [{ path: "$HOME/work", kbs: ["kb-a"] }],
                },
              ],
              placeholders: [{ key: "repo:infra", kbs: ["kb-a"], path: "$HOME/src/infra" }],
              entries: [
                { key: "server_url", value: "http://example.com/mcp", origin: "file", description: "MCP endpoint.", default: "x" },
                { key: "update_check", value: true, origin: "default", description: "Look up releases.", default: "true" },
                {
                  key: "scopes",
                  value: { codex: "workspace", kiro: "home" },
                  origin: "file",
                  description: "Where a provider receives KB artifacts.",
                  default: "{}",
                },
              ],
            },
          ],
        }),
    });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: /^Client/ }));
    expect(screen.getByRole("heading", { name: "laptop" })).toBeInTheDocument();
    expect(screen.getByText(/Client v-client · reported/)).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Client Connection settings" })).toHaveTextContent("server_url");
    expect(screen.getByRole("list", { name: "Client Updates & sync settings" })).toHaveTextContent("update_check");
    // A map is a table under its row, its size beside the key.
    expect(screen.getByLabelText("Value of scopes")).toHaveTextContent("2 entries");
    const scopes = screen.getByLabelText("Entries of scopes");
    expect(scopes).toHaveTextContent("codex");
    expect(scopes).toHaveTextContent("home");
    // Below the settings, one section per KB with its own client settings, named by their YAML key.
    const kbA = screen.getByRole("list", { name: "Client settings of kb-a" });
    expect(within(kbA).getByLabelText("Value of clients.<agent>.kbs")).toHaveTextContent("claude");
    // A value with keys is a table under its row, one key a line.
    const where = within(kbA).getByLabelText("Entries of workspaces");
    expect(where).toHaveTextContent("claude");
    expect(where).toHaveTextContent("$HOME/work");
    const paths = within(kbA).getByLabelText("Entries of paths");
    expect(paths).toHaveTextContent("repo:infra");
    expect(paths).toHaveTextContent("$HOME/src/infra");
    // the KBs are pills, the active one picked
    const picker = screen.getByRole("group", { name: "Knowledge bases on this machine" });
    expect(within(picker).getByRole("button", { name: "kb-a" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "YAML" }));
    expect(screen.getByLabelText(".cartographer.yaml")).toHaveTextContent("server_url: http://example.com/mcp");
  });

  it("lists a worker's reported settings with its replicas, or the template and a note when none reported", async () => {
    const entries = [
      { key: "name", value: "team-a", origin: "file", description: "Perimeter name.", default: '""' },
      { key: "models[].api_key_env", value: "CLOUD_API_KEY", origin: "file", description: "Key variable.", default: '""' },
    ];
    stubApi({
      "/settings": () =>
        json({
          ...SETTINGS,
          workers: [
            { perimeter: "team-a", version: "v9", replicas: 2, kbs: ["kb-a"], entries, yaml: "name: team-a\n", received_at: "2026-10-16T10:00:00Z" },
          ],
          worker_template: { entries, yaml: "# name: \"\"\n" },
        }),
    });
    const user = userEvent.setup();
    const first = render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: "Worker" }));
    expect(screen.getByRole("heading", { name: "team-a" })).toBeInTheDocument();
    expect(screen.getByText(/Worker v9 · 2 replicas · reported/)).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Worker name settings" })).toHaveTextContent("team-a");
    expect(screen.getByRole("list", { name: "Worker models settings" })).toHaveTextContent("models[].api_key_env");
    await user.click(screen.getByRole("button", { name: "YAML" }));
    expect(screen.getByLabelText("worker.yaml")).toHaveTextContent("name: team-a");
    first.unmount();

    stubApi({
      "/settings": () => json({ ...SETTINGS, workers: [], worker_template: { entries, yaml: "# name\n" } }),
    });
    render(<App />);
    const tabs2 = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs2).getByRole("tab", { name: "Worker" }));
    expect(screen.getByText(/No worker has reported/)).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Worker name settings" })).toBeInTheDocument();
  });

  it("keeps two workers of the same perimeter name apart", async () => {
    const row = (replicas: number, yaml: string) => ({
      perimeter: "team-a",
      replicas,
      kbs: ["kb-a"],
      entries: [{ key: "name", value: "team-a", origin: "file", description: "Perimeter name.", default: '""' }],
      yaml,
      received_at: "2026-10-16T10:00:00Z",
    });
    stubApi({ "/settings": () => json({ ...SETTINGS, workers: [row(1, "name: one\n"), row(3, "name: two\n")] }) });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    const workerTabs = within(tabs).getAllByRole("tab", { name: "Worker · team-a" });
    expect(workerTabs).toHaveLength(2);
    await user.click(workerTabs[1]!);
    expect(screen.getByText(/^Worker · 3 replicas · reported/)).toBeInTheDocument();
    expect(within(tabs).getAllByRole("tab", { selected: true }).map((t) => t.id)).toHaveLength(1);
    await user.click(screen.getByRole("button", { name: "YAML" }));
    expect(screen.getByLabelText("worker.yaml")).toHaveTextContent("name: two");
  });

  it("shows what a client can set before any client of the viewer has reported", async () => {
    const template = {
      entries: [{ key: "workspaces", value: {}, origin: "default", description: "Which KBs a provider receives.", default: "{}" }],
      yaml: "# --- available settings: uncomment a key to set it ---\n# workspaces:\n#   claude: []\n",
    };
    stubApi({ "/settings": () => json({ ...SETTINGS, client_template: template }) });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: "Client" }));
    // workspaces is set per KB: it reads in each KB's section, under its YAML key.
    expect(screen.queryByRole("list", { name: "Client Agents settings" })).toBeNull();
    expect(
      within(screen.getByRole("list", { name: "Client settings of kb-a" })).getByLabelText("Value of workspaces"),
    ).toHaveTextContent("everywhere");
    await user.click(screen.getByRole("button", { name: "YAML" }));
    expect(screen.getByLabelText(".cartographer.yaml")).toHaveTextContent("# workspaces:");
  });

  it("reaches the panel from the rail", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a");
    stubApi({ "/settings": () => json(SETTINGS) });
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole("button", { name: "Settings" }));
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    expect(within(tabs).getByRole("tab", { name: "Client" })).toHaveAttribute("aria-selected", "true");
    expect(window.location.search).toContain("panel=settings");
  });

  it("says so when the settings cannot be read", async () => {
    stubApi({ "/settings": () => json({ error: { code: "internal", message: "internal error" } }, 500) });
    render(<App />);
    expect(await screen.findByRole("button", { name: /retry|try again/i })).toBeInTheDocument();
  });

  it("says a byte count in the unit it fills", () => {
    expect(humanBytes(67108864)).toBe("64 MiB");
    expect(humanBytes(1536)).toBe("1.5 KiB");
    expect(humanBytes(12)).toBe("12 B");
  });

  it("formats lists and empty values", () => {
    expect(formatValue([])).toBe("(none)");
    expect(formatValue(["a", "b"])).toBe("a, b");
    expect(formatValue("")).toBe("(empty)");
    expect(formatValue(false)).toBe("false");
  });
});
