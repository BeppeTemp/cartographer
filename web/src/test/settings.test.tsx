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
      entries: [{ key: "kbs[].write_gate", value: "error", origin: "file", description: "Refuse a write.", default: "off" }],
      git: { has_remote: true, sync: true, push_state: "idle" },
    },
  ],
};

describe("the Settings panel", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=settings");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("lists each key with its value, origin and restart marker, and never a secret", async () => {
    stubApi({ "/settings": () => json(SETTINGS) });
    render(<App />);
    const server = await screen.findByRole("list", { name: "Server settings" });
    const sync = within(server).getByText("git.sync").closest("li")!;
    expect(within(sync).getByText("env")).toBeInTheDocument();
    expect(within(sync).getByText("restart required")).toBeInTheDocument();
    expect(within(sync).getByLabelText("Value of git.sync")).toHaveTextContent("false");
    const auto = within(server).getByText("git.autocommit").closest("li")!;
    expect(within(auto).getByText("default")).toBeInTheDocument();
    const tokens = within(server).getByText("auth.tokens").closest("li")!;
    expect(within(tokens).getByLabelText("Value of auth.tokens")).toHaveTextContent("(set)");
    expect(within(tokens).getByText("file")).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "KB kb-a settings" })).toBeInTheDocument();
    expect(screen.getByText(/v1\.3\.0 is available/)).toBeInTheDocument();
  });

  it("reaches the panel from the rail", async () => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a");
    stubApi({ "/settings": () => json(SETTINGS) });
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole("button", { name: "Settings" }));
    expect(await screen.findByRole("list", { name: "Server settings" })).toBeInTheDocument();
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
