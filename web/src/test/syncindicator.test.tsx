import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SyncIndicator } from "../components/SyncIndicator";
import type { KBStatus } from "../api/types";
import { json } from "./fixtures";

const base: KBStatus = {
  has_remote: true,
  remote_url: "https://example.com/team/kb-a.git",
  push_state: "clean",
  unpushed_commits: 0,
  behind_commits: 0,
  last_push_at: null,
  last_sync_in_at: null,
  branch: "main",
  branch_source: "custom-source",
};

function serve(status: KBStatus | null) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => (status ? json(status) : json({ error: { code: "not_found", message: "not found" } }, 404))),
  );
}

beforeEach(() => vi.unstubAllGlobals());
afterEach(() => vi.unstubAllGlobals());

describe("SyncIndicator (#1092)", () => {
  const cases: [string, KBStatus, string][] = [
    ["ok", base, "On remote"],
    ["attention", { ...base, push_state: "pending", unpushed_commits: 2 }, "2 to push"],
    ["problem", { ...base, push_state: "failed", push_last_error: "rejected" }, "Push failed"],
    ["warning", { ...base, push_state: "no_remote", has_remote: false, remote_url: "" }, "No remote"],
    ["warning", { ...base, push_state: "disabled", has_remote: false }, "Not versioned"],
  ];
  for (const [tone, status, label] of cases) {
    it(`draws "${label}" (${tone}) as a labelled button`, async () => {
      serve(status);
      render(<SyncIndicator kb="kb-a" live={0} />);
      const button = await screen.findByRole("button", { name: `Sync: ${label}` });
      expect(button).toHaveTextContent(label);
      expect(button.className).toContain(`sync__button--${tone}`);
    });
  }

  it("opens a popover with every detail, prints branch_source as received and says what is unknown", async () => {
    serve({ ...base, push_state: "failed", push_last_error: "remote rejected the push" });
    const user = userEvent.setup();
    render(<SyncIndicator kb="kb-a" live={0} />);
    const button = await screen.findByRole("button", { name: "Sync: Push failed" });
    expect(button).toHaveAttribute("aria-expanded", "false");
    await user.click(button);
    const dialog = screen.getByRole("dialog", { name: "Sync details" });
    expect(button).toHaveAttribute("aria-expanded", "true");
    expect(dialog).toHaveTextContent("main");
    expect(dialog).toHaveTextContent("(custom-source)");
    expect(dialog).toHaveTextContent("as of the last fetch");
    expect(dialog).toHaveTextContent("Unknown since the server restarted");
    expect(dialog).toHaveTextContent("Not fetched since the server started");
    expect(dialog).toHaveTextContent("remote rejected the push");
    expect(dialog).toHaveTextContent("https://example.com/team/kb-a.git");
  });

  it("says a KB with no remote is not backed up", async () => {
    serve({ ...base, push_state: "no_remote", has_remote: false, remote_url: "" });
    const user = userEvent.setup();
    render(<SyncIndicator kb="kb-a" live={0} />);
    await user.click(await screen.findByRole("button", { name: "Sync: No remote" }));
    expect(screen.getByRole("dialog")).toHaveTextContent("not backed up");
  });

  it("closes on Escape and returns focus to the button, and on an outside click", async () => {
    serve(base);
    const user = userEvent.setup();
    render(
      <>
        <SyncIndicator kb="kb-a" live={0} />
        <p>elsewhere</p>
      </>,
    );
    const button = await screen.findByRole("button", { name: "Sync: On remote" });
    await user.click(button);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(button).toHaveFocus();
    await user.click(button);
    await user.click(screen.getByText("elsewhere"));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("keeps Escape from reaching the shell's own document handler", async () => {
    serve(base);
    const user = userEvent.setup();
    const shell = vi.fn();
    document.addEventListener("keydown", shell);
    render(<SyncIndicator kb="kb-a" live={0} />);
    await user.click(await screen.findByRole("button", { name: "Sync: On remote" }));
    await user.keyboard("{Escape}");
    document.removeEventListener("keydown", shell);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(shell).not.toHaveBeenCalled();
  });

  it("shows no count rows for a KB without a remote, and the branch without a source", async () => {
    serve({ ...base, push_state: "no_remote", has_remote: false, remote_url: "", branch_source: undefined });
    const user = userEvent.setup();
    render(<SyncIndicator kb="kb-a" live={0} />);
    await user.click(await screen.findByRole("button", { name: "Sync: No remote" }));
    const dialog = screen.getByRole("dialog");
    expect(dialog).not.toHaveTextContent("To push");
    expect(dialog).not.toHaveTextContent("Behind");
    expect(dialog).toHaveTextContent("main");
  });

  it("hides the branch source and the last push and fetch rows for a KB without a remote", async () => {
    serve({ ...base, push_state: "no_remote", has_remote: false, remote_url: "" });
    const user = userEvent.setup();
    render(<SyncIndicator kb="kb-a" live={0} />);
    await user.click(await screen.findByRole("button", { name: "Sync: No remote" }));
    const dialog = screen.getByRole("dialog");
    expect(dialog).not.toHaveTextContent("Last push");
    expect(dialog).not.toHaveTextContent("Last fetch");
    expect(dialog).not.toHaveTextContent("custom-source");
  });

  it("shortens a push error to git's first fatal line, the whole on request", async () => {
    const whole = "Pushing to remote\nfatal: unable to access the remote: connection refused\nfatal: the remote end hung up";
    serve({ ...base, push_state: "failed", push_last_error: whole });
    const user = userEvent.setup();
    render(<SyncIndicator kb="kb-a" live={0} />);
    await user.click(await screen.findByRole("button", { name: "Sync: Push failed" }));
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveTextContent("unable to access the remote: connection refused");
    expect(dialog).not.toHaveTextContent("hung up");
    await user.click(screen.getByRole("button", { name: "Show the whole error" }));
    expect(screen.getByRole("dialog")).toHaveTextContent("hung up");
    expect(screen.getByRole("button", { name: "Show less" })).toBeInTheDocument();
  });

  it("draws nothing on a 404, and nothing for a payload without push_state", async () => {
    serve(null);
    const { container, unmount } = render(<SyncIndicator kb="kb-a" live={0} />);
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(container.querySelector(".sync")).toBeNull();
    unmount();
    serve({ stale_count: 0 });
    const second = render(<SyncIndicator kb="kb-a" live={0} />);
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(second.container.querySelector(".sync")).toBeNull();
  });
});
