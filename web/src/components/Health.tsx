import { useMemo, useState } from "react";
import type {
  CheckCatalog,
  CollectionSummary,
  KBStatus,
  LintFinding,
  LintReport,
  LintSample,
  MaintenanceReview,
  MaintenanceSummary,
  ReviewItem,
} from "../api/types";
import {
  checkFix,
  checkHelp,
  checkLabel,
  groupFindings,
  groupReview,
  reviewInfo,
  reviewKinds,
  type CheckGroup,
  type ReviewGroup,
} from "../lib/health";
import { Count, Page, PageHeader, PageSection, Quiet, Segmented, SkeletonRows } from "./Page";
import { collectionVar } from "../lib/palette";
import { Icon } from "./Icon";
import { LintTrend } from "./LintTrend";
import { ErrorState } from "./States";

/**
 * Health (D338, D365, D704): what Cartographer found, and on a second tab what
 * it checks. *Issues* counts the kinds of issue and their occurrences, shows a
 * page field (one square per page, clustered by Map) and lists one row per
 * kind -- a lint check or a kind of kb_review proposal -- problems first, then
 * suggestions; an open row says what it is and how to fix it. *What it checks*
 * explains the rules by category, with the result on this KB and the trend.
 *
 * Like the rest of the Atlas it reads and never writes: a fix happens in a
 * kb-doctor session the operator starts from an agent.
 *
 * The agenda is filtered by the page's own Map picker (the rail only
 * navigates). Status and the maintenance summary describe the whole KB, so a
 * principal that cannot see all of it gets neither (both 404) and the page
 * shows what it can.
 */
