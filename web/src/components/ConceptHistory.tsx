import { useEffect, useState } from "react";
import { fetchAssets, fetchHistory } from "../api/client";
import type { AssetEntry, ConceptHistory as History } from "../api/types";
import { relativeDay } from "./Page";
import { ErrorState, Skeleton } from "./States";

interface Props {
  kb: string;
  conceptId: string;
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function extension(path: string): string {
  const name = path.split("/").pop() ?? path;
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : "file";
}

/** The History tab: a concept's revisions and, when it is expanded, its
 *  assets. Self-fetching, so a concept nobody opens the tab on costs nothing. */
export function ConceptHistory({ kb, conceptId }: Props) {
  const [history, setHistory] = useState<History | null>(null);
  const [error, setError] = useState<unknown>(null);
  // Not expanded (400), invisible or exported statically: no assets, no noise.
  const [assets, setAssets] = useState<AssetEntry[]>([]);

  useEffect(() => {
    const ctl = new AbortController();
    setHistory(null);
    setError(null);
    setAssets([]);
    fetchHistory(kb, conceptId, ctl.signal)
      .then(setHistory)
      .catch((e) => {
        if (!ctl.signal.aborted) setError(e);
      });
    fetchAssets(kb, conceptId, ctl.signal)
      .then((list) => setAssets(list.assets ?? []))
      .catch(() => {});
    return () => ctl.abort();
  }, [kb, conceptId]);

  if (error) return <ErrorState error={error} />;
  if (!history) return <Skeleton lines={4} label={`Loading history of ${conceptId}`} />;

  return (
    <div className="concept-history">
      <section>
        <h3 className="inspector__group-title">Revisions</h3>
        {history.revisions.length === 0 ? (
          <p className="inspector__hint">
            Every change to a concept is a git commit, and this is where they are listed. Nothing is recorded for this
            one yet.
          </p>
        ) : (
          <ol className="concept-history__list">
            {history.revisions.map((r) => (
              <li key={r.sha} className="concept-history__rev">
                <p className="concept-history__subject">{r.subject}</p>
                <p className="concept-history__meta">
                  <time dateTime={r.at} title={new Date(r.at).toLocaleString("en")}>
                    {relativeDay(r.at)}
                  </time>
                  <span>{r.author}</span>
                  <code>{r.sha.slice(0, 7)}</code>
                </p>
              </li>
            ))}
          </ol>
        )}
        {history.truncated && <p className="inspector__hint">Older revisions are not shown.</p>}
        {history.note && <p className="inspector__hint">{history.note}</p>}
      </section>
      {assets.length > 0 && (
        <section>
          <h3 className="inspector__group-title">Assets</h3>
          <ul className="concept-history__list">
            {assets.map((a) => (
              <li key={a.path} className="concept-history__asset">
                <code>{a.path}</code>
                <span>{formatSize(a.size)}</span>
                <span>{extension(a.path)}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}
