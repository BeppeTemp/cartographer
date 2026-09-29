import { useEffect, useMemo, useRef, useState } from "react";
import type { Concept, LintFinding } from "../api/types";
import { withoutRedundantLinksSection } from "../lib/linksection";
import { collectionVar } from "../lib/palette";
import { Markdown } from "./Markdown";
import { ErrorState, Skeleton } from "./States";
import { SeverityBadge } from "./SeverityBadge";

interface Props {
  conceptId: string | null;
  concept: Concept | null;
  error: unknown;
  loading: boolean;
  findings: LintFinding[];
  /** A concept's title, when the loaded graph knows it: links are read by
   *  name, with the id beneath. */
  titleOf?(id: string): string | undefined;
  onNavigate(id: string): void;
  onPreview(id: string | null): void;
  /** Opens an artifact on the Artifacts panel. */
  onOpenArtifact?(kind: string, name: string): void;
  onClose(): void;
}

/** Below this many sections an outline is a list of what is already in view. */
const OUTLINE_MIN = 3;

/** The body opens with the concept's own title as a Markdown H1, which the
 *  header already shows: drop that one line rather than print it twice. */
function withoutLeadingTitle(body: string): string {
  return body.replace(/^\s*#\s+[^\n]*\n+/, "");
}

type Tab = "content" | "links" | "meta";

export function Inspector({
  conceptId,
  concept,
  error,
  loading,
  findings,
  titleOf = () => undefined,
  onNavigate,
  onPreview,
  onOpenArtifact,
  onClose,
}: Props) {
  const [tab, setTab] = useState<Tab>("content");
  const contentRef = useRef<HTMLDivElement | null>(null);

  // The outline without the document's own H1 (the header shows it).
  const outline = useMemo(() => {
    const entries = concept?.outline ?? [];
    return entries[0]?.level === 1 ? entries.slice(1) : entries;
  }, [concept]);

  // Headings are found by their text: the Markdown renderer gives them no
  // ids, and a section title is unique enough within one concept.
  const goToSection = (title: string) => {
    const headings = contentRef.current?.querySelectorAll("h1, h2, h3, h4, h5, h6") ?? [];
    for (const h of headings) {
      if (h.textContent?.trim() === title.trim()) {
        h.scrollIntoView?.({ block: "start", behavior: "smooth" });
        return;
      }
    }
  };
  const linkCount =
    (concept?.outbound.length ?? 0) + (concept?.inbound.length ?? 0) + (concept?.used_by?.length ?? 0);

  // A new concept always opens on its content: carrying the previous tab over
  // means a user who opened "Links" once never sees a body again.
  useEffect(() => setTab("content"), [conceptId]);

  if (!conceptId) {
    return (
      <aside className="inspector" aria-label="Concept inspector">
        <div className="state">
          <p className="state__title">Nothing selected</p>
          <p className="state__detail">
            Pick a concept in the graph, or press
            <kbd className="kbd">Ctrl</kbd>
            <kbd className="kbd">K</kbd>
            to search.
          </p>
        </div>
      </aside>
    );
  }

  return (
    <aside className="inspector" aria-label={`Inspector for ${conceptId}`}>
      <header className="inspector__head">
        <div className="inspector__identity">
          <p className="inspector__eyebrow">{concept?.collection || conceptId.split("/")[0]}</p>
          <h2 className="inspector__title">{concept?.title || shortName(conceptId)}</h2>
          <code className="inspector__id">{conceptId}</code>
        </div>
        <button type="button" className="button button--icon" onClick={onClose} aria-label="Close inspector">
          &times;
        </button>
      </header>

      <div className="inspector__tabs" role="tablist" aria-label="Concept sections">
        {(["content", "links", "meta"] as Tab[]).map((name) => (
          <button
            key={name}
            type="button"
            role="tab"
            id={`tab-${name}`}
            aria-selected={tab === name}
            aria-controls={`panel-${name}`}
            className="inspector__tab"
            onClick={() => setTab(name)}
          >
            {name === "content" ? "Content" : name === "links" ? "Links" : "Metadata"}
            {name === "links" && linkCount > 0 && <span className="inspector__tab-count">{linkCount}</span>}
          </button>
        ))}
      </div>

      <div className="inspector__body">
        {loading && <Skeleton lines={5} label={`Loading ${conceptId}`} />}
        {!loading && error ? <ErrorState error={error} /> : null}
        {!loading && !error && concept && (
          <>
            {findings.length > 0 && (
              <section className="inspector__findings" aria-label="Lint findings for this concept">
                {findings.map((finding, index) => (
                  <p key={index} className="inspector__finding">
                    <SeverityBadge severity={finding.severity} />
                    <span>
                      <strong>{finding.check}</strong> {finding.message}
                    </span>
                  </p>
                ))}
              </section>
            )}

            <div
              role="tabpanel"
              id="panel-content"
              aria-labelledby="tab-content"
              hidden={tab !== "content"}
              ref={contentRef}
            >
              {/* A table of contents the reader opens when they want it:
                  always open, it pushed the text below the fold. */}
              {outline.length >= OUTLINE_MIN && (
                <details className="inspector__toc">
                  <summary>
                    On this page <span className="inspector__count">{outline.length}</span>
                  </summary>
                  <nav className="inspector__outline" aria-label="Outline">
                    {outline.map((entry, index) => (
                      <button
                        key={index}
                        type="button"
                        className="inspector__outline-item"
                        style={{ paddingInlineStart: `calc(var(--space-2) * ${Math.max(0, entry.level - 2)})` }}
                        onClick={() => goToSection(entry.title)}
                      >
                        {entry.title}
                      </button>
                    ))}
                  </nav>
                </details>
              )}
              {concept.body.trim() ? (
                <Markdown onNavigate={onNavigate}>
                  {withoutRedundantLinksSection(withoutLeadingTitle(concept.body), concept.id, concept.outbound)}
                </Markdown>
              ) : (
                <p className="inspector__hint">This concept has no body.</p>
              )}
            </div>

            <div role="tabpanel" id="panel-links" aria-labelledby="tab-links" hidden={tab !== "links"}>
              <LinkGroup
                title="Links to"
                empty="This concept links to nothing."
                ids={concept.outbound}
                titleOf={titleOf}
                onNavigate={onNavigate}
                onPreview={onPreview}
              />
              <LinkGroup
                title="Linked from"
                empty="Nothing links here yet."
                ids={concept.inbound}
                titleOf={titleOf}
                onNavigate={onNavigate}
                onPreview={onPreview}
              />
              {!!concept.used_by?.length && (
                <section className="inspector__group">
                  <h3 className="inspector__group-title">
                    Used by <span className="inspector__count">{concept.used_by.length}</span>
                  </h3>
                  <p className="inspector__hint">Skills, agents and hooks that point agents at this concept.</p>
                  <ul className="inspector__links">
                    {concept.used_by.map((a) => (
                      <li key={`${a.kind}/${a.name}`}>
                        <button
                          type="button"
                          className="inspector__link"
                          onClick={() => onOpenArtifact?.(a.kind, a.name)}
                          disabled={!onOpenArtifact}
                        >
                          <span className="inspector__link-kind" aria-hidden="true">◆</span>
                          <span className="inspector__link-text">
                            <span className="inspector__link-title">{a.name}</span>
                            <span className="inspector__link-id">{a.kind}</span>
                          </span>
                        </button>
                      </li>
                    ))}
                  </ul>
                </section>
              )}
              {concept.broken.length > 0 && (
                <section className="inspector__group">
                  <h3 className="inspector__group-title">Broken</h3>
                  <p className="inspector__hint">
                    These targets are not concepts in this KB, so they have no node in the graph.
                  </p>
                  <ul className="inspector__chips">
                    {concept.broken.map((id) => (
                      <li key={id}>
                        <span className="chip chip--broken">
                          <span aria-hidden="true">&#9888;</span>
                          {id}
                        </span>
                      </li>
                    ))}
                  </ul>
                </section>
              )}
            </div>

            <div role="tabpanel" id="panel-meta" aria-labelledby="tab-meta" hidden={tab !== "meta"}>
              <dl className="inspector__meta">
                {Object.entries(concept.frontmatter).map(([key, value]) => (
                  <div key={key} className="inspector__meta-row">
                    <dt>{key}</dt>
                    <dd>{formatValue(value)}</dd>
                  </div>
                ))}
                <div className="inspector__meta-row">
                  <dt>body</dt>
                  <dd>{concept.body_bytes} bytes</dd>
                </div>
                <div className="inspector__meta-row">
                  <dt>hash</dt>
                  <dd>
                    <code>{concept.content_hash.slice(0, 16)}</code>
                  </dd>
                </div>
              </dl>
            </div>
          </>
        )}
      </div>
    </aside>
  );
}

function LinkGroup({
  title,
  empty,
  ids,
  titleOf,
  onNavigate,
  onPreview,
}: {
  title: string;
  empty: string;
  ids: string[];
  titleOf(id: string): string | undefined;
  onNavigate(id: string): void;
  onPreview(id: string | null): void;
}) {
  return (
    <section className="inspector__group">
      <h3 className="inspector__group-title">
        {title} <span className="inspector__count">{ids.length}</span>
      </h3>
      {ids.length === 0 ? (
        <p className="inspector__hint">{empty}</p>
      ) : (
        // Rows, not chips: a link is read by its title, and a wall of ids
        // wrapped as chips gave the eye nothing to scan.
        <ul className="inspector__links">
          {ids.map((id) => {
            const title = titleOf(id);
            const collection = id.includes("/") ? id.slice(0, id.indexOf("/")) : "";
            return (
              <li key={id}>
                <button
                  type="button"
                  className="inspector__link"
                  onClick={() => onNavigate(id)}
                  onMouseEnter={() => onPreview(id)}
                  onMouseLeave={() => onPreview(null)}
                  onFocus={() => onPreview(id)}
                  onBlur={() => onPreview(null)}
                >
                  <span className="inspector__link-swatch" aria-hidden="true" style={{ background: collectionVar(collection) }} />
                  <span className="inspector__link-text">
                    <span className="inspector__link-title">{title ?? shortName(id)}</span>
                    <span className="inspector__link-id">{id}</span>
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

function shortName(id: string): string {
  const cut = id.lastIndexOf("/");
  return cut === -1 ? id : id.slice(cut + 1);
}

function formatValue(value: unknown): string {
  if (Array.isArray(value)) return value.map((v) => String(v)).join(", ");
  if (value === null || value === undefined) return "";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}
