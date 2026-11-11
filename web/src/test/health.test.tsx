import { render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { componentRow, componentsAnswer, json, stubApi } from "./fixtures";

/** Health (D338, D704) through the App: what was found, what is checked, and nothing that writes. */
describe("the Health panel", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=health");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  // Health shows lint problems only (D947): the doctor's proposals and the
  // observations are decisions, listed elsewhere, and Health does not even ask.
  it("does not list the review's proposals beside the lint problems", async () => {
    stubApi({
      "/maintenance/review": () =>
        json({
          items: [{ kind: "duplicate_candidate", concepts: ["infra/a", "infra/b"], evidence: "two titles", suggested_action: "", weight: 1 }],
        }),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    await within(panel).findByRole("heading", { level: 1 });
    expect(within(panel).queryByText("Possible duplicates")).not.toBeInTheDocument();
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    expect(fetchMock.mock.calls.map((c) => String(c[0])).filter((u) => u.includes("/maintenance/review"))).toEqual([]);
  });

  it("lands the old Maintenance and Observatory links on Health", async () => {
    stubApi();
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=maintenance");
    render(<App />);
    expect(await screen.findByRole("region", { name: "Health" })).toBeInTheDocument();
  });

  it("only ever reads", async () => {
    stubApi();
    render(<App />);
    await screen.findByRole("region", { name: "Health" });
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    for (const call of fetchMock.mock.calls) {
      const init = call[1] as RequestInit | undefined;
      expect(init?.method ?? "GET").toBe("GET");
    }
  });

  const NOT_FOUND = () => json({ error: { code: "not_found", message: "not found" } }, 404);
  const summary = (repair: object) => () =>
    json({ last_doctor: "2026-01-01", next_doctor: "2026-01-15", maintenance: { repair } });

  it("says which surveyor maintains the KB, who repairs and its doctor dates, only when a live surveyor names it", async () => {
    stubApi({
      "/components": () =>
        json(componentsAnswer(componentRow({ declared: true }, [{ id: "w-1" }, { id: "w-2", state: "running" }]))),
      "/maintenance/summary": summary({ owner: "surveyor", state: "surveyor", by: "team-a", since: "2026-01-01T00:00:00Z" }),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    const line = await within(panel).findByText(/Maintained by/);
    expect(line).toHaveTextContent(
      "Maintained by team-a (2 replicas, running) · repairs by team-a · last doctor 2026-01-01 · next due 2026-01-15",
    );
  });

  it("names Meridian as the repairer while the server keeps the repair", async () => {
    stubApi({
      "/components": () => json(componentsAnswer(componentRow({}, [{}]))),
      "/maintenance/summary": summary({ owner: "server", state: "server" }),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    expect(await within(panel).findByText(/Maintained by/)).toHaveTextContent("repairs by Meridian · last doctor");
  });

  it("shows no maintainer line without a surveyor, or when the route fails", async () => {
    stubApi();
    const { unmount } = render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    await within(panel).findByRole("heading", { level: 1 });
    expect(within(panel).queryByText(/Maintained by/)).not.toBeInTheDocument();
    unmount();
    stubApi({ "/components": NOT_FOUND });
    render(<App />);
    const again = await screen.findByRole("region", { name: "Health" });
    await within(again).findByRole("heading", { level: 1 });
    expect(within(again).queryByText(/Maintained by/)).not.toBeInTheDocument();
  });

  it("names every perimeter when two maintain the KB", async () => {
    stubApi({
      "/components": () =>
        json(componentsAnswer(componentRow({ name: "team-b" }, [{ id: "w-1" }]), componentRow({ name: "team-a" }, [{ id: "w-2" }]))),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    const line = await within(panel).findByText(/Maintained by/);
    expect(line).toHaveTextContent("Maintained by team-b (1 replica, idle), team-a (1 replica, idle)");
  });

  describe("an expected surveyor", () => {
    const silent = (over: object = {}) =>
      componentRow({ declared: true, state: "silent", last_seen_at: new Date(Date.now() - 3 * 3600_000).toISOString(), ...over });

    it("is warned about when silent, as a notice and not as a finding", async () => {
      stubApi({ "/components": () => json(componentsAnswer(silent())) });
      render(<App />);
      const panel = await screen.findByRole("region", { name: "Health" });
      const notice = await within(panel).findByRole("status");
      expect(notice).toHaveTextContent("Surveyor team-a is expected and has not reported; last seen 3 h ago.");
      expect(notice).not.toHaveTextContent("Meridian is repairing");
      expect(within(panel).queryByText(/Maintained by/)).not.toBeInTheDocument();
    });

    it("says the server repairs meanwhile in the fallback state", async () => {
      stubApi({
        "/components": () => json(componentsAnswer(silent({ last_seen_at: null }))),
        "/maintenance/summary": summary({ owner: "server", state: "fallback", by: "team-a", since: "2026-01-01T00:00:00Z" }),
      });
      render(<App />);
      const panel = await screen.findByRole("region", { name: "Health" });
      const notice = await within(panel).findByRole("status");
      expect(notice).toHaveTextContent("Surveyor team-a is expected and has not reported. Meridian is repairing this KB meanwhile.");
    });

    it("is warned about when degraded, next to the replicas that are up", async () => {
      stubApi({
        "/components": () =>
          json(componentsAnswer(componentRow({ declared: true, state: "degraded", min_replicas: 2 }, [{ id: "w-1" }]))),
      });
      render(<App />);
      const panel = await screen.findByRole("region", { name: "Health" });
      expect(await within(panel).findByRole("status")).toHaveTextContent(
        "Surveyor team-a is expected to run 2 replicas and 1 is reporting.",
      );
      expect(within(panel).getByText(/Maintained by/)).toBeInTheDocument();
    });

    it("adds the fallback sentence only to the silent notice the repair names", async () => {
      stubApi({
        "/components": () =>
          json(
            componentsAnswer(
              componentRow({ name: "team-a", declared: true, state: "degraded", min_replicas: 2 }, [{}]),
              silent({ name: "team-b" }),
              silent({ name: "team-c" }),
            ),
          ),
        "/maintenance/summary": summary({ owner: "server", state: "fallback", by: "team-b" }),
      });
      render(<App />);
      const panel = await screen.findByRole("region", { name: "Health" });
      await within(panel).findAllByRole("status");
      const text = (name: RegExp) => within(panel).getAllByRole("status").find((n) => name.test(n.textContent ?? ""))!;
      expect(text(/team-a/)).not.toHaveTextContent("Meridian is repairing");
      expect(text(/team-b/)).toHaveTextContent("Meridian is repairing this KB meanwhile.");
      expect(text(/team-c/)).not.toHaveTextContent("Meridian is repairing");
    });

    it("says nothing while starting, or when it is up", async () => {
      stubApi({
        "/components": () =>
          json(
            componentsAnswer(
              componentRow({ declared: true, state: "starting" }),
              componentRow({ name: "team-b", declared: true }, [{}]),
            ),
          ),
      });
      render(<App />);
      const panel = await screen.findByRole("region", { name: "Health" });
      await within(panel).findByText(/Maintained by/);
      expect(within(panel).queryByRole("status")).not.toBeInTheDocument();
    });

    it("lights the rail's Health mark, which stays dark without it", async () => {
      stubApi({ "/components": () => json(componentsAnswer(silent())) });
      window.history.replaceState(null, "", "/ui/?kb=kb-a");
      const { unmount } = render(<App />);
      expect(await screen.findByRole("button", { name: "Health, problems to decide" })).toBeInTheDocument();
      unmount();
      stubApi();
      render(<App />);
    });
  });

  it("names the surveyor that holds the repair where a finding fixes itself", async () => {
    stubApi({
      "/lint": () =>
        json({
          findings: [{ path: "infra/a.md", concept: "infra/a", check: "broken_link", message: "missing target", handler: "auto" }],
          count: 1,
          by_check: { broken_link: 1 },
        }),
      "/maintenance/summary": summary({ owner: "surveyor", state: "surveyor", by: "team-a" }),
    });
    render(<App />);
    const panel = await screen.findByRole("region", { name: "Health" });
    expect(await within(panel).findByTitle("the team-a Surveyor fixes it by itself at its next run")).toBeInTheDocument();
  });

  // The page must look as it did before the registry when nothing is expected
  // and nothing is live: an empty answer and a 404 render the same DOM.
  it("renders the same Health DOM for an empty components answer and for a 404", async () => {
    const dom = async (components: () => Response) => {
      stubApi({ "/components": components });
      window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=health");
      const { unmount } = render(<App />);
      const panel = await screen.findByRole("region", { name: "Health" });
      await within(panel).findByRole("heading", { level: 1 });
      await new Promise((r) => setTimeout(r, 30));
      const html = panel.outerHTML;
      unmount();
      return html;
    };
    // The rail's mark stays dark in both cases (the rail is on the Atlas page).
    const railDark = async (components: () => Response) => {
      stubApi({ "/components": components });
      window.history.replaceState(null, "", "/ui/?kb=kb-a");
      const { unmount } = render(<App />);
      expect(await screen.findByRole("button", { name: "Health" })).toBeInTheDocument();
      unmount();
    };
    await railDark(() => json(componentsAnswer()));
    await railDark(NOT_FOUND);
    const empty = await dom(() => json(componentsAnswer()));
    const missing = await dom(NOT_FOUND);
    expect(missing).toBe(empty);
    expect(empty).not.toMatch(/Maintained by|health__notice/);
  });
});
