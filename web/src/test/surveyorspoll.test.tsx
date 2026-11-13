import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useComponents } from "../components/Surveyors";

const fetchComponents = vi.fn();
vi.mock("../api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/client")>()),
  fetchComponents: (...args: unknown[]) => fetchComponents(...args),
}));

const POLL_MS = 60_000;

async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

function setVisibility(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  act(() => {
    document.dispatchEvent(new Event("visibilitychange"));
  });
}

describe("useComponents polling", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    fetchComponents.mockReset();
    fetchComponents.mockResolvedValue({ components: [] });
    setVisibility("visible");
  });
  afterEach(() => vi.useRealTimers());

  it("beats while visible", async () => {
    renderHook(() => useComponents("a", 0));
    await advance(0);
    expect(fetchComponents).toHaveBeenCalledTimes(1);
    await advance(POLL_MS);
    expect(fetchComponents).toHaveBeenCalledTimes(2);
  });

  it("does not tick while the page is hidden and refreshes once when it is visible again", async () => {
    renderHook(() => useComponents("a", 0));
    await advance(0);
    setVisibility("hidden");
    await advance(POLL_MS * 3);
    expect(fetchComponents).toHaveBeenCalledTimes(1);
    setVisibility("visible");
    await advance(0);
    expect(fetchComponents).toHaveBeenCalledTimes(2);
  });
});
