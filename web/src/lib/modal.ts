import { useEffect, type KeyboardEvent, type RefObject } from "react";

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/** What Tab can reach inside `panel`, in order. A radio group is one stop, its
 *  checked radio, as the browser treats it: counting every radio would make the
 *  wrap-around miss when the checked one is not the first in the DOM. */
function tabStops(panel: HTMLElement): HTMLElement[] {
  return [...panel.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => {
    if (!(el instanceof HTMLInputElement) || el.type !== "radio" || el.checked || !el.name) return true;
    const group = panel.querySelectorAll<HTMLInputElement>(`input[type="radio"][name="${CSS.escape(el.name)}"]`);
    return ![...group].some((r) => r.checked);
  });
}

/**
 * What makes a dialog modal and not just look it: focus moves into `panelRef`
 * on open, Tab and Shift+Tab cycle inside it, Escape closes it, and focus
 * returns to whatever opened it. Escape is stopped here so the shell's own
 * Escape (clear the selection) does not also fire -- closing a dialog must not
 * throw away what the user selected.
 *
 * Returns the keydown handler for the element that wraps the dialog.
 */
export function useModal(panelRef: RefObject<HTMLElement | null>, onClose: () => void) {
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    const panel = panelRef.current;
    const first = panel ? tabStops(panel)[0] : undefined;
    (first ?? panel)?.focus();
    return () => {
      // The opener may have been unmounted by the change that closed the
      // dialog; focusing a detached node would silently drop focus to <body>.
      if (opener && opener.isConnected) opener.focus();
    };
  }, [panelRef]);

  return (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      event.stopPropagation();
      event.nativeEvent.stopImmediatePropagation();
      onClose();
      return;
    }
    if (event.key !== "Tab") return;
    const panel = panelRef.current;
    if (!panel) return;
    const items = tabStops(panel);
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
}
