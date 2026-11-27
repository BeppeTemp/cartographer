import {
  createContext,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";
import { fetchAudit, fetchChanges, fetchJournal, fetchRetired, fetchSurveyorHistory, optional } from "../api/client";
import type {
  AuditLog,
  ChangesResponse,
  CollectionSummary,
  ComponentRow,
  ConceptChange,
  GraphSnapshot,
  RetiredConcept,
  SurveyorHistoryResponse,
} from "../api/types";
import {
  changedAt,
  bySurveyor,
  changeKind,
  isSurveyorAuthor,
  journalMap,
  journalShape,
  parseJournal,
  shownReasons,
  type ChangeKind,
} from "../lib/activity";
import { collectionVar } from "../lib/palette";
import { ErrorState } from "./States";
import { ReportCandidatesSection } from "./Candidates";
import { age, runDate, shortTokens, surveyorRuns } from "./Surveyors";
import { touchedByRun } from "../lib/activity";
import { SurveyorActivity } from "./SurveyorActivity";
import { FigureTiles, WhenStrip, useBuckets, type StripKey } from "./ActivityCharts";
import { ConceptDiff, prefetchDiff } from "./ConceptDiff";
import { Icon } from "./Icon";
import {
  Avatar,
  FilterChip,
  FilterChips,
  Hero,
  HeroRow,
  MapDot,
  Page,
  PageHeader,
  PageSection,
  Segmented,
  ActivitySkeleton,
} from "./Page";

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
/** The KB and the window start a Changes row diffs against: set once by the
 *  page, read by every row, so the rows' props stay what they show. */
const DiffSpan = createContext<{ kb: string; from: string; to?: string; reopen?: string | null } | null>(null);

/** The kinds of change and of run on the strip, each with a hue of the wheel. */
const CHANGE_KEYS: StripKey[] = [
  { key: "new", word: "new", hue: "var(--hue-1)" },
  { key: "edited", word: "edited", hue: "var(--hue-3)" },
  { key: "reorganised", word: "reorganised", hue: "var(--hue-11)" },
];
const RUN_KEYS: StripKey[] = [
  { key: "poll", word: "poll", hue: "var(--hue-3)" },
  { key: "ingest", word: "ingest", hue: "var(--hue-1)" },
  { key: "doctor", word: "doctor", hue: "var(--hue-5)" },
  { key: "repair", word: "repair", hue: "var(--hue-10)" },
  { key: "synthesis", word: "synthesis", hue: "var(--hue-11)" },
  { key: "failed", word: "failed", hue: "var(--sev-error)" },
];
const hueOf = (keys: StripKey[], key: string) => keys.find((k) => k.key === key)?.hue;

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
/** How many of the newest earlier changes an empty window shows. */
const EARLIER = 20;

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
  components = [],
  tabs,
  reopen,
  onOpen,
}: {
  kb: string;
  /** The concept the reader just came back from: its row's diff is open. */
  reopen?: string | null;
  snapshot: GraphSnapshot | null;
  collections?: CollectionSummary[];
  live?: number;
  /** The components that concern the KB (D1901), read once by the App. */
  components?: ComponentRow[];
  /** The section tabs, drawn under the header. */
  tabs?: ReactNode;
  onOpen(conceptId: string): void;
}) {
  const [since, setSince] = useState<Window>("7d");
  const [author, setAuthor] = useState<string | null>(null);
  const [map, setMap] = useState<string | null>(null);
  // The page's two tabs, drawn only for a KB a Surveyor works on.
  const [tab, setTab] = useState<"changes" | "surveyor">("changes");
  const [data, setData] = useState<ChangesResponse | null>(null);
  const [earlier, setEarlier] = useState<ConceptChange[] | null>(null);
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
    setEarlier(null);
    setError(null);
  }, [kb]);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    fetchChanges(kb, SINCE[since], controller.signal)
      // An empty window still shows the KB's latest work: the newest changes
      // from before it, on the same timeline. They are read before anything
      // is replaced, so the page never shows the notice without them.
      .then(async (res) => {
        const quiet = since !== "all" && (res.concepts ?? []).every((c) => changeKind(c) === "maintenance");
        const before = quiet ? await optional(fetchChanges(kb, SINCE.all, controller.signal)) : null;
        return { res, before };
      })
      .then(({ res, before }) => {
        if (controller.signal.aborted) return;
        setData(res);
        setEarlier(
          before
            ? (before.concepts ?? [])
                .filter((c) => changeKind(c) !== "maintenance")
                .sort((a, b) => Date.parse(changedAt(b)) - Date.parse(changedAt(a)))
                .slice(0, EARLIER)
            : null,
        );
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

  // Whether the window on screen holds nothing but upkeep: the quiet notice
  // takes the summary's place, and the earlier changes follow it.
  const windowEmpty =
    data !== null && (data.concepts ?? []).every((c) => changeKind(c) === "maintenance") && shown !== "all";
  const earlierDays = useMemo(() => groupByDay(earlier ?? []), [earlier]);

  // What the KB retired (archived, deprecated, superseded), keyed by concept:
  // a retired concept's row on the timeline says so, with where it went and
  // why. A narrowed principal's 404 leaves the rows as plain changes.
  const [retired, setRetired] = useState<ReadonlyMap<string, RetiredConcept>>(new Map());
  useEffect(() => {
    setRetired(new Map());
  }, [kb]);
  useEffect(() => {
    const controller = new AbortController();
    fetchRetired(kb, controller.signal)
      .then((res) => setRetired(new Map(res.concepts.map((r) => [r.id, r]))))
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
  // The Changes tab is the people's: a change only the Surveyor made is on the
  // Surveyor tab, and a shared one shows its human authors only.
  const people = useMemo(
    () =>
      all.flatMap((c) => {
        const authors = c.authors ?? [];
        if (authors.length === 0) return [c];
        const humans = authors.filter((a) => !isSurveyorAuthor(a));
        return humans.length === 0 ? [] : [humans.length === authors.length ? c : { ...c, authors: humans }];
      }),
    [all],
  );
  // What the filters and the "Where" bar count: the reader's changes, and the
  // server's upkeep only when it is on show.
  const counted = useMemo(
    () => (showUpkeep ? people : people.filter((c) => changeKind(c) !== "maintenance")),
    [people, showUpkeep],
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
    () => people.filter((c) => (!author || c.authors?.includes(author)) && (!map || mapOf(c) === map)),
    [people, author, map],
  );
  // The Surveyor's share of the window: its tab, and the tab exists only when
  // this KB has runs of the Surveyor's or changes made by it.
  // The Surveyor tab's Where: the Maps of the pages it wrote in the window.
  const [svMap, setSvMap] = useState<string | null>(null);
  const surveyorChanges = useMemo(() => all.filter((c) => bySurveyor(c) && changeKind(c) !== "maintenance"), [all]);
  const surveyorMaps = useMemo(() => rank(surveyorChanges.map(mapOf)), [surveyorChanges]);
  useEffect(() => {
    if (svMap !== null && !surveyorMaps.some(([m]) => m === svMap)) setSvMap(null);
  }, [svMap, surveyorMaps]);
  const keptRuns = useMemo(() => surveyorRuns(kb, components), [kb, components]);
  const [task, setTask] = useState<string | null>(null);
  const windowRuns = useMemo(() => {
    const d = DAYS[shown];
    const from = d === null ? 0 : Date.now() - d * DAY_MS;
    return keptRuns.filter((r) => !(Date.parse(r.started_at) < from));
  }, [keptRuns, shown]);
  const surveyorTasks = useMemo(() => rank(windowRuns.map((r) => r.task)), [windowRuns]);
  const runPoints = useMemo(
    () =>
      windowRuns
        .filter((r) => !task || r.task === task)
        .map((r) => ({
          t: Date.parse(r.started_at),
          key: r.outcome === "failed" || r.outcome === "budget_exhausted" ? "failed" : r.task,
        })),
    [windowRuns, task],
  );
  const idleTokens = useMemo(
    () =>
      windowRuns
        .filter((r) => (r.tokens_spent ?? 0) > 0 && touchedByRun(r, all).length === 0)
        .reduce((n, r) => n + (r.tokens_spent ?? 0), 0),
    [windowRuns, all],
  );
  const lastRun = windowRuns[0] ?? keptRuns[0];
  const runBuckets = useBuckets(runPoints, DAYS[shown] === null ? 0 : Date.now() - DAYS[shown]! * DAY_MS);
  const hasSurveyor = keptRuns.length > 0 || surveyorChanges.length > 0;
  // The persisted daily counters (surveyor-history): optional, a 404 (an older
  // server, a caller that does not see the KB whole) leaves the figures of the
  // kept runs. The "all" window asks for the longest history the server keeps.
  // It is kept with the days it was asked for: the window on screen (`shown`)
  // lags the selector until the changes arrive, so an answer for a shorter
  // window than the one shown must not stand in for it.
  const [asked, setAsked] = useState<{ days: number; data: SurveyorHistoryResponse } | null>(null);
  useEffect(() => {
    setAsked(null);
  }, [kb]);
  const historyDays = Math.min(DAYS[since] ?? HISTORY_MAX_DAYS, HISTORY_MAX_DAYS);
  useEffect(() => {
    if (!hasSurveyor) return;
    const controller = new AbortController();
    optional(fetchSurveyorHistory(kb, historyDays, controller.signal))
      .then((res) => {
        if (!controller.signal.aborted) setAsked(res && Array.isArray(res.days) ? { days: historyDays, data: res } : null);
      })
      .catch(() => undefined);
    return () => controller.abort();
  }, [kb, hasSurveyor, historyDays, live, attempt]);
  const view = hasSurveyor ? tab : "changes";
  const history = asked && asked.days >= Math.min(DAYS[shown] ?? HISTORY_MAX_DAYS, HISTORY_MAX_DAYS) ? asked.data : null;
  const surveyorSummary = useMemo(
    () => summarizeSurveyor(keptRuns, surveyorChanges, DAYS[shown], history),
    [keptRuns, surveyorChanges, shown, history],
  );

  const runNote = surveyorSummary.sinceDate ? `since ${surveyorSummary.sinceDate}` : null;
  const kinds = useMemo(() => new Map(filtered.map((c) => [c, changeKind(c)])), [filtered]);
  const count = (k: ChangeKind) => filtered.filter((c) => kinds.get(c) === k).length;
  const changePoints = useMemo(
    () =>
      filtered
        .filter((c) => kinds.get(c) !== "maintenance")
        .map((c) => ({ t: Date.parse(changedAt(c)), key: kinds.get(c) ?? "edited" }))
        .filter((p) => Number.isFinite(p.t)),
    [filtered, kinds],
  );
  const changeBuckets = useBuckets(changePoints, DAYS[shown] === null ? 0 : Date.now() - DAYS[shown]! * DAY_MS);
  const upkeep = count("maintenance");
  // A window of nothing but upkeep is quiet whatever the upkeep toggle says:
  // its toggle is in the summary, which the quiet notice replaces.
  const rows = useMemo(
    () => (windowEmpty ? [] : showUpkeep ? filtered : filtered.filter((c) => kinds.get(c) !== "maintenance")),
    [filtered, kinds, showUpkeep, windowEmpty],
  );
  const days = useMemo(() => groupByDay(rows), [rows]);
  // The server's day series counts every change of the window; the concepts
  // keep each one once, at its newest change, so they only stand in when a
  // filter narrows the page (the series cannot be filtered) or the server is
  // older than the series (D1057).
  const touchedCount = useMemo(
    () => new Set(filtered.filter((c) => kinds.get(c) !== "maintenance").map((c) => c.id)).size,
    [filtered, kinds],
  );
  const retiredCount = useMemo(
    () => filtered.filter((c) => kinds.get(c) !== "maintenance" && retired.has(c.id)).length,
    [filtered, kinds, retired],
  );

  return (
    <DiffSpan.Provider value={data ? { kb, from: data.since, reopen } : null}>
      <Page label="Recent activity" busy={loading} className="timeline">
        <PageHeader
          eyebrow="Activity"
          title={view === "surveyor" ? "What the Surveyor did" : "What users changed"}
          actions={
            <>
              {hasSurveyor && (
                <Segmented<"changes" | "surveyor">
                  className="segmented--appear"
                  label="Activity of"
                  value={view}
                  options={[
                    ["changes", "Users"],
                    ["surveyor", "Surveyor"],
                  ]}
                  onChange={setTab}
                />
              )}
              <Segmented<Window> label="Changes since" value={since} options={WINDOWS} onChange={setSince} />
            </>
          }
        />
        {tabs}

        <div className="page__body" data-loading={loading && data !== null}>
          {view === "surveyor" &&
            (error ? (
              <ErrorState error={error} onRetry={() => setAttempt((n) => n + 1)} />
            ) : !data ? (
              <ActivitySkeleton label="Reading the Surveyor's work" />
            ) : (
              <>
                <Hero label="Summary and filters" className="activity-hero">
                  <FigureTiles
                    label="The Surveyor in the window"
                    items={[
                      {
                        label: "Runs",
                        value: surveyorSummary.runs,
                        title: [surveyorSummary.failed > 0 ? `${surveyorSummary.failed} failed` : null, runNote].filter(Boolean).join(" · ") || "all ok",
                        hue: surveyorSummary.failed > 0 ? "var(--sev-error)" : hueOf(RUN_KEYS, "poll"),
                      },
                      {
                        label: "Pages written",
                        hue: hueOf(RUN_KEYS, "ingest"),
                        value: surveyorSummary.created + surveyorSummary.changed,
                        title: `${surveyorSummary.created} new`,
                      },
                      {
                        label: "Tokens",
                        hue: hueOf(RUN_KEYS, "doctor"),
                        value: shortTokens(surveyorSummary.tokens),
                        title: `${surveyorSummary.tokens} tokens`,
                      },
                      {
                        label: "Tokens wasted",
                        hue: hueOf(RUN_KEYS, "repair"),
                        value: shortTokens(idleTokens),
                        title: `${idleTokens} tokens on runs that wrote no page`,
                      },
                      {
                        label: "Last run",
                        hue: lastRun
                          ? lastRun.outcome === "failed" || lastRun.outcome === "budget_exhausted"
                            ? "var(--sev-error)"
                            : hueOf(RUN_KEYS, lastRun.task)
                          : "var(--text-muted)",
                        value: lastRun ? age(lastRun.started_at) : "—",
                        title: lastRun ? `${lastRun.task} · ${lastRun.outcome.replace(/_/g, " ")}` : undefined,
                      },
                    ]}
                  />
                  <HeroRow label="When">
                    <WhenStrip data={runBuckets} unit={["run", "runs"]} keys={RUN_KEYS} />
                  </HeroRow>
                  {history && <RunTrend history={history} days={DAYS[shown]} />}
                  <div className="activity-facet-row activity-facet-row--even">
                    {surveyorTasks.length > 0 && (
                      <HeroRow label="What">
                        <FilterChips label="Filter by task">
                          {surveyorTasks.map(([name, n]) => (
                            <FilterChip
                              key={name}
                              label={name}
                              count={n}
                              pressed={task === name}
                              hue={hueOf(RUN_KEYS, name)}
                              mark={
                                <span
                                  className="map-dot"
                                  aria-hidden="true"
                                  style={{ "--map": hueOf(RUN_KEYS, name) } as CSSProperties}
                                />
                              }
                              onToggle={() => setTask(task === name ? null : name)}
                            />
                          ))}
                        </FilterChips>
                      </HeroRow>
                    )}
                    {surveyorMaps.length > 0 && (
                      <HeroRow label="Where">
                        <FilterChips label="Filter by Map">
                          {surveyorMaps.map(([name, n]) => (
                            <FilterChip
                              key={name || "(root)"}
                              label={mapTitles.get(name) ?? name}
                              count={n}
                              pressed={svMap === name}
                              hue={collectionVar(name || "(root)")}
                              mark={<MapDot map={name} />}
                              onToggle={() => setSvMap(svMap === name ? null : name)}
                            />
                          ))}
                        </FilterChips>
                      </HeroRow>
                    )}
                  </div>
                </Hero>
                <SurveyorActivity
                  hue={(t) => hueOf(RUN_KEYS, t)}
                  task={task}
                  kb={kb}
                  components={components}
                  from={DAYS[shown] === null ? 0 : Date.now() - DAYS[shown]! * DAY_MS}
                  changes={all}
                  map={svMap === null ? undefined : (c) => mapOf(c) === svMap}
                  renderPage={(c, from, to) => (
                    <DiffSpan.Provider key={c.id} value={{ kb, from, to, reopen }}>
                      <Entry
                        change={c}
                        title={titles.get(c.id) ?? c.id}
                        mapTitle={mapTitles.get(mapOf(c)) ?? mapOf(c)}
                        retired={retired.get(c.id)}
                        onOpen={onOpen}
                      />
                    </DiffSpan.Provider>
                  )}
                />
              </>
            ))}
          {view === "changes" && data && !windowEmpty && (
            <Hero label="Summary and filters" className="activity-hero">
              <FigureTiles
                label="Changes in the window"
                items={[
                  { label: "New", value: count("new"), hue: hueOf(CHANGE_KEYS, "new") },
                  { label: "Edited", value: count("edited"), hue: hueOf(CHANGE_KEYS, "edited") },
                  { label: "Reorganised", value: count("reorganised"), hue: hueOf(CHANGE_KEYS, "reorganised") },
                  { label: "Retired", value: retiredCount, hue: "var(--hue-7)" },
                  {
                    label: "Touched",
                    title: "Distinct concepts changed in the window",
                    value: touchedCount,
                    hue: "var(--text-muted)",
                  },
                ]}
              />
              <HeroRow label="When">
                <WhenStrip data={changeBuckets} unit={["change", "changes"]} keys={CHANGE_KEYS} />
              </HeroRow>
              <div className="activity-facet-row">
                {maps.length > 0 && (
                  <HeroRow label="Where">
                    <FilterChips label="Filter by Map">
                      {maps.map(([name, n]) => (
                        <FilterChip
                          key={name || "(root)"}
                          label={mapTitles.get(name) ?? name}
                          count={n}
                          pressed={map === name}
                          hue={collectionVar(name || "(root)")}
                          mark={<MapDot map={name} />}
                          onToggle={() => setMap(map === name ? null : name)}
                        />
                      ))}
                    </FilterChips>
                  </HeroRow>
                )}
                {(authors.length > 1 || upkeep > 0) && (
                  <HeroRow label="Who">
                    {authors.length > 1 && (
                      <ul className="activity-people" aria-label="Filter by author">
                        {authors.map(([name, n]) => {
                          const label = `${name.split(" <")[0]}: ${n} ${n === 1 ? "change" : "changes"}`;
                          return (
                            <li key={name}>
                              <button
                                type="button"
                                className="activity-person"
                                aria-pressed={author === name}
                                aria-label={label}
                                title={label}
                                onClick={() => setAuthor(author === name ? null : name)}
                              >
                                <Avatar name={name} />
                              </button>
                            </li>
                          );
                        })}
                      </ul>
                    )}
                    {upkeep > 0 && (
                      <label
                        className="filter-chip filter-chip--check activity-upkeep-chip"
                        data-checked={showUpkeep}
                        title="Changes made by automatic maintenance"
                      >
                        <input type="checkbox" checked={showUpkeep} onChange={(e) => setShowUpkeep(e.target.checked)} />
                        <span className="filter-chip__name">Maintenance</span>
                        <span className="filter-chip__count">{upkeep}</span>
                      </label>
                    )}
                  </HeroRow>
                )}
              </div>
            </Hero>
          )}

          {view === "surveyor" ? null : error ? (
            <ErrorState error={error} onRetry={() => setAttempt((n) => n + 1)} />
          ) : !data ? (
            <ActivitySkeleton label="Reading the history" />
          ) : rows.length === 0 ? (
            <>
              <div className="timeline__empty">
                <span className="timeline__empty-icon" aria-hidden="true">
                  <Icon name="moon" size={18} />
                </span>
                <div className="timeline__empty-text">
                  <p className="timeline__empty-title">Quiet {shown === "1d" ? "day" : "stretch"}.</p>
                  <p className="timeline__empty-note">
                    No concept edited {map ? `in ${mapTitles.get(map) ?? map} ` : ""}
                    {when(shown)}
                    {author ? ` by ${author}` : ""}.
                  </p>
                </div>
                {(author || map) && (
                  <button
                    type="button"
                    className="button timeline__empty-action"
                    onClick={() => {
                      setAuthor(null);
                      setMap(null);
                    }}
                  >
                    Clear {author && map ? "filters" : "filter"}
                  </button>
                )}
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
              {windowEmpty && earlierDays.length > 0 && (
                <>
                  <h2 className="timeline__earlier">Earlier</h2>
                  <TimelineDays
                    days={earlierDays}
                    titles={titles}
                    mapTitles={mapTitles}
                    retired={retired}
                    onOpen={onOpen}
                  />
                </>
              )}
            </>
          ) : (
            <TimelineDays days={days} titles={titles} mapTitles={mapTitles} retired={retired} onOpen={onOpen} />
          )}
          {view === "changes" && data?.truncated && (
            <p className="page-note">
              Showing the {people.length} most recent changes: narrow the window to see the rest.
            </p>
          )}
          {view === "changes" && <ReportCandidatesSection kb={kb} live={live} />}
          {view === "changes" && journal !== null && (data !== null || error !== null) && (
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
          {view === "changes" && audit !== null && (data !== null || error !== null) && <AuditSection audit={audit} />}
        </div>
      </Page>
    </DiffSpan.Provider>
  );
}

/** The concept changes grouped by day: a day heading with its count, then one row per change. */
function TimelineDays({
  days,
  titles,
  mapTitles,
  retired,
  onOpen,
}: {
  days: { key: string; label: string; items: ConceptChange[] }[];
  titles: ReadonlyMap<string, string>;
  mapTitles: ReadonlyMap<string, string>;
  retired: ReadonlyMap<string, RetiredConcept>;
  onOpen(conceptId: string): void;
}) {
  return (
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
                retired={retired.get(c.id)}
                onOpen={onOpen}
              />
            ))}
          </ol>
        </li>
      ))}
    </ol>
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
    const measure = () => setClamped([...el.querySelectorAll("p")].some((p) => p.scrollHeight > p.clientHeight + 1));
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
  // Nothing to read, nothing shown: no log kept, or none recorded yet.
  if (!audit.enabled || entries.length === 0) return null;
  return (
    <PageSection title="Audit log" id="activity-audit" count={entries.length}>
      {
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
      }
    </PageSection>
  );
}

