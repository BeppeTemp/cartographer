import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { generateSnapshot, json, stubApi, openPanels } from "./fixtures";

/** Selection feedback on the 2,000-node fixture of the budget (perf.test.tsx). */
const FIXTURE = generateSnapshot(2000);

describe("selection feedback", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/");
    localStorage.clear();
    openPanels();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("shows the inspector skeleton before the concept arrives", async () => {
    // The concept request never resolves: what the user sees in the
    // meantime is the skeleton, and it must not wait on the network.
    stubApi({
      "/graph": () => json(FIXTURE),
      "/concept": () => new Promise<Response>(() => {}) as unknown as Response,
    });
    // Selected through the search, the keyboard path to a concept: plain
    // selectors, not *ByRole, which would compute every accessible name.
    const user = userEvent.setup();
    render(<App />);
    const id = FIXTURE.nodes[0]!.id;
    await waitFor(() => expect(document.querySelector("[data-testid=graph-view]")).not.toBeNull(), {
      timeout: 10000,
    });
    await user.keyboard("{Control>}k{/Control}");
    await user.keyboard(`${id}{Enter}`);
    await waitFor(() =>
      expect(document.querySelector(`aside[aria-label="Inspector for ${id}"]`)).not.toBeNull(),
    );
    // Skeleton, not content: it rendered while the request is still pending.
    expect(screen.getByText(`Loading ${FIXTURE.nodes[0]!.id}`)).toBeInTheDocument();
  }, 20000);
});
