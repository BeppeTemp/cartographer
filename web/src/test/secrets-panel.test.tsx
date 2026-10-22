import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { generateIdentity } from "age-encryption";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { json, stubApi } from "./fixtures";
import { FIXTURE_FILE, FIXTURE_KEY } from "./secretsFixture";

/** The Secrets panel: structure without a key, values decrypted in the page with one. */
describe("the Secrets panel", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a&panel=secrets");
    localStorage.clear();
    localStorage.setItem("cartographer.panel.rail", "0");
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  function stub(secrets: boolean) {
    const fetchMock = stubApi();
    const route = fetchMock.getMockImplementation()!;
    const requested: { url: string; body: unknown }[] = [];
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      requested.push({ url, body: init?.body });
      if (url.endsWith("/kbs")) {
        return json({ kbs: [{ name: "kb-a", status: "normal", ready: true, artifacts: true, secrets }] });
      }
      if (url.includes("/secrets")) {
        const entries = FIXTURE_FILE.entries.map((e) =>
          e.pointer === "/demo/username" ? { ...e, referenced_by: ["infra/a"] } : e,
        );
        return json({ files: [{ ...FIXTURE_FILE, entries }] });
      }
      return route(url);
    });
    return requested;
  }

  it("is not offered without the right, and a link to it falls back to the atlas", async () => {
    stub(false);
    render(<App />);
    expect(await screen.findByRole("button", { name: "Health" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Secrets" })).not.toBeInTheDocument();
    await waitFor(() => expect(window.location.search).not.toContain("panel=secrets"));
  });

  it("tells an empty KB where a secrets file goes and links the docs (D864)", async () => {
    const kind = {
      kind: "secret",
      title: "Secrets",
      kb_path: "secrets/<name>.sops.yaml",
      doc: "docs/skills-services-secrets.md#sops-files",
      kb_only: true,
    };
    const fetchMock = stubApi({ "/secrets": () => json({ files: [], kind }) });
    const route = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (url: string) =>
      url.endsWith("/kbs")
        ? json({ kbs: [{ name: "kb-a", status: "normal", ready: true, artifacts: true, secrets: true }] })
        : route(url),
    );
    render(<App />);
    expect(await screen.findByText("This KB keeps no secrets.")).toBeInTheDocument();
    expect(screen.getByText("secrets/<name>.sops.yaml").tagName).toBe("CODE");
    const link = screen.getByRole("link", { name: "Docs" });
    expect(link).toHaveAttribute(
      "href",
      "https://github.com/BeppeTemp/cartographer/blob/main/docs/skills-services-secrets.md#sops-files",
    );
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("keeps the plain empty state against a server that sends no kind", async () => {
    const fetchMock = stubApi({ "/secrets": () => json({ files: [] }) });
    const route = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (url: string) =>
      url.endsWith("/kbs")
        ? json({ kbs: [{ name: "kb-a", status: "normal", ready: true, artifacts: true, secrets: true }] })
        : route(url),
    );
    render(<App />);
    expect(await screen.findByText("This KB keeps no secrets.")).toBeInTheDocument();
    expect(screen.getByText(/Files named secrets\/\*\.sops\.yaml appear here/)).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Docs" })).not.toBeInTheDocument();
  });

  it("lists the structure masked, then decrypts with a pasted key without sending it anywhere", { timeout: 20_000 }, async () => {
    const requested = stub(true);
    const user = userEvent.setup();
    render(<App />);
    // The values open in the file's detail, as an artifact's files do.
    await user.click(await screen.findByRole("button", { name: /demo\.sops\.yaml/ }));
    expect(await screen.findByText("secrets/demo.sops.yaml")).toBeInTheDocument();
    expect(screen.getByLabelText("Value of /demo/ports/1")).toHaveTextContent("••••••••");
    expect(screen.getByText(/^used by/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reveal value of /demo/username" })).toBeDisabled();

    const field = screen.getByLabelText("Age secret key");
    await user.type(field, await generateIdentity());
    await user.click(screen.getByRole("button", { name: "Decrypt" }));
    // Decryption runs real crypto: under a loaded test run it can take longer
    // than findBy's default second.
    expect(await screen.findByRole("alert", undefined, { timeout: 5000 })).toHaveTextContent(/not one of their recipients/);
    expect(screen.getByLabelText("Value of /demo/username")).toHaveTextContent("••••••••");

    await user.clear(field);
    await user.type(field, FIXTURE_KEY);
    await user.click(screen.getByRole("button", { name: "Decrypt" }));
    // The button is there, disabled, before the values are open: wait for it to
    // be enabled, or the click lands on a disabled button while decrypting.
    const reveal = screen.getByRole("button", { name: "Reveal value of /demo/username" });
    await waitFor(() => expect(reveal).toBeEnabled(), { timeout: 5000 });
    await user.click(reveal);
    expect(screen.getByLabelText("Value of /demo/username")).toHaveTextContent("fake-user");
    expect(screen.getByLabelText("Value of /demo/password")).toHaveTextContent("••••••••");
    expect(screen.queryByLabelText("Age secret key")).not.toBeInTheDocument();

    // The key never reaches the network, the URL or the storages.
    for (const r of requested) {
      expect(r.url).not.toContain("AGE-SECRET-KEY");
      expect(String(r.body ?? "")).not.toContain("AGE-SECRET-KEY");
    }
    expect(window.location.href).not.toContain("AGE-SECRET-KEY");
    expect(JSON.stringify({ ...localStorage })).not.toContain("AGE-SECRET-KEY");
    expect(JSON.stringify({ ...sessionStorage })).not.toContain("AGE-SECRET-KEY");

    await user.click(screen.getByRole("button", { name: "Lock" }));
    expect(screen.getByLabelText("Value of /demo/username")).toHaveTextContent("••••••••");
  });
});