export function Health({
  report,
  status,
  summary,
  checks,
  review = null,
  pending = false,
  scopeTitle,
  allFindings = null,
  collections = [],
  hmap = null,
  onMap,
  loading,
  error,
  onReveal,
  onRetry,
}: {
  report: LintReport | null;
  /** kb_status's knowledge signals; null when the principal cannot see the whole KB. */
  status: KBStatus | null;
  /** The maintenance summary (D323); null when not visible or not arrived. */
  summary: MaintenanceSummary | null;
  /** Every check the server runs (D365); null leaves the reference out. */
  checks?: CheckCatalog | null;
  /** The doctor's proposals beyond lint (D704); null leaves them out. */
  review?: MaintenanceReview | null;
  /** The review and the checks are still being read: the headline, which
   *  counts the review and sits beside the checks' switch, waits for them. */
  pending?: boolean;
  /** Title of the Map the agenda is scoped to; null = whole KB. */
  scopeTitle: string | null;
  /** The unfiltered findings: the picker counts over them, so a filter never hides its siblings. */
  allFindings?: LintFinding[] | null;
  collections?: CollectionSummary[];
  hmap?: string | null;
  onMap?(map: string | null): void;
  loading: boolean;
  error: unknown;
  onReveal(concept: string | null, message: string): void;
  onRetry(): void;
}) {
  // The agenda is the page; what the checks look for is a tab beside it (D365).
  const [tab, setTab] = useState<"agenda" | "checks">("agenda");
  // The pages of the agenda row under the pointer: the field lights only those.
  const [focus, setFocusRaw] = useState<Set<string> | null>(null);
  // A row about no page (a map's index, a KB file) lights nothing: dimming the
  // whole field for it would read as "every page is clean".
  // Which half of a two-colour square the row lights: its own colour only.
  const [focusTone, setFocusTone] = useState<Tone>("problem");
  const setFocus = (pages: Set<string> | null, tone: Tone = "problem") => {
    setFocusRaw(pages && pages.size > 0 ? pages : null);
    setFocusTone(tone);
  };

  // The headline counts lint and the doctor's proposals, which arrive in
  // separate requests: it waits until both have answered or failed, so the
  // numbers never change under the reader.
  const settled = !!report && !pending;

  const groups = useMemo(() => groupFindings(report?.findings ?? []), [report]);
  const proposals = useMemo(() => groupReview(review?.items ?? [], hmap), [review, hmap]);

  // The page field: every page of every Map, marked by its worst finding.
  // Over the unfiltered findings, so choosing a Map never empties the others.
  const field = useMemo(
    () => pageField(collections, allFindings ?? report?.findings ?? [], review?.items ?? []),
    [collections, allFindings, report, review],
  );

  if (error && !report) return <ErrorState error={error} onRetry={onRetry} />;

  const where = scopeTitle ? ` in ${scopeTitle}` : "";
  // One row per cause. map_without_templates is the lint face of a
  // template_proposal: while the review carries the proposal, it is listed once.
  const proposing = proposals.some((p) => p.kind === "template_proposal");
  const causes = groups.filter((g) => !(proposing && g.check === "map_without_templates"));
  // Broken first, then the heaviest: the bar's order is the reading order.
  const bySize = (a: CheckGroup, b: CheckGroup) =>
    Number(b.severity === "error") - Number(a.severity === "error") || rowSize(b) - rowSize(a);
  const problems = causes.filter((g) => g.severity !== "info").sort(bySize);
  const improvements = causes.filter((g) => g.severity === "info").sort(bySize);
  const issues = causes.length + proposals.length;
  const touched = field.reduce((n, m) => n + m.marks.length, 0);
  // Every single finding and proposal: the kinds are what to read, this is how much there is.
  const occurrences = causes.reduce((n, g) => n + g.count, 0) + proposals.reduce((n, p) => n + p.items.length, 0);
  const pages = field.reduce((n, m) => n + m.total, 0);
  // Bars are relative to the heaviest row: what matters is which issue weighs most.
  const heaviest = Math.max(1, ...causes.map(rowSize), ...proposals.map((p) => p.items.length));

  return (
    <Page label="Health" busy={loading} className="health">
      <PageHeader
        eyebrow="Health"
        title={
          !settled ? (
            <>Reading the KB&apos;s health…</>
          ) : tab === "checks" ? (
            <>What Cartographer checks</>
          ) : issues > 0 ? (
            <>
              Cartographer found <Count>{issues}</Count> {issues === 1 ? "kind of issue" : "kinds of issues"}
              {where}
            </>
          ) : (
            <>No issues found{where}</>
          )
        }
        actions={
          checks && settled ? (
            <Segmented<"agenda" | "checks">
              label="Health view"
              value={tab}
              options={[
                ["agenda", "Issues"],
                ["checks", "What it checks"],
              ]}
              onChange={setTab}
            />
          ) : undefined
        }
        help={
          <p>
            The checks are fixed rules, no AI involved. Mechanical problems Cartographer fixes by itself; the rest you
            fix with your agent, which proposes each change and waits for your yes.
          </p>
        }
      />
      {/* Below the header, not its subtitle: the header's controls sit on the
        title line on every page, and a subtitle would push them down. */}
      {settled && (
        <p className="health__lede">
          {tab === "checks"
            ? "Every page is read against fixed rules. This is what they look for, and what they found here."
            : issues > 0 && pages > 0
              ? `${plural(occurrences, "occurrence")} in all, on ${touched} of ${pages} pages. Open one to see where.`
              : "Every page is read against fixed rules, and none of them found anything."}
        </p>
      )}

      {tab === "checks" && checks && settled ? (
        <Coverage catalog={checks} report={report} review={review?.items ?? []} trend={summary?.lint_history} />
      ) : (
        <>
          {/* Over the whole KB, not the chosen Map: a clean Map must not take
            away the field, which is also the way back to every Map. */}
          {report && touched > 0 && (
            <div className="hfield">
              <PageField
                field={field}
                focus={focus}
                focusTone={focusTone}
                hmap={hmap}
                onMap={onMap}
                onReveal={onReveal}
              />
              <p className="pfield__legend">
                <span className="pfield__key" data-tone="warning" /> a problem
                <span className="pfield__key" data-tone="info" /> a suggestion
                <span className="pfield__key" data-both /> both
                <span className="pfield__key" /> fine
                <span className="pfield__hint">· one square per page · click a map to see only its pages</span>
              </p>
            </div>
          )}

          {error ? (
            <ErrorState error={error} onRetry={onRetry} />
          ) : !report ? (
            loading ? (
              <SkeletonRows label="Loading the issues" rows={4} />
            ) : null
          ) : issues === 0 ? (
            <Quiet>{scopeTitle ? `${scopeTitle} passes every check.` : "This KB passes every check."}</Quiet>
          ) : (
            <div className="agenda-list" aria-label="Issues">
              {problems.length > 0 && (
                <AgendaGroup tone="problem" title="Problems" count={problems.length}>
                  {problems.map((g) => (
                    <CheckRow
                      key={g.check}
                      group={g}
                      tone="problem"
                      heaviest={heaviest}
                      onFocus={setFocus}
                      onReveal={onReveal}
                    />
                  ))}
                </AgendaGroup>
              )}
              {proposals.length + improvements.length > 0 && (
                <AgendaGroup tone="suggestion" title="Suggestions" count={proposals.length + improvements.length}>
                  {proposals.map((p) => (
                    <ProposalRow key={p.kind} group={p} heaviest={heaviest} onFocus={setFocus} onReveal={onReveal} />
                  ))}
                  {improvements.map((g) => (
                    <CheckRow
                      key={g.check}
                      group={g}
                      tone="suggestion"
                      heaviest={heaviest}
                      onFocus={setFocus}
                      onReveal={onReveal}
                    />
                  ))}
                </AgendaGroup>
              )}
            </div>
          )}

          {status && hasKnowledge(status) && <Knowledge status={status} onReveal={onReveal} />}
        </>
      )}
    </Page>
  );
}

