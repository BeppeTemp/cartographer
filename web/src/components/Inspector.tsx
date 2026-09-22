import { useEffect, useState } from "react";
import type { Concept, LintFinding } from "../api/types";
import { Markdown } from "./Markdown";
import { ErrorState, Skeleton } from "./States";
import { SeverityBadge } from "./SeverityBadge";

interface Props {
  conceptId: string | null;
  concept: Concept | null;
  error: unknown;
  loading: boolean;
  findings: LintFinding[];
  onNavigate(id: string): void;
  onPreview(id: string | null): void;
  onClose(): void;
}

type Tab = "content" | "links" | "meta";

export function Inspector({
  conceptId,
  concept,
  error,
  loading,
  findings,
  onNavigate,
  onPreview,
  onClose,
}: Props) {
  const [tab, setTab] = useState<Tab>("content");

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
            >
              {concept.outline.length > 0 && (
                <nav className="inspector__outline" aria-label="Outline">
                  {concept.outline.map((entry, index) => (
                    <span
                      key={index}
                      className="inspector__outline-item"
                      style={{ paddingInlineStart: `calc(var(--space-2) * ${entry.level})` }}
                    >
                      {entry.title}
                    </span>
                  ))}
                </nav>
              )}
              {concept.body.trim() ? (
                <Markdown onNavigate={onNavigate}>{concept.body}</Markdown>
              ) : (
                <p className="inspector__hint">This concept has no body.</p>
              )}
            </div>

            <div role="tabpanel" id="panel-links" aria-labelledby="tab-links" hidden={tab !== "links"}>
              <LinkGroup
                title="Outbound"
                empty="This concept links to nothing."
                ids={concept.outbound}
                onNavigate={onNavigate}
                onPreview={onPreview}
              />
              <LinkGroup
                title="Backlinks"
                empty="Nothing links here yet."
                ids={concept.inbound}
                onNavigate={onNavigate}
                onPreview={onPreview}
              />
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
  onNavigate,
  onPreview,
}: {
  title: string;
  empty: string;
  ids: string[];
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
        <ul className="inspector__chips">
          {ids.map((id) => (
            <li key={id}>
              <button
                type="button"
                className="chip"
                onClick={() => onNavigate(id)}
                onMouseEnter={() => onPreview(id)}
                onMouseLeave={() => onPreview(null)}
                onFocus={() => onPreview(id)}
                onBlur={() => onPreview(null)}
              >
                {id}
              </button>
            </li>
          ))}
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
