import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import { LIVE_INTERVAL_MS, useLiveRevision } from "../lib/live";

const fetchRevision = vi.fn();
vi.mock("../api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/client")>()),
  fetchRevision: (...args: unknown[]) => fetchRevision(...args),
}));

async function beat() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(LIVE_INTERVAL_MS);
  });
}

describe("useLiveRevision (D336)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    fetchRevision.mockReset();
  });
  afterEach(() => vi.useRealTimers());

  it("bumps only when the revision changes", async () => {
    fetchRevision.mockResolvedValue({ revision: "a-1" });
    const { result } = renderHook(() => useLiveRevision("kb", true));
    await beat();
    expect(result.current).toBe(0);
    fetchRevision.mockResolvedValue({ revision: "a-2" });
    await beat();
    expect(result.current).toBe(1);
    await beat();
    expect(result.current).toBe(1);
  });

  it("stops polling on a 404: no revision for a narrowed principal", async () => {
    fetchRevision.mockRejectedValue(new ApiError(404, "not_found", "not found"));
    renderHook(() => useLiveRevision("kb", true));
    await beat();
    await beat();
    expect(fetchRevision).toHaveBeenCalledTimes(1);
  });

  it("does not poll while disabled", async () => {
    renderHook(() => useLiveRevision("kb", false));
    await beat();
    expect(fetchRevision).not.toHaveBeenCalled();
  });
});
