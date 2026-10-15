import { useEffect, useMemo, useState } from "react";
import { fetchSources } from "../api/client";
import type { SourceEntry, SourceList } from "../api/types";
import { Count, Page, PageHeader } from "./Page";
import { EmptyState, ErrorState, Skeleton } from "./States";

/** The ledger's date as the other pages say dates; the raw text when it does not parse. */
function when(iso: string | undefined): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  return Number.isNaN(t) ? iso : new Date(t).toLocaleDateString("en", { day: "numeric", month: "short", year: "numeric" });
}

/**
 * The Sources panel: the ledger of what was brought into the KB (`source_list`),
 * newest first, each with its ingest status and the concepts that cite it.
 */
export function Sources({
  kb,
  reloadKey,
  titleOf = () => undefined,
  onOpenConcept,
  onRetry,
  onFailure,
}: {
  kb: string;
  reloadKey: string;
  titleOf?(id: string): string | undefined;
  onOpenConcept?(id: string): void;
  onRetry(): void;
  onFailure(err: unknown): boolean;
}) {
  const [list, setList] = useState<SourceList | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setList(null);
    setLoading(true);
  }, [kb]);

  useEffect(() => {
    const controller = new AbortController();
    fetchSources(kb, controller.signal)
      .then((data) => {
        setList(data);
        setError(null);
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted || onFailure(err)) return;
        setError(err);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kb, reloadKey]);

  // The server lists oldest first; the panel reads newest first.
  const sources = useMemo(
    () => [...(list?.sources ?? [])].sort((a, b) => (Date.parse(b.timestamp ?? "") || 0) - (Date.parse(a.timestamp ?? "") || 0)),
    [list],
  );

  if (loading && !list) return <Skeleton lines={6} label="Loading sources" />;
  if (error) return <ErrorState error={error} onRetry={onRetry} />;
  if (!list) return null;
  if (sources.length === 0) {
    return (
      <EmptyState
        title="No sources registered yet"
        detail="The source ledger records each document, thread or transcript brought into the KB. An agent registers one with source_register (the kb-ingest flow does it for you), then marks it ingested once its content is merged into the pages. The ledger lives in a journal Map, sources by default, which map_create makes once."
      />
    );
  }
  return (
    <Page label="Sources" className="ref-page">
      <PageHeader
        eyebrow="Sources"
        title={
          <>
            <Count>{sources.length}</Count> source{sources.length === 1 ? "" : "s"} in the ledger.
          </>
        }
        subtitle="Newest first. A source is registered with source_register; the pages that cite it list it in their provenance."
      />
      <ul className="ref-list">
        {sources.map((s) => (
          <SourceRow key={s.id} entry={s} titleOf={titleOf} onOpenConcept={onOpenConcept} />
        ))}
      </ul>
    </Page>
  );
}

function SourceRow({
  entry: s,
  titleOf,
  onOpenConcept,
}: {
  entry: SourceEntry;
  titleOf(id: string): string | undefined;
  onOpenConcept?(id: string): void;
}) {
  return (
    <li className="ref-row">
      <div className="ref-row__head">
        <span className="ref-row__title">{s.title || s.id}</span>
        <code className="ref-row__name">{s.id}</code>
        <span className="pill" data-status={s.ingest_status}>
          {s.ingest_status}
        </span>
        {s.source_kind && <span className="pill">{s.source_kind}</span>}
        {s.timestamp && <time dateTime={s.timestamp}>{when(s.timestamp)}</time>}
      </div>
      <dl className="ref-facts">
        <div>
          <dt>cited by</dt>
          <dd>
            {s.cited_by === 0
              ? "no page yet"
              : (s.cited_by_ids ?? []).map((id) =>
                  onOpenConcept ? (
                    <button key={id} type="button" className="ref-link" onClick={() => onOpenConcept(id)}>
                      {titleOf(id) ?? id}
                    </button>
                  ) : (
                    <span key={id}>{titleOf(id) ?? id}</span>
                  ),
                )}
          </dd>
        </div>
      </dl>
    </li>
  );
}
