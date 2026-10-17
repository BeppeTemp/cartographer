import type { LintFinding, LintReport, ReviewItem } from "../api/types";

/** One cause on the Health page: a check's findings, folded by message. */
export interface CheckGroup {
  check: string;
  /** The worst severity among its findings. */
  severity: string;
  count: number;
  /** Who acts on it (D365): all automatic, none, or some of each. */
  handler: "auto" | "doctor" | "mixed";
  /** Distinct messages, most pages first: an import's one cause repeated on
   *  a hundred pages is one line, not a hundred. */
  messages: { message: string; findings: LintFinding[] }[];
}

const RANK: Record<string, number> = { error: 0, warning: 1, info: 2 };

/** Findings grouped by check, worst severity first, then the largest. */
export function groupFindings(findings: LintFinding[]): CheckGroup[] {
  const byCheck = new Map<string, LintFinding[]>();
  for (const f of findings) (byCheck.get(f.check) ?? byCheck.set(f.check, []).get(f.check)!).push(f);
  const groups: CheckGroup[] = [];
  for (const [check, list] of byCheck) {
    const byMessage = new Map<string, LintFinding[]>();
    for (const f of list) (byMessage.get(f.message) ?? byMessage.set(f.message, []).get(f.message)!).push(f);
    const autos = list.filter((f) => f.handler === "auto").length;
    groups.push({
      check,
      severity: list.reduce((worst, f) => (rank(f.severity) < rank(worst) ? f.severity : worst), "info"),
      count: list.length,
      handler: autos === list.length ? "auto" : autos === 0 ? "doctor" : "mixed",
      messages: [...byMessage]
        .map(([message, items]) => ({ message, findings: items }))
        .sort((a, b) => b.findings.length - a.findings.length || a.message.localeCompare(b.message)),
    });
  }
  return groups.sort(
    (a, b) => rank(a.severity) - rank(b.severity) || b.count - a.count || a.check.localeCompare(b.check),
  );
}

function rank(severity: string): number {
  return RANK[severity] ?? 3;
}

/** What a check finds, in words; the check's own name for the rest. */
const LABELS: Record<string, string> = {
  unknown_type: "Pages with a type the KB does not declare",
  broken_link: "Broken links",
  broken_relation: "Fields pointing to a missing page",
  orphan: "Pages nothing links to",
  island: "Groups of pages cut off from the rest",
  junk_file: "Junk files",
  machine_path: "Paths of one machine in the text",
  nonstandard_field: "Non-standard field names",
  tool_param_field: "Tool parameters saved as fields",
  stringified_list: "Lists written as text",
  title_h1_mismatch: "Title and heading disagree",
  missing_title: "Pages without a title",
  duplicate_link: "Duplicate links",
  repeated_link: "The same link repeated in the text",
  reciprocal_link_item: "Back-links the graph already has",
  bare_link_list: "Link lists without a word of context",
  link_to_retired: "Links to retired pages",
  index_lists_retired: "Indexes listing retired pages",
  index_incomplete: "Indexes missing pages",
  map_without_templates: "Maps without templates",
  map_misfit: "Pages that fit another map better",
  title_quality: "Titles to tighten",
  missing_value_contract: "Fields without a vocabulary",
  facet_sprawl: "Too many spellings of one facet",
  concept_oversize: "Oversized pages",
  missing_registry: "No placeholder registry",
  skill_git_command: "Skills that run git commands",
  stale_claim: "Claims past their date",
  stale_open: "Open work untouched for a long time",
  invalid_field_value: "Field values outside the vocabulary",
  missing_required_field: "Required fields missing",
  missing_frontmatter: "Pages without frontmatter",
  unparseable_frontmatter: "Frontmatter that does not parse",
  missing_type: "Pages without a type",
  empty_concept: "Empty pages",
  nonslug_file_name: "File names to normalise",
  value_case_variant: "Values spelled in different cases",
  unmapped_folder: "Folders that are not a map",
  prose_value: "Sentences where a value belongs",
  malformed_frontmatter: "Frontmatter written loosely",
  status_semantics: "Statuses the map does not use",
  mangled_placeholder: "Placeholders written out in words",
  forbidden_field: "Fields the map forbids",
  closed_with_open_items: "Closed work with open items",
  open_marker: "TODO markers left in the text",
  imported_draft: "Imported pages still drafts",
  secrets_on_non_service: "Secrets on a page that is no service",
  sops_format_mismatch: "Secret files in the wrong format",
  sops_missing_file: "Secret files that do not exist",
  legacy_path: "Paths in an old layout",
  template_section_missing: "Template sections missing",
  template_missing: "Pages without a template",
  template_unknown: "Templates that do not exist",
  template_not_allowed: "Templates the map does not allow",
  template_type_mismatch: "Type and template disagree",
  template_field_missing: "Template fields missing",
  template_field_value: "Template field values not allowed",
  template_extra_section: "Sections the template does not have",
  template_section_alias: "Sections under another name",
  template_section_order: "Sections out of order",
  concept_too_deep: "Pages nested too deep",
  index_link_form: "Index links in an odd form",
  unknown_placeholder: "Placeholders nobody declared",
  forbidden_term: "Terms the map forbids",
  source_uncited: "Sources nothing cites",
  cut_concept: "Pages the graph hangs on",
  contract_malformed: "Glossary that does not parse",
  map_oversize: "Oversized maps",
  legacy_archive_descriptor: "Archives in the old format",
  index_stale: "Generated indexes out of date",
  expanded_missing_index: "Expanded pages without an index",
  expanded_ambiguous: "Pages written twice (file and folder)",
  expanded_as_category: "Folders used as categories",
  unlistable_assets: "Asset folders that cannot be read",
  oversized_asset: "Oversized assets",
  orphan_asset: "Assets nothing uses",
  stray_file: "Stray files among the pages",
  skill_invalid: "Invalid skills",
  skill_warning: "Skill warnings",
  legacy_tool_name: "Old tool names in artifacts",
  skill_broken_ref: "Skills pointing at nothing",
  skill_missing_perimeter: "Instructions without a perimeter",
  cross_kb_path: "Artifacts reaching into another KB",
  artifact_unused: "Artifacts no agent uses",
  junk_asset: "Junk assets",
  missing_instructions: "No instructions for agents",
  hook_invalid: "Invalid hooks",
  unused_placeholder: "Placeholders nobody uses",
  lint_ignore_invalid: "Exemptions that exempt nothing",
};

