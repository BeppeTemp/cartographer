import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { ApiError, clearToken } from "../api/client";
import { ErrorState } from "../components/States";
import { json, stubApi } from "./fixtures";

describe("ErrorState", () => {
  it("shows the HTTP status line for a server error", () => {
    render(<ErrorState error={new ApiError(500, "internal", "boom")} />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByText("HTTP 500 · internal")).toBeInTheDocument();
  });

  it("shows no status line when the server is unreachable", () => {
    render(<ErrorState error={new ApiError(0, "offline", "gone")} />);
    expect(screen.getByText("Server unreachable")).toBeInTheDocument();
    expect(screen.queryByText(/HTTP/)).toBeNull();
  });
});

describe.each([
  ["work", "/work", "Work list unavailable", { total: 0, open_concepts: 0, by_status: {}, by_map: {}, open_items: 0, entries: [] }],
  ["activity", "/changes", "History unavailable", { concepts: [], truncated: false }],
])("the %s page on a failed fetch", (panel, path, title, ok) => {
  beforeEach(() => {
    window.history.replaceState(null, "", `/ui/?kb=kb-a&panel=${panel}`);
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("shows an ErrorState and retries", async () => {
    let calls = 0;
    stubApi({
      [path]: () => (calls++ === 0 ? json({ error: { code: "internal", message: "boom" } }, 500) : json(ok)),
    });
    const user = userEvent.setup();
    render(<App />);
    expect(await screen.findByText("Server error")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(title);
    expect(screen.queryByText(/boom/)).toBeNull();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await vi.waitFor(() => expect(screen.queryByText("Server error")).toBeNull());
    expect(calls).toBe(2);
  });
});

describe("the token prompt", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/");
    localStorage.clear();
    sessionStorage.clear();
    clearToken();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("does not claim a rejection on the first, tokenless 401", async () => {
    stubApi({ "/kbs": () => json({ error: { code: "unauthorized", message: "no" } }, 401) });
    render(<App />);
    expect(await screen.findByLabelText(/bearer token/i)).toBeInTheDocument();
    expect(screen.queryByText(/not accepted/i)).toBeNull();
  });

  it("says so when a submitted token is refused", async () => {
    stubApi({ "/kbs": () => json({ error: { code: "unauthorized", message: "no" } }, 401) });
    const user = userEvent.setup();
    render(<App />);
    await user.type(await screen.findByLabelText(/bearer token/i), "wrong");
    await user.click(screen.getByRole("button", { name: /open the atlas/i }));
    expect(await screen.findByText(/not accepted/i)).toBeInTheDocument();
  });
});
