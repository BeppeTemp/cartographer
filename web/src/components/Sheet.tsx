import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * A modal sheet: how navigation and the inspector appear below 1,024px, where
 * there is no room for them beside the graph.
 *
 * Modal in the full sense, because a sheet that only looks modal is worse
 * than none: focus moves into it on open, Tab cycles inside it, Escape and the
 * scrim close it, and focus returns to whatever opened it. Escape is stopped
 * here so the shell's own Escape (clear the selection) does not also fire --
 * closing the inspector sheet must not throw away what the user selected.
 */
export function Sheet({
  side,
  label,
  onClose,
  children,
}: {
  side: "start" | "end";
  label: string;
  onClose(): void;
  children: ReactNode;
}) {
  const panelRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    const panel = panelRef.current;
    const first = panel?.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? panel)?.focus();
    return () => {
      // The opener may have been unmounted by the change that closed the
      // sheet; focusing a detached node would silently drop focus to <body>.
      if (opener && opener.isConnected) opener.focus();
    };
  }, []);

  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      event.stopPropagation();
      event.nativeEvent.stopImmediatePropagation();
      onClose();
      return;
    }
    if (event.key !== "Tab") return;
    const panel = panelRef.current;
    if (!panel) return;
    const items = [...panel.querySelectorAll<HTMLElement>(FOCUSABLE)];
    if (items.length === 0) return;
    const first = items[0]!;
    const last = items[items.length - 1]!;
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };

  return (
    <div className="sheet" onKeyDown={onKeyDown}>
      <div className="sheet__scrim" aria-hidden="true" onClick={onClose} />
      <div
        ref={panelRef}
        className={`sheet__panel sheet__panel--${side}`}
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
