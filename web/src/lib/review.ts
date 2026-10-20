import { checkLabel } from "./health";

/**
 * The kinds of kb_review proposal in words (D948): they are decisions, not
 * problems, so Work shows them under "to decide" and Health never does.
 */
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
  observation: {
    label: "Shapes to look at",
    help: "A state of the KB a maintainer may look at and leave as it is.",
    fix: "Decide whether it needs a change; the item says what would close it.",
    unit: ["observation", "observations"],
  },
  scattered_work: {
    label: "Work scattered in checklists",
    help: "Open checkboxes spread across pages.",
    fix: "Turn them into work items in the map that tracks work.",
    unit: ["page", "pages"],
  },
};

/** A proposal kind's label, help, action and unit; an unknown kind falls back to its name. */
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
