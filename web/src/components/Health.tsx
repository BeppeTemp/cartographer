import { useMemo, useState } from "react";
import type {
  CheckCatalog,
  CollectionSummary,
  Contradiction,
  GitConflict,
  LintFinding,
  LintReport,
} from "../api/types";
import {
  categoryHue,
  categoryOrder,
  checkFix,
  checkHelp,
  checkLabel,
  groupByCategory,
  groupFindings,
  OTHER,
  pageCategories,
  rowSize,
  type CheckGroup,
} from "../lib/health";
import { Count, Page, PageHeader, PageSection, Quiet, Segmented, SkeletonRows } from "./Page";
import { collectionVar } from "../lib/palette";
import { Icon } from "./Icon";
import { ErrorState } from "./States";

/**
 * Health (D338, D365, D704, D947): what Cartographer found, and on a second tab
 * what it checks. A lint finding is a problem, and Health shows only those.
 * *Issues* counts the kinds of problem and their occurrences, shows a page
 * field (one square per page, clustered by Map, painted with the colours of
 * the categories of its problems) and lists one row per lint check under its
 * category, the largest first; an open row says what it is and how to fix it. The
 * doctor's other proposals (the kb_review items, observations included) are
 * decisions, not problems: they are Work's (D948). *What it checks* explains
 * the rules by category, with the result on this KB.
 *
 * Like the rest of the Atlas it reads and never writes: a fix happens in a
 * kb-doctor session the operator starts from an agent.
 *
 * The agenda is filtered by the page's own Map picker (the rail only
 * navigates). Status describes the whole KB, so a
 * principal that cannot see all of it does not get it (404) and the page
 * shows what it can.
 */
