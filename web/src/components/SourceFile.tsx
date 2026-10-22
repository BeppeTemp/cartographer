import { useEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "./Icon";

/**
 * A file shown as its source: name and a note in the head, a copy button,
 * numbered lines, and Cmd/Ctrl-A selecting the file alone while the pointer
 * is over it. Settings shows its YAML in it, Artifacts the KB's instructions.
 */
export function SourceFile({
  name,
  note,
  text,
  lines = text.replace(/\n$/, "").split("\n"),
  renderLine = (line, i) => <SourceLine key={i}>{line}</SourceLine>,
}: {
  name: string;
  note?: ReactNode;
  /** What the copy button copies: the whole file. */
  text: string;
  /** The lines shown, when they are not the file's own. */
  lines?: string[];
  renderLine?(line: string, i: number): ReactNode;
}) {
  const ref = useRef<HTMLPreElement>(null);
  const hovered = useRef(false);
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      const pre = ref.current;
      if (!pre || ev.key.toLowerCase() !== "a" || !(ev.metaKey || ev.ctrlKey) || ev.altKey || ev.shiftKey) return;
      if (!hovered.current && !pre.contains(document.activeElement)) return;
      const target = ev.target as HTMLElement | null;
      if (target && target !== pre && target.closest("input, textarea, [contenteditable='true']")) return;
      ev.preventDefault();
      const range = document.createRange();
      range.selectNodeContents(pre);
      const sel = window.getSelection();
      sel?.removeAllRanges();
      sel?.addRange(range);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable: the file can still be selected */
    }
  };
  return (
    <figure className="settings__file">
      <figcaption className="settings__file-head">
        <span className="settings__file-name">{name}</span>
        {note && <span className="settings__file-note">{note}</span>}
        <button
          type="button"
          className="button button--icon"
          aria-label={`Copy ${name}`}
          title={copied ? "Copied" : "Copy the file"}
          onClick={() => void copy()}
        >
          <Icon name={copied ? "check" : "copy"} size={14} />
        </button>
      </figcaption>
      <pre
        ref={ref}
        className="settings__yaml"
        aria-label={name}
        tabIndex={0}
        onMouseEnter={() => (hovered.current = true)}
        onMouseLeave={() => (hovered.current = false)}
      >
        <code>{lines.map((line, i) => renderLine(line, i))}</code>
      </pre>
    </figure>
  );
}

/** One numbered line of a source file. */
export function SourceLine({ children }: { children: ReactNode }) {
  return (
    <span className="yaml__line">
      {children}
      {"\n"}
    </span>
  );
}

/**
 * A Markdown line coloured the way the YAML view colours a file: headings in
 * the key colour, frontmatter fences and HTML comments muted, the rest plain.
 */
export function MarkdownLine({ line }: { line: string }) {
  if (/^#{1,6}\s/.test(line)) return <SourceLine>{<span className="yaml__key">{line}</span>}</SourceLine>;
  if (line.trim() === "---" || /^\s*<!--.*-->\s*$/.test(line))
    return <SourceLine>{<span className="yaml__comment">{line}</span>}</SourceLine>;
  return <SourceLine>{line}</SourceLine>;
}
