import { useEffect, useMemo, useState, type CSSProperties, type ReactNode } from "react";
import { ApiError, fetchRetired } from "../api/client";
import type { RetiredConcept, RetiredJournal, RetiredResponse } from "../api/types";
import { collectionVar } from "../lib/palette";
import { EmptyState, ErrorState } from "./States";
import {
  Count,
  FilterChip,
  FilterChips,
  Hero,
  HeroRow,
  MapDot,
  Page,
  PageHeader,
  PageSection,
  Quiet,
  RetiredSkeleton,
  SubtitleBone,
  TitleBone,
} from "./Page";

/** The statuses the page lists, in the order the chips show them. */
const STATUSES = [
  ["archived", "Archived"],
  ["deprecated", "Deprecated"],
  ["superseded", "Superseded"],
] as const;

/**
 * What the KB retired (D820): every archived, deprecated or superseded concept
 * with where it went, when, why and what replaced it. The story is read from
 * the root log by the server, so a page retired before this view existed has
 * one; a status set by hand has none and says so. A tab of Activity: retirement
 * is something that happened to the KB.
 */
export function Retired({
  kb,
  live = 0,
  tabs,
  onOpen,
}: {
  kb: string;
  live?: number;
  /** The section tabs (Activity · Retired), drawn under the header. */
  tabs?: ReactNode;
  onOpen(conceptId: string): void;
}) {
  const [data, setData] = useState<RetiredResponse | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState<string | null>(null);
  const [text, setText] = useState("");

  // A new KB starts blank, filters included; a live refetch (D337) keeps what is on screen.
  useEffect(() => {
    setData(null);
    setError(null);
    setStatus(null);
    setText("");
  }, [kb]);

  useEffect(() => {
    const controller = new AbortController();
    fetchRetired(kb, controller.signal)
      .then((res) => {
        setData(res);
        setError(null);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err);
      });
    return () => controller.abort();
  }, [kb, live, attempt]);

  const all = useMemo(() => data?.concepts ?? [], [data]);
  const counts = useMemo(() => {
    const out = new Map<string, number>();
    for (const c of all) out.set(c.status, (out.get(c.status) ?? 0) + 1);
    return out;
  }, [all]);
  const mapTitles = useMemo(() => new Map((data?.maps ?? []).map((m) => [m.name, m.title || m.name])), [data]);
  // A status a live refetch emptied has no chip left to clear it: drop it.
  const shown = status !== null && counts.has(status) ? status : null;
  const rows = useMemo(() => {
    const needle = text.trim().toLowerCase();
    return all.filter(
      (c) =>
        (shown === null || c.status === shown) &&
        (!needle ||
          [c.title, c.id, c.from, c.reason, c.supersede_reason].some((v) => v?.toLowerCase().includes(needle))),
    );
  }, [all, shown, text]);

  // A snapshot that never exported the view answers 404: say so, do not retry.
  const unavailable = error instanceof ApiError && error.isNotFound;
  return (
    <Page label="Retired concepts" className="retired">
      <PageHeader
        eyebrow="Retired"
        title={<Headline data={data} failed={error !== null} />}
        subtitle={!data && !error ? <SubtitleBone w="12ch" /> : data && all.length > 0 ? breakdown(counts) : undefined}
        actions={
          !data || all.length > 0 ? (
            <input
              className="page-search"
              type="search"
              aria-label="Filter retired concepts"
              placeholder="Filter title, id and reason"
              disabled={!data}
              value={text}
              onChange={(e) => setText(e.target.value)}
            />
          ) : null
        }
      />
      {tabs}

      {all.length > 0 && (
        <Hero label="Filters">
          <HeroRow label="Status">
            <FilterChips label="Filter by status">
              {STATUSES.filter(([name]) => counts.has(name)).map(([name, label]) => (
                <FilterChip
                  key={name}
                  label={label}
                  count={counts.get(name) ?? 0}
                  pressed={shown === name}
                  onToggle={() => setStatus(shown === name ? null : name)}
                />
              ))}
            </FilterChips>
          </HeroRow>
        </Hero>
      )}

      {unavailable ? (
        <EmptyState
          title="The retired view is not available"
          detail="This server or snapshot does not provide it."
        />
      ) : error ? (
        <ErrorState error={error} onRetry={() => setAttempt((n) => n + 1)} />
      ) : !data ? (
        <RetiredSkeleton label="Reading what was retired" />
      ) : all.length === 0 ? (
        <EmptyState
          title="Nothing retired yet"
          detail="concept_archive files a page in the archive map and supersede points it at its successor; either one appears here with its date and reason."
        />
      ) : rows.length === 0 ? (
        <Quiet tone="neutral">Nothing matches these filters.</Quiet>
      ) : (
        <ul className="retired__list" aria-label="Retired concepts">
          {rows.map((c) => (
            <li key={c.id}>
              <Row concept={c} mapTitle={mapTitles.get(c.map ?? "") ?? c.map ?? ""} onOpen={onOpen} />
            </li>
          ))}
        </ul>
      )}

      {data && data.journals.length > 0 && (
        <PageSection title="Journals" id="retired-journals" count={data.journals.length}>
          <ul className="retired__journals">
            {data.journals.map((j) => (
              <li key={j.name} className="retired__journal">
                <span className="retired__journal-name">
                  <MapDot map={j.name} />
                  {j.title || j.name}
                </span>
                <span>{harvestLine(j)}</span>
              </li>
            ))}
          </ul>
        </PageSection>
      )}
    </Page>
  );
}