type Tone = "problem" | "suggestion";

/** One band of the agenda: its mark, its name, how many rows. */
function AgendaGroup({
  tone,
  title,
  count,
  children,
}: {
  tone: Tone;
  title: string;
  count: number;
  children: React.ReactNode;
}) {
  return (
    // Open on arrival; the reader folds a band to read the other.
    <details className="agenda" data-tone={tone} aria-label={title} open>
      <summary className="agenda__head">
        <span className="agenda__mark" aria-hidden="true" />
        <h2 className="agenda__title">{title}</h2>
        <span className="agenda__n">{count}</span>
      </summary>
      <ul className="agenda__rows">{children}</ul>
    </details>
  );
}

/** One page of the field: the most serious problem it has, if any, and whether it also has a suggestion. */
interface FieldMark {
  id: string;
  problem: "error" | "warning" | null;
  suggestion: boolean;
}

/** One Map of the page field: its pages, and the marked ones by id. */
interface FieldMap {
  name: string;
  title: string;
  total: number;
  marks: FieldMark[];
}

const markRank = (m: FieldMark) =>
  (m.problem === "error" ? 0 : m.problem === "warning" ? 1 : 2) + (m.suggestion ? 0 : 0.5);

/**
 * Every page of every Map with what the list shows for it: its most serious
 * problem and whether it also has a suggestion (a note, or a proposal naming
 * the page). The field and the list count the same things, so a Map with
 * suggestions in the list never looks clean here.
 */
function pageField(collections: CollectionSummary[], findings: LintFinding[], review: ReviewItem[]): FieldMap[] {
  const marks = new Map<string, FieldMark>();
  const at = (id: string) => marks.get(id) ?? marks.set(id, { id, problem: null, suggestion: false }).get(id)!;
  for (const f of findings) {
    if (!f.concept) continue;
    const m = at(f.concept);
    if (f.severity === "error") m.problem = "error";
    else if (f.severity === "warning") m.problem = m.problem ?? "warning";
    else m.suggestion = true;
  }
  for (const it of review) {
    for (const c of it.concepts) {
      // A suggestion about a whole Map (`<map>/_map`: a template to adopt) is no
      // page's: its row lights the whole cluster on hover instead.
      if (!c.endsWith("/_map")) at(c).suggestion = true;
    }
  }
  return collections
    .map((c) => {
      const mine = [...marks.values()]
        .filter((m) => m.id === c.name || m.id.startsWith(`${c.name}/`))
        .sort((a, b) => markRank(a) - markRank(b) || a.id.localeCompare(b.id));
      return {
        name: c.name,
        title: c.title || c.name,
        total: Math.max(c.concepts, mine.length),
        marks: mine,
      };
    })
    .filter((m) => m.total > 0)
    .sort((a, b) => b.total - a.total || a.name.localeCompare(b.name));
}

