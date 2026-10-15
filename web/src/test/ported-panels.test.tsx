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

  describe("Services", () => {
    beforeEach(() => open("services"));

    it("explains what a Service is and which tool fills it when there are none", async () => {
      stubApi();
      render(<App />);
      expect(await screen.findByText("No services documented yet")).toBeInTheDocument();
      expect(screen.getByText(/concept of type Service/)).toBeInTheDocument();
    });

    it("lists the services and opens one in the atlas", async () => {
      stubApi({
        "/services": () =>
          json({ services: [{ id: "infra/api", title: "Public API", status: "active" }, { id: "infra/db" }] }),
      });
      const user = userEvent.setup();
      render(<App />);
      const panel = await screen.findByRole("region", { name: "Services" });
      expect(within(panel).getByText("active")).toBeInTheDocument();
      await user.click(within(panel).getByRole("button", { name: "Public API" }));
      await waitFor(() => expect(window.location.search).toContain("concept=infra%2Fapi"));
      expect(window.location.search).not.toContain("panel=services");
    });
  });

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
      await within(panel).findByText(/No open contradictions/);
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
      expect(await screen.findByText("Who is looking")).toBeInTheDocument();
      expect(screen.getByText("reader")).toBeInTheDocument();
      expect(screen.getByText(/can read the maps infra/)).toBeInTheDocument();
    });

    it("leaves the section out when whoami fails", async () => {
      stubApi({ "/settings": () => json(settings) });
      render(<App />);
      await screen.findByRole("region", { name: "Settings" });
      expect(screen.queryByText("Who is looking")).not.toBeInTheDocument();
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