export function Health({
  report,
  checks,
  contradictions = null,
  conflicts = null,
  pending = false,
  scopeTitle,
  allFindings = null,
  collections = [],
  hmap = null,
  onMap,
  loading,
  error,
  onReveal,
  onWork = () => undefined,
  onRetry,
}: {
  report: LintReport | null;
  /** Every check the server runs (D365); null leaves the reference out. */
  checks?: CheckCatalog | null;
  /** The open Contradiction concepts; null when not visible or not arrived. */
  contradictions?: Contradiction[] | null;
  /** Open git rebase conflicts; null when not visible or not arrived, and the
   *  section is shown only when there are some. */
  conflicts?: GitConflict[] | null;
  /** The checks are still being read: the headline, which sits beside the
   *  checks' switch, waits for them. */
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
  /** Opens the Work panel, where the open contradictions are listed. */
  onWork?(): void;
  onRetry(): void;
}) {
  // The agenda is the page; what the checks look for is a tab beside it (D365).
  const [tab, setTab] = useState<"agenda" | "checks">("agenda");
  // The pages of the agenda row under the pointer, and its category: the field
  // lights only those pages, in that category's colour.
  const [focus, setFocusRaw] = useState<Focus | null>(null);
  // A row about no page (a map's index, a KB file) lights nothing: dimming the
  // whole field for it would read as "every page is clean".
  const setFocus = (f: Focus | null) => setFocusRaw(f && f.pages.size > 0 ? f : null);

  // The headline waits until the lint and the checks have answered or failed,
  // so the numbers never change under the reader.
  const settled = !!report && !pending;

  const groups = useMemo(() => groupFindings(report?.findings ?? []), [report]);
  // One heading per category that has findings, in the catalog's order; the
  // rows under it are the largest first.
  const sections = useMemo(() => groupByCategory(groups, checks), [groups, checks]);

  // The page field: every page of every Map, marked when it has a problem.
  // Over the unfiltered findings, so choosing a Map never empties the others.
  const field = useMemo(
    () => pageField(collections, allFindings ?? report?.findings ?? [], checks),
    [collections, allFindings, report, checks],
  );

  if (error && !report) return <ErrorState error={error} onRetry={onRetry} />;

  const where = scopeTitle ? ` in ${scopeTitle}` : "";
  // One row per cause: the headline counts them, the bars weigh them against the heaviest.
  const issues = groups.length;
  const touched = field.reduce((n, m) => n + m.marks.length, 0);
  // Every single finding: the kinds are what to read, this is how much there is.
  const occurrences = groups.reduce((n, g) => n + g.count, 0);
  const pages = field.reduce((n, m) => n + m.total, 0);
  // Nothing found anywhere in the KB: the goal state. It keeps the page of the
  // issues state (headline, field, a list, the sections below) and says what
  // was read instead of what was found.
  const allClear = settled && !!report && issues === 0 && touched === 0 && pages > 0;
  const scopePages = (hmap && field.find((m) => m.name === hmap)?.total) || pages;
  // Rules that could run here (D371): an idle rule's silence is no pass.
  const rules = checks ? checks.checks.filter((c) => c.active !== false).length : 0;
  // Bars are relative to the heaviest row: what matters is which issue weighs most.
  const heaviest = Math.max(1, ...groups.map(rowSize));

  return (
    <Page label="Health" busy={loading} className="health">
      <PageHeader
        eyebrow="Health"
        title={
          !settled ? (
            <>Reading the KB&apos;s health…</>
          ) : tab === "checks" ? (
            <>What Cartographer checks</>
          ) : allClear ? (
            <>
              <Count>Every page</Count> passes every check{where}
            </>
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
            : allClear
              ? clearLede(scopePages, rules)
              : issues > 0 && pages > 0
                ? `${plural(occurrences, "occurrence")} in all, on ${touched} of ${pages} pages. Open one to see where.`
                : "Every page is read against fixed rules, and none of them found anything."}
        </p>
      )}

      {tab === "checks" && checks && settled ? (
        <Coverage catalog={checks} report={report} />
      ) : (
        <>
          {/* Over the whole KB, not the chosen Map: a clean Map must not take
            away the field, which is also the way back to every Map. */}
          {report && (touched > 0 || allClear) && (
            <div className="hfield">
              <PageField field={field} focus={focus} clear={allClear} hmap={hmap} onMap={onMap} onReveal={onReveal} />
              <p className="pfield__legend">
                {allClear ? (
                  <>
                    <span className="pfield__key" data-tone="ok" /> fine
                  </>
                ) : (
                  <>
                    {fieldCategories(field, checks).map((c) => (
                      <span key={c} className="pfield__item">
                        <span
                          className="pfield__key"
                          data-category={c}
                          style={{ "--tone": categoryHue(c) } as React.CSSProperties}
                        />
                        {categoryTitle(c)}
                      </span>
                    ))}
                    <span className="pfield__item">
                      <span className="pfield__key" /> fine
                    </span>
                  </>
                )}
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
            allClear && checks ? (
              <AllClear catalog={checks} onOpen={() => setTab("checks")} />
            ) : (
              <Quiet>{scopeTitle ? `${scopeTitle} passes every check.` : "This KB passes every check."}</Quiet>
            )
          ) : (
            <div className="agenda-list" aria-label="Issues">
              {sections.map((s) => (
                <AgendaGroup key={s.category} category={s.category} count={s.rows.length}>
                  {s.rows.map((g) => (
                    <CheckRow
                      key={g.check}
                      group={g}
                      category={s.category}
                      heaviest={heaviest}
                      onFocus={setFocus}
                      onReveal={onReveal}
                    />
                  ))}
                </AgendaGroup>
              ))}
            </div>
          )}

          {contradictions && <Contradictions items={contradictions} onWork={onWork} />}

          {!!conflicts?.length && <GitConflicts items={conflicts} onReveal={onReveal} />}
        </>
      )}
    </Page>
  );
}