function Row({
  concept: c,
  mapTitle,
  onOpen,
}: {
  concept: RetiredConcept;
  mapTitle: string;
  onOpen(id: string): void;
}) {
  const reason = c.reason || c.supersede_reason;
  return (
    <div className="retired__row" style={{ "--map": collectionVar(c.map || "(root)") } as CSSProperties}>
      <div className="retired__head">
        <button type="button" className="retired__title" onClick={() => onOpen(c.id)}>
          {c.title || c.id}
        </button>
        <span className="pill" data-status={c.status}>
          {c.status}
        </span>
        {mapTitle && (
          <span className="retired__map">
            <MapDot map={c.map ?? ""} />
            {mapTitle}
          </span>
        )}
      </div>
      <p className="retired__meta">
        <code className="retired__id">{c.id}</code>
        {c.from && (
          <span>
            from <code className="retired__id">{c.from}</code>
          </span>
        )}
        {c.retired_at ? (
          <time dateTime={c.retired_at} title={c.retired_at}>
            {c.retired_at.slice(0, 10)}
          </time>
        ) : (
          <span>
            no retirement recorded{c.timestamp ? ` · updated ${c.timestamp.slice(0, 10)}` : ""}
          </span>
        )}
        {c.superseded_by && (
          <span>
            replaced by{" "}
            <button type="button" className="retired__link" onClick={() => onOpen(c.superseded_by!)}>
              {c.superseded_by_title || c.superseded_by}
            </button>
          </span>
        )}
      </p>
      {reason && <p className="retired__reason">{reason}</p>}
    </div>
  );
}

function Headline({ data, failed }: { data: RetiredResponse | null; failed: boolean }) {
  if (!data) return <>{failed ? "Retired concepts unavailable" : <TitleBone label="Reading what was retired" />}</>;
  const n = data.concepts.length;
  if (n === 0) return <>Nothing is retired.</>;
  return (
    <>
      <Count>{n}</Count> retired concept{n === 1 ? "" : "s"}.
    </>
  );
}

/** "3 archived · 1 deprecated": the count per status, those that exist. */
function breakdown(counts: Map<string, number>): string {
  return STATUSES.filter(([name]) => counts.has(name))
    .map(([name]) => `${counts.get(name)} ${name}`)
    .join(" · ");
}

function harvestLine(j: RetiredJournal): string {
  const archived = `${j.archived} archived`;
  const waiting = `${j.waiting} closed ${j.waiting === 1 ? "entry" : "entries"} waiting to be harvested`;
  return `${waiting}${j.harvest_after ? ` (after ${j.harvest_after} days)` : ""} · ${archived}`;
}
