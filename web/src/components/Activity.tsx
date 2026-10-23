import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { fetchAudit, fetchChanges, fetchJournal } from "../api/client";
import type { AuditLog, ChangesResponse, CollectionSummary, ConceptChange, GraphSnapshot } from "../api/types";
import { changedAt, changeKind, journalMap, journalShape, mostWorked, parseJournal, shownReasons, type ChangeKind } from "../lib/activity";
import { collectionVar } from "../lib/palette";
import { ErrorState } from "./States";
import { Icon } from "./Icon";
import { Avatar, Count, Hero, MapBar, MapDot, Page, PageHeader, PageSection, Segmented, TitleBone, ActivitySkeleton } from "./Page";

const WINDOWS = [
  ["1d", "1d"],
  ["7d", "7d"],
  ["30d", "30d"],
  ["90d", "90d"],
  ["1y", "1y"],
  ["all", "All"],
] as const;
type Window = (typeof WINDOWS)[number][0];
/** The window's length in days; null for the whole history. */
const DAYS: Record<Window, number | null> = {
  "1d": 1,
  "7d": 7,
  "30d": 30,
  "90d": 90,
  "1y": 365,
  all: null,
};
/** What changes_since is asked: it takes <N>d, and "all" reaches further
 *  back than any KB has existed. */