/** The all-clear subline: what was read, and against how many rules (none known while the catalog is missing). */
function clearLede(pages: number, rules: number) {
  return (
    <>
      <strong>{plural(pages, "page")}</strong> read against{" "}
      {rules > 0 ? <strong>{plural(rules, "rule")}</strong> : "fixed rules"}, and none of them found anything.
    </>
  );
}

/**
 * The all-clear in place of the issues list: one row per family of rules, with
 * how many of its rules ran and found nothing. A family whose rules all have
 * nothing to read here (D371) says so instead of claiming a pass. A row opens
 * the rules themselves, on the other tab.
 */
function AllClear({ catalog, onOpen }: { catalog: CheckCatalog; onOpen(): void }) {
  const families = catalog.categories
    .map((category) => {
      const [title, about] = CATEGORY[category] ?? [category, ""];
      const mine = catalog.checks.filter((c) => c.category === category);
      return { category, title, about, total: mine.length, ran: mine.filter((c) => c.active !== false).length };
    })
    .filter((f) => f.total > 0);
  return (
    <ul className="rules clear" aria-label="Rule families">
      {families.map((f) => (
        <li key={f.category} className="rules__group">
          <button
            type="button"
            className="rules__head clear__row"
            data-idle={f.ran === 0 || undefined}
            title="See these rules in What it checks"
            onClick={onOpen}
          >
            <span className="clear__title">
              <span className="clear__mark" aria-hidden="true">
                {f.ran > 0 ? "✓" : "–"}
              </span>
              {f.title}
            </span>
            <span className="rules__about">{f.about}</span>
            <span className="rules__tally">
              {f.ran > 0 ? `${plural(f.ran, "rule")}, all clear` : "nothing to read here"}
            </span>
          </button>
        </li>
      ))}
    </ul>
  );
}

/** The agenda row under the pointer: its pages, and the category whose colour they take. */
interface Focus {
  pages: Set<string>;
  category: string;
}

/** A category's name for a reader: its title, "Other" for the checks the catalog does not list. */
function categoryTitle(category: string): string {
  return category === OTHER ? "Other" : (CATEGORY[category]?.[0] ?? category);
}

/** The colour mark of a category: the one the field's squares, the legend and the row bars share. */
function CategoryMark({ category }: { category: string }) {
  return (
    <span className="catmark" aria-hidden="true" style={{ "--tone": categoryHue(category) } as React.CSSProperties} />
  );
}

/** One category of the agenda: its colour, its name, how many kinds of issue, then those rows. */
function AgendaGroup({ category, count, children }: { category: string; count: number; children: React.ReactNode }) {
  const title = categoryTitle(category);
  const id = `agenda-${category}`;
  return (
    // The rows below take the category's colour for their bars.
    <div
      className="agenda"
      role="group"
      aria-labelledby={id}
      style={{ "--tone": categoryHue(category) } as React.CSSProperties}
    >
      <div className="agenda__head">
        <CategoryMark category={category} />
        <h2 className="agenda__title" id={id}>
          {title}
        </h2>
        <span className="agenda__n">{count}</span>
      </div>
      <ul className="agenda__rows">{children}</ul>
    </div>
  );
}

/** One page of the field with a problem, by id, and the categories of its problems in catalog order. */
interface FieldMark {
  id: string;
  cats: string[];
}

/** One Map of the page field: its pages, and the marked ones by id. */
interface FieldMap {
  name: string;
  title: string;
  total: number;
  marks: FieldMark[];
}

/**
 * Every page of every Map, marked when the list has a problem for it. The field
 * and the list count the same things, so a Map with problems in the list never
 * looks clean here.
 */