/**
 * What a check looks for, in one plain sentence: the Checks tab shows it under
 * the label, so the catalogue explains itself without a trip to the docs. A
 * check missing here shows its label alone.
 */
const HELP: Record<string, string> = {
  // Pages
  malformed_frontmatter: "Frontmatter YAML that parses only by luck: bad indentation, tabs, unquoted colons.",
  stringified_list: 'A list field written as one string ("a, b") instead of a YAML list.',
  stale_claim: "A page whose review date has passed: what it says may no longer be true.",
  status_semantics: 'An "active"-like status on a page whose map treats that status as open work.',
  machine_path: "A path that exists only on one machine (~/…, /Users/…): use a {{repo:…}} or {{path:…}} placeholder.",
  mangled_placeholder: "A placeholder an editor rewrote in words, so it no longer resolves.",
  missing_title: "A page with no title field.",
  title_h1_mismatch: "The title field and the page's # heading say different things.",
  title_quality: 'A title that is too long or carries a status word ("old", "dismissed") that will go stale.',
  missing_required_field: "A field the map's contract requires is missing.",
  invalid_field_value: "A field value outside the vocabulary the map declares.",
  forbidden_field: "A field the map's contract forbids.",
  nonstandard_field: 'A synonym of a standard field ("updated" for timestamp): tools read only the standard name.',
  tool_param_field: "A tool argument saved into the frontmatter by mistake.",
  prose_value: "A sentence where a single value belongs, e.g. in status.",
  stale_open: "Open work nobody has touched for a long time.",
  closed_with_open_items: "Work marked done that still has unchecked boxes.",
  open_marker: "TODO or FIXME left in the text.",
  repeated_link: "The same link written several times in one page.",
  missing_frontmatter: "A page without frontmatter: no type, no title, nothing the tools can read.",
  unparseable_frontmatter: "Frontmatter that is not valid YAML.",
  missing_type: "A page without a type.",
  concept_too_deep: "A page nested deeper than three folders.",
  nonslug_file_name: "A file name with spaces, capitals or accents: links to it break easily.",
  empty_concept: "A page with nothing in it: write it or retire it.",
  concept_oversize: "A page too long to read in one go: split it.",
  imported_draft: "A page brought in by an import and never reviewed.",
  secrets_on_non_service: "Secrets attached to a page that is not a service.",
  sops_format_mismatch: "An encrypted secret file whose format does not match its extension.",
  sops_missing_file: "A page cites a secret file that does not exist.",
  legacy_path: "A path written in an old KB layout.",
  // Templates
  template_section_missing: "A section the page's template expects is missing.",
  template_missing: "A page in a map with templates that follows none.",
  template_unknown: "A page names a template that does not exist.",
  template_not_allowed: "A page uses a template its map does not allow.",
  template_type_mismatch: "The page's type and its template's type disagree.",
  template_field_missing: "A field the template requires is missing.",
  template_field_value: "A field value the template does not allow.",
  template_extra_section: "A section the template does not have.",
  template_section_alias: "A template section written under another name.",
  template_section_order: "The template's sections in a different order.",
  // Links
  broken_link: "A link to a page that does not exist.",
  duplicate_link: "The same link listed twice in one list.",
  reciprocal_link_item: 'A "see also" entry the graph already shows as a back-link: one more thing to keep in sync.',
  bare_link_list: "A list of links with no word on why each one matters.",
  index_link_form: "An index link spelled in an unusual form (…/index.md).",
  orphan: "A page nothing links to and that links to nothing.",
  broken_relation: "A relation field (superseded_by, part_of…) pointing at a page that does not exist.",
  link_to_retired: "Live pages still linking to a retired one.",
  unknown_placeholder: "A {{repo:…}} or {{path:…}} placeholder not declared in paths.yaml.",
  forbidden_term: "A term the map's glossary says not to use.",
  source_uncited: "A registered source no page cites.",
  cut_concept: "A page the graph hangs on: remove it and part of the KB is cut off.",
  island: "A group of pages linked to each other but to nothing else.",
  map_misfit: "A page whose links point mostly into another map: it may belong there.",
  // Vocabularies
  unknown_type: "A type the KB does not declare.",
  value_case_variant: "One value spelled in different cases (Active, active).",
  contract_malformed: "A map's _map.md contract that does not parse.",
  facet_sprawl: "A field such as tags with so many one-off values nobody can filter on it.",
  missing_value_contract: "A field used with a few recurring values but no declared vocabulary.",
  // Maps and indexes
  map_without_templates: "A map whose pages share a shape but declare no template.",
  map_oversize: "A map with too many pages at the top level: split it by theme.",
  legacy_archive_descriptor: "An archive described in the old format.",
  index_incomplete: "A page missing from its map's index.",
  index_lists_retired: "An index still listing a retired page.",
  index_stale: "A generated index out of date.",
  expanded_missing_index: "A page expanded into a folder without its index.md.",
  expanded_ambiguous: "A page that exists both as a file and as a folder.",
  expanded_as_category: "A page folder used as a category: many children its index never links.",
  unlistable_assets: "An asset folder that cannot be read.",
  oversized_asset: "An asset above the size limit.",
  orphan_asset: "An asset no page cites.",
  unmapped_folder: "A folder of pages that is not a declared map.",
  stray_file: "A non-Markdown file among the pages.",
  // Artifacts
  skill_invalid: "A skill that fails validation and no agent can load.",
  skill_warning: "A skill that loads but breaks a recommendation.",
  legacy_tool_name: "An artifact naming an old MCP tool name.",
  skill_broken_ref: "A skill pointing at a script or file that does not exist.",
  skill_git_command: "A skill telling the agent to run git on the KB: writes go through the tools.",
  skill_missing_perimeter: "Instructions that do not say which part of the KB they govern.",
  cross_kb_path: "An artifact hard-coding another KB's location.",
  artifact_unused: "A skill or agent no client has used for a long time.",
  hook_invalid: "A hook definition that does not parse.",
  // KB files
  junk_file: "A system or editor file tracked in git (.DS_Store, backups).",
  junk_asset: "A system or editor file among the assets.",
  missing_instructions: "No instructions.md telling agents how to work on this KB.",
  missing_registry: "Placeholders are cited but paths.yaml does not exist.",
  unused_placeholder: "A placeholder declared in paths.yaml that no page uses.",
  lint_ignore_invalid: "An exemption that exempts nothing: the check it names never fires there.",
};

