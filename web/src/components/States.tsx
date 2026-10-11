import type { ReactNode } from "react";
import { ApiError } from "../api/client";
import { Icon } from "./Icon";

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

/** The shell before the first answer: the real top bar, rail and canvas
 *  outline with skeleton blocks where content will land, so the first paint
 *  already has the final layout and only content fills in. Built from the
 *  shell's own classes, so nothing moves when the real shell replaces it. */
export function ShellSkeleton({
  label,
  narrow = false,
  railCollapsed = false,
}: {
  label: string;
  narrow?: boolean;
  railCollapsed?: boolean;
}) {
  const bodyClass = [
    "shell__body",
    railCollapsed ? "shell__body--rail-collapsed" : "",
    narrow ? "shell__body--narrow" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const block = (width: string, height = "var(--space-4)") => (
    <span className="skeleton shell-skeleton__block" aria-hidden="true" style={{ width, height }} />
  );
  return (
    <div className="shell" aria-busy="true">
      <header className="topbar">
        <div className="topbar__brand shell-skeleton__brand">
          <img
            className="topbar__mark topbar__mark--light"
            src={`${import.meta.env.BASE_URL}brand/coordinate-pine.svg`}
            alt=""
            width={28}
            height={28}
          />
          <img
            className="topbar__mark topbar__mark--dark"
            src={`${import.meta.env.BASE_URL}brand/coordinate-sage.svg`}
            alt=""
            width={28}
            height={28}
          />
          <span className="topbar__name">Cartographer</span>
        </div>
        {!narrow && <div className="skeleton shell-skeleton__picker" aria-hidden="true" />}
        <div className="topbar__search field shell-skeleton__search" aria-hidden="true" />
      </header>
      <div className={bodyClass}>
        {!narrow && (
          <nav className="rail" aria-label="Loading navigation">
            <ul className="rail__panels" aria-hidden="true">
              {[0, 1, 2].map((i) => (
                <li key={i}>
                  <div className="rail__panel">
                    {railCollapsed ? block("var(--space-5)") : block(`${70 - i * 12}%`)}
                  </div>
                </li>
              ))}
            </ul>
            {!railCollapsed && (
              <div className="rail__group" aria-hidden="true">
                {block("40%", "var(--space-3)")}
                {[0, 1, 2, 3].map((i) => (
                  <div key={i} className="rail__collection">
                    {block(`${80 - i * 10}%`)}
                  </div>
                ))}
              </div>
            )}
          </nav>
        )}
        <main id="main" className="main" tabIndex={-1}>
          <GraphSeed label={label} />
        </main>
      </div>
    </div>
  );
}

/** The graph before it exists: a point of light pulsing where it will open
 *  out from (scene bloom), so loading and the first frame are one motion. */
export function GraphSeed({ label }: { label: string }) {
  return (
    <div className="graph-seed" role="status" aria-live="polite">
      <span className="sr-only">{label}</span>
      <span className="graph-seed__dot" aria-hidden="true" />
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
  const offline = error instanceof ApiError && error.isOffline;
  const tech = error instanceof ApiError && error.status > 0 ? `HTTP ${error.status} · ${error.code}` : null;
  return (
    <div className="state state--error" role="alert">
      <ErrorGlyph name={offline ? "offline" : "alert"} />
      <p className="state__title">{title}</p>
      <p className="state__detail">{detail}</p>
      {tech && <p className="state__tech">{tech}</p>}
      {onRetry && (
        <button type="button" className="button" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}

/** The soft tinted circle above an error title; shared with the ErrorBoundary page. */
export function ErrorGlyph({ name = "alert" }: { name?: "alert" | "offline" }) {
  return (
    <span className="state__glyph" aria-hidden="true">
      <Icon name={name} size={22} />
    </span>
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
