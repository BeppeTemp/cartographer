import type { ReactNode } from "react";
import { ApiError } from "../api/client";

/**
 * Every non-happy path is a designed state, not a blank panel. They live
 * together in one file so it stays obvious when one is missing: skeleton,
 * empty, not-found, offline and failure.
 */

export function Skeleton({ lines = 3, label }: { lines?: number; label: string }) {
  return (
    <div className="state" role="status" aria-live="polite">
      <span className="sr-only">{label}</span>
      <div style={{ display: "grid", gap: "var(--space-3)", width: "min(420px, 100%)" }}>
        {Array.from({ length: lines }, (_, i) => (
          <div
            key={i}
            className="skeleton"
            aria-hidden="true"
            style={{ height: "var(--space-4)", width: `${100 - i * 12}%` }}
          />
        ))}
      </div>
    </div>
  );
}

export function EmptyState({
  title,
  detail,
  action,
}: {
  title: string;
  detail?: string;
  action?: ReactNode;
}) {
  return (
    <div className="state">
      <p className="state__title">{title}</p>
      {detail && <p className="state__detail">{detail}</p>}
      {action}
    </div>
  );
}

/**
 * ErrorState translates an ApiError into something a person can act on. The
 * 404 case deliberately says "not found or not visible": the server does not
 * distinguish them, and pretending otherwise would teach the user a rule that
 * is not true.
 */
export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { title, detail } = describe(error);
  return (
    <div className="state state--error" role="alert">
      <p className="state__title">{title}</p>
      <p className="state__detail">{detail}</p>
      {onRetry && (
        <button type="button" className="button" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}

export function describe(error: unknown): { title: string; detail: string } {
  if (error instanceof ApiError) {
    if (error.isOffline) {
      return {
        title: "Server unreachable",
        detail: "Cartographer did not answer. Check that the server is still running, then retry.",
      };
    }
    if (error.isNotFound) {
      return {
        title: "Not found",
        detail: "This item does not exist, or it is not visible to your token.",
      };
    }
    if (error.isUnauthorized) {
      return { title: "Session expired", detail: "Your token is no longer accepted." };
    }
    if (error.status >= 500) {
      return {
        title: "Server error",
        detail: "Cartographer failed to answer this request. The details are in the server log.",
      };
    }
    return { title: "Request rejected", detail: error.message };
  }
  return { title: "Something went wrong", detail: "An unexpected error interrupted this panel." };
}
