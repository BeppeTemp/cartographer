import { useEffect, useMemo, useState } from "react";
import { fetchChanges } from "../api/client";
import type { ChangesResponse, GraphSnapshot } from "../api/types";

const WINDOWS = ["1d", "7d", "30d"] as const;

/**
 * What changed in the KB, and who changed it: the agents' own
 * `changes_since`, as a page beside the Observatory. A reader reviewing a
 * session narrows it to one author and opens each concept on the atlas.
 */
export function Activity({
  kb,
  snapshot,
  onOpen,
}: {
  kb: string;
  snapshot: GraphSnapshot | null;
  onOpen(conceptId: string): void;
}) {
  const [since, setSince] = useState<(typeof WINDOWS)[number]>("7d");
  const [author, setAuthor] = useState<string | null>(null);
  const [data, setData] = useState<ChangesResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setData(null);
    setError(null);
    fetchChanges(kb, since, controller.signal)
      .then(setData)
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof Error ? err.message : String(err));
      });
    return () => controller.abort();
  }, [kb, since]);

  const titles = useMemo(
    () => new Map((snapshot?.nodes ?? []).filter((n) => n.title).map((n) => [n.id, n.title!])),
    [snapshot],
  );

  // Authors by how much they changed: the busiest first.
  const authors = useMemo(() => {
    const counts = new Map<string, number>();
    for (const c of data?.concepts ?? []) for (const a of c.authors ?? []) counts.set(a, (counts.get(a) ?? 0) + 1);
    return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  }, [data]);

  // An author who has no change in the new window is not a filter any more.
  useEffect(() => {
    if (author && !authors.some(([a]) => a === author)) setAuthor(null);
  }, [author, authors]);

  const rows = (data?.concepts ?? []).filter((c) => !author || c.authors?.includes(author));

  return (
    <section className="activity" aria-label="Recent activity">
      <header className="activity__head">
        <div>
          <p className="observatory__eyebrow">Activity</p>
          <h1 className="observatory__title">{headline(data, since, author)}</h1>
        </div>
        <div className="legend__switch activity__window" role="group" aria-label="Changes since">
          {WINDOWS.map((v) => (
            <button key={v} type="button" className="legend__option" aria-pressed={since === v} onClick={() => setSince(v)}>
              {v}
            </button>
          ))}
        </div>
      </header>

      {authors.length > 1 && (
        <ul className="activity__authors" aria-label="Filter by author">
          {authors.map(([name, count]) => (
            <li key={name}>
              <button
                type="button"
                className="chip"
                aria-pressed={author === name}
                onClick={() => setAuthor(author === name ? null : name)}
              >
                {name}
                <span className="chip__count">{count}</span>
              </button>
            </li>
          ))}
        </ul>
      )}

      {error ? (
        <p className="activity__note">Could not read the history: {error}</p>
      ) : !data ? (
        <p className="activity__note">Reading the history…</p>
      ) : rows.length === 0 ? (
        <p className="activity__note">No concept changed in the last {since}.</p>
      ) : (
        <ul className="activity__list">
          {rows.map((c) => (
            <li key={c.id}>
              <button
                type="button"
                className="activity__row"
                onClick={() => onOpen(c.id)}
                disabled={c.change === "deleted"}
              >
                <span className="activity__row-title">{titles.get(c.id) ?? c.id}</span>
                <span className="activity__row-meta">
                  <span className={`activity__tag activity__tag--${c.change}`}>{c.change}</span>
                  {" · "}
                  {relative(c.last_at)}
                  {c.authors?.length ? ` · ${c.authors.join(", ")}` : ""}
                </span>
                {c.reasons?.[0] && <span className="activity__reason">{c.reasons[0]}</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
      {data && (
        <p className="activity__note activity__foot">
          {data.commit_count} commit{data.commit_count === 1 ? "" : "s"}
          {data.truncated ? " · list truncated" : ""}
        </p>
      )}
    </section>
  );
}

/** The headline says it in words, like the Observatory's. */
function headline(data: ChangesResponse | null, since: string, author: string | null): string {
  if (!data) return "Reading the history…";
  const n = data.concepts.filter((c) => !author || c.authors?.includes(author)).length;
  const who = author ? ` by ${author}` : "";
  const when = since === "1d" ? "today" : `in the last ${since.replace("d", " days")}`;
  if (n === 0) return `Nothing changed${who} ${when}.`;
  return `${n === 1 ? "One concept" : `${n} concepts`} changed${who} ${when}.`;
}

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["day", 86_400_000],
  ["hour", 3_600_000],
  ["minute", 60_000],
];

function relative(iso: string): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return iso;
  const diff = t - Date.now();
  const fmt = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
  for (const [unit, ms] of UNITS) {
    if (Math.abs(diff) >= ms || unit === "minute") return fmt.format(Math.round(diff / ms), unit);
  }
  return iso;
}