/**
 * The KB at a glance: a square per page, one cluster per Map in the Map's hue,
 * coloured by what the page has -- a problem, a suggestion, or both, split on
 * the diagonal. A cluster is the Map filter; a coloured square opens its page.
 */
function PageField({
  field,
  focus,
  focusTone,
  hmap,
  onMap,
  onReveal,
}: {
  field: FieldMap[];
  /** Pages to light, the rest dimmed; null shows every mark. */
  focus: Set<string> | null;
  /** The colour the lit pages show: a problem row lights the problem half of a two-colour square. */
  focusTone: Tone;
  hmap: string | null;
  onMap?(map: string | null): void;
  onReveal(concept: string | null, message: string): void;
}) {
  return (
    <div className="pfield" role="group" aria-label="Pages by Map" data-focus={focus ? focusTone : undefined}>
      {field.map((m) => {
        // A row about the whole Map (a template to adopt) lights the whole cluster.
        const whole = focus?.has(`map:${m.name}`) || undefined;
        const clean = Math.max(0, m.total - m.marks.length);
        return (
          <div
            key={m.name}
            className="pfield__map"
            data-chosen={hmap === m.name || undefined}
            data-dim={(hmap && hmap !== m.name) || undefined}
            style={{ "--map-hue": collectionVar(m.name) } as React.CSSProperties}
          >
            <button
              type="button"
              className="pfield__label"
              aria-pressed={hmap === m.name}
              title={`${m.marks.length} of ${m.total} pages with an issue · ${hmap === m.name ? "click to show every map" : "click to see only these"}`}
              onClick={() => onMap?.(hmap === m.name ? null : m.name)}
            >
              {m.title}
            </button>
            <span className="pfield__dots">
              {/* A mouse shortcut, not a control: a 9px square fails the WCAG
                target size, and every page it opens is also a button in the
                list below (the 2.5.8 equivalent-control exception). */}
              {m.marks.map((d) => (
                <span
                  key={d.id}
                  className="pfield__dot"
                  data-page
                  data-tone={d.problem ?? "info"}
                  data-both={(d.problem && d.suggestion) || undefined}
                  data-lit={whole || focus?.has(d.id) || undefined}
                  title={d.id}
                  aria-hidden="true"
                  onClick={() => onReveal(d.id, "")}
                />
              ))}
              {Array.from({ length: clean }, (_, i) => (
                <span key={i} className="pfield__dot" data-lit={whole} aria-hidden="true" />
              ))}
            </span>
          </div>
        );
      })}
    </div>
  );
}

/** The pages (or maps, or files) a check row touches. */
function rowSize(g: CheckGroup): number {
  return new Set(g.messages.flatMap((m) => m.findings.map((f) => f.concept ?? f.path ?? ""))).size;
}

/** Whether there is anything to list below the Knowledge facet's counts. */
function hasKnowledge(status: KBStatus): boolean {
  return !!status.open_gaps?.total || (status.search_misses ?? []).length > 0 || (status.stale_count ?? 0) > 0;
}

/**
 * The line every agenda row shares: what it is, a bar for how much of the KB
 * it weighs against the heaviest row, and the count in its own unit. What it
 * means and where it is are one click away, in the row's detail.
 */