/**
 * What a doctor session decides about a check, as the options it weighs: the
 * agenda row shows this under the label, which already says what is wrong. A
 * check missing here falls back to what it looks for.
 */
const DECIDE: Record<string, string> = {
  unknown_type: "Declare the type in the KB, or give the pages one it already has.",
  orphan: "Link each page to what it relates to, or retire it.",
  island: "Link the group to the rest of the KB, or accept it as separate.",
  broken_link: "Point the link at the right page, or remove it.",
  broken_relation: "Point the field at the right page, or clear it.",
  index_incomplete: "Add the page to its map's index, or let the server keep the index.",
  index_lists_retired: "Drop the retired page from the index.",
  link_to_retired: "Point the sentence at what replaced the page, or keep it where the mention is history.",
  bare_link_list: "Say in a few words why each link matters, or accept the list as it is.",
  reciprocal_link_item: "Drop the entry and rely on the back-link, or keep both in sync.",
  map_misfit: "Move the page to the map it links to, or keep it where it is.",
  missing_value_contract: "Declare the field's vocabulary, or leave it free.",
  facet_sprawl: "Agree a short vocabulary for the field, or stop using it.",
  title_quality: "Shorten the title or drop the status word, or keep it as it is.",
  concept_oversize: "Split the page, or accept its length.",
  machine_path: "Replace the path with a placeholder, or accept it if it is not on this machine.",
  junk_file: "Remove the file from git.",
  junk_asset: "Remove the file.",
  missing_registry: "Create paths.yaml declaring the placeholders the pages cite.",
  skill_git_command: "Rewrite the step to use the tools, or accept it if it targets another repository.",
  stale_claim: "Check the page is still true, then set a new review date.",
  stale_open: "Close the work, update it, or say who it waits on.",
  closed_with_open_items: "Tick or move the open boxes, or reopen the work.",
  open_marker: "Do the TODO, or turn it into a work item.",
  empty_concept: "Write the page, or retire it.",
  imported_draft: "Review the page and mark it ready, or retire it.",
  status_semantics: "Set the status the map uses for that state.",
  value_case_variant: "Pick one spelling for the value.",
  stray_file: "Attach the file to the page it belongs to, or remove it.",
  unmapped_folder: "Make the folder a map, or move its pages into one.",
  map_oversize: "Split the map by theme.",
  expanded_as_category: "Link the children from the page's index, or move them to a map.",
  cut_concept: "Add a second path to the pages that hang on it.",
  source_uncited: "Cite the source where it is used, or drop it.",
  forbidden_term: "Use the term the glossary prefers.",
  template_missing: "Bind the page to its map's template, or keep it free-form.",
  template_section_missing: "Fill the section from the sources, or record what is missing.",
  template_extra_section: "Move the section's text into a template section or another page.",
  artifact_unused: "Remove the skill or agent, or keep it for later.",
};