const SINCE: Record<Window, string> = {
  "1d": "1d",
  "7d": "7d",
  "30d": "30d",
  "90d": "90d",
  "1y": "365d",
  all: "36500d",
};
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
  tabs,
  onOpen,
}: {
  kb: string;
  snapshot: GraphSnapshot | null;
  collections?: CollectionSummary[];
  live?: number;
  /** The section tabs (Activity · Retired), drawn under the header. */
  tabs?: ReactNode;
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
  const [error, setError] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);
  // The server's own upkeep is counted apart and listed only on request: it
  // can touch every concept in a morning without anyone learning anything.
  const [showUpkeep, setShowUpkeep] = useState(false);

  // A new KB starts blank. A new window or a live refetch (D337) keeps what
  // is on screen until the fresh answer replaces it: no flash of skeleton.
  useEffect(() => {
    setData(null);
    setError(null);
  }, [kb]);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    fetchChanges(kb, SINCE[since], controller.signal)
      .then((res) => {
        setData(res);
        setShown(since);
        setError(null);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [kb, since, live, attempt]);

  // The KB's log (log_append): whole-KB only, so a narrowed principal's 404
  // leaves the section out rather than showing an error.
  const [journal, setJournal] = useState<string | null>(null);
  useEffect(() => {
    setJournal(null);
  }, [kb]);
  useEffect(() => {
    const controller = new AbortController();
    fetchJournal(kb, controller.signal)
      .then((res) => setJournal(res.text ?? ""))
      .catch(() => undefined);
    return () => controller.abort();
  }, [kb, live, attempt]);

  // The audit log (admin only): any other principal's 404 leaves it out.
  const [audit, setAudit] = useState<AuditLog | null>(null);
  useEffect(() => {
    setAudit(null);
  }, [kb]);
  useEffect(() => {
    const controller = new AbortController();
    fetchAudit(kb, controller.signal)
      .then(setAudit)
      .catch(() => undefined);
    return () => controller.abort();
  }, [kb, live, attempt]);

  const titles = useMemo(
    () => new Map((snapshot?.nodes ?? []).filter((n) => n.title).map((n) => [n.id, n.title!])),
    [snapshot],
  );
  const mapTitles = useMemo(() => new Map(collections.map((c) => [c.name, c.title || c.name])), [collections]);
  const all = useMemo(() => data?.concepts ?? [], [data]);
  // What the filters and the "Where" bar count: the reader's changes, and the
  // server's upkeep only when it is on show.
  const counted = useMemo(
    () => (showUpkeep ? all : all.filter((c) => changeKind(c) !== "maintenance")),
    [all, showUpkeep],
  );

  // Authors and Maps by how much they changed: the busiest first.
  const authors = useMemo(() => rank(counted.flatMap((c) => c.authors ?? [])), [counted]);
  const maps = useMemo(() => rank(counted.map(mapOf)), [counted]);

  // A filter with no change in the new window is not a filter any more.
  useEffect(() => {
    if (author && !authors.some(([a]) => a === author)) setAuthor(null);
  }, [author, authors]);
  useEffect(() => {
    if (map && !maps.some(([m]) => m === map)) setMap(null);
  }, [map, maps]);

  const filtered = useMemo(
    () => all.filter((c) => (!author || c.authors?.includes(author)) && (!map || mapOf(c) === map)),
    [all, author, map],
  );
  const kinds = useMemo(() => new Map(filtered.map((c) => [c, changeKind(c)])), [filtered]);
  const count = (k: ChangeKind) => filtered.filter((c) => kinds.get(c) === k).length;
  const upkeep = count("maintenance");
  const rows = useMemo(
    () => (showUpkeep ? filtered : filtered.filter((c) => kinds.get(c) !== "maintenance")),
    [filtered, kinds, showUpkeep],
  );
  const days = useMemo(() => groupByDay(rows), [rows]);
  // The server's day series counts every change of the window; the concepts
  // keep each one once, at its newest change, so they only stand in when a
  // filter narrows the page (the series cannot be filtered) or the server is
  // older than the series (D1057).
  const byDay = map === null && author === null ? data?.changes_by_day : undefined;
  const histogram = useMemo(
    () => perDay(filtered, kinds, DAYS[shown], byDay),
    [filtered, kinds, shown, byDay],
  );
  const top = useMemo(() => mostWorked(filtered), [filtered]);

  return (
    <Page label="Recent activity" busy={loading} className="timeline">
      <PageHeader
        eyebrow="Activity"
        title={
          <Headline
            data={data}
            failed={error !== null}
            n={filtered.length - upkeep}
            since={shown}
            author={author}
            mapTitle={map ? mapTitles.get(map) : null}
          />
        }
        actions={<Segmented<Window> label="Changes since" value={since} options={WINDOWS} onChange={setSince} />}
      />
      {tabs}

      <div className="page__body" data-loading={loading && data !== null}>
        {data && (
          <Hero label="Summary and filters" className="activity-hero">
            <div className="activity-sum">
              <div className="activity-sum__lead">
                <ul className="activity-stats" aria-label="Changes in the window">
                  <Stat n={count("edited")} label="edited" />
                  <Stat n={count("new")} label="new" tone="ok" />
                  <Stat n={count("reorganised")} label="reorganised" />
                  {authors.length > 1 && <Stat n={authors.length} label="authors" />}
                </ul>
                {top && top.count > 1 && (
                  <button type="button" className="activity-top" onClick={() => onOpen(top.change.id)}>
                    <span className="activity-top__label">Most worked on</span>
                    <span className="activity-top__title">{titles.get(top.change.id) ?? top.change.id}</span>
                    <span className="activity-top__count">{top.count} changes</span>
                  </button>
                )}
              </div>
              {histogram.length > 1 && <ActivityBars bars={histogram} />}
            </div>
            {(maps.length > 0 || authors.length > 1 || upkeep > 0) && (
              <div className="activity-facets">
                {maps.length > 0 && (
                  <div className="activity-facet activity-where">
                    <span className="activity-facet__label">Where</span>
                    <div className="activity-where__body">
                      <MapBar maps={maps} active={map} />
                      <ul className="activity-legend" aria-label="Filter by Map">
                        {maps.map(([name, n]) => (
                          <li key={name}>
                            <button
                              type="button"
                              className="activity-legend__item"
                              aria-pressed={map === name}
                              style={{ "--map": collectionVar(name || "(root)") } as CSSProperties}
                              onClick={() => setMap(map === name ? null : name)}
                            >
                              <MapDot map={name} />
                              <span>{mapTitles.get(name) ?? name}</span>
                              <span className="activity-legend__count">{n}</span>
                            </button>
                          </li>
                        ))}
                      </ul>
                    </div>
                  </div>
                )}
                {authors.length > 1 && (
                  <div className="activity-facet activity-who">
                    <span className="activity-facet__label">Who</span>
                    <ul className="activity-people" aria-label="Filter by author">
                      {authors.map(([name, n]) => {
                        const label = `${name}: ${n} ${n === 1 ? "change" : "changes"}`;
                        return (
                          <li key={name}>
                            <button
                              type="button"
                              className="activity-person"
                              aria-pressed={author === name}
                              aria-label={label}
                              title={label}
                              style={{ "--map": collectionVar(name) } as CSSProperties}
                              onClick={() => setAuthor(author === name ? null : name)}
                            >
                              <Avatar name={name} />
                            </button>
                          </li>
                        );
                      })}
                    </ul>
                  </div>
                )}
                {upkeep > 0 && (
                  <button
                    type="button"
                    className="activity-upkeep"
                    aria-pressed={showUpkeep}
                    onClick={() => setShowUpkeep((v) => !v)}
                  >
                    + {upkeep} touched by automatic maintenance · {showUpkeep ? "hide" : "show"}
                  </button>
                )}
              </div>
            )}
          </Hero>
        )}

        {error ? (
          <ErrorState error={error} onRetry={() => setAttempt((n) => n + 1)} />
        ) : !data ? (
          <ActivitySkeleton label="Reading the history" />
        ) : rows.length === 0 ? (
          <div className="timeline__empty">
            <span className="timeline__empty-icon" aria-hidden="true">
              <Icon name="moon" size={18} />
            </span>
            <div className="timeline__empty-text">
              <p className="timeline__empty-title">Quiet {shown === "1d" ? "day" : "stretch"}.</p>
              <p className="timeline__empty-note">
                No concept edited {when(shown)}
                {author ? ` by ${author}` : ""}.
              </p>
            </div>
            {/* An empty window offers the next wider one rather than leaving the reader to guess. */}
            {(() => {
              const next = WINDOWS[WINDOWS.findIndex(([w]) => w === shown) + 1];
              return next ? (
                <button type="button" className="button timeline__empty-action" onClick={() => setSince(next[0])}>
                  {next[0] === "all"
                    ? "Show the whole history"
                    : next[0] === "1y"
                      ? "Show the last year"
                      : `Show the last ${DAYS[next[0]]} days`}
                </button>
              ) : null;
            })()}
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
        {journal !== null && (data !== null || error !== null) && (
          <PageSection title="Journal" id="activity-journal">
            {journal.trim() === "" ? (
              <p className="page-note">
                The journal is the KB&apos;s log: one entry per working session, written by agents with{" "}
                <code>log_append</code> when they close a piece of work. Nothing has been logged yet.
              </p>
            ) : (
              <>
                <p className="page-note">
                  The last entries of the KB&apos;s log, written by agents with <code>log_append</code>.
                </p>
                <JournalEntries text={journal} mapTitles={mapTitles} onOpen={onOpen} />
              </>
            )}
          </PageSection>
        )}
        {audit !== null && (data !== null || error !== null) && <AuditSection audit={audit} />}
      </div>
    </Page>
  );
}

/**
 * The journal tail as a log, grouped by day like the timeline above it: a
 * day heading with its count, then one row per entry, its time beside its
 * lines. An entry whose heading is not a time keeps it as its label.
 */
function JournalEntries({
  text,
  mapTitles,
  onOpen,
}: {
  text: string;
  mapTitles: ReadonlyMap<string, string>;
  onOpen(conceptId: string): void;
}) {
  const maps = useMemo(() => new Set(mapTitles.keys()), [mapTitles]);
  const days = useMemo(() => {
    const groups: { key: string; label: string; items: { at: string | null; t: number; body: string }[] }[] = [];
    for (const e of parseJournal(text)) {
      const t = e.at ? Date.parse(e.at) : NaN;
      const key = Number.isFinite(t) ? dayKey(t) : "undated";
      let g = groups[groups.length - 1];
      if (!g || g.key !== key) {
        g = { key, label: Number.isFinite(t) ? dayLabel(t) : "Undated", items: [] };
        groups.push(g);
      }
      g.items.push({ at: e.at, t, body: e.body });
    }
    return groups;
  }, [text]);
  return (
    <ol className="journal">
      {days.map((day, d) => (
        <li key={`${day.key}-${d}`} className="journal__day">
          <h3 className="timeline__date">
            <span>{day.label}</span>
            <span className="timeline__date-count">{day.items.length}</span>
          </h3>
          <ol className="timeline__entries">
            {day.items.map((e, i) => (
              <JournalEntry
                key={`${e.at}-${i}`}
                at={e.at}
                t={e.t}
                body={e.body}
                map={journalMap(e.body, maps)}
                mapTitles={mapTitles}
                onOpen={onOpen}
              />
            ))}
          </ol>
        </li>
      ))}
    </ol>
  );
}

/** One journal entry on the timeline's rail: time, a node in the colour of the
 *  Map the entry names (the accent when it names none), then the note or the
 *  tool call, whose concept opens like a timeline row. A long note is clamped
 *  and opens in place. */
function JournalEntry({
  at,
  t,
  body,
  map,
  mapTitles,
  onOpen,
}: {
  at: string | null;
  t: number;
  body: string;
  map: string | null;
  mapTitles: ReadonlyMap<string, string>;
  onOpen(conceptId: string): void;
}) {
  const shape = journalShape(body);
  const [open, setOpen] = useState(false);
  // "Show more" only where the clamp actually cut something.
  const noteRef = useRef<HTMLDivElement>(null);
  const [clamped, setClamped] = useState(false);
  useLayoutEffect(() => {
    const el = noteRef.current;
    if (!el || open) return;
    const measure = () =>
      setClamped([...el.querySelectorAll("p")].some((p) => p.scrollHeight > p.clientHeight + 1));
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [open, body]);
  const time = Number.isFinite(t) ? (
    <time
      className="timeline__time"
      dateTime={at!}
      title={new Date(t).toLocaleString("en", { dateStyle: "medium", timeStyle: "medium" })}
    >
      {clock(at!)}
    </time>
  ) : (
    <span className="timeline__time">{at}</span>
  );
  return (
    <li
      className={`timeline__entry journal__entry journal__entry--${shape.kind}`}
      style={{ "--map": map ? collectionVar(map) : "var(--accent)" } as CSSProperties}
    >
      <div className="journal__item">
        {time}
        <span className="timeline__track" aria-hidden="true">
          <span className="timeline__node" />
        </span>
        {shape.kind === "tool" ? (
          <p className="journal__tool">
            <span className="journal__tool-name">{shape.tool}</span>
            {map ? (
              <button type="button" className="journal__target" onClick={() => onOpen(shape.target)}>
                <span className="timeline__map">
                  <MapDot map={map} />
                  {mapTitles.get(map) ?? map}
                </span>
                <span className="journal__target-id">{shape.target.slice(map.length + 1)}</span>
              </button>
            ) : (
              <span className="journal__target-id">{shape.target}</span>
            )}
          </p>
        ) : (
          <div className="journal__note" ref={noteRef}>
            <p className="journal__lead" data-open={open}>
              {shape.lead}
            </p>
            {shape.details && (
              <p className="journal__details" data-open={open}>
                {shape.details}
              </p>
            )}
            {(clamped || open) && (
              <button type="button" className="journal__more" aria-expanded={open} onClick={() => setOpen((v) => !v)}>
                {open ? "Show less" : "Show more"}
              </button>
            )}
          </div>
        )}
      </div>
    </li>
  );
}

/** Who called which tool on this KB, newest first; admins only. */
function AuditSection({ audit }: { audit: AuditLog }) {
  // An attempt and its completion are two entries: the completions say how it ended.
  const entries = audit.entries.filter((e) => e.phase !== "attempt");
  return (
    <PageSection title="Audit log" id="activity-audit" count={entries.length}>
      {!audit.enabled ? (
        <p className="page-note">This server keeps no audit log.</p>
      ) : entries.length === 0 ? (
        <p className="page-note">
          Nothing recorded yet. The audit log lists every tool call: who made it, and how it ended.
        </p>
      ) : (
        <>
          <p className="page-note">
            The most recent tool calls on this KB, with the principal that made each one. Visible to admins only.
          </p>
          <ul className="rows">
            {entries.map((e, i) => (
              <li key={`${e.at}-${i}`} className="row audit__row">
                <span className="row__body">
                  <span className="row__title">
                    <code>{e.tool}</code> by {e.principal || "an unnamed principal"}
                  </span>
                  <span className="row__meta">
                    <time dateTime={e.at}>{new Date(e.at).toLocaleString("en")}</time>
                    <span className="pill" data-status={e.outcome}>
                      {e.outcome}
                    </span>
                    {e.read_only && <span className="pill">read-only</span>}
                    {e.commit_sha && <code>{e.commit_sha.slice(0, 7)}</code>}
                  </span>
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </PageSection>
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
  const reasons = shownReasons(c);
  const [first, ...more] = reasons;
  return (
    <li className="timeline__entry" style={{ "--map": collectionVar(mapOf(c)) } as CSSProperties}>
      <button type="button" className="timeline__item" onClick={() => onOpen(c.id)} disabled={c.change === "deleted"}>
        <time
          className="timeline__time"
          dateTime={changedAt(c)}
          title={new Date(changedAt(c)).toLocaleString("en", { dateStyle: "medium", timeStyle: "medium" })}
        >
          {clock(changedAt(c))}
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
            <span className="timeline__reason" title={reasons.join("\n")}>
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

const GLYPH: Record<string, string> = {
  added: "+",
  modified: "~",
  deleted: "−",
  renamed: "→",
};
const TONE: Record<string, string> = {
  added: "pill--ok",
  deleted: "pill--error",
};

function ChangePill({ change }: { change: string }) {
  return (
    <span className={`pill ${TONE[change] ?? ""} timeline__change--${change}`}>
      <span aria-hidden="true">{GLYPH[change] ?? "•"}</span>
      {change}
    </span>
  );
}

/** One figure of the summary line: a bold number and its word. A zero fades;
 *  the new concepts, when there are any, are the one good-news tone. */
function Stat({ n, label, tone }: { n: number; label: string; tone?: "ok" }) {
  return (
    <li className="activity-stat" data-tone={n === 0 ? "muted" : tone}>
      <strong>{n}</strong> {label}
    </li>
  );
}

type DayBar = { day: number; label: string; work: number; upkeep: number };

/**
 * Concepts last touched per day (per week or month over a long window), as a
 * bar per stretch placed by real time -- the look of the growth replay's
 * timeline. The bar is the reader's changes, scaled on their own peak with
 * the square root, so a quiet day still shows beside a busy one; the server's
 * upkeep is a faint tick under the baseline, so a maintenance sweep never
 * dwarfs the work. Bars widen when there are few. Every bar is a labelled hit
 * target, so the numbers reach a keyboard and a screen reader. The caption
 * beside the track reads the hovered bar (the latest, at rest) and keeps a
 * fixed width, so it never moves the bars under the pointer.
 */
function ActivityBars({ bars }: { bars: DayBar[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const peak = Math.max(1, ...bars.map((b) => b.work));
  const upPeak = Math.max(1, ...bars.map((b) => b.upkeep));
  const height = (n: number) => (n === 0 ? 0 : Math.max(10, Math.sqrt(n / peak) * 100));
  const shown = bars[hover ?? bars.length - 1]!;
  const at = (i: number) => `${((i + 0.5) / bars.length) * 100}%`;
  const width = Math.max(2, Math.min(10, Math.floor(300 / bars.length) - 2));
  const daily = bars.length < 2 || bars[1]!.day - bars[0]!.day <= DAY_MS;
  return (
    <figure
      className="activity-bars"
      style={{ "--bar-w": `${width}px` } as CSSProperties}
      onMouseLeave={() => setHover(null)}
    >
      <ol
        className="activity-bars__track"
        aria-label={`Changes per ${daily ? "day" : "stretch"}, ${bars[0]!.label} to today`}
      >
        {bars.map((b, i) => (
          <li key={b.day} style={{ left: at(i) }}>
            <button
              type="button"
              className="activity-bars__day"
              data-hover={hover === i || undefined}
              aria-label={`${b.label}: ${b.work} changed${b.upkeep ? `, ${b.upkeep} maintenance` : ""}`}
              onMouseEnter={() => setHover(i)}
              onFocus={() => setHover(i)}
              onBlur={() => setHover(null)}
              tabIndex={-1}
            >
              <span className="activity-bars__bar" style={{ height: `${height(b.work)}%` }} />
              {b.upkeep > 0 && (
                <span
                  className="activity-bars__upkeep"
                  style={{ opacity: 0.25 + 0.6 * Math.sqrt(b.upkeep / upPeak) }}
                />
              )}
            </button>
          </li>
        ))}
      </ol>
      <figcaption className="activity-bars__caption" aria-live="polite" title={`${bars[0]!.label} – today`}>
        {hover === null ? (daily ? "today" : "latest") : shown.label} · <strong>{shown.work}</strong> changed
        {shown.upkeep > 0 && <> · {shown.upkeep} maintenance</>}
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
  return new Date(t).toLocaleDateString("en", {
    weekday: "long",
    day: "numeric",
    month: "short",
  });
}

/** The newest day first, each day's concepts newest first. */
function groupByDay(rows: ConceptChange[]): { key: string; label: string; items: ConceptChange[] }[] {
  const sorted = [...rows].sort((a, b) => Date.parse(changedAt(b)) - Date.parse(changedAt(a)));
  const out: { key: string; label: string; items: ConceptChange[] }[] = [];
  for (const c of sorted) {
    const t = Date.parse(changedAt(c));
    const key = Number.isFinite(t) ? dayKey(t) : "unknown";
    let day = out[out.length - 1];
    if (!day || day.key !== key) {
      day = {
        key,
        label: Number.isFinite(t) ? dayLabel(t) : "Undated",
        items: [],
      };
      out.push(day);
    }
    day.items.push(c);
  }
  return out;
}

/** One bar per day of the window -- or, for the whole history, from the
 *  oldest change on -- oldest first, today last. */
function perDay(
  rows: ConceptChange[],
  kinds: Map<ConceptChange, ChangeKind>,
  days: number | null,
  byDay?: Record<string, { work: number; upkeep?: number }>,
): DayBar[] {
  const today = startOfDay(Date.now());
  // A UTC day of the server's series, at its noon: the local day it falls on
  // for every offset within twelve hours of UTC.
  const series = byDay
    ? Object.entries(byDay)
        .map(([d, n]) => ({ t: Date.parse(`${d}T12:00:00Z`), n }))
        .filter((e) => Number.isFinite(e.t))
    : null;
  let span = days;
  if (span === null) {
    const times = series ? series.map((e) => e.t) : rows.map((c) => Date.parse(changedAt(c))).filter(Number.isFinite);
    const oldest = Math.min(today, ...times);
    span = Math.max(2, Math.round((today - startOfDay(oldest)) / DAY_MS) + 1);
  }
  const bars: DayBar[] = Array.from({ length: span }, (_, i) => {
    const t = today - (span! - 1 - i) * DAY_MS;
    return {
      day: t,
      label: new Date(t).toLocaleDateString("en", {
        day: "numeric",
        month: "short",
        year: span! > 365 ? "numeric" : undefined,
      }),
      work: 0,
      upkeep: 0,
    };
  });
  const index = new Map(bars.map((b, i) => [dayKey(b.day), i]));
  if (series) {
    for (const { t, n } of series) {
      const i = index.get(dayKey(t));
      if (i === undefined) continue;
      bars[i]!.work += n.work;
      bars[i]!.upkeep += n.upkeep ?? 0;
    }
    return bucket(bars);
  }
  for (const c of rows) {
    const t = Date.parse(changedAt(c));
    const i = Number.isFinite(t) ? index.get(dayKey(t)) : undefined;
    if (i === undefined) continue;
    if (kinds.get(c) === "maintenance") bars[i]!.upkeep++;
    else bars[i]!.work++;
  }
  return bucket(bars);
}

/** Past six weeks a bar per day is a comb too thin to read: the days are
 *  summed into weeks, past a year into 30-day stretches, the last one ending
 *  today. A bar then says its range. */
function bucket(days: DayBar[]): DayBar[] {
  const step = days.length <= 45 ? 1 : days.length <= 400 ? 7 : 30;
  if (step === 1) return days;
  const out: DayBar[] = [];
  for (let end = days.length; end > 0; end -= step) {
    const part = days.slice(Math.max(0, end - step), end);
    const first = part[0]!;
    const last = part[part.length - 1]!;
    out.unshift({
      day: first.day,
      label: part.length === 1 ? first.label : `${first.label} – ${last.label}`,
      work: part.reduce((n, d) => n + d.work, 0),
      upkeep: part.reduce((n, d) => n + d.upkeep, 0),
    });
  }
  return out;
}

function when(since: Window): string {
  if (since === "1d") return "today";
  if (since === "1y") return "in the last year";
  if (since === "all") return "since the KB began";
  return `in the last ${since.replace("d", " days")}`;
}

/** The headline says it in words; the count leads. */
function Headline({
  data,
  failed,
  n,
  since,
  author,
  mapTitle,
}: {
  data: ChangesResponse | null;
  failed: boolean;
  n: number;
  since: Window;
  author: string | null;
  mapTitle: string | null | undefined;
}) {
  if (!data) return <>{failed ? "History unavailable" : <TitleBone label="Reading the history" />}</>;
  const who = author ? ` by ${author}` : "";
  const where = mapTitle ? ` in ${mapTitle}` : "";
  if (n === 0) return <>{`Nothing edited${where}${who} ${when(since)}.`}</>;
  return (
    <>
      <Count>{data.truncated ? `${n}+` : n}</Count> {n === 1 ? "concept" : "concepts"} edited{where}
      {who} {when(since)}.
    </>
  );
}

function clock(iso: string): string {
  const t = Date.parse(iso);
  return Number.isFinite(t)
    ? new Date(t).toLocaleTimeString("en", {
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      })
    : "";
}
