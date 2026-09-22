import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Markdown } from "../components/Markdown";
import { clearToken, hasToken, restoreToken, setToken } from "../api/client";

describe("markdown is rendered as untrusted content", () => {
  it("does not execute or emit raw HTML from a concept body", () => {
    const body = [
      "# Heading",
      "",
      "<script>window.__pwned = true;</script>",
      "",
      '<img src="x" onerror="window.__pwned = true">',
      "",
      "<div onclick=\"window.__pwned = true\">click</div>",
    ].join("\n");

    const { container } = render(<Markdown>{body}</Markdown>);

    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("img")).toBeNull();
    expect(container.innerHTML).not.toContain("onerror");
    expect(container.innerHTML).not.toContain("onclick");
    expect((window as unknown as { __pwned?: boolean }).__pwned).toBeUndefined();
    // The heading still renders: disabling HTML must not disable Markdown.
    expect(screen.getByRole("heading", { name: "Heading" })).toBeInTheDocument();
  });

  it("marks external links noopener noreferrer and leaves internal ones alone", () => {
    const { container } = render(
      <Markdown>{"[out](https://example.invalid/page) and [in](#section)"}</Markdown>,
    );
    const external = container.querySelector('a[href="https://example.invalid/page"]');
    expect(external).toHaveAttribute("rel", expect.stringContaining("noopener"));
    expect(external).toHaveAttribute("rel", expect.stringContaining("noreferrer"));
    expect(external).toHaveAttribute("target", "_blank");

    const internal = container.querySelector('a[href="#section"]');
    expect(internal).not.toHaveAttribute("target");
  });

  it("never fetches a remote image", () => {
    const { container } = render(<Markdown>{"![alt](https://example.invalid/pixel.gif)"}</Markdown>);
    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByRole("img", { name: "alt" })).toBeInTheDocument();
  });
});

describe("the bearer token", () => {
  beforeEach(() => {
    clearToken();
    localStorage.clear();
    sessionStorage.clear();
  });

  it("stays in memory when the viewer does not ask to be remembered", () => {
    setToken("secret-token");
    expect(hasToken()).toBe(true);
    expect(sessionStorage.getItem("cartographer.token")).toBeNull();
    expect(JSON.stringify(localStorage)).not.toContain("secret-token");
  });

  it("goes to sessionStorage, never localStorage, when remembered for the tab", () => {
    setToken("secret-token", true);
    expect(sessionStorage.getItem("cartographer.token")).toBe("secret-token");
    expect(JSON.stringify(localStorage)).not.toContain("secret-token");
  });

  it("is cleared from every store on sign-out", () => {
    setToken("secret-token", true);
    clearToken();
    expect(hasToken()).toBe(false);
    expect(sessionStorage.getItem("cartographer.token")).toBeNull();
  });

  it("survives a storage accessor that throws", () => {
    const spy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("denied");
    });
    expect(() => setToken("secret-token", true)).not.toThrow();
    expect(hasToken()).toBe(true);
    spy.mockRestore();
  });

  it("restores nothing when the tab has no stored token", () => {
    expect(restoreToken()).toBeNull();
  });

  it("keeps a token typed without 'remember' across the boot that follows sign-in", () => {
    // The sign-in reboots the app, and the boot calls restoreToken: an empty
    // tab store must not erase the token that was just entered.
    setToken("secret-token");
    expect(restoreToken()).toBe("secret-token");
    expect(hasToken()).toBe(true);
  });

  it("travels in the Authorization header, never in the URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ kbs: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    setToken("secret-token");

    const { fetchKBs } = await import("../api/client");
    await fetchKBs();

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).not.toContain("secret-token");
    expect((init.headers as Record<string, string>).Authorization).toBe("Bearer secret-token");
    vi.unstubAllGlobals();
  });
});
