import { useEffect, useMemo, useRef, useState } from "react";
import { ApiError, fetchDiff } from "../api/client";
import type { ConceptDiff as Diff, DiffFile } from "../api/types";
import { type DiffLine, diffLines, hunks } from "../lib/linediff";
import { Icon } from "./Icon";

/* Diffs already asked for, by kb, id and span: a row warms its diff when the
   pointer reaches it, so opening it shows the files at once. Bounded, oldest
   dropped first. */
const cache = new Map<string, Promise<Diff>>();
const key = (kb: string, id: string, from: string, to?: string) => `${kb}\u0000${id}\u0000${from}\u0000${to ?? ""}`;

/** Start reading a diff before it is opened (on hover or focus of its row). */
export function prefetchDiff(kb: string, id: string, from: string, to?: string): Promise<Diff> {
  const k = key(kb, id, from, to);
  let p = cache.get(k);
  if (!p) {
    p = fetchDiff(kb, id, from, to);
    // A failed read is not kept: the next open asks again.
    p.catch(() => cache.delete(k));
    cache.set(k, p);
    if (cache.size > 200) cache.delete(cache.keys().next().value!);
  }
  return p;
}

/**
 * What changed in one concept over a span, as a forge shows a commit: the
 * revisions it runs between, then every file of the concept that changed (an
 * expanded concept is a directory), each folded to its name and its +/−
 * counts until opened. An open file shows its changed lines with three lines
 * of context, the rest folded. Each file carries its own actions.
 */
export function ConceptDiff({
  kb,
  id,
  from,
  to,
  expandMain,
  onOpen,
  onUnavailable,
}: {
  kb: string;
  id: string;
  /** Back from the page: its main file opens as it was left. */
  expandMain?: boolean;
  /** Span start, RFC3339: the text before is the last revision before it. */
  from: string;
  /** Span end, RFC3339; now when absent. */
  to?: string;
  onOpen?(conceptId: string): void;
  /** The server has no diff route (a static export) or no such concept: 404. */
  onUnavailable?(): void;
}) {
  const unavailable = useRef(onUnavailable);
  unavailable.current = onUnavailable;
  const [diff, setDiff] = useState<Diff | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    setError(null);
    prefetchDiff(kb, id, from, to)
      .then((d) => live && setDiff(d))
      .catch((e: unknown) => {
        if (!live) return;
        if (e instanceof ApiError && e.isNotFound && unavailable.current) return unavailable.current();
        setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      live = false;
    };
  }, [kb, id, from, to]);

  return (
    <div className="concept-diff" role="region" aria-label={`What changed in ${id}`}>
      <div className="concept-diff__head">
        <span className="concept-diff__path">
          {diff
            ? `${diff.files?.length ?? 0} ${diff.files?.length === 1 ? "file" : "files"} changed`
            : error
              ? "Could not read the change"
              : "Reading the change…"}
        </span>
        {diff && (
          <span className="concept-diff__revs">
            {diff.base ? diff.base.sha.slice(0, 7) : "new"} → {diff.head ? diff.head.sha.slice(0, 7) : "deleted"}
            {diff.commits > 1 && ` · ${diff.commits} commits`}
          </span>
        )}
      </div>
      {!diff && !error && (
        <ul className="concept-diff__files" aria-hidden="true">
          <li className="concept-diff__file concept-diff__file--bone" />
        </ul>
      )}
      {error && <p className="concept-diff__note">{error}</p>}
      {diff?.note && <p className="concept-diff__note">{diff.note}</p>}
      {diff && (diff.files?.length ?? 0) === 0 && !diff.note && (
        <p className="concept-diff__note">No change to the files in this span.</p>
      )}
      {diff?.files && diff.files.length > 0 && (
        <ul className="concept-diff__files">
          {diff.files.map((f, i) => (
            <FileDiff
              key={f.path}
              file={f}
              base={folderOf(diff.path)}
              startOpen={expandMain && i === 0}
              onOpenPage={onOpen && !(f.path === diff.path && f.status === "deleted") ? () => onOpen(id) : undefined}
            />
          ))}
        </ul>
      )}
      {diff?.truncated && (
        <p className="concept-diff__note">
          {diff.base
            ? "Long change: not every file or line is shown."
            : "The history is longer than the diff reads: what shows as added may have been there before."}
        </p>
      )}
    </div>
  );
}

/**
 * One file, folded to its name and counts; open, its hunks. Each file has its
 * own two actions, always in the same place: the whole file as it is at the
 * end of the span, and the page it belongs to in the inspector. One that does
 * not apply (a binary file, a deleted page) is shown disabled, not hidden.
 */