function AgendaLine({
  label,
  size,
  unit,
  heaviest,
  broken,
  auto,
}: {
  label: string;
  size: number;
  unit: readonly [string, string];
  heaviest: number;
  broken?: boolean;
  /** Cartographer fixes it by itself: shown, so the list is everything found, but nobody's to do. */
  auto?: boolean;
}) {
  return (
    <summary className="arow">
      <span className="arow__label">
        {broken && <span className="agenda__broken">broken</span>}
        {label}
        {auto && (
          <span className="arow__auto" title="Cartographer fixes it by itself at its next run">
            <Icon name="repair" size={12} /> fixes itself
          </span>
        )}
      </span>
      <span className="arow__bar" aria-hidden="true">
        <span style={{ width: `${Math.max(2, Math.round((size / heaviest) * 100))}%` }} />
      </span>
      <span className="arow__count">
        {size} <span className="arow__unit">{size === 1 ? unit[0] : unit[1]}</span>
      </span>
    </summary>
  );
}

/** One specific case of an issue: what was found, and the pages it was found on. */
interface Case {
  key: string;
  what: string;
  /** Concepts: each opens on the Atlas. */
  pages: string[];
  /** Files that are no concept (a map's index, a KB file): named, with nothing to open. */
  files?: string[];
}

/**
 * An open issue: two short lines -- what it is, how it gets fixed -- then one
 * line per case with its page count. The pages themselves wait behind the
 * count: forty page ids under every case is a wall, not an answer.
 */
function IssueDetail({
  what,
  fix,
  cases,
  rule,
  onReveal,
}: {
  what: string;
  fix: string;
  cases: Case[];
  rule?: string;
  onReveal(concept: string | null, message: string): void;
}) {
  return (
    <div className="arow__detail">
      <dl className="issue__about">
        {what && (
          <div>
            <dt>What it is</dt>
            <dd>{what}</dd>
          </div>
        )}
        <div>
          <dt>To fix</dt>
          <dd>{fix}</dd>
        </div>
      </dl>
      <ul className="issue__cases">
        {cases.slice(0, MESSAGE_CAP).map((c) => (
          <CaseRow key={c.key} item={c} onReveal={onReveal} />
        ))}
      </ul>
      {cases.length > MESSAGE_CAP && (
        <p className="page-note">and {plural(cases.length - MESSAGE_CAP, "more")}: your agent lists them all.</p>
      )}
      {rule && (
        <p className="arow__check">
          rule <code>{rule}</code>
        </p>
      )}
    </div>
  );
}

/** A page id as a reader names it: its last segment, the map in the tooltip. */
function shortPage(id: string): string {
  return id.split("/").pop() || id;
}

/** One case: what was found and where; one page opens directly, several unfold on click, a file is only named. */
function CaseRow({ item, onReveal }: { item: Case; onReveal(concept: string | null, message: string): void }) {
  const [open, setOpen] = useState(false);
  const one = item.pages.length === 1 ? item.pages[0]! : null;
  const files = item.files ?? [];
  return (
    <li className="issue__case">
      <span className="issue__what">{item.what}</span>
      {one ? (
        <button type="button" className="issue__page" title={one} onClick={() => onReveal(one, "")}>
          {shortPage(one)}
        </button>
      ) : item.pages.length > 1 ? (
        <button type="button" className="issue__toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
          {plural(item.pages.length, "page")} {open ? "▴" : "▾"}
        </button>
      ) : files.length > 0 ? (
        <code className="issue__file" title="Not a page: open it from an agent session">
          {files[0]}
          {files.length > 1 && ` +${files.length - 1}`}
        </code>
      ) : (
        <span />
      )}
      {open && (
        <span className="issue__pages">
          {item.pages.map((p) => (
            <button key={p} type="button" className="issue__page" title={p} onClick={() => onReveal(p, "")}>
              {shortPage(p)}
            </button>
          ))}
        </span>
      )}
    </li>
  );
}

