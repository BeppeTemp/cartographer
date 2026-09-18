import { useEffect, useMemo, useRef, useState } from "react";
import type { GraphNode } from "../api/types";
import { collectionVar } from "../lib/palette";

interface Props {
  open: boolean;
  nodes: GraphNode[];
  onClose(): void;
  onSelect(id: string): void;
}

const MAX_RESULTS = 40;

/**
 * Search over the loaded node set, opened with Ctrl/Cmd+K.
 *
 * It filters what is already in memory rather than calling the server on every
 * keystroke: the graph endpoint has already returned the ids for this scope,
 * and a remote round trip per character would be slower and would leak the
 * query to the access log.
 */
export function CommandPalette({ open, nodes, onClose, onSelect }: Props) {
  const [query, setQuery] = useState("");
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLUListElement | null>(null);
  const restoreFocusTo = useRef<Element | null>(null);

  useEffect(() => {
    if (!open) return;
    restoreFocusTo.current = document.activeElement;
    setQuery("");
    setCursor(0);
    // The effect runs after mount, so the input is already in the tree: focus
    // it now rather than a frame later, or the first keystroke goes nowhere.
    inputRef.current?.focus();
    return () => {
      // Focus goes back where it came from: leaving it on <body> strands a
      // keyboard user at the top of the document.
      (restoreFocusTo.current as HTMLElement | null)?.focus?.();
    };
  }, [open]);

  const results = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const matches = needle
      ? nodes.filter((node) => node.id.toLowerCase().includes(needle))
      : nodes;
    return [...matches]
      .sort((a, b) => {
        // An exact segment match beats a substring buried in a path.
        const aStarts = a.id.toLowerCase().startsWith(needle) ? 0 : 1;
        const bStarts = b.id.toLowerCase().startsWith(needle) ? 0 : 1;
        if (aStarts !== bStarts) return aStarts - bStarts;
        return a.id.localeCompare(b.id);
      })
      .slice(0, MAX_RESULTS);
  }, [nodes, query]);

  useEffect(() => setCursor(0), [query]);

  useEffect(() => {
    // scrollIntoView is missing in jsdom and absent from very old engines;
    // keeping the cursor visible is a nicety, never a reason to crash the
    // dialog.
    const option = listRef.current?.querySelector<HTMLElement>('[aria-selected="true"]');
    option?.scrollIntoView?.({ block: "nearest" });
  }, [cursor]);

  if (!open) return null;

  const commit = (id: string | undefined) => {
    if (!id) return;
    onSelect(id);
    onClose();
  };

  return (
    <div className="palette__backdrop" role="presentation" onMouseDown={onClose}>
      <div
        className="palette"
        role="dialog"
        aria-modal="true"
        aria-label="Search concepts"
        onMouseDown={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          // On the dialog, not only on the input: a user who has tabbed into
          // the footer must still be able to dismiss it.
          if (event.key !== "Escape") return;
          event.preventDefault();
          onClose();
        }}
      >
        <input
          ref={inputRef}
          className="palette__input"
          type="text"
          value={query}
          placeholder="Search concepts by id"
          aria-label="Search concepts by id"
          aria-controls="palette-results"
          aria-activedescendant={results[cursor] ? `palette-option-${cursor}` : undefined}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "ArrowDown") {
              event.preventDefault();
              setCursor((c) => Math.min(c + 1, results.length - 1));
            } else if (event.key === "ArrowUp") {
              event.preventDefault();
              setCursor((c) => Math.max(c - 1, 0));
            } else if (event.key === "Enter") {
              event.preventDefault();
              commit(results[cursor]?.id);
            }
          }}
        />
        <ul id="palette-results" className="palette__results" role="listbox" ref={listRef}>
          {results.length === 0 ? (
            <li className="palette__empty">No concept matches &ldquo;{query}&rdquo;.</li>
          ) : (
            results.map((node, index) => (
              <li
                key={node.id}
                id={`palette-option-${index}`}
                role="option"
                aria-selected={index === cursor}
                className="palette__result"
                onMouseEnter={() => setCursor(index)}
                onMouseDown={(event) => {
                  event.preventDefault();
                  commit(node.id);
                }}
              >
                <span
                  className="palette__swatch"
                  aria-hidden="true"
                  style={{ background: collectionVar(node.collection ?? "") }}
                />
                <Highlighted text={node.id} needle={query.trim()} />
                <span className="palette__hint">{node.collection}</span>
              </li>
            ))
          )}
        </ul>
        <footer className="palette__footer">
          <kbd className="kbd">&uarr;</kbd>
          <kbd className="kbd">&darr;</kbd> to move
          <kbd className="kbd">Enter</kbd> to open
          <kbd className="kbd">Esc</kbd> to dismiss
        </footer>
      </div>
    </div>
  );
}

function Highlighted({ text, needle }: { text: string; needle: string }) {
  if (!needle) return <span className="palette__label">{text}</span>;
  const at = text.toLowerCase().indexOf(needle.toLowerCase());
  if (at === -1) return <span className="palette__label">{text}</span>;
  return (
    <span className="palette__label">
      {text.slice(0, at)}
      <mark>{text.slice(at, at + needle.length)}</mark>
      {text.slice(at + needle.length)}
    </span>
  );
}
