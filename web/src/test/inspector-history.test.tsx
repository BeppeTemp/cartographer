import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Concept } from "../api/types";
import { Inspector } from "../components/Inspector";
import { json, stubApi } from "./fixtures";

const concept: Concept = {
  id: "infra/a",
  title: "Alpha",
  collection: "infra",
  frontmatter: {},
  body: "A service.",
  body_bytes: 10,
  outline: [],
  content_hash: "abc123",
  outbound: [],
  inbound: [],
  broken: [],
};

const history = (extra = {}) =>
  json({
    id: "infra/a",
    revisions: [
      { sha: "1234567890abcdef", at: "2026-01-02T10:00:00Z", author: "user", subject: "Describe the service", path: "infra/a.md" },
      { sha: "fedcba0987654321", at: "2026-01-01T10:00:00Z", author: "agent-a", subject: "Create the concept", path: "infra/a.md" },
    ],
    truncated: false,
    ...extra,
  });

function setup(overrides: Record<string, () => Response>) {
  const fetchMock = stubApi(overrides);
  vi.stubGlobal("fetch", fetchMock);
  render(
    <Inspector
      kb="kb-a"
      conceptId="infra/a"
      concept={concept}
      error={null}
      loading={false}
      findings={[]}
      onNavigate={() => {}}
      onPreview={() => {}}
      onClose={() => {}}
    />,
  );
  return fetchMock;
}

const urls = (m: ReturnType<typeof stubApi>) => m.mock.calls.map((c) => String(c[0]));
const notExpanded = () => json({ error: { code: "invalid_request", message: "not expanded" } }, 400);

describe("the Inspector History tab", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("requests nothing until the tab is opened, then lists the revisions", async () => {
    const m = setup({ "/history": () => history(), "/assets": notExpanded });
    expect(urls(m).some((u) => u.includes("/history"))).toBe(false);
    await userEvent.setup().click(screen.getByRole("tab", { name: "History" }));
    expect(await screen.findByText("Describe the service")).toBeInTheDocument();
    expect(screen.getByText("Create the concept")).toBeInTheDocument();
    expect(screen.getByText("1234567")).toBeInTheDocument();
    expect(screen.getByText("agent-a")).toBeInTheDocument();
    expect(screen.queryByText("Assets")).not.toBeInTheDocument();
  });

  it("says when the list is truncated and shows the server's note", async () => {
    setup({ "/history": () => history({ truncated: true, note: "no git history here" }), "/assets": notExpanded });
    await userEvent.setup().click(screen.getByRole("tab", { name: "History" }));
    expect(await screen.findByText("Older revisions are not shown.")).toBeInTheDocument();
    expect(screen.getByText("no git history here")).toBeInTheDocument();
  });

  it("lists the assets of an expanded concept", async () => {
    setup({
      "/history": () => history(),
      "/assets": () =>
        json({ id: "infra/a", assets: [{ path: "diagram.png", size: 2048, sha256: "x", executable: false }] }),
    });
    await userEvent.setup().click(screen.getByRole("tab", { name: "History" }));
    expect(await screen.findByText("diagram.png")).toBeInTheDocument();
    expect(screen.getByText("2.0 KB")).toBeInTheDocument();
    expect(screen.getByText("png")).toBeInTheDocument();
  });
});
