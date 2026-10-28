import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { staticReleases } from "../api/static";
import type { ReleaseEntry, ReleasesView } from "../api/types";
import { withoutTitleLine } from "../components/Releases";
import { json, openPanels, stubApi } from "./fixtures";

const entry = (version: string, over: Partial<ReleaseEntry> = {}): ReleaseEntry => ({
  version,
  name: `v${version}`,
  url: `https://example.com/releases/v${version}`,
  published_at: "2026-09-02T10:00:00Z",
  body: `### Features\n\n* thing in ${version}`,
  truncated: false,
  prerelease: false,
  current: false,
  ...over,
});

const VIEW: ReleasesView = {
  current: "1.2.3",
  latest: "v1.3.0",
  channel: "homebrew",
  upgrade_command: "brew upgrade --cask example/tap/cartographer",
  releases: [entry("1.3.0"), entry("1.2.3", { current: true }), entry("1.2.2", { prerelease: true })],
  releases_url: "https://example.com/releases",
};

function stub(view: ReleasesView | (() => Response) = VIEW) {
  return stubApi({
    "/health": () => json({ version: "1.2.3" }),
    "/releases": typeof view === "function" ? view : () => json(view),
  });
}

async function openFromRail() {
  const user = userEvent.setup();
  render(<App />);
  await user.click(await screen.findByRole("button", { name: /Server version v1\.2\.3/ }));
  return { user, sheet: await screen.findByRole("dialog", { name: "Releases" }) };
}