/** How a check's findings get fixed, as the options a doctor session weighs; empty when unknown. */
export function checkFix(check: string): string {
  return DECIDE[check] ?? "";
}

/** One sentence on what a check looks for; empty when there is none. */
export function checkHelp(check: string): string {
  return HELP[check] ?? "";
}

export function checkLabel(check: string): string {
  if (LABELS[check]) return LABELS[check];
  const words = check.replace(/_/g, " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/** What the trend line says: the total at the start and end of the window, and the daily totals between. */
export interface Trend {
  from: number;
  to: number;
  /** "this week" when the window covers the last 7 days, else "over N days". */
  span: string;
  points: number[];
}

/**
 * The trend of the lint totals (D370): samples of the last 7 days, or all of
 * them when the history is younger than a week. Null with fewer than two.
 */
export function trendOf(history: { at: string; total: number }[], now: number): Trend | null {
  const week = 7 * 86_400_000;
  const win = history.filter((s) => now - Date.parse(s.at) <= week);
  if (win.length < 2) return null;
  const first = win[0]!;
  const days = Math.max(1, Math.round((Date.parse(win[win.length - 1]!.at) - Date.parse(first.at)) / 86_400_000));
  return {
    from: first.total,
    to: win[win.length - 1]!.total,
    span: days >= 6 ? "this week" : `over ${days} ${days === 1 ? "day" : "days"}`,
    points: win.map((s) => s.total),
  };
}

/** Whether a finding's path lies in the Map (the server's own scope rule: the folder or beneath it). */
export function inMap(f: { path: string }, map: string): boolean {
  return f.path === map || f.path.startsWith(`${map}/`);
}

/** The report narrowed to one Map in the page, totals recomputed; null map = whole report. */
export function filterReportByMap(report: LintReport | null, map: string | null): LintReport | null {
  if (!report || !map) return report;
  const findings = report.findings.filter((f) => inMap(f, map));
  const by_severity: Record<string, number> = {};
  const by_check: Record<string, number> = {};
  for (const f of findings) {
    by_severity[f.severity] = (by_severity[f.severity] ?? 0) + 1;
    by_check[f.check] = (by_check[f.check] ?? 0) + 1;
  }
  return { ...report, findings, count: findings.length, total: findings.length, by_severity, by_check };
}

/** One kind of kb_review proposal on the Health agenda (D704). */
export interface ReviewGroup {
  kind: string;
  items: ReviewItem[];
}

/**
 * The review items by kind, heaviest kind first; a Map keeps the items that
 * touch one of its pages. lint_judgement items never arrive: the agenda lists
 * them by check.
 */
export function groupReview(items: ReviewItem[], map: string | null): ReviewGroup[] {
  const byKind = new Map<string, ReviewItem[]>();
  for (const it of items) {
    if (map && !it.concepts.some((c) => inMap({ path: c }, map))) continue;
    (byKind.get(it.kind) ?? byKind.set(it.kind, []).get(it.kind)!).push(it);
  }
  return [...byKind]
    .map(([kind, list]) => ({ kind, items: list.sort((a, b) => b.weight - a.weight) }))
    .sort((a, b) => b.items.length - a.items.length || a.kind.localeCompare(b.kind));
}

/** A proposal kind in words: what it is, how to act on it, and what one item is called. */
const REVIEW: Record<string, { label: string; help: string; fix: string; unit: [string, string] }> = {
  template_proposal: {
    label: "Templates to adopt",
    help: "Pages in a map already share a structure that no template declares.",
    fix: "Adopt it as the map's template, so new pages follow it, or keep the map free-form.",
    unit: ["template", "templates"],
  },
  glossary_gap: {
    label: "Terms nobody defines",
    help: "A word many pages use and none explains.",
    fix: "Define it once in the glossary, or accept it as common knowledge.",
    unit: ["term", "terms"],
  },
  repeated_fact: {
    label: "Facts copied into many pages",
    help: "The same line, word for word, in several pages.",
    fix: "Keep it in the page it is about and link to it from the others.",
    unit: ["fact", "facts"],
  },
  zombie_work: {
    label: "Open work tied to retired pages",
    help: "Work still open that depends on something already retired.",
    fix: "Close it, or point it at what replaced the retired page.",
    unit: ["item", "items"],
  },
  duplicate_candidate: {
    label: "Possible duplicates",
    help: "Two pages of one map with nearly the same title.",
    fix: "Merge them, or link each to the other saying how they differ.",
    unit: ["pair", "pairs"],
  },
  map_naming: {
    label: "Map names to align",
    help: "Map titles mix languages, shapes or capitals.",
    fix: "Agree one naming scheme and rename the maps' titles to it.",
    unit: ["scheme", "schemes"],
  },
  harvest_candidate: {
    label: "Old journal entries to archive",
    help: "Closed entries past the age their journal keeps them live.",
    fix: "Move their lasting lessons to the live pages, then archive them.",
    unit: ["entry", "entries"],
  },
  status_reclassify: {
    label: "Statuses to correct",
    help: "Pages marked active that read as something else.",
    fix: "Set the status the page really has.",
    unit: ["page", "pages"],
  },
  read_hotspot: {
    label: "Pages read too often whole",
    help: "Long pages agents keep reading end to end.",
    fix: "Split them, or turn them into an index of shorter pages.",
    unit: ["page", "pages"],
  },
  promotion_candidate: {
    label: "Procedures hidden in journals",
    help: "A journal entry that holds a step-by-step procedure.",
    fix: "Give the procedure a page of its own and link it from the entry.",
    unit: ["entry", "entries"],
  },
  scattered_work: {
    label: "Work scattered in checklists",
    help: "Open checkboxes spread across pages.",
    fix: "Turn them into work items in the map that tracks work.",
    unit: ["page", "pages"],
  },
};

/** Every proposal kind the Atlas can name, in reading order. */
export function reviewKinds(): string[] {
  return Object.keys(REVIEW);
}

export function reviewInfo(kind: string): { label: string; help: string; fix: string; unit: [string, string] } {
  return (
    REVIEW[kind] ?? {
      label: checkLabel(kind),
      help: "",
      fix: "Your agent proposes what to do.",
      unit: ["item", "items"],
    }
  );
}