function FileDiff({
  file: f,
  base,
  startOpen,
  onOpenPage,
}: {
  file: DiffFile;
  base: string;
  startOpen?: boolean;
  onOpenPage?(): void;
}) {
  const short = (p: string) => (p.startsWith(base) ? p.slice(base.length) : p);
  const [open, setOpen] = useState(!!startOpen);
  const [whole, setWhole] = useState(false);
  const script = useMemo(() => (f.binary ? [] : diffLines(f.before, f.after)), [f]);
  const tooLarge = script === null;
  const parts = useMemo(() => (script ? hunks(script) : []), [script]);
  const added = parts.reduce((n, h) => n + h.lines.filter((l) => l.kind === "add").length, 0);
  const removed = parts.reduce((n, h) => n + h.lines.filter((l) => l.kind === "del").length, 0);
  const canView = !f.binary && f.status !== "deleted";
  return (
    <li className="concept-diff__file">
      <div className="concept-diff__file-row">
        <button
          type="button"
          className="concept-diff__file-head"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          <span className="concept-diff__chevron" aria-hidden="true">
            ›
          </span>
          <span className="concept-diff__file-path" title={f.path}>
            {f.old_path ? `${short(f.old_path)} → ` : ""}
            {short(f.path)}
          </span>
          <span className="concept-diff__status" data-status={f.status}>
            {f.status}
          </span>
          {f.binary ? (
            <span className="concept-diff__stat">binary</span>
          ) : tooLarge ? (
            <span className="concept-diff__stat">too large to diff</span>
          ) : (
            <span className="concept-diff__stat">
              <span data-kind="add">+{added}</span> <span data-kind="del">−{removed}</span>
            </span>
          )}
        </button>
        <span className="concept-diff__actions">
          <button
            type="button"
            disabled={!canView}
            className="concept-diff__action"
            aria-pressed={open && whole}
            aria-label={open && whole ? `Changed lines of ${short(f.path)}` : `Whole file ${short(f.path)}`}
            title={
              !canView
                ? f.binary
                  ? "A binary file has no text to show"
                  : "The file is gone at the end of the span"
                : open && whole
                  ? "Back to the changed lines"
                  : "The whole file at the end of the span"
            }
            onClick={() => {
              setWhole(!(open && whole));
              setOpen(true);
            }}
          >
            <Icon name="eye" size={14} />
          </button>
          <button
            type="button"
            className="concept-diff__action"
            disabled={!onOpenPage}
            aria-label="Open the page"
            title={onOpenPage ? "Open the page this file belongs to, as it is now" : "The page is gone"}
            onClick={onOpenPage}
          >
            <Icon name="atlas" size={14} />
          </button>
        </span>
      </div>
      {open &&
        (f.binary ? (
          <p className="concept-diff__note">A binary file: its content is not compared.</p>
        ) : whole ? (
          <table className="concept-diff__body">
            <tbody>
              {f.after
                .replace(/\n$/, "")
                .split("\n")
                .map((t, i) => (
                  <tr key={i} className="concept-diff__line" data-kind="same">
                    <td className="concept-diff__num" />
                    <td className="concept-diff__num">{i + 1}</td>
                    <td className="concept-diff__text">{t || " "}</td>
                  </tr>
                ))}
            </tbody>
          </table>
        ) : tooLarge ? (
          <p className="concept-diff__note">Too large to compare line by line: use the whole file.</p>
        ) : parts.length === 0 ? (
          <p className="concept-diff__note">No change to the text.</p>
        ) : (
          <table className="concept-diff__body">
            <tbody>
              {parts.map((h, i) => (
                <HunkRows key={i} skipped={h.skipped} lines={h.lines} />
              ))}
            </tbody>
          </table>
        ))}
      {open && f.truncated && <p className="concept-diff__note">Long file: only its beginning is compared.</p>}
    </li>
  );
}

function HunkRows({ skipped, lines }: { skipped: number; lines: DiffLine[] }) {
  return (
    <>
      {skipped > 0 && (
        <tr className="concept-diff__fold">
          <td colSpan={3}>
            ⋯ {skipped} unchanged {skipped === 1 ? "line" : "lines"}
          </td>
        </tr>
      )}
      {lines.map((l, i) => (
        <tr key={i} className="concept-diff__line" data-kind={l.kind}>
          <td className="concept-diff__num">{l.kind === "add" ? "" : l.a}</td>
          <td className="concept-diff__num">{l.kind === "del" ? "" : l.b}</td>
          <td className="concept-diff__text">
            <span className="concept-diff__sign" aria-hidden="true">
              {l.kind === "add" ? "+" : l.kind === "del" ? "−" : " "}
            </span>
            {l.text || " "}
          </td>
        </tr>
      ))}
    </>
  );
}

/** The folder a concept's files share, so each file reads as `index.md` or
 *  `system-state.jsonl`, not the whole path: an expanded concept's directory,
 *  else the flat file's own folder. */
function folderOf(main: string): string {
  const i = main.lastIndexOf("/");
  return i < 0 ? "" : main.slice(0, i + 1);
}
