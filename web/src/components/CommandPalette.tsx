import { useEffect, useMemo, useRef, useState } from "react";
import { fetchSearch } from "../api/client";
import { nameOf } from "../lib/names";
import type { GraphNode, SearchHit } from "../api/types";
import { collectionVar } from "../lib/palette";

interface Props {
  open: boolean;
  /** The KB the full-text search runs against; null skips it. */
  kb?: string | null;
  nodes: GraphNode[];
  onClose(): void;
  onSelect(id: string): void;
}

const MAX_RESULTS = 40;
/** The full-text search waits for a pause in typing: one request per pause. */
const TEXT_DEBOUNCE_MS = 220;

/** One row: a title match from memory, or a full-text hit from the server
 *  carrying the excerpt that matched. */
interface Row {
  id: string;
  name: string;
  showId: boolean;
  collection: string;
  snippet?: string;
}

/** Snippets are raw Markdown excerpts: heading marks and emphasis are noise
 *  in a one-line preview. */
function cleanSnippet(s: string): string {
  return s
    .replace(/^#+\s*/gm, "")
    .replace(/[*_`>]/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

/**
 * Search, opened with Ctrl/Cmd+K, in two passes. Titles and ids match
 * instantly against the loaded node set; after a pause in typing the server's
 * full-text search (the agents' own `search` tool, which never records the
 * reader's queries as knowledge gaps) adds the concepts whose *text* matches,
 * each with the excerpt that matched.
 */
export function CommandPalette({ open, kb = null, nodes, onClose, onSelect }: Props) {
  const [textHits, setTextHits] = useState<SearchHit[]>([]);
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

  useEffect(() => {
    const needle = query.trim();
    setTextHits([]);
    if (!open || !kb || needle.length < 2) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      fetchSearch(kb, needle, controller.signal)
        .then((r) => setTextHits(r.results))
        .catch(() => undefined);
    }, TEXT_DEBOUNCE_MS);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [open, kb, query]);

  const titleMatches = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const matches = needle
      ? nodes.filter(
          (node) => node.id.toLowerCase().includes(needle) || (node.title ?? "").toLowerCase().includes(needle),
        )
      : nodes;
    return [...matches]
      .sort((a, b) => {
        // A name that starts with the query beats a substring buried in it.
        const aStarts = nameOf(a).toLowerCase().startsWith(needle) ? 0 : 1;
        const bStarts = nameOf(b).toLowerCase().startsWith(needle) ? 0 : 1;
        if (aStarts !== bStarts) return aStarts - bStarts;
        return nameOf(a).localeCompare(nameOf(b));
      })
      .slice(0, MAX_RESULTS);
  }, [nodes, query]);

  const results: Row[] = useMemo(() => {
    const rows: Row[] = titleMatches.map((node) => ({
      id: node.id,
      name: nameOf(node),
      showId: !!node.title,
      collection: node.collection ?? "",
    }));
    const seen = new Set(rows.map((r) => r.id));
    for (const hit of textHits) {
      const snippet = hit.snippet ? cleanSnippet(hit.snippet) : undefined;
      const existing = rows.find((r) => r.id === hit.id);
      if (existing) {
        existing.snippet ??= snippet;
        continue;
      }
      if (seen.has(hit.id)) continue;
      seen.add(hit.id);
      rows.push({
        id: hit.id,
        name: hit.title || hit.id,
        showId: !!hit.title,
        collection: hit.id.includes("/") ? hit.id.slice(0, hit.id.indexOf("/")) : "",
        snippet,
      });
    }
    return rows.slice(0, MAX_RESULTS);
  }, [titleMatches, textHits]);

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
          placeholder="Search concepts — titles, ids and full text"
          aria-label="Search concepts by title, id or text"
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
            results.map((row, index) => (
              <li
                key={row.id}
                id={`palette-option-${index}`}
                data-concept-id={row.id}
                role="option"
                aria-selected={index === cursor}
                className="palette__result"
                onMouseEnter={() => setCursor(index)}
                onMouseDown={(event) => {
                  event.preventDefault();
                  commit(row.id);
                }}
              >
                <span
                  className="palette__swatch"
                  aria-hidden="true"
                  style={{ background: collectionVar(row.collection) }}
                />
                <span className="palette__text">
                  <Highlighted text={row.name} needle={query.trim()} />
                  {row.showId && <span className="palette__id">{row.id}</span>}
                  {row.snippet && <span className="palette__snippet">{row.snippet}</span>}
                </span>
                <span className="palette__hint">{row.collection}</span>
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