/** One cause: its check's findings folded into one row, opened to what it is, how to fix it, and where. */
function CheckRow({
  group,
  tone,
  heaviest,
  onFocus,
  onReveal,
}: {
  group: CheckGroup;
  tone: Tone;
  heaviest: number;
  onFocus(pages: Set<string> | null, tone?: Tone): void;
  onReveal(concept: string | null, message: string): void;
}) {
  const all = group.messages.flatMap((m) => m.findings);
  // What one finding is about: a page, a map (its contract or its index), or a file of the KB.
  const ofMap = (path = "") => path.endsWith("_map.md") || /(^|\/)index\.md$/.test(path);
  const unit = all.every((f) => !f.concept && ofMap(f.path))
    ? (["map", "maps"] as const)
    : all.every((f) => f.concept)
      ? (["page", "pages"] as const)
      : (["file", "files"] as const);
  const fix =
    group.handler === "auto"
      ? "Nothing to do: Cartographer fixes it by itself at its next run."
      : (checkFix(group.check) || "Your agent proposes a fix and waits for your yes.") +
        (group.handler === "mixed" ? " Cartographer fixes some of them by itself." : "");
  return (
    <li
      className="arow-item"
      id={`check-${group.check}`}
      data-tone={tone}
      onMouseEnter={() => onFocus(new Set(all.flatMap((f) => (f.concept ? [f.concept] : []))), tone)}
      onMouseLeave={() => onFocus(null)}
    >
      <details>
        <AgendaLine
          label={checkLabel(group.check)}
          size={rowSize(group)}
          unit={unit}
          heaviest={heaviest}
          broken={group.severity === "error"}
          auto={group.handler === "auto"}
        />
        <IssueDetail
          what={checkHelp(group.check)}
          fix={fix}
          rule={group.check}
          cases={group.messages.map((m) => ({
            key: m.message,
            what: m.message,
            pages: [...new Set(m.findings.flatMap((f) => (f.concept ? [f.concept] : [])))],
            files: [...new Set(m.findings.flatMap((f) => (f.concept ? [] : [f.path])))],
          }))}
          onReveal={onReveal}
        />
      </details>
    </li>
  );
}

/** The pages an item names: a map's contract (`<map>/_map`) is no page to open, and its evidence names the map. */
function pagesOf(it: ReviewItem): string[] {
  return it.concepts.filter((c) => !c.endsWith("/_map"));
}

/** One kind of kb_review proposal, folded into one row, opened to what it is, how to act, and where. */
function ProposalRow({
  group,
  heaviest,
  onFocus,
  onReveal,
}: {
  group: ReviewGroup;
  heaviest: number;
  onFocus(pages: Set<string> | null, tone?: Tone): void;
  onReveal(concept: string | null, message: string): void;
}) {
  const info = reviewInfo(group.kind);
  return (
    <li
      className="arow-item"
      id={`review-${group.kind}`}
      data-tone="suggestion"
      onMouseEnter={() =>
        onFocus(
          new Set(
            group.items.flatMap((it) => it.concepts.map((c) => (c.endsWith("/_map") ? `map:${c.slice(0, -5)}` : c))),
          ),
          "suggestion",
        )
      }
      onMouseLeave={() => onFocus(null)}
    >
      <details>
        <AgendaLine label={info.label} size={group.items.length} unit={info.unit} heaviest={heaviest} />
        <IssueDetail
          what={info.help}
          fix={info.fix}
          cases={group.items.map((it, i) => ({
            key: `${it.term ?? ""}${it.concepts.join(",")}${i}`,
            // A glossary gap is its term; the rest say it in their evidence.
            what: it.term ?? it.evidence,
            pages: pagesOf(it),
          }))}
          onReveal={onReveal}
        />
      </details>
    </li>
  );
}

const MESSAGE_CAP = 30;

/** A category's name for a reader, and one line on what its rules read. */
const CATEGORY: Record<string, [string, string]> = {
  pages: ["Pages", "Each page on its own: its fields, its title, its text, its secrets."],
  templates: ["Templates", "Pages against the template their map asks them to follow."],
  validity: ["Readable pages", "Whether a file is a page the tools can read at all."],
  links: ["Links", "Links between pages, and pages left out of the web of links."],
  values: ["Types and values", "The types and values pages use, across the KB."],
  maps: ["Maps", "Maps, their indexes, their folders and their files."],
  artifacts: ["Agent files", "The skills, agents and instructions the KB hands to agents."],
  kb: ["KB settings", "KB-wide files: placeholders, hooks, exemptions."],
};

