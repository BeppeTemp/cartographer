import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TopBar } from "../components/TopBar";
import { json, stubApi } from "./fixtures";

const props = {
  kbs: [],
  activeKB: null,
  theme: "dark" as const,
  offline: false,
  onKBChange: () => {},
  onThemeChange: () => {},
  onOpenPalette: () => {},
};

const branding = {
  company: "Example Co",
  logo_light: "logo-light.svg",
  logo_dark: "logo-dark.svg",
  version: "abc123",
};

beforeEach(() => {
  document.head.querySelectorAll("#branding-favicon").forEach((n) => n.remove());
  localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("the Atlas wearing a branding", () => {
  it("changes nothing when the server has none (404)", async () => {
    const fetchMock = stubApi();
    render(<TopBar {...props} />);
    await waitFor(() => expect(fetchMock.mock.calls.some((c) => String(c[0]).endsWith("/branding"))).toBe(true));
    expect(screen.getByText("Cartographer")).toBeTruthy();
    expect(document.querySelector(".topbar__logo")).toBeNull();
  });

  it("puts the logo before the Cartographer mark, and leaves the favicon alone", async () => {
    stubApi({ "/branding": () => json(branding) });
    render(<TopBar {...props} />);
    const logos = await waitFor(() => {
      const found = document.querySelectorAll<HTMLImageElement>(".topbar__logo");
      expect(found.length).toBe(2);
      return found;
    });
    expect(logos[0]?.alt).toBe("Example Co");
    // The logos are data: URLs, cached for the next load's first paint.
    expect(logos[0]?.getAttribute("src")).toMatch(/^data:/);
    expect(logos[1]?.getAttribute("src")).toMatch(/^data:/);
    expect(screen.getByText("Cartographer")).toBeTruthy();
    // the logo leads the brand button, before the Cartographer mark and word
    const brand = logos[0]?.parentElement as HTMLElement;
    expect(brand.firstElementChild).toBe(logos[0]);
    expect(brand.querySelector(".topbar__mark")).not.toBeNull();
    expect(document.getElementById("branding-stylesheet")).toBeNull();
    expect(document.getElementById("branding-favicon")).toBeNull();
    expect(JSON.parse(localStorage.getItem("cartographer.branding") ?? "{}").favicon).toBeUndefined();
    expect(JSON.parse(localStorage.getItem("cartographer.branding") ?? "{}").version).toBe("abc123");
  });

  it("shows the cached logo with the first paint, before the server answers", async () => {
    localStorage.setItem(
      "cartographer.branding",
      JSON.stringify({ company: "Example Co", light: "data:image/png;base64,AA", dark: "data:image/png;base64,BB", version: "abc123" }),
    );
    stubApi({ "/branding": () => new Promise<Response>(() => {}) as never });
    render(<TopBar {...props} />);
    const logos = document.querySelectorAll<HTMLImageElement>(".topbar__logo");
    expect(logos.length).toBe(2);
    expect(logos[0]?.getAttribute("src")).toBe("data:image/png;base64,AA");
  });

  it("removes a favicon link an earlier branding left, and ignores a cached favicon", async () => {
    const stale = document.createElement("link");
    stale.id = "branding-favicon";
    stale.rel = "icon";
    stale.href = "data:image/png;base64,ZZ";
    document.head.appendChild(stale);
    localStorage.setItem(
      "cartographer.branding",
      JSON.stringify({ company: "Example Co", light: "data:image/png;base64,AA", dark: "data:image/png;base64,AA", version: "abc123", favicon: "data:image/png;base64,ZZ" }),
    );
    stubApi({ "/branding": () => json(branding) });
    render(<TopBar {...props} />);
    expect(document.getElementById("branding-favicon")).toBeNull();
    await waitFor(() => expect(document.querySelectorAll(".topbar__logo").length).toBe(2));
    expect(document.getElementById("branding-favicon")).toBeNull();
  });

  it("drops the cached logo when the server has no branding any more", async () => {
    localStorage.setItem(
      "cartographer.branding",
      JSON.stringify({ company: "Example Co", light: "data:image/png;base64,AA", dark: "data:image/png;base64,AA", version: "old" }),
    );
    stubApi();
    render(<TopBar {...props} />);
    await waitFor(() => expect(document.querySelector(".topbar__logo")).toBeNull());
    expect(localStorage.getItem("cartographer.branding")).toBeNull();
  });

  it("ignores a cache whose dark logo or company is not valid", async () => {
    const stored = (v: object) => localStorage.setItem("cartographer.branding", JSON.stringify(v));
    stubApi({ "/branding": () => new Promise<Response>(() => {}) as never });
    stored({ company: "Example Co", light: "data:image/png;base64,AA", dark: "javascript:alert(1)", version: "v" });
    const first = render(<TopBar {...props} />);
    expect(document.querySelector(".topbar__logo")).toBeNull();
    first.unmount();
    stored({ light: "data:image/png;base64,AA", version: "v" });
    render(<TopBar {...props} />);
    expect(document.querySelector(".topbar__logo")).toBeNull();
  });

  it("stores the dark logo only when it differs from the light one", async () => {
    stubApi({ "/branding": () => json({ ...branding, logo_dark: branding.logo_light }) });
    render(<TopBar {...props} />);
    await waitFor(() => expect(document.querySelectorAll(".topbar__logo").length).toBe(2));
    const cached = JSON.parse(localStorage.getItem("cartographer.branding") ?? "{}");
    expect(cached.light).toMatch(/^data:/);
    expect(cached.dark).toBeUndefined();
  });
});