function pageField(
  collections: CollectionSummary[],
  findings: LintFinding[],
  catalog: CheckCatalog | null | undefined,
): FieldMap[] {
  const marked = pageCategories(findings, catalog);
  return collections
    .map((c) => {
      const mine: FieldMark[] = [...marked.keys()]
        .filter((id) => id === c.name || id.startsWith(`${c.name}/`))
        .sort((a, b) => a.localeCompare(b))
        .map((id) => ({ id, cats: marked.get(id)! }));
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

/** A square holds at most four colours: its first four categories, in catalog order. */
const MAX_COLOURS = 4;

/** The categories the field's squares show, in catalog order ("other" last): what the legend names. */
function fieldCategories(field: FieldMap[], catalog: CheckCatalog | null | undefined): string[] {
  const shown = new Set(field.flatMap((m) => m.marks.flatMap((d) => d.cats.slice(0, MAX_COLOURS))));
  return categoryOrder(catalog).filter((c) => shown.has(c));
}

/** The colours of a square as the custom properties the stylesheet's gradients read: --c1..--c4. */
function squareColours(cats: string[]): React.CSSProperties {
  return Object.fromEntries(
    cats.slice(0, MAX_COLOURS).map((c, i) => [`--c${i + 1}`, categoryHue(c)]),
  ) as React.CSSProperties;
}

/**
 * The KB at a glance: a square per page, one cluster per Map in the Map's hue,
 * coloured when the page has a problem: the colours of the categories of its
 * problems (one solid, two halves, three stripes, four quadrants -- the
 * gradients are in the stylesheet, fed by --c1..--c4). A cluster is the Map
 * filter; a coloured square opens its page.
 */
function PageField({
  field,
  focus,
  clear = false,
  hmap,
  onMap,
  onReveal,
}: {
  field: FieldMap[];
  /** Pages to light in one category's colour, the rest dimmed; null shows every mark. */
  focus: Focus | null;
  /** Nothing found in the KB: every square is "fine", shown in its green. */
  clear?: boolean;
  hmap: string | null;
  onMap?(map: string | null): void;
  onReveal(concept: string | null, message: string): void;
}) {
  return (
    <div
      className="pfield"
      role="group"
      aria-label="Pages by Map"
      data-focus={focus ? "problem" : undefined}
      data-clear={clear || undefined}
    >
      {field.map((m) => {
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
              {m.marks.map((d) => {
                const lit = !!focus?.pages.has(d.id);
                // A lit square shows only the colour of the row that lit it.
                const cats = lit ? [focus!.category] : d.cats;
                return (
                  <span
                    key={d.id}
                    className="pfield__dot"
                    data-page
                    data-problem
                    data-n={Math.min(cats.length, MAX_COLOURS)}
                    data-lit={lit || undefined}
                    style={squareColours(cats)}
                    title={d.id}
                    aria-hidden="true"
                    onClick={() => onReveal(d.id, "")}
                  />
                );
              })}
              {Array.from({ length: clean }, (_, i) => (
                <span key={i} className="pfield__dot" aria-hidden="true" />
              ))}
            </span>
          </div>
        );
      })}
    </div>
  );
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
  auto,
}: {
  label: string;
  size: number;
  unit: readonly [string, string];
  heaviest: number;
  /** Cartographer fixes it by itself: shown, so the list is everything found, but nobody's to do. */
  auto?: boolean;
}) {
  return (
    <summary className="arow">
      <span className="arow__label">
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
          {plural(item.pages.length, "page")} <span className="chevron" aria-hidden="true" />
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
  category,
  heaviest,
  onFocus,
  onReveal,
}: {
  group: CheckGroup;
  /** Its category: the pages the row lights take this colour. */
  category: string;
  heaviest: number;
  onFocus(focus: Focus | null): void;
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
      onMouseEnter={() => onFocus({ pages: new Set(all.flatMap((f) => (f.concept ? [f.concept] : []))), category })}
      onMouseLeave={() => onFocus(null)}
    >
      <details>
        <AgendaLine
          label={checkLabel(group.check)}
          size={rowSize(group)}
          unit={unit}
          heaviest={heaviest}
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
  kb: ["KB settings", "KB-wide files: placeholders, hooks."],
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
function Coverage({ catalog, report }: { catalog: CheckCatalog; report: LintReport }) {
  const count = (name: string) => report.by_check[name] ?? 0;
  // A check that cannot run here (D371) is neither clean nor dirty: its zero would lie.
  const idle = (c: CheckCatalog["checks"][number]) => c.active === false;
  const auto = catalog.checks.filter((c) => c.auto).length;
  const found = catalog.checks.filter((c) => count(c.name) > 0).length;
  const skipped = catalog.checks.filter(idle).length;
  const groups: [string, string, string, Rule[]][] = catalog.categories
    .map((category): [string, string, string, Rule[]] => {
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
      return [category, title, about, rules];
    })
    .filter(([, , , rules]) => rules.length > 0);
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

      <div className="rules">
        {groups.map(([category, title, about, rules]) => {
          const hits = rules.filter((r) => (r.n ?? 0) > 0).length;
          const sorted = [...rules].sort(
            (a, b) =>
              Number(a.n === null) - Number(b.n === null) || (b.n ?? 0) - (a.n ?? 0) || a.name.localeCompare(b.name),
          );
          return (
            <details key={title} className="rules__group">
              <summary className="rules__head">
                <span className="rules__title">
                  <CategoryMark category={category} />
                  {title}
                </span>
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

/**
 * The Contradiction concepts still open block commit_gate, so Health says so;
 * deciding which page holds is work, listed in Work beside the rest.
 */
function Contradictions({ items, onWork }: { items: Contradiction[]; onWork(): void }) {
  return (
    <PageSection title="Contradictions" id="health-contradictions" count={items.length}>
      {items.length === 0 ? (
        <Quiet>
          No open contradictions. An agent that finds two pages disagreeing records one (
          <code>contradiction_report</code>
          ); while it is open, <code>commit_gate</code> blocks.
        </Quiet>
      ) : (
        <p className="page-note">
          {items.length === 1 ? "One open contradiction blocks" : `${items.length} open contradictions block`}{" "}
          <code>commit_gate</code> until someone decides which page holds.{" "}
          <button type="button" className="ref-link" onClick={onWork}>
            See {items.length === 1 ? "it" : "them"} in Work
          </button>
          .
        </p>
      )}
    </PageSection>
  );
}

function plural(n: number, word: string, many = `${word}s`): string {
  return `${n} ${n === 1 ? word : many}`;
}

/**
 * Git rebase conflicts the sync could not settle by itself: a concept (or a
 * reserved file) with two versions waiting for an agent to pick one with
 * git_conflict_resolve. Shown only when there are some.
 */
function GitConflicts({
  items,
  onReveal,
}: {
  items: GitConflict[];
  onReveal(concept: string | null, message: string): void;
}) {
  return (
    <PageSection title="Git conflicts" id="health-conflicts" count={items.length}>
      <p className="page-note">
        A sync met two versions of these files and stopped. An agent settles each one with{" "}
        <code>git_conflict_resolve</code> (the <code>kb-conflict-resolve</code> skill walks it through).
      </p>
      <ul className="rows">
        {items.map((c) => (
          <li key={c.concept_id} className="health__contradiction">
            <button
              type="button"
              className="row"
              disabled={c.kind === "reserved"}
              onClick={() => onReveal(c.concept_id, "")}
            >
              <span className="health__glyph" aria-hidden="true">
                ⑂
              </span>
              <span className="row__body">
                <span className="row__title">{c.concept_id}</span>
                <span className="row__meta">
                  {c.kind === "reserved" && <span className="pill">reserved file</span>}
                  <span className="pill">
                    {c.resolution_strategy ? `resolution: ${c.resolution_strategy}` : "unresolved"}
                  </span>
                  {!!c.files && c.files.length > 1 && <span>{c.files.length} files in the same rebase</span>}
                </span>
              </span>
            </button>
          </li>
        ))}
      </ul>
    </PageSection>
  );
}
