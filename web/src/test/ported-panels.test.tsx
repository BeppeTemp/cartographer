import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { json, stubApi } from "./fixtures";

function open(panel: string) {
  window.history.replaceState(null, "", `/ui/?kb=kb-a&panel=${panel}`);
  localStorage.clear();
  localStorage.setItem("cartographer.panel.rail", "0");
  sessionStorage.clear();
}

describe("the ported panels", () => {
  afterEach(() => vi.unstubAllGlobals());

  describe("Health git conflicts", () => {
    beforeEach(() => open("health"));

    it("shows the section only when there are conflicts", async () => {
      stubApi({
        "/conflicts": () =>
          json([{ concept_id: "infra/a", path: "data/infra/a.md", files: ["data/infra/a.md"] }]),
      });
      render(<App />);
      const panel = await screen.findByRole("region", { name: "Health" });
      expect(await within(panel).findByText("Git conflicts")).toBeInTheDocument();
      expect(within(panel).getByText("infra/a")).toBeInTheDocument();
      expect(within(panel).getByText(/git_conflict_resolve/)).toBeInTheDocument();
    });

    it("leaves the section out when there are none or the caller cannot see them", async () => {
      stubApi();
      render(<App />);
      const panel = await screen.findByRole("region", { name: "Health" });
      const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
      await waitFor(() => expect(fetchMock.mock.calls.some((c) => String(c[0]).includes("/conflicts"))).toBe(true));
      await within(panel).findByRole("heading", { level: 1 });
      expect(within(panel).queryByText("Git conflicts")).not.toBeInTheDocument();
    });
  });

  describe("Activity audit log", () => {
    beforeEach(() => open("activity"));

    it("lists the entries when the route answers", async () => {
      stubApi({
        "/audit": () =>
          json({
            enabled: true,
            entries: [{ at: "2026-09-01T10:00:00Z", tool: "concept_write", principal: "agent-a", outcome: "ok", commit_sha: "1234567890" }],
          }),
      });
      render(<App />);
      expect(await screen.findByText("Audit log")).toBeInTheDocument();
      expect(screen.getByText("concept_write")).toBeInTheDocument();
      expect(screen.getByText(/agent-a/)).toBeInTheDocument();
    });

    it("is hidden on a 404", async () => {
      stubApi();
      render(<App />);
      await screen.findByText("Journal");
      expect(screen.queryByText("Audit log")).not.toBeInTheDocument();
    });
  });

  describe("Settings who is looking", () => {
    beforeEach(() => open("settings"));
    const settings = { version: "v1", server: [], kbs: [] };

    it("says the principal and its scopes per KB in words", async () => {
      stubApi({
        "/settings": () => json(settings),
        "/whoami": () =>
          json({ id: "reader", admin: false, permissions: [{ kb: "kb-a", write: false, maps: ["infra"] }] }),
      });
      render(<App />);
      const who = await screen.findByLabelText("Who is looking");
      expect(within(who).getByText("reader")).toBeInTheDocument();
      expect(who).toHaveTextContent(/in kb-a you can read the maps infra/);
    });

    it("says a rule that cannot write artifacts", async () => {
      stubApi({
        "/settings": () => json(settings),
        "/whoami": () =>
          json({ id: "worker-a", admin: false, permissions: [{ kb: "kb-a", write: true, artifacts: "r" }] }),
      });
      render(<App />);
      const who = await screen.findByLabelText("Who is looking");
      expect(who).toHaveTextContent(/can read and write every map, not skills, agents or hooks/);
    });

    it("leaves the section out when whoami fails", async () => {
      stubApi({ "/settings": () => json(settings) });
      render(<App />);
      await screen.findByRole("region", { name: "Settings" });
      expect(screen.queryByLabelText("Who is looking")).not.toBeInTheDocument();
    });
  });

  describe("Supersede chain", () => {
    beforeEach(() => open("atlas"));

    it("links the successor and the predecessors in the Links tab", async () => {
      window.history.replaceState(null, "", "/ui/?kb=kb-a&concept=infra%2Fa");
      stubApi({
        "/concept": () =>
          json({
            id: "infra/a",
            title: "Alpha",
            collection: "infra",
            frontmatter: {},
            body: "x",
            body_bytes: 1,
            outline: [],
            content_hash: "h",
            outbound: [],
            inbound: [],
            broken: [],
            supersedes: ["infra/old"],
            superseded_by: "infra/new",
          }),
      });
      const user = userEvent.setup();
      render(<App />);
      await user.click(await screen.findByRole("tab", { name: /Links/ }));
      const section = await screen.findByRole("region", { name: "Supersede chain" });
      await user.click(within(section).getByRole("button", { name: /Replaced by/ }));
      await waitFor(() => expect(window.location.search).toContain("concept=infra%2Fnew"));
    });
  });
});
