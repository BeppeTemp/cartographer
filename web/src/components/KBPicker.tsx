import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import type { KBSummary } from "../api/types";
import { Icon } from "./Icon";

interface Props {
  kbs: KBSummary[];
  activeKB: string | null;
  onChange(name: string): void;
}

/**
 * The KB picker: a select-only combobox (WAI-ARIA APG) drawn by the page, not
 * a native <select>. The native one opens the operating system's menu, which
 * on macOS is a blue system popover that nothing in the stylesheet can reach.
 * Focus stays on the button; the highlighted option is aria-activedescendant.
 */
export function KBPicker({ kbs, activeKB, onChange }: Props) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const listId = useId();
  const current = kbs.find((kb) => kb.name === activeKB);

  // A click anywhere else closes the menu, as a native one would.
  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", onPointer);
    return () => document.removeEventListener("pointerdown", onPointer);
  }, [open]);

  const show = () => {
    setActive(Math.max(0, kbs.findIndex((kb) => kb.name === activeKB)));
    setOpen(true);
  };
  const choose = (name: string) => {
    setOpen(false);
    if (name !== activeKB) onChange(name);
  };

  const onKeyDown = (event: KeyboardEvent) => {
    if (!open) {
      if (["ArrowDown", "ArrowUp", "Enter", " "].includes(event.key)) {
        event.preventDefault();
        show();
      }
      return;
    }
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        setActive((i) => Math.min(kbs.length - 1, i + 1));
        break;
      case "ArrowUp":
        event.preventDefault();
        setActive((i) => Math.max(0, i - 1));
        break;
      case "Home":
        event.preventDefault();
        setActive(0);
        break;
      case "End":
        event.preventDefault();
        setActive(kbs.length - 1);
        break;
      case "Enter":
      case " ":
        event.preventDefault();
        if (kbs[active]) choose(kbs[active].name);
        break;
      case "Escape":
        event.preventDefault();
        setOpen(false);
        break;
      case "Tab":
        setOpen(false);
        break;
    }
  };

  return (
    <div ref={rootRef} className="topbar__kb picker">
      <button
        type="button"
        className="field picker__trigger"
        role="combobox"
        aria-label="Knowledge Base"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={listId}
        aria-activedescendant={open ? `${listId}-${active}` : undefined}
        onClick={() => (open ? setOpen(false) : show())}
        onKeyDown={onKeyDown}
      >
        <span className="picker__value">
          {current ? current.name : "Choose a KB"}
          {current && !current.ready && <span className="picker__note"> (degraded)</span>}
        </span>
        <span className="field__adornment">
          <Icon name="chevron" size={16} />
        </span>
      </button>
      <ul id={listId} className="picker__menu" role="listbox" aria-label="Knowledge Base" hidden={!open}>
        {kbs.map((kb, i) => (
          <li
            key={kb.name}
            id={`${listId}-${i}`}
            className="picker__option"
            role="option"
            aria-selected={kb.name === activeKB}
            data-active={i === active || undefined}
            onPointerEnter={() => setActive(i)}
            onClick={() => choose(kb.name)}
          >
            <span className="picker__value">{kb.name}</span>
            {!kb.ready && <span className="picker__note">degraded</span>}
            {kb.name === activeKB && <Icon name="check" size={16} />}
          </li>
        ))}
      </ul>
    </div>
  );
}
