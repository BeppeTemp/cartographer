import type { LintFinding } from "../api/types";

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
  return groups.sort((a, b) => rank(a.severity) - rank(b.severity) || b.count - a.count || a.check.localeCompare(b.check));
}

function rank(severity: string): number {
  return RANK[severity] ?? 3;
}

/** What a check finds, in words; the check's own name for the rest. */
const LABELS: Record<string, string> = {
  unknown_type: "Pages with a type the KB does not declare",
  broken_link: "Broken links",
  broken_relation: "Relations to a missing concept",
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

export function checkLabel(check: string): string {
  if (LABELS[check]) return LABELS[check];
  const words = check.replace(/_/g, " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}