function Entry({
  change: c,
  title,
  mapTitle,
  retired,
  onOpen,
}: {
  change: ConceptChange;
  title: string;
  mapTitle: string;
  /** Set when the concept is retired: the row says so, and where it went and why. */
  retired?: RetiredConcept;
  onOpen(conceptId: string): void;
}) {
  const reasons = retired ? retiredReasons(retired) : shownReasons(c);
  const [first, ...more] = reasons;
  const span = useContext(DiffSpan);
  // Back from the page this row opened: its diff is open again.
  const [open, setOpen] = useState(() => span?.reopen === c.id);
  return (
    <li className="timeline__entry" style={{ "--map": collectionVar(mapOf(c)) } as CSSProperties}>
      <button
        type="button"
        className="timeline__item"
        aria-expanded={open}
        onPointerEnter={() => span && prefetchDiff(span.kb, c.id, span.from, span.to)}
        onFocus={() => span && prefetchDiff(span.kb, c.id, span.from, span.to)}
        onClick={() => (span ? setOpen((v) => !v) : onOpen(c.id))}
      >
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
            {retired ? <RetiredPill status={retired.status} /> : <ChangePill change={c.added && c.change !== "deleted" ? "added" : c.change} />}
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
      {open && span && (
        <div className="timeline__diff">
          <ConceptDiff
            kb={span.kb}
            id={c.id}
            from={span.from}
            to={span.to}
            expandMain={span.reopen === c.id}
            onOpen={c.change === "deleted" ? undefined : onOpen}
            onUnavailable={
              c.change === "deleted"
                ? undefined
                : () => {
                    setOpen(false);
                    onOpen(c.id);
                  }
            }
          />
        </div>
      )}
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

function RetiredPill({ status }: { status: string }) {
  return <span className={`pill timeline__retired timeline__retired--${status}`}>{status}</span>;
}

/** What a retired row says under its title: what replaced it, why, where it was. */
function retiredReasons(r: RetiredConcept): string[] {
  const out: string[] = [];
  if (r.superseded_by) out.push(`Replaced by ${r.superseded_by_title || r.superseded_by}`);
  const why = r.supersede_reason || r.reason;
  if (why) out.push(why);
  if (r.from && r.from !== r.id) out.push(`Was ${r.from}`);
  if (out.length === 0) out.push("No retirement recorded: the status was set by hand");
  return out;
}

/**
 * What the Surveyors did on the KB in the window: runs by outcome (by start),
 * the sources they ingested (ok ingest runs), tokens spent, and the pages their
 * commits created or changed. Always an object: with nothing to say, all zeros.
 * `kept` is `surveyorRuns(kb, components)`, computed once by the caller.
 */
function summarizeSurveyor(
  kept: ReturnType<typeof surveyorRuns>,
  changes: ConceptChange[],
  days: number | null,
  history: SurveyorHistoryResponse | null = null,
) {
  const from = days === null ? 0 : Date.now() - days * DAY_MS;
  const pages = {
    created: changes.filter((c) => changeKind(c) === "new").length,
    changed: changes.filter((c) => changeKind(c) !== "new").length,
  };
  const start = historyStart(history, days);
  if (history && start !== null && history.first_day !== null && history.first_day <= start) {
    // The counters reach back to the window's start: they are exact, and no
    // "since" is needed. Failed and budget-exhausted runs are both failures.
    const rows = history.days.filter((r) => r.day >= start);
    const sum = (f: (r: SurveyorHistoryResponse["days"][number]) => boolean, key: "runs" | "tokens" = "runs") =>
      rows.filter(f).reduce((n, r) => n + r[key], 0);
    return {
      sinceDate: null,
      runs: sum(() => true),
      names: [...new Set(rows.map((r) => r.perimeter))],
      ok: sum((r) => r.outcome === "ok"),
      failed: sum(isFailed),
      ingested: sum((r) => r.outcome === "ok" && r.task === "ingest"),
      tokens: sum(() => true, "tokens"),
      ...pages,
    };
  }
  const runs = kept.filter((r) => !(Date.parse(r.started_at) < from));
  const ok = runs.filter((r) => r.outcome === "ok");
  // The server keeps the last runs only (a cap per perimeter, in memory): the
  // run-based figures are complete only inside the kept range. Older runs may
  // be gone when the window starts before the oldest kept one, or a perimeter
  // holds exactly the cap's worth. The pages come from commits and are exact.
  const oldest = Math.min(...kept.map((r) => Date.parse(r.started_at)).filter((t) => !Number.isNaN(t)));
  const perPerimeter = new Map<string, number>();
  for (const r of kept) perPerimeter.set(r.perimeter, (perPerimeter.get(r.perimeter) ?? 0) + 1);
  const atCap = [...perPerimeter.values()].some((n) => n >= RUNS_KEPT_CAP);
  const partial = kept.length > 0 && Number.isFinite(oldest) && (from < oldest || atCap);
  return {
    // With counters that do not reach the window's start, the note says since
    // when the server has counted, not since the oldest run it still keeps.
    sinceDate: partial ? (history?.first_day ? runDate(dayDate(history.first_day)) : runDate(new Date(oldest))) : null,
    runs: runs.length,
    names: [...new Set(runs.map((r) => r.perimeter))],
    ok: ok.length,
    failed: runs.filter((r) => r.outcome === "failed" || r.outcome === "budget_exhausted").length,
    ingested: ok.filter((r) => r.task === "ingest").length,
    tokens: runs.reduce((n, r) => n + (r.tokens_spent ?? 0), 0),
    ...pages,
  };
}

/** The most days the history route serves (its retention). */
const HISTORY_MAX_DAYS = 90;

const isFailed = (r: { outcome: string }) => r.outcome === "failed" || r.outcome === "budget_exhausted";

/** A UTC day "YYYY-MM-DD" as a local date at noon, so its label never slips a day. */
function dayDate(day: string): Date {
  const [y, m, d] = day.split("-").map(Number);
  return new Date(y!, (m ?? 1) - 1, d ?? 1, 12);
}

/** The UTC day a window of `days` days starts on (today is day one), by the server's clock; null for "all". */
function historyStart(history: SurveyorHistoryResponse | null, days: number | null): string | null {
  if (!history || days === null) return null;
  const now = Date.parse(history.now);
  const base = Number.isNaN(now) ? Date.now() : now;
  return new Date(base - (days - 1) * DAY_MS).toISOString().slice(0, 10);
}

/** The server's cap of kept runs per perimeter (`maxRunsKept`). */
const RUNS_KEPT_CAP = 20;

/**
 * The runs of the window day by day, stacked ok / failed / skipped, from the
 * server's persisted counters; the same numbers sit in a table under a
 * `<details>` for a reader who does not see the bars.
 */
function RunTrend({ history, days }: { history: SurveyorHistoryResponse; days: number | null }) {
  const now = Date.parse(history.now);
  const base = Number.isNaN(now) ? Date.now() : now;
  const today = new Date(base).toISOString().slice(0, 10);
  const span = Math.min(days ?? HISTORY_MAX_DAYS, HISTORY_MAX_DAYS);
  let first = new Date(base - (span - 1) * DAY_MS).toISOString().slice(0, 10);
  // "All" starts where the server's counting does.
  if (days === null && history.first_day && history.first_day > first) first = history.first_day;
  const columns = new Map<string, { ok: number; failed: number; skipped: number }>();
  for (let t = Date.parse(first); t <= Date.parse(today); t += DAY_MS) {
    columns.set(new Date(t).toISOString().slice(0, 10), { ok: 0, failed: 0, skipped: 0 });
  }
  for (const r of history.days) {
    const c = columns.get(r.day);
    if (!c) continue;
    if (r.outcome === "ok") c.ok += r.runs;
    else if (isFailed(r)) c.failed += r.runs;
    else c.skipped += r.runs;
  }
  const list = [...columns.entries()];
  const peak = Math.max(1, ...list.map(([, c]) => c.ok + c.failed + c.skipped));
  const total = list.reduce((n, [, c]) => n + c.ok + c.failed + c.skipped, 0);
  return (
    <section className="run-trend" aria-label="Runs per day">
      {total > 0 && (
        <>
          <ul className="run-trend__bars" aria-hidden="true">
            {list.map(([day, c]) => (
              <li
                key={day}
                className="run-trend__day"
                title={`${runDate(dayDate(day))}: ${c.ok} ok, ${c.failed} failed, ${c.skipped} skipped`}
              >
                <span className="run-trend__seg" data-kind="failed" style={{ height: `${(c.failed / peak) * 100}%` }} />
                <span className="run-trend__seg" data-kind="skipped" style={{ height: `${(c.skipped / peak) * 100}%` }} />
                <span className="run-trend__seg" data-kind="ok" style={{ height: `${(c.ok / peak) * 100}%` }} />
              </li>
            ))}
          </ul>
          <details className="run-trend__table">
            <summary>Runs per day as a table</summary>
            <table>
              <thead>
                <tr>
                  <th scope="col">Day</th>
                  <th scope="col">ok</th>
                  <th scope="col">failed</th>
                  <th scope="col">skipped</th>
                </tr>
              </thead>
              <tbody>
                {list
                  .filter(([, c]) => c.ok + c.failed + c.skipped > 0)
                  .map(([day, c]) => (
                    <tr key={day}>
                      <th scope="row">{runDate(dayDate(day))}</th>
                      <td>{c.ok}</td>
                      <td>{c.failed}</td>
                      <td>{c.skipped}</td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </details>
        </>
      )}
      {!history.persisted && <p className="page-note">Kept in memory only: a restart forgets it.</p>}
    </section>
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

function when(since: Window): string {
  if (since === "1d") return "today";
  if (since === "1y") return "in the last year";
  if (since === "all") return "since the KB began";
  return `in the last ${since.replace("d", " days")}`;
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