/** One rule as the reference lists it: its name, what it looks for, who fixes it, what it found here. */
interface Rule {
  key: string;
  name: string;
  help: string;
  auto?: boolean;
  /** Findings here; null when the rule has nothing to read in this KB. */
  n: number | null;
  title?: string;
}

/**
 * What Cartographer checks, told so a newcomer understands it: how checking
 * works in three steps, then one line per category -- what its rules read and
 * what they found here -- folded until the reader opens it. Rules that found something
 * come first; the ones with nothing to read here wait at the end.
 */
function Coverage({
  catalog,
  report,
  review,
  trend,
}: {
  catalog: CheckCatalog;
  report: LintReport;
  review: ReviewItem[];
  trend?: LintSample[];
}) {
  const count = (name: string) => report.by_check[name] ?? 0;
  // A check that cannot run here (D371) is neither clean nor dirty: its zero would lie.
  const idle = (c: CheckCatalog["checks"][number]) => c.active === false;
  const auto = catalog.checks.filter((c) => c.auto).length;
  const found = catalog.checks.filter((c) => count(c.name) > 0).length;
  const skipped = catalog.checks.filter(idle).length;
  const suggested = new Map<string, number>();
  for (const it of review) suggested.set(it.kind, (suggested.get(it.kind) ?? 0) + 1);
  const groups: [string, string, Rule[]][] = catalog.categories
    .map((category): [string, string, Rule[]] => {
      const [title, about] = CATEGORY[category] ?? [category, ""];
      const rules = catalog.checks
        .filter((c) => c.category === category)
        .map((c) => ({
          key: c.name,
          name: checkLabel(c.name),
          help: checkHelp(c.name),
          auto: c.auto,
          n: idle(c) ? null : count(c.name),
          title: idle(c) ? c.reason : c.name,
        }));
      return [title, about, rules];
    })
    .filter(([, , rules]) => rules.length > 0);
  groups.push([
    "Suggestions",
    "Patterns across many pages: never wrong, but worth a decision.",
    reviewKinds().map((kind) => ({
      key: kind,
      name: reviewInfo(kind).label,
      help: reviewInfo(kind).help,
      n: suggested.get(kind) ?? 0,
      title: kind,
    })),
  ]);
  return (
    <section className="health__coverage" id="health-coverage" aria-label="Checks">
      <ol className="howto" aria-label="How checking works">
        <li>
          <Icon name="search" size={16} />
          <strong>It reads every page</strong>
          <span>{catalog.checks.length} fixed rules, on every write and at every start. No AI.</span>
        </li>
        <li>
          <Icon name="repair" size={16} />
          <strong>It fixes the sure ones</strong>
          <span>{auto} rules have a fix that cannot go wrong: Cartographer applies it by itself.</span>
        </li>
        <li>
          <Icon name="doctor" size={16} />
          <strong>It shows you the rest</strong>
          <span>They appear under Issues, and your agent fixes them with you.</span>
        </li>
      </ol>

      <p className="coverage__summary">
        In this KB: <strong>{catalog.checks.length - found - skipped}</strong> rules pass · <strong>{found}</strong>{" "}
        found something
        {skipped > 0 && (
          <>
            {" "}
            · <strong>{skipped}</strong> have nothing to read here
          </>
        )}
      </p>
      <LintTrend history={trend} />

      <div className="rules">
        {groups.map(([title, about, rules]) => {
          const hits = rules.filter((r) => (r.n ?? 0) > 0).length;
          const sorted = [...rules].sort(
            (a, b) =>
              Number(a.n === null) - Number(b.n === null) || (b.n ?? 0) - (a.n ?? 0) || a.name.localeCompare(b.name),
          );
          return (
            <details key={title} className="rules__group">
              <summary className="rules__head">
                <span className="rules__title">{title}</span>
                <span className="rules__about">{about}</span>
                <span className="rules__tally" data-hits={hits > 0 || undefined}>
                  {hits > 0 ? `${hits} of ${rules.length} found something` : `${rules.length} rules, all clear`}
                </span>
              </summary>
              <ul className="rules__list">
                {sorted.map((r) => (
                  <li
                    key={r.key}
                    className="rule"
                    data-found={(r.n ?? 0) > 0 || undefined}
                    data-inactive={r.n === null || undefined}
                    title={r.title}
                  >
                    <span className="rule__mark" aria-hidden="true">
                      {r.n === null ? "–" : r.n > 0 ? "●" : "✓"}
                    </span>
                    <span className="rule__name">
                      {r.name}
                      {r.auto && (
                        <span className="rule__auto" title="Cartographer fixes it by itself">
                          <Icon name="repair" size={12} />
                          <span className="sr-only">automatic</span>
                        </span>
                      )}
                    </span>
                    <span className="rule__help">{r.n === null ? `Nothing to read here: ${r.title}` : r.help}</span>
                    <span className="rule__count">{r.n === null ? "–" : r.n}</span>
                  </li>
                ))}
              </ul>
            </details>
          );
        })}
      </div>
    </section>
  );
}

