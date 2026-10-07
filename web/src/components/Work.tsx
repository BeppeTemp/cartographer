import { useEffect, useMemo, useState, type CSSProperties } from "react";
import { fetchWork } from "../api/client";
import type { CollectionSummary, WorkEntry, WorkResponse } from "../api/types";
import { collectionVar } from "../lib/palette";
import {
  Band,
  Count,
  Facet,
  FacetRow,
  FacetRows,
  MapBar,
  MapDot,
  Page,
  PageHeader,
  Quiet,
  Segmented,
  SkeletonRows,
  Stats,
} from "./Page";

type Layout = "status" | "map";
const LAYOUTS = [
  ["status", "By status"],
  ["map", "By map"],
] as const;

/**
 * A concept listed only for its unchecked items is grouped apart: its own
 * status is closed (done, active…), and showing it as a status column would
 * read as open work in that state.
 */
const ITEMS_ONLY = "unchecked items";

/** Columns in the order work moves through them; anything else follows, sorted. */
const STATUS_ORDER = ["decision-needed", "blocked", "in-progress", "open", "proposed", ITEMS_ONLY];

/**
 * Open work (D302): concepts in an open status for their map, of any type,
 * and unchecked items in any concept. A read-only view of `work_list`: work
 * changes through the agents' ordinary write tools, so there is no drag here.
 * The band counts it and filters it by Map and by status; the board below
 * lays it out as columns by status, or as one card per Map.
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
  const [error, setError] = useState<string | null>(null);
  const [layout, setLayout] = useState<Layout>("status");
  const [map, setMap] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [staleOnly, setStaleOnly] = useState(false);
  const [text, setText] = useState("");

  // A new KB starts blank; a live refetch (D336) keeps what is on screen
  // until the fresh answer replaces it.
  useEffect(() => {
    setData(null);
    setError(null);
  }, [kb]);

  useEffect(() => {
    const controller = new AbortController();
    fetchWork(kb, controller.signal)
      .then(setData)
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof Error ? err.message : String(err));
      });
    return () => controller.abort();
  }, [kb, live]);

  const mapTitles = useMemo(() => new Map(collections.map((c) => [c.name, c.title || c.name])), [collections]);
  const titleOfMap = (m: string) => (m ? (mapTitles.get(m) ?? m) : "(root)");

  const all = useMemo(() => data?.entries ?? [], [data]);
  const entries = useMemo(() => {
    const needle = text.trim().toLowerCase();
    return all.filter(
      (e) =>
        (map === null || (e.map ?? "") === map) &&
        (status === null || groupOf(e) === status) &&
        (!staleOnly || e.stale) &&
        (!needle ||
          (e.title ?? e.id).toLowerCase().includes(needle) ||
          e.items.some((i) => i.text.toLowerCase().includes(needle))),
    );
  }, [all, map, status, staleOnly, text]);

  const groups = useMemo(() => {
    const out = new Map<string, WorkEntry[]>();
    for (const e of entries) {
      const key = layout === "status" ? groupOf(e) : e.map || "";
      out.set(key, [...(out.get(key) ?? []), e]);
    }
    // Statuses in the order work moves through them; Maps busiest first.
    const order = ([k, list]: [string, WorkEntry[]]) => (layout === "status" ? rankStatus(k) : -list.length);
    return [...out.entries()].sort((a, b) => order(a) - order(b) || a[0].localeCompare(b[0]));
  }, [entries, layout]);

  // Facet counts over everything, so a filter never hides its own siblings.
  const maps = useMemo(() => count(all.map((e) => e.map ?? "")), [all]);
  const statuses = useMemo(
    () => count(all.map(groupOf)).sort((a, b) => rankStatus(a[0]) - rankStatus(b[0]) || a[0].localeCompare(b[0])),
    [all],
  );
  const stale = all.filter((e) => e.stale).length;
  const deciding = all.filter((e) => e.open_phase && e.status === "decision-needed").length;

  return (
    <Page label="Open work" className="work">
      <PageHeader
        eyebrow="Work"
        title={<Headline data={data} />}
        help={
          <p>
            A concept is open work when its status is listed in the map&apos;s open_statuses, or is one of the default open
            statuses (open, in-progress, blocked, decision-needed, proposed). &quot;active&quot; means the page is valid, not
            that work is pending.
          </p>
        }
        actions={
          data && data.total > 0 ? (
            <>
              <input
                className="page-search"
                type="search"
                aria-label="Filter work"
                placeholder="Filter title and items"
                value={text}
                onChange={(e) => setText(e.target.value)}
              />
              <Segmented<Layout> label="Group by" value={layout} options={LAYOUTS} onChange={setLayout} />
            </>
          ) : null
        }
      />

      {data && data.total > 0 && (
        <Band label="Summary and filters">
          <Facet className="work__summary">
            <Stats
              items={[
                { label: data.open_concepts === 1 ? "open concept" : "open concepts", value: data.open_concepts },
                { label: data.open_items === 1 ? "unchecked item" : "unchecked items", value: data.open_items },
                { label: "need a decision", value: deciding, tone: deciding ? "warning" : "muted" },
                { label: "stale", value: stale, tone: stale ? "warning" : "muted" },
              ]}
            />
          </Facet>

          <Facet title="Where" id="work-where">
            <MapBar maps={maps} active={map} />
            <FacetRows label="Filter by Map" columns={2}>
              {maps.map(([name, n]) => (
                <li key={name || "(root)"}>
                  <FacetRow
                    label={titleOfMap(name)}
                    count={n}
                    pressed={map === name}
                    hue={collectionVar(name || "(root)")}
                    mark={<MapDot map={name} />}
                    onToggle={() => setMap(map === name ? null : name)}
                  />
                </li>
              ))}
            </FacetRows>
          </Facet>

          <Facet title="Status" id="work-status">
            <FacetRows label="Filter by status">
              {statuses.map(([name, n]) => (
                <li key={name}>
                  <FacetRow
                    label={name}
                    count={n}
                    pressed={status === name}
                    mark={<StatusDot status={name} />}
                    onToggle={() => setStatus(status === name ? null : name)}
                  />
                </li>
              ))}
            </FacetRows>
            <label className="work__stale-toggle">
              <input type="checkbox" checked={staleOnly} onChange={(e) => setStaleOnly(e.target.checked)} />
              Stale only
            </label>
          </Facet>
        </Band>
      )}

      {error ? (
        <p className="page-note">Could not read the work list: {error}</p>
      ) : !data ? (
        <SkeletonRows label="Reading the work list" />
      ) : data.total === 0 ? (
        <Quiet>No open work: no concept in an open status and no unchecked item.</Quiet>
      ) : entries.length === 0 ? (
        <Quiet tone="neutral">Nothing matches these filters.</Quiet>
      ) : (
        <div
          className={layout === "status" ? "work__board" : "work__maps"}
          role="region"
          aria-label={layout === "status" ? "Work by status" : "Work by map"}
        >
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
              <ul className="work__cards">
                {list.map((e) => (
                  <li key={e.id}>
                    <WorkCard
                      entry={e}
                      mapTitle={titleOfMap(e.map ?? "")}
                      showMap={layout === "status"}
                      showStatus={layout === "map"}
                      onOpen={onOpen}
                    />
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
      {data && data.next_offset !== undefined && (
        <p className="page-note">
          Showing the first {data.entries.length} of {data.total}.
        </p>
      )}
    </Page>
  );
}

function WorkCard({
  entry,
  mapTitle,
  showMap,
  showStatus,
  onOpen,
}: {
  entry: WorkEntry;
  mapTitle: string;
  showMap: boolean;
  showStatus: boolean;
  onOpen(id: string): void;
}) {
  const [open, setOpen] = useState(false);
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
          {entry.type && <span className="pill">{entry.type}</span>}
          {showStatus && (
            <span className="pill">
              <StatusDot status={groupOf(entry)} />
              {groupOf(entry)}
            </span>
          )}
          {entry.age_days !== undefined && (
            <span className={entry.stale ? "pill pill--warning" : "work__age"} title={`Last updated ${entry.age_days} days ago`}>
              {entry.stale ? `stale · ${entry.age_days}d` : `${entry.age_days}d`}
            </span>
          )}
          {entry.stale && entry.age_days === undefined && <span className="pill pill--warning">stale</span>}
        </span>
      </button>
      {n > 0 && (
        <button type="button" className="work__items-toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
          <span className="work__chevron" aria-hidden="true" />
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

/** A status as a coloured dot; the word beside it carries the meaning. */
function StatusDot({ status }: { status: string }) {
  return <span className="status-dot" data-status={statusTone(status)} aria-hidden="true" />;
}

function statusTone(status: string): string {
  switch (status) {
    case "blocked":
      return "error";
    case "decision-needed":
      return "warning";
    case "in-progress":
      return "active";
    case "open":
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

function groupOf(e: WorkEntry): string {
  return e.open_phase ? statusOf(e) : ITEMS_ONLY;
}

function Headline({ data }: { data: WorkResponse | null }) {
  if (!data) return <>Reading the work list…</>;
  if (data.total === 0) return <>Nothing is open.</>;
  return (
    <>
      <Count>{data.total}</Count> {data.total === 1 ? "concept carries" : "concepts carry"} open work,{" "}
      <Count>{data.open_items}</Count> unchecked item{data.open_items === 1 ? "" : "s"}.
    </>
  );
}
