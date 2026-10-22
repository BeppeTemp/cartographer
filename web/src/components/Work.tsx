import { Fragment, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { fetchMaintenanceReview, fetchPendingSources, fetchStatus, fetchWork } from "../api/client";
import type { CollectionSummary, KBStatus, ReviewItem, SourceEntry, WorkEntry, WorkResponse } from "../api/types";
import { reviewInfo } from "../lib/review";
import { collectionVar } from "../lib/palette";
import { ErrorState } from "./States";
import { Icon } from "./Icon";
import {
  Count,
  FilterChip,
  FilterChips,
  Hero,
  HeroRow,
  MapBar,
  MapDot,
  Page,
  PageHeader,
  Quiet,
  Segmented,
  TitleBone,
  WorkSkeleton,
} from "./Page";

type Layout = "status" | "map";
const LAYOUTS = [
  ["status", "By status"],
  ["map", "By map"],
] as const;

type SearchMiss = NonNullable<KBStatus["search_misses"]>[number];

/** What a card stands for when it is not a concept. */
type Pseudo = { kind: "miss"; query: string; count: number; lastSeen?: string } | { kind: "review"; item: ReviewItem };

/** A work entry, or a card with no page behind it (a search miss, a proposal). */
export type BoardEntry = WorkEntry & { pseudo?: Pseudo };

/** The board's cards that do not come from `work_list`, and the searches closed since. */
interface Extra {
  entries: BoardEntry[];
  found: string[];
}
const NO_EXTRA: Extra = { entries: [], found: [] };

/**
 * A concept listed only for its unchecked items is grouped apart: its own
 * status is closed (done, active…), and showing it as a status column would
 * read as open work in that state.
 */
const ITEMS_ONLY = "unchecked items";

/** The column of the sources registered and not ingested yet (D818). */
const TO_INGEST = "to ingest";

/** The column of the doctor's proposals (D704): each one waits for a decision. */
const TO_DECIDE = "to decide";

/** A proposal names at most this many pages on its card; the rest are counted. */
const MAX_REFS = 4;

/**
 * Work a concept carries by what it is rather than by its status: each reason
 * has its own column, named for what a person does about it.
 */
const REASON_COLUMN: Record<NonNullable<WorkEntry["reason"]>, string> = {
  contradiction: "to resolve",
  gap: "to answer",
  review: "to review",
};

/** Columns in the order work moves through them; anything else follows, sorted. */
const STATUS_ORDER = [
  REASON_COLUMN.contradiction,
  "decision-needed",
  "blocked",
  "in-progress",
  "open",
  REASON_COLUMN.gap,
  "proposed",
  REASON_COLUMN.review,
  TO_DECIDE,
  TO_INGEST,
  ITEMS_ONLY,
];

/**
 * Open work (D302): concepts in an open status for their map, of any type,
 * and unchecked items in any concept. A read-only view of `work_list`: work
 * changes through the agents' ordinary write tools, so there is no drag here.
 * The band counts it and filters it by Map and by status; the board below
 * lays it out as columns by status, or as one card per Map.
 *
 * Everything the KB still has to do is here, and Health keeps the lint
 * problems. Two things are work without being a concept, and join the board
 * client-side: the searches that found nothing (a page to write: "to answer")
 * and the doctor's proposals (a choice to make: "to decide"). Neither has a
 * page of its own, so a card for a search opens nothing.
 */
export function Work({
  kb,
  collections = [],
  live = 0,
  onOpen,
}: {
  kb: string;
  collections?: CollectionSummary[];
  live?: number;
  onOpen(conceptId: string): void;
}) {
  const [data, setData] = useState<WorkResponse | null>(null);
  const [extra, setExtra] = useState<Extra>(NO_EXTRA);
  const [error, setError] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);
  const [layout, setLayout] = useState<Layout>("status");
  const [map, setMap] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [staleOnly, setStaleOnly] = useState(false);
  const [text, setText] = useState("");

  // A new KB starts blank; a live refetch (D337) keeps what is on screen
  // until the fresh answer replaces it.
  useEffect(() => {
    setData(null);
    setExtra(NO_EXTRA);
    setError(null);
  }, [kb]);

  useEffect(() => {
    const controller = new AbortController();
    // The pending sources join the open work: ingesting them is work like
    // any other. So do the searches nothing answered and the doctor's
    // proposals. Each is optional: without one (an older server, no ledger, a
    // principal that cannot see the whole KB: kb_status answers 404) the list
    // is the work list alone.
    Promise.all([
      fetchWork(kb, controller.signal),
      fetchPendingSources(kb, controller.signal).catch(() => null),
      fetchStatus(kb, controller.signal).catch(() => null),
      fetchMaintenanceReview(kb, controller.signal).catch(() => null),
    ])
      .then(([res, pending, status, review]) => {
        setData(withPendingSources(res, pending?.sources ?? []));
        setExtra(extraWork(status?.search_misses ?? [], review?.items ?? []));
        setError(null);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err);
      });
    return () => controller.abort();
  }, [kb, live, attempt]);

  const mapTitles = useMemo(() => new Map(collections.map((c) => [c.name, c.title || c.name])), [collections]);
  const titleOfMap = (m: string) => (m ? (mapTitles.get(m) ?? m) : "(root)");

  const all = useMemo<BoardEntry[]>(() => [...(data?.entries ?? []), ...extra.entries], [data, extra]);
  const entries = useMemo(() => {
    const needle = text.trim().toLowerCase();
    return all.filter(
      (e) =>
        (map === null || (e.map ?? "") === map) &&
        (status === null || groupOf(e) === status) &&
        (!staleOnly || e.stale) &&
        (!needle ||
          (e.title ?? e.id).toLowerCase().includes(needle) ||
          (e.pseudo?.kind === "review" && e.pseudo.item.suggested_action.toLowerCase().includes(needle)) ||
          e.items.some((i) => i.text.toLowerCase().includes(needle))),
    );
  }, [all, map, status, staleOnly, text]);

  const groups = useMemo(() => {
    const out = new Map<string, BoardEntry[]>();
    for (const e of entries) {
      const key = layout === "status" ? groupOf(e) : e.map || "";
      out.set(key, [...(out.get(key) ?? []), e]);
    }
    // Statuses in the order work moves through them; Maps busiest first.
    const order = ([k, list]: [string, BoardEntry[]]) => (layout === "status" ? rankStatus(k) : -list.length);
    return [...out.entries()].sort((a, b) => order(a) - order(b) || a[0].localeCompare(b[0]));
  }, [entries, layout]);

  // Facet counts over everything, so a filter never hides its own siblings.
  const maps = useMemo(() => count(all.map((e) => e.map ?? "")), [all]);
  const statuses = useMemo(
    () => count(all.map(groupOf)).sort((a, b) => rankStatus(a[0]) - rankStatus(b[0]) || a[0].localeCompare(b[0])),
    [all],
  );
  const stale = all.filter((e) => e.stale).length;
  const misses = extra.entries.filter((e) => e.pseudo?.kind === "miss").length;
  const decisions = extra.entries.length - misses;
  const empty = data !== null && data.total === 0 && extra.entries.length === 0;

  return (
    <Page label="Open work" className="work">
      <PageHeader
        eyebrow="Work"
        title={<Headline data={data} misses={misses} decisions={decisions} failed={error !== null} />}
        help={
          <>
            <p>
              A concept is open work when its status is listed in the map&apos;s open_statuses, or is one of the default
              open statuses (open, in-progress, blocked, decision-needed, proposed). &quot;active&quot; means the page
              is valid, not that work is pending.
            </p>
            <p>
              Two more things are work without being a page. A search that found nothing is &quot;to answer&quot;: a
              page to write, or a question to record, so ask your agent. A proposal from the doctor is &quot;to
              decide&quot;: adopt it or dismiss it.
            </p>
          </>
        }
        actions={
          // Standing while the list loads too, so the header does not shift when it lands.
          !data || !empty ? (
            <>
              <input
                className="page-search"
                type="search"
                aria-label="Filter work"
                placeholder="Filter title and items"
                disabled={!data}
                value={text}
                onChange={(e) => setText(e.target.value)}
              />
              <Segmented<Layout> label="Group by" value={layout} options={LAYOUTS} onChange={setLayout} />
            </>
          ) : null
        }
      />

      {data && !empty && (
        <Hero label="Summary and filters">
          <HeroRow label="Where">
            <MapBar maps={maps} active={map} />
            <FilterChips label="Filter by Map">
              {maps.map(([name, n]) => (
                <FilterChip
                  key={name || "(root)"}
                  label={titleOfMap(name)}
                  count={n}
                  pressed={map === name}
                  hue={collectionVar(name || "(root)")}
                  mark={<MapDot map={name} />}
                  onToggle={() => setMap(map === name ? null : name)}
                />
              ))}
            </FilterChips>
          </HeroRow>
          <HeroRow label="Status">
            <FilterChips label="Filter by status">
              {statuses.map(([name, n]) => (
                <FilterChip
                  key={name}
                  label={name}
                  count={n}
                  pressed={status === name}
                  mark={<StatusDot status={name} />}
                  onToggle={() => setStatus(status === name ? null : name)}
                />
              ))}
              <li>
                <label className="filter-chip filter-chip--check" data-checked={staleOnly}>
                  <input type="checkbox" checked={staleOnly} onChange={(e) => setStaleOnly(e.target.checked)} />
                  <span className="filter-chip__name">Stale only</span>
                  <span className="filter-chip__count">{stale}</span>
                </label>
              </li>
            </FilterChips>
          </HeroRow>
        </Hero>
      )}

      {error ? (
        <ErrorState error={error} onRetry={() => setAttempt((n) => n + 1)} />
      ) : !data ? (
        <WorkSkeleton label="Reading the work list" />
      ) : empty ? (
        <Quiet>No open work: no concept in an open status and no unchecked item.</Quiet>
      ) : entries.length === 0 ? (
        <Quiet tone="neutral">Nothing matches these filters.</Quiet>
      ) : (
        <div className="work__board" role="region" aria-label={layout === "status" ? "Work by status" : "Work by map"}>
          {groups.map(([key, list]) => (
            <section
              key={key || "(root)"}
              className="work__column"
              data-status={layout === "status" ? statusTone(key) : undefined}
              style={layout === "map" ? ({ "--map": collectionVar(key || "(root)") } as CSSProperties) : undefined}
              aria-label={`${layout === "status" ? key : titleOfMap(key)}, ${list.length}`}
            >
              <h2 className="work__column-title">
                {layout === "status" ? <StatusDot status={key} /> : <MapDot map={key} />}
                {layout === "status" ? key : titleOfMap(key)} <span className="page-section__count">{list.length}</span>
              </h2>
              <ColumnCards
                list={list}
                render={(e) => (
                  <WorkCard
                    entry={e}
                    mapTitle={titleOfMap(e.map ?? "")}
                    showMap={layout === "status"}
                    showStatus={layout === "map"}
                    onOpen={onOpen}
                  />
                )}
              />
            </section>
          ))}
        </div>
      )}
      {data && extra.found.length > 0 && (
        // A search that found nothing and now finds a page closed by itself
        // (D319): no longer work, named so the reader sees the gap was filled.
        <section className="work__answered" aria-label="Searches answered since">
          <p className="page-note">
            <Icon name="check" size={14} /> Answered since: {extra.found.length}{" "}
            {extra.found.length === 1 ? "search" : "searches"} that found nothing before now{" "}
            {extra.found.length === 1 ? "finds" : "find"} a page.
          </p>
          <ul className="work__answered-list">
            {extra.found.map((q) => (
              <li key={q} className="chip work__answered-chip">
                &ldquo;{q}&rdquo;
              </li>
            ))}
          </ul>
        </section>
      )}
      {data && data.next_offset !== undefined && (
        <p className="page-note">
          Showing the first {data.entries.length} of {data.total}.
        </p>
      )}
    </Page>
  );
}