describe("the release history sheet", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a");
    localStorage.clear();
    sessionStorage.clear();
    openPanels();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("opens from the version in the rail's foot and lists every release, newest first", async () => {
    const fetchMock = stub();
    const { sheet } = await openFromRail();
    expect(sheet).toHaveAttribute("aria-modal", "true");
    const list = await within(sheet).findByRole("list", { name: "Releases, newest first" });
    const headings = within(list).getAllByRole("heading", { level: 3 }).filter((h) => /^v\d/.test(h.textContent ?? ""));
    expect(headings.map((h) => h.textContent)).toEqual(["v1.3.0", "v1.2.3", "v1.2.2"]);
    // A changelog is rendered, not shown as source.
    expect(within(list).getByText("thing in 1.3.0")).toBeInTheDocument();
    // Nothing is fetched until the sheet is opened.
    expect(fetchMock.mock.calls.filter(([u]) => String(u).includes("/releases"))).toHaveLength(1);
  });

  it("does not fetch the history before the version is clicked", async () => {
    const fetchMock = stub();
    render(<App />);
    await screen.findByRole("button", { name: /Server version v1\.2\.3/ });
    expect(fetchMock.mock.calls.some(([u]) => String(u).includes("/releases"))).toBe(false);
  });

  it("marks the release the server runs, and only that one", async () => {
    stub();
    const { sheet } = await openFromRail();
    await within(sheet).findByRole("list", { name: "Releases, newest first" });
    expect(within(sheet).getAllByText("Current")).toHaveLength(1);
    const current = within(sheet).getByRole("heading", { name: "v1.2.3" }).closest("section")!;
    expect(within(current).getByText("Current")).toBeInTheDocument();
    expect(current).toHaveAttribute("aria-current", "true");
    // Every 0.x release carries GitHub's prerelease flag (D82): no badge for it.
    expect(within(sheet).queryByText("Pre-release")).toBeNull();
  });

  it("brings the current release into view only when newer ones precede it", async () => {
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    try {
      stub();
      const first = await openFromRail();
      await within(first.sheet).findByRole("list", { name: "Releases, newest first" });
      await waitFor(() => expect(scroll).toHaveBeenCalledTimes(1));
      expect(scroll.mock.contexts[0]).toBe(within(first.sheet).getByRole("heading", { name: "v1.2.3" }).closest("section"));
      expect(scroll).toHaveBeenCalledWith({ block: "nearest" });
    } finally {
      delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
    }
  });

  it("does not scroll when the current release is already the newest", async () => {
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    try {
      stub({
        current: "1.2.3",
        releases: [entry("1.2.3", { current: true }), entry("1.2.2")],
        releases_url: "https://example.com/releases",
      });
      const { sheet } = await openFromRail();
      await within(sheet).findByRole("list", { name: "Releases, newest first" });
      expect(scroll).not.toHaveBeenCalled();
    } finally {
      delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
    }
  });

  it("shows the upgrade block only when the server names a command", async () => {
    stub();
    const first = await openFromRail();
    const upgrade = await within(first.sheet).findByRole("region", { name: "Upgrade" });
    expect(within(upgrade).getByText("v1.3.0 is available")).toBeInTheDocument();
    expect(within(upgrade).getByText("brew upgrade --cask example/tap/cartographer")).toBeInTheDocument();
    expect(within(upgrade).getByRole("button", { name: "Copy the upgrade command" })).toBeInTheDocument();
  });

  it("has no upgrade block without a command, even with a newer release known", async () => {
    stub({ ...VIEW, upgrade_command: undefined, channel: undefined });
    const { sheet } = await openFromRail();
    await within(sheet).findByRole("list", { name: "Releases, newest first" });
    expect(within(sheet).queryByRole("region", { name: "Upgrade" })).toBeNull();
  });

  it("says why there is no history, and links to GitHub, when the update check is off", async () => {
    stub({ current: "1.2.3", releases: [], unavailable: "disabled", releases_url: "https://example.com/releases" });
    const { sheet } = await openFromRail();
    expect(await within(sheet).findByText(/update check is off on this server/)).toBeInTheDocument();
    expect(within(sheet).queryByRole("list", { name: "Releases, newest first" })?.children.length ?? 0).toBe(0);
    const link = within(sheet).getByRole("link", { name: /All releases on GitHub/ });
    expect(link).toHaveAttribute("href", "https://example.com/releases");
    expect(link).toHaveAttribute("rel", expect.stringContaining("noopener"));
  });

  it("says GitHub could not be reached when there is nothing to show", async () => {
    stub({ current: "1.2.3", releases: [], unavailable: "unreachable", releases_url: "https://example.com/releases" });
    const { sheet } = await openFromRail();
    expect(await within(sheet).findByText(/GitHub could not be reached/)).toBeInTheDocument();
    expect(within(sheet).getByRole("link", { name: /All releases on GitHub/ })).toBeInTheDocument();
  });

  it("serves the last list with a notice when a refresh failed", async () => {
    stub({ ...VIEW, stale: true });
    const { sheet } = await openFromRail();
    expect(await within(sheet).findByText(/this is the last list fetched/)).toBeInTheDocument();
    expect(within(sheet).getByRole("heading", { name: "v1.3.0" })).toBeInTheDocument();
  });

  it("points a cut changelog at GitHub", async () => {
    stub({ ...VIEW, releases: [entry("1.2.3", { current: true, truncated: true }), entry("1.2.2")] });
    const { sheet } = await openFromRail();
    const link = await within(sheet).findByRole("link", { name: /read it on GitHub/ });
    expect(link).toHaveAttribute("href", "https://example.com/releases/v1.2.3");
    expect(within(sheet).getAllByText(/Truncated/)).toHaveLength(1);
  });

  it("fails visibly, with a retry, when the server does not answer", async () => {
    let calls = 0;
    stub(() => {
      calls += 1;
      return calls === 1 ? json({ error: { code: "internal", message: "internal error" } }, 500) : json(VIEW);
    });
    const { user, sheet } = await openFromRail();
    await within(sheet).findByRole("alert");
    await user.click(within(sheet).getByRole("button", { name: "Try again" }));
    expect(await within(sheet).findByRole("heading", { name: "v1.3.0" })).toBeInTheDocument();
  });

  it("closes with Escape and returns focus to the version", async () => {
    stub();
    const { user, sheet } = await openFromRail();
    await within(sheet).findByRole("list", { name: "Releases, newest first" });
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "Releases" })).toBeNull();
    expect(screen.getByRole("button", { name: /Server version v1\.2\.3/ })).toHaveFocus();
  });

  it("is dropped when it only restates the version and the date", () => {
    const body = "## [0.24.0](https://example.com/compare/v0.23.0...v0.24.0) (2026-10-12)\n\n\n### Features\n\n* a thing";
    expect(withoutTitleLine(entry("0.24.0", { body }))).toBe("### Features\n\n* a thing");
  });

  it("is kept when it is about something else, and a body without one is untouched", () => {
    const other = "## Highlights\n\n* a thing";
    expect(withoutTitleLine(entry("0.24.0", { body: other }))).toBe(other);
    expect(withoutTitleLine(entry("0.24.0", { body: "plain text" }))).toBe("plain text");
    // 0.24.0 must not eat the heading of 0.24.01 (a regexp dot is not a dot).
    expect(withoutTitleLine(entry("0.2.0", { body: "## [0x2y0](u) (d)\n\ntext" }))).toBe("## [0x2y0](u) (d)\n\ntext");
  });
});

describe("the static demo", () => {
  it("answers the release history locally: there is no server to ask GitHub", () => {
    const view = staticReleases();
    expect(view.unavailable).toBe("disabled");
    expect(view.releases).toEqual([]);
    expect(view.releases_url).toMatch(/^https:\/\//);
  });
});
