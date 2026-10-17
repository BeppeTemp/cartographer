import { useEffect, useRef, useState, type ReactNode } from "react";
import { useModal } from "../lib/modal";

/**
 * A modal sheet: how navigation and the inspector appear below 1,024px, where
 * there is no room for them beside the graph.
 *
 * Modal in the full sense (`useModal`), because a sheet that only looks modal
 * is worse than none; the scrim closes it too.
 */
export function Sheet({
  side,
  label,
  wide = false,
  onClose,
  children,
}: {
  side: "start" | "end";
  label: string;
  /** Room to read prose: the release history, not a rail or an inspector. */
  wide?: boolean;
  onClose(): void;
  children: ReactNode;
}) {
  const panelRef = useRef<HTMLDivElement | null>(null);
  const onKeyDown = useModal(panelRef, onClose);

  return (
    <div className="sheet" onKeyDown={onKeyDown}>
      <div className="sheet__scrim" aria-hidden="true" onClick={onClose} />
      <div
        ref={panelRef}
        className={`sheet__panel sheet__panel--${side}${wide ? " sheet__panel--wide" : ""}`}
        role="dialog"
        aria-modal="true"
        aria-label={label}
        tabIndex={-1}
      >
        <button
          type="button"
          className="sheet__close button button--icon"
          onClick={onClose}
          aria-label={`Close ${label.toLowerCase()}`}
        >
          &times;
        </button>
        {children}
      </div>
    </div>
  );
}

/** Tracks a media query, so layout decisions that change the DOM (not just
 *  its styling) can follow the viewport. */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => window.matchMedia?.(query).matches ?? false);
  useEffect(() => {
    const list = window.matchMedia?.(query);
    if (!list) return;
    const onChange = () => setMatches(list.matches);
    onChange();
    list.addEventListener?.("change", onChange);
    return () => list.removeEventListener?.("change", onChange);
  }, [query]);
  return matches;
}