function plural(n: number, word: string, many = `${word}s`): string {
  return `${n} ${n === 1 ? word : many}`;
}

/**
 * What the KB does not know, beside what is wrong with it: the knowledge gaps
 * agents recorded, the searches that found nothing, and the pages past their
 * review date. Lint says a page is malformed; these say a page is missing or
 * stale.
 */
function Knowledge({
  status,
  onReveal,
}: {
  status: KBStatus;
  onReveal(concept: string | null, message: string): void;
}) {
  const gaps = status.open_gaps;
  const misses = status.search_misses ?? [];
  const stale = status.stale_count ?? 0;
  const total = (gaps?.total ?? 0) + misses.filter((m) => !m.resolved).length + stale;
  return (
    <PageSection title="What the KB doesn't know" id="health-gaps" count={total}>
      <>
        {!!gaps?.total && (
          <div className="health__group" role="group" aria-labelledby="health-open-gaps">
            <h3 className="health__group-title" id="health-open-gaps">
              Open questions and gaps
              <span className="page-section__count">{gaps.total}</span>
            </h3>
            <ul className="rows">
              {(gaps.recent ?? []).map((g) => (
                <li key={g.id}>
                  <button type="button" className="row" onClick={() => onReveal(g.id, "")}>
                    <span className="health__glyph" aria-hidden="true">
                      ◌
                    </span>
                    <span className="row__body">
                      <span className="row__title">{g.title || g.id}</span>
                      <span className="row__meta">
                        <span className="pill">{(g.kind ?? "gap").replace(/_/g, " ")}</span>
                        <code>{g.id}</code>
                      </span>
                    </span>
                    <span className="row__end health__go">Open concept</span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
        {misses.length > 0 && (
          <div className="health__group" role="group" aria-labelledby="health-misses">
            <h3 className="health__group-title" id="health-misses">
              Searched for, not found
              <span className="page-section__count">{misses.length}</span>
            </h3>
            <p className="page-note">
              What agents searched for in the last 30 days and the KB could not answer. A ticked one finds something
              now.
            </p>
            <ul className="health__misses">
              {misses.map((m) => (
                <li
                  key={m.query}
                  className={m.resolved ? "chip chip--resolved" : "chip"}
                  title={m.resolved ? "This search finds something now" : undefined}
                >
                  {m.resolved && (
                    <span className="chip__resolved" aria-hidden="true">
                      ✓
                    </span>
                  )}
                  {m.query}
                  {m.resolved && <span className="sr-only"> (now found)</span>}
                  <span className="chip__count">{m.count}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
        {stale > 0 && (
          <Quiet tone="neutral">
            {stale === 1 ? "One concept is" : `${stale} concepts are`} past its review date (<code>review_after</code>).
          </Quiet>
        )}
      </>
    </PageSection>
  );
}
