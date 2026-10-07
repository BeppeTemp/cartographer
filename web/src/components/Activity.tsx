import { useEffect, useMemo, useState, type CSSProperties } from "react";
import { fetchChanges } from "../api/client";
import type { ChangesResponse, CollectionSummary, ConceptChange, GraphSnapshot } from "../api/types";
import { collectionVar } from "../lib/palette";
import {
  Avatar,
  Band,
  Count,
  Facet,
  FacetRow,
  FacetRows,
  MapBar,
  MapDot,
  Page,
  PageHeader,
  Segmented,
  SkeletonRows,
  Stats,
} from "./Page";

const WINDOWS = [
  ["1d", "1d"],
  ["7d", "7d"],
  ["30d", "30d"],
] as const;
type Window = (typeof WINDOWS)[number][0];
const DAYS: Record<Window, number> = { "1d": 1, "7d": 7, "30d": 30 };
const DAY_MS = 86_400_000;

/**
 * What changed in the KB, and who changed it: the agents' own
 * `changes_since`, as a page beside Work and Health. The band sums the
 * window up — how much, where, by whom, and on which days — and the timeline
 * below groups each concept under the day it last changed. A reader reviewing
 * a session narrows it to one author or one Map and opens each concept on the
 * atlas.
 */
export function Activity({
  kb,
  snapshot,
  collections = [],
  live = 0,
  onOpen,
}: {
  kb: string;
  snapshot: GraphSnapshot | null;
  collections?: CollectionSummary[];
  live?: number;
  onOpen(conceptId: string): void;
}) {
  const [since, setSince] = useState<Window>("7d");
  const [author, setAuthor] = useState<string | null>(null);
  const [map, setMap] = useState<string | null>(null);
  const [data, setData] = useState<ChangesResponse | null>(null);
  // The window the data on screen answers: while a new window loads, the
  // old answer stays (dimmed) and keeps describing itself correctly.
  const [shown, setShown] = useState<Window>(since);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // A new KB starts blank. A new window or a live refetch (D336) keeps what
  // is on screen until the fresh answer replaces it: no flash of skeleton.
  useEffect(() => {
    setData(null);
    setError(null);
  }, [kb]);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    fetchChanges(kb, since, controller.signal)
      .then((res) => {
        setData(res);
        setShown(since);
        setError(null);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [kb, since, live]);

  const titles = useMemo(
    () => new Map((snapshot?.nodes ?? []).filter((n) => n.title).map((n) => [n.id, n.title!])),
    [snapshot],
  );
  const mapTitles = useMemo(() => new Map(collections.map((c) => [c.name, c.title || c.name])), [collections]);
  const all = useMemo(() => data?.concepts ?? [], [data]);

  // Authors and Maps by how much they changed: the busiest first.
  const authors = useMemo(() => rank(all.flatMap((c) => c.authors ?? [])), [all]);
  const maps = useMemo(() => rank(all.map(mapOf)), [all]);

  // A filter with no change in the new window is not a filter any more.
  useEffect(() => {
    if (author && !authors.some(([a]) => a === author)) setAuthor(null);
  }, [author, authors]);
  useEffect(() => {
    if (map && !maps.some(([m]) => m === map)) setMap(null);
  }, [map, maps]);

  const rows = useMemo(
    () => all.filter((c) => (!author || c.authors?.includes(author)) && (!map || mapOf(c) === map)),
    [all, author, map],
  );
  const days = useMemo(() => groupByDay(rows), [rows]);
  const histogram = useMemo(() => perDay(rows, DAYS[shown]), [rows, shown]);
  const added = rows.filter((c) => c.change === "added").length;
  const deleted = rows.filter((c) => c.change === "deleted").length;

  return (
    <Page label="Recent activity" busy={loading} className="timeline">
      <PageHeader
        eyebrow="Activity"
        title={<Headline data={data} n={rows.length} since={shown} author={author} mapTitle={map ? mapTitles.get(map) : null} />}
        actions={<Segmented<Window> label="Changes since" value={since} options={WINDOWS} onChange={setSince} />}
      />

      <div className="page__body" data-loading={loading && data !== null}>
        {data && all.length > 0 && (
          <Band label="Summary and filters">
            <Facet className="timeline__summary">
              <Stats
                items={[
                  { label: "concepts", value: rows.length },
                  { label: "new", value: added, tone: added ? "ok" : "muted" },
                  { label: "removed", value: deleted, tone: deleted ? "error" : "muted" },
                  { label: data.commit_count === 1 ? "commit" : "commits", value: data.commit_count },
                ]}
              />
              {histogram.length > 1 && <DayBars bars={histogram} />}
            </Facet>

            <Facet title="Where" id="activity-where">
              <MapBar maps={maps} active={map} />
              <FacetRows label="Filter by Map" columns={2}>
                {maps.map(([name, count]) => (
                  <li key={name}>
                    <FacetRow
                      label={mapTitles.get(name) ?? name}
                      count={count}
                      pressed={map === name}
                      hue={collectionVar(name)}
                      mark={<MapDot map={name} />}
                      onToggle={() => setMap(map === name ? null : name)}
                    />
                  </li>
                ))}
              </FacetRows>
            </Facet>

            <Facet title="Who" id="activity-who">
              <FacetRows label="Filter by author">
                {authors.map(([name, count]) => (
                  <li key={name}>
                    <FacetRow
                      label={name}
                      count={count}
                      pressed={author === name}
                      mark={<Avatar name={name} size="small" />}
                      onToggle={() => setAuthor(author === name ? null : name)}
                    />
                  </li>
                ))}
              </FacetRows>
            </Facet>
          </Band>
        )}

        {error ? (
          <p className="page-note">Could not read the history: {error}</p>
        ) : !data ? (
          <SkeletonRows label="Reading the history" />
        ) : rows.length === 0 ? (
          <div className="timeline__empty">
            <p className="timeline__empty-title">Quiet {shown === "1d" ? "day" : "stretch"}.</p>
            <p className="page-note">
              No concept changed {when(shown)}
              {author ? ` by ${author}` : ""}.
            </p>
          </div>
        ) : (
          <ol className="timeline__days">
            {days.map((day) => (
              <li key={day.key} className="timeline__day">
                <h2 className="timeline__date">
                  <span>{day.label}</span>
                  <span className="timeline__date-count">{day.items.length}</span>
                </h2>
                <ol className="timeline__entries">
                  {day.items.map((c) => (
                    <Entry
                      key={c.id}
                      change={c}
                      title={titles.get(c.id) ?? c.id}
                      mapTitle={mapTitles.get(mapOf(c)) ?? mapOf(c)}
                      onOpen={onOpen}
                    />
                  ))}
                </ol>
              </li>
            ))}
          </ol>
        )}
        {data?.truncated && (
          <p className="page-note">Showing the {all.length} most recent changes: narrow the window to see the rest.</p>
        )}
      </div>
    </Page>
  );
}

function Entry({
  change: c,
  title,
  mapTitle,
  onOpen,
}: {
  change: ConceptChange;
  title: string;
  mapTitle: string;
  onOpen(conceptId: string): void;
}) {
  const [first, ...more] = c.reasons ?? [];
  return (
    <li className="timeline__entry" style={{ "--map": collectionVar(mapOf(c)) } as CSSProperties}>
      <button type="button" className="timeline__item" onClick={() => onOpen(c.id)} disabled={c.change === "deleted"}>
        <time className="timeline__time" dateTime={c.last_at} title={new Date(c.last_at).toLocaleString()}>
          {clock(c.last_at)}
        </time>
        <span className="timeline__track" aria-hidden="true">
          <span className="timeline__node" />
        </span>
        <span className="timeline__item-body">
          <span className="timeline__item-head">
            <span className="timeline__title">{title}</span>
            <span className="timeline__map">
              <MapDot map={mapOf(c)} />
              {mapTitle}
            </span>
            <ChangePill change={c.change} />
          </span>
          {first && (
            <span className="timeline__reason" title={c.reasons!.join("\n")}>
              {first}
              {more.length > 0 && ` +${more.length}`}
            </span>
          )}
        </span>
        {c.authors?.length ? (
          <span className="timeline__authors" aria-label={`By ${c.authors.join(", ")}`}>
            {c.authors.map((a) => (
              <span key={a} title={a}>
                <Avatar name={a} size="small" />
              </span>
            ))}
          </span>
        ) : null}
      </button>
    </li>
  );
}

const GLYPH: Record<string, string> = { added: "+", modified: "~", deleted: "−", renamed: "→" };
const TONE: Record<string, string> = { added: "pill--ok", deleted: "pill--error" };

function ChangePill({ change }: { change: string }) {
  return (
    <span className={`pill ${TONE[change] ?? ""} timeline__change--${change}`}>
      <span aria-hidden="true">{GLYPH[change] ?? "•"}</span>
      {change}
    </span>
  );
}

/** Concepts last touched per day: one series, one colour, a tooltip per bar. */
function DayBars({ bars }: { bars: { key: string; label: string; count: number }[] }) {
  const max = Math.max(1, ...bars.map((b) => b.count));
  return (
    <figure className="timeline__chart">
      <div
        className="timeline__bars"
        role="img"
        aria-label={`Changes per day: ${bars.map((b) => `${b.label} ${b.count}`).join(", ")}`}
      >
        {bars.map((b) => (
          <span key={b.key} className="timeline__bar-slot" title={`${b.label}: ${b.count}`}>
            <span className="timeline__bar" data-empty={b.count === 0} style={{ height: `${(b.count / max) * 100}%` }} />
          </span>
        ))}
      </div>
      <figcaption className="timeline__axis">
        <span>{bars[0]!.label}</span>
        <span>today</span>
      </figcaption>
    </figure>
  );
}

/** The top-level collection a concept lives in: its id's first segment. */
function mapOf(c: Pick<ConceptChange, "id">): string {
  const i = c.id.indexOf("/");
  return i < 0 ? c.id : c.id.slice(0, i);
}

function rank(values: string[]): [string, number][] {
  const counts = new Map<string, number>();
  for (const v of values) counts.set(v, (counts.get(v) ?? 0) + 1);
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
}

function dayKey(t: number): string {
  const d = new Date(t);
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

function startOfDay(t: number): number {
  const d = new Date(t);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

function dayLabel(t: number, now = Date.now()): string {
  const diff = Math.round((startOfDay(now) - startOfDay(t)) / DAY_MS);
  if (diff === 0) return "Today";
  if (diff === 1) return "Yesterday";
  return new Date(t).toLocaleDateString("en", { weekday: "long", day: "numeric", month: "short" });
}

/** The newest day first, each day's concepts newest first. */
function groupByDay(rows: ConceptChange[]): { key: string; label: string; items: ConceptChange[] }[] {
  const sorted = [...rows].sort((a, b) => Date.parse(b.last_at) - Date.parse(a.last_at));
  const out: { key: string; label: string; items: ConceptChange[] }[] = [];
  for (const c of sorted) {
    const t = Date.parse(c.last_at);
    const key = Number.isFinite(t) ? dayKey(t) : "unknown";
    let day = out[out.length - 1];
    if (!day || day.key !== key) {
      day = { key, label: Number.isFinite(t) ? dayLabel(t) : "Undated", items: [] };
      out.push(day);
    }
    day.items.push(c);
  }
  return out;
}

/** One bar per day of the window, oldest first, today last. */
function perDay(rows: ConceptChange[], days: number): { key: string; label: string; count: number }[] {
  const today = startOfDay(Date.now());
  const bars = Array.from({ length: days }, (_, i) => {
    const t = today - (days - 1 - i) * DAY_MS;
    return { key: dayKey(t), label: new Date(t).toLocaleDateString("en", { day: "numeric", month: "short" }), count: 0 };
  });
  const index = new Map(bars.map((b, i) => [b.key, i]));
  for (const c of rows) {
    const t = Date.parse(c.last_at);
    const i = Number.isFinite(t) ? index.get(dayKey(t)) : undefined;
    if (i !== undefined) bars[i]!.count++;
  }
  return bars;
}

function when(since: string): string {
  return since === "1d" ? "today" : `in the last ${since.replace("d", " days")}`;
}

/** The headline says it in words; the count leads. */
function Headline({
  data,
  n,
  since,
  author,
  mapTitle,
}: {
  data: ChangesResponse | null;
  n: number;
  since: string;
  author: string | null;
  mapTitle: string | null | undefined;
}) {
  if (!data) return <>Reading the history…</>;
  const who = author ? ` by ${author}` : "";
  const where = mapTitle ? ` in ${mapTitle}` : "";
  if (n === 0) return <>{`Nothing changed${where}${who} ${when(since)}.`}</>;
  return (
    <>
      <Count>{data.truncated ? `${n}+` : n}</Count> {n === 1 ? "concept" : "concepts"} changed{where}
      {who} {when(since)}.
    </>
  );
}

function clock(iso: string): string {
  const t = Date.parse(iso);
  return Number.isFinite(t)
    ? new Date(t).toLocaleTimeString("en", { hour: "2-digit", minute: "2-digit", hour12: false })
    : "";
}