/**
 * A column's cards in a list that scrolls on its own: the columns of a row
 * share its height, capped, so a column of four hundred proposals no longer
 * sets the height of the whole board. The list says at which ends it has
 * more to show, and the stylesheet fades those ends.
 */
function ColumnCards({ list, render }: { list: BoardEntry[]; render(e: BoardEntry): ReactNode }) {
  const ref = useRef<HTMLUListElement>(null);
  const [more, setMore] = useState<{ above: boolean; below: boolean }>({ above: false, below: false });
  const measure = useCallback(() => {
    const el = ref.current;
    if (!el) return;
    const above = el.scrollTop > 1;
    const below = el.scrollTop + el.clientHeight < el.scrollHeight - 1;
    setMore((m) => (m.above === above && m.below === below ? m : { above, below }));
  }, []);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [measure, list]);
  return (
    <ul
      ref={ref}
      className="work__cards"
      tabIndex={0}
      aria-label="Cards"
      data-more-above={more.above || undefined}
      data-more-below={more.below || undefined}
      onScroll={measure}
    >
      {list.map((e) => (
        <li key={e.id}>{render(e)}</li>
      ))}
    </ul>
  );
}

function WorkCard({
  entry,
  mapTitle,
  showMap,
  showStatus,
  onOpen,
}: {
  entry: BoardEntry;
  mapTitle: string;
  showMap: boolean;
  showStatus: boolean;
  onOpen(id: string): void;
}) {
  const [open, setOpen] = useState(false);
  if (entry.pseudo) {
    return (
      <PseudoCard
        entry={entry}
        pseudo={entry.pseudo}
        mapTitle={mapTitle}
        showMap={showMap}
        showStatus={showStatus}
        onOpen={onOpen}
      />
    );
  }
  const n = entry.items.length;
  return (
    <div className="work__card" style={{ "--map": collectionVar(entry.map || "(root)") } as CSSProperties}>
      <button type="button" className="work__card-main" onClick={() => onOpen(entry.id)}>
        {showMap && (
          <span className="work__card-map">
            <MapDot map={entry.map ?? ""} />
            {mapTitle}
          </span>
        )}
        <span className="work__card-title">{entry.title || entry.id}</span>
        <span className="work__card-meta">
          {entry.reason && entry.reason !== "review" ? (
            <span className="pill">{(entry.fields?.contradiction_kind || entry.reason).replace(/_/g, " ")}</span>
          ) : (
            entry.type && <span className="pill">{entry.type}</span>
          )}
          {showStatus && (
            <span className="pill">
              <StatusDot status={groupOf(entry)} />
              {groupOf(entry)}
            </span>
          )}
          {entry.age_days !== undefined && (
            <span
              className={entry.stale ? "pill pill--warning" : "work__age"}
              title={`Last updated ${entry.age_days} days ago`}
            >
              {entry.stale ? `stale · ${entry.age_days}d` : `${entry.age_days}d`}
            </span>
          )}
          {entry.stale && entry.age_days === undefined && <span className="pill pill--warning">stale</span>}
        </span>
      </button>
      {n > 0 && (
        <button type="button" className="work__items-toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
          <span className="chevron" aria-hidden="true" />
          {n} open item{n === 1 ? "" : "s"}
        </button>
      )}
      {open && (
        <ul className="work__items">
          {entry.items.map((i) => (
            <li key={i.line}>
              <span className="work__box" aria-hidden="true" />
              <span>
                {i.text}
                {i.section && <span className="work__item-section"> — {i.section}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * A card with no page behind it. A search nobody answered opens nothing: its
 * row says how often it was asked and the page header says what to do. A
 * proposal opens the page it names when it names one, and lists them when it
 * names several.
 */
function PseudoCard({
  entry,
  pseudo,
  mapTitle,
  showMap,
  showStatus,
  onOpen,
}: {
  entry: BoardEntry;
  pseudo: Pseudo;
  mapTitle: string;
  showMap: boolean;
  showStatus: boolean;
  onOpen(id: string): void;
}) {
  const miss = pseudo.kind === "miss" ? pseudo : null;
  const item = pseudo.kind === "review" ? pseudo.item : null;
  // A map's contract (`<map>/_map`) is no page to open: its evidence names the map.
  const pages = (item?.concepts ?? []).filter((c) => !c.endsWith("/_map"));
  const target = pages.length === 1 ? pages[0]! : null;
  const title = miss ? `\u201c${miss.query}\u201d` : (entry.title ?? entry.id);
  const body = (
    <>
      {showMap && entry.map && (
        <span className="work__card-map">
          <MapDot map={entry.map} />
          {mapTitle}
        </span>
      )}
      <span className="work__card-title" title={title}>
        {title}
      </span>
      <span className="work__card-meta">
        <span className="pill">{item ? reviewInfo(item.kind).label : "searched, not found"}</span>
        {showStatus && (
          <span className="pill">
            <StatusDot status={groupOf(entry)} />
            {groupOf(entry)}
          </span>
        )}
        {miss && (
          <span className="work__age">
            searched {miss.count === 1 ? "once" : `${miss.count} times`}
            {miss.lastSeen ? `, last on ${miss.lastSeen.slice(0, 10)}` : ""}
          </span>
        )}
      </span>
      {item?.suggested_action && (
        <span className="work__card-note" title={item.suggested_action}>
          {item.suggested_action}
        </span>
      )}
    </>
  );
  const refs = target ? [] : pages;
  return (
    <div className="work__card" style={{ "--map": collectionVar(entry.map || "(root)") } as CSSProperties}>
      {target ? (
        <button type="button" className="work__card-main" onClick={() => onOpen(target)}>
          {body}
        </button>
      ) : (
        <div className="work__card-main work__card-main--static">{body}</div>
      )}
      {refs.length > 0 && (
        <ul className="work__refs" aria-label="Pages concerned">
          {refs.slice(0, MAX_REFS).map((c) => (
            <li key={c}>
              <button type="button" className="ref-link" onClick={() => onOpen(c)}>
                {c}
              </button>
            </li>
          ))}
          {refs.length > MAX_REFS && <li className="work__refs-more">+{refs.length - MAX_REFS} more</li>}
        </ul>
      )}
    </div>
  );
}

/** A status as a coloured dot; the word beside it carries the meaning. */
function StatusDot({ status }: { status: string }) {
  return <span className="status-dot" data-status={statusTone(status)} aria-hidden="true" />;
}

function statusTone(status: string): string {
  switch (status) {
    case "blocked":
      return "error";
    case "decision-needed":
    case REASON_COLUMN.contradiction:
      return "warning";
    case "in-progress":
      return "active";
    case "open":
    case REASON_COLUMN.gap:
      return "info";
    case ITEMS_ONLY:
      return "items";
    default:
      return "neutral";
  }
}

function rankStatus(status: string): number {
  const i = STATUS_ORDER.indexOf(status);
  return i < 0 ? STATUS_ORDER.length - 1.5 : i;
}

function count(values: string[]): [string, number][] {
  const counts = new Map<string, number>();
  for (const v of values) counts.set(v, (counts.get(v) ?? 0) + 1);
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
}

function statusOf(e: WorkEntry): string {
  return e.status || "none";
}

function groupOf(e: BoardEntry): string {
  if (e.reason) return REASON_COLUMN[e.reason];
  return e.open_phase ? statusOf(e) : ITEMS_ONLY;
}

function Headline({
  data,
  misses,
  decisions,
  failed,
}: {
  data: WorkResponse | null;
  misses: number;
  decisions: number;
  failed: boolean;
}) {
  if (!data) return <>{failed ? "Work list unavailable" : <TitleBone label="Reading the work list" />}</>;
  if (data.total === 0 && misses + decisions === 0) return <>Nothing is open.</>;
  // One clause per kind of work that exists, so none is named when it is zero.
  const parts: ReactNode[] = [];
  if (data.total > 0) {
    parts.push(
      <>
        <Count>{data.total}</Count> {data.total === 1 ? "concept carries" : "concepts carry"} open work
      </>,
      <>
        <Count>{data.open_items}</Count> unchecked item{data.open_items === 1 ? "" : "s"}
      </>,
    );
  }
  if (misses > 0) {
    parts.push(
      <>
        <Count>{misses}</Count> {misses === 1 ? "search" : "searches"} to answer
      </>,
    );
  }
  if (decisions > 0) {
    parts.push(
      <>
        <Count>{decisions}</Count> {decisions === 1 ? "proposal" : "proposals"} to decide
      </>,
    );
  }
  return (
    <>
      {parts.map((p, i) => (
        <Fragment key={i}>
          {i > 0 && ", "}
          {p}
        </Fragment>
      ))}
      .
    </>
  );
}

/**
 * What agents searched for and the KB could not answer (D247) becomes work "to
 * answer", beside the open gaps; the doctor's proposals (D704) become work "to
 * decide". A search that finds something now is no work: it is only named, so
 * the gap it showed is seen to have closed.
 */
export function extraWork(misses: SearchMiss[], review: ReviewItem[]): Extra {
  const asked: BoardEntry[] = misses
    .filter((m) => !m.resolved)
    .map((m) => ({
      id: `search:${m.query}`,
      title: m.query,
      map: "",
      reason: "gap",
      open_phase: true,
      stale: false,
      items: [],
      pseudo: { kind: "miss", query: m.query, count: m.count, lastSeen: m.last_seen },
    }));
  const proposed: BoardEntry[] = review.map((item, i) => ({
    id: `review:${i}:${item.kind}`,
    // The evidence names the term of a glossary gap and says how often it is used.
    title: item.evidence,
    // The map of the first page named, a map's contract (`<map>/_map`) included.
    map: item.concepts[0]?.includes("/") ? item.concepts[0].slice(0, item.concepts[0].indexOf("/")) : "",
    status: TO_DECIDE,
    open_phase: true,
    stale: false,
    items: [],
    pseudo: { kind: "review", item },
  }));
  return { entries: [...asked, ...proposed], found: misses.filter((m) => m.resolved).map((m) => m.query) };
}

/** The work list with each pending source added as a "to ingest" entry. */
export function withPendingSources(res: WorkResponse, sources: SourceEntry[]): WorkResponse {
  // A Source already open work by its status is listed once, as it is.
  const listed = new Set(res.entries.map((e) => e.id));
  const pending = sources.filter((src) => src.ingest_status === "pending" && !listed.has(src.id));
  if (pending.length === 0) return res;
  const now = Date.now();
  const entries: WorkEntry[] = pending.map((src) => {
    const t = Date.parse(src.timestamp ?? "");
    return {
      id: src.id,
      title: src.title,
      type: "Source",
      map: src.id.includes("/") ? src.id.slice(0, src.id.indexOf("/")) : "",
      status: TO_INGEST,
      open_phase: true,
      timestamp: src.timestamp,
      age_days: Number.isNaN(t) ? undefined : Math.max(0, Math.floor((now - t) / 86_400_000)),
      stale: false,
      items: [],
    };
  });
  const by_map = { ...res.by_map };
  for (const e of entries) by_map[e.map ?? ""] = (by_map[e.map ?? ""] ?? 0) + 1;
  return {
    ...res,
    total: res.total + entries.length,
    open_concepts: res.open_concepts + entries.length,
    by_status: { ...res.by_status, [TO_INGEST]: entries.length },
    by_map,
    entries: [...res.entries, ...entries],
  };
}
