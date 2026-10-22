import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import type { KBStatus } from "../api/types";
import { LIVE_INTERVAL_MS } from "../lib/live";
import { SETTLED_INTERVAL_MS, useSyncStatus } from "../lib/syncstatus";

const fetchStatus = vi.fn();
vi.mock("../api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/client")>()),
  fetchStatus: (...args: unknown[]) => fetchStatus(...args),
}));

const ok: KBStatus = { has_remote: true, push_state: "clean", unpushed_commits: 0, behind_commits: 0 };
const pending: KBStatus = { ...ok, push_state: "pending", unpushed_commits: 2 };

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

describe("useSyncStatus (#1092)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    fetchStatus.mockReset();
    setVisibility("visible");
  });
  afterEach(() => vi.useRealTimers());

  it("reads on mount and again when the KB changes", async () => {
    fetchStatus.mockResolvedValue(ok);
    const { result, rerender } = renderHook(({ kb }) => useSyncStatus(kb, true, 0), { initialProps: { kb: "a" } });
    await advance(0);
    expect(result.current.status).toEqual(ok);
    expect(fetchStatus).toHaveBeenCalledTimes(1);
    rerender({ kb: "b" });
    await advance(0);
    expect(fetchStatus).toHaveBeenCalledTimes(2);
    expect(fetchStatus.mock.calls[1]?.[0]).toBe("b");
  });

  it("refetches when the live tick moves: a push completing moves no concept file, so the tick alone is not enough", async () => {
    fetchStatus.mockResolvedValue(ok);
    const { rerender } = renderHook(({ live }) => useSyncStatus("a", true, live), { initialProps: { live: 0 } });
    await advance(0);
    rerender({ live: 1 });
    await advance(0);
    expect(fetchStatus).toHaveBeenCalledTimes(2);
  });

  it("polls on its own: every 10 s while unsettled, every 60 s once ok", async () => {
    fetchStatus.mockResolvedValue(pending);
    renderHook(() => useSyncStatus("a", true, 0));
    await advance(0);
    expect(fetchStatus).toHaveBeenCalledTimes(1);
    await advance(LIVE_INTERVAL_MS);
    await advance(LIVE_INTERVAL_MS);
    expect(fetchStatus).toHaveBeenCalledTimes(3);

    fetchStatus.mockResolvedValue(ok);
    await advance(LIVE_INTERVAL_MS); // reads the settled answer
    expect(fetchStatus).toHaveBeenCalledTimes(4);
    await advance(LIVE_INTERVAL_MS * 3);
    expect(fetchStatus).toHaveBeenCalledTimes(4);
    await advance(SETTLED_INTERVAL_MS);
    expect(fetchStatus).toHaveBeenCalledTimes(5);
  });

  it("does not poll while the tab is hidden and checks again when it comes back", async () => {
    fetchStatus.mockResolvedValue(pending);
    renderHook(() => useSyncStatus("a", true, 0));
    await advance(0);
    setVisibility("hidden");
    await advance(LIVE_INTERVAL_MS * 3);
    expect(fetchStatus).toHaveBeenCalledTimes(1);
    setVisibility("visible");
    await advance(0);
    expect(fetchStatus).toHaveBeenCalledTimes(2);
  });

  it("keeps the last value on a network error", async () => {
    fetchStatus.mockResolvedValueOnce(pending);
    const { result } = renderHook(() => useSyncStatus("a", true, 0));
    await advance(0);
    fetchStatus.mockRejectedValue(new TypeError("offline"));
    await advance(LIVE_INTERVAL_MS);
    expect(result.current.status).toEqual(pending);
    expect(result.current.hidden).toBe(false);
  });

  it("hides on a 404 and stops asking", async () => {
    fetchStatus.mockRejectedValue(new ApiError(404, "not_found", "not found"));
    const { result } = renderHook(() => useSyncStatus("a", true, 0));
    await advance(0);
    expect(result.current).toEqual({ status: null, hidden: true });
    await advance(SETTLED_INTERVAL_MS * 2);
    expect(fetchStatus).toHaveBeenCalledTimes(1);
  });

  it("does nothing while disabled", async () => {
    renderHook(() => useSyncStatus("a", false, 0));
    await advance(SETTLED_INTERVAL_MS);
    expect(fetchStatus).not.toHaveBeenCalled();
  });
});
