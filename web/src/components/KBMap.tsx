import { useEffect, useState } from "react";
import { ApiError, fetchKBMap } from "../api/client";
import { currentSource, pollIntervalMs } from "../api/static";
import type { KBMap as KBMapData } from "../api/types";
import { Page, PageHeader, PageSection } from "./Page";
import { EmptyState, ErrorState } from "./States";

interface Props {
  /** A map the app fetched a moment ago: used as the first answer, and a fetch
   *  younger than one poll is not repeated at mount. */
  initial?: { map: KBMapData; at: number } | null;
  /** Opens the KB's own atlas. */
  onOpenKB(name: string): void;
  /** Each answer, so the app keeps the latest as the next panel's seed. */
  onLoaded?(map: KBMapData): void;
  /** The server has no such route (an older one): the app hides the entry. */
  onUnavailable?(): void;
}

/**
 * The KB map, minimal shell: the KBs the caller can read as rows by layer
 * (core first), and the two layers behind a toggle each, as lists. The server
 * decides what is visible and what is counted; this draws it. The drawing
 * itself (layout, edge weight, colours) is left to a later design session.
 */
export function KBMap({ initial, onOpenKB, onLoaded, onUnavailable }: Props) {
  const [map, setMap] = useState<KBMapData | null>(initial?.map ?? null);
  const [error, setError] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);
  const [showRelations, setShowRelations] = useState(true);
  const [showLinks, setShowLinks] = useState(true);

  // The answer carries an ETag, so a poll that finds nothing new is a 304.
  useEffect(() => {
    let stopped = false;
    let controller: AbortController | null = null;
    const load = () => {
      if (stopped || document.visibilityState !== "visible") return;
      controller?.abort();
      controller = new AbortController();
      const mine = controller;
      fetchKBMap(mine.signal)
        .then((next) => {
          if (stopped || mine.signal.aborted) return;
          setMap(next);
          setError(null);
          onLoaded?.(next);
        })
        .catch((err: unknown) => {
          if (stopped || mine.signal.aborted) return;
          setError(err);
          if (err instanceof ApiError && err.status === 404) onUnavailable?.();
        });
    };
    const interval = pollIntervalMs(currentSource());
    if (!initial || Date.now() - initial.at >= interval) load();
    const timer = window.setInterval(load, interval);
    document.addEventListener("visibilitychange", load);
    return () => {
      stopped = true;
      controller?.abort();
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", load);
    };
    // `initial` seeds the first render only: a later change must not refetch.
  }, [attempt]);

  if (error && !map) return <ErrorState error={error} onRetry={() => setAttempt((n) => n + 1)} />;
  if (!map) return <Page label="KB map" busy>{null}</Page>;
  if (map.nodes.length === 0) {
    return <EmptyState title="No Knowledge Base is visible" detail="Nothing is mounted that your token can read." />;
  }

  const layers = new Map<number, string[]>();
  for (const node of map.nodes) layers.set(node.layer, [...(layers.get(node.layer) ?? []), node.name]);
  const rows = [...layers.entries()].sort((a, b) => a[0] - b[0]);

  return (
    <Page label="KB map">
      <PageHeader
        eyebrow="KB map"
        title={`${map.nodes.length} Knowledge Bases`}
        subtitle={map.truncated ? "The link counts are a lower bound: the server stopped counting." : undefined}
      />
      <PageSection title="Knowledge Bases" id="kbmap-layers">
        {rows.map(([layer, names]) => (
          <div key={layer} role="group" aria-label={`Layer ${layer}`} className="kbmap__row">
            {names.map((name) => (
              <button key={name} type="button" className="button" onClick={() => onOpenKB(name)}>
                {name}
              </button>
            ))}
          </div>
        ))}
      </PageSection>
      <PageSection title="Layers" id="kbmap-toggles">
        <label>
          <input type="checkbox" checked={showRelations} onChange={(e) => setShowRelations(e.target.checked)} />
          Relations
        </label>
        <label>
          <input type="checkbox" checked={showLinks} onChange={(e) => setShowLinks(e.target.checked)} />
          Cross-KB links
        </label>
      </PageSection>
      {showRelations && (
        <PageSection title="Relations" id="kbmap-relations" count={map.relations.length}>
          <ul aria-label="Relations">
            {map.relations.map((r) => (
              <li key={`${r.from}>${r.to}`}>{`${r.from} → ${r.to}`}</li>
            ))}
          </ul>
        </PageSection>
      )}
      {showLinks && (
        <PageSection title="Cross-KB links" id="kbmap-links" count={map.links.length}>
          <ul aria-label="Cross-KB links">
            {map.links.map((l) => (
              <li key={`${l.from}>${l.to}`}>{`${l.from} → ${l.to} (${l.count})`}</li>
            ))}
          </ul>
        </PageSection>
      )}
    </Page>
  );
}
