package kb

import "strings"

// The ingest pipeline's data model (D1201): a campaign is one IngestPlan
// concept and each unit of work one IngestUnit concept, so a campaign can be
// resumed, extended and executed by several agents at once. The server never
// calls a model or fetches anything; it only keeps the shape.

// IngestUnitStatuses is the vocabulary of an IngestUnit's status. draft is
// written by the planner before the operator's checkpoint and is never queued,
// since work_list lists it only where open_statuses names it.
var IngestUnitStatuses = []string{"draft", "open", "in-progress", "done", "skipped", "blocked"}

// IngestExecutors is the vocabulary of an IngestUnit's executor.
var IngestExecutors = []string{"any", "large", "human"}

// IngestPlanStatuses is the vocabulary of an IngestPlan's status.
var IngestPlanStatuses = []string{"open", "in-progress", "done"}

// IngestPlanTemplate is the text of templates/ingest-plan.md. No H2 is
// mandatory; the body only names what the planner writes.
func IngestPlanTemplate() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("type: " + IngestPlanType + "\n")
	b.WriteString("title: \"{{title}}\"\n")
	b.WriteString(TemplateMetaDescription + ": One ingest campaign: the layout, ownership and rules its units follow\n")
	b.WriteString(TemplateMetaRequiredFields + ": [status]\n")
	b.WriteString(TemplateMetaOptionalFields + ": [sources, maps, timestamp]\n")
	b.WriteString(TemplateMetaFieldValues + "status: [" + strings.Join(IngestPlanStatuses, ", ") + "]\n")
	b.WriteString(TemplateMetaOpenSections + ": true\n")
	b.WriteString("---\n# {{title}}\n\n")
	b.WriteString("Write here: the Map layout, the subject-ownership table (subject -> the one concept that owns it) and the extraction rules.\n")
	return b.String()
}

// IngestUnitTemplate is the text of templates/ingest-unit.md. extract and
// verify are one line; the body is the unit's self-contained brief.
func IngestUnitTemplate() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("type: " + IngestUnitType + "\n")
	b.WriteString("title: \"{{title}}\"\n")
	b.WriteString(TemplateMetaDescription + ": One claimable unit of ingest work: a source, the concepts it feeds, what to take and how to check it\n")
	b.WriteString(TemplateMetaRequiredFields + ": [source, targets, extract, verify, executor, status]\n")
	b.WriteString(TemplateMetaOptionalFields + ": [plan, claimed_by, claimed_at, review_after, waiting_on, timestamp, content_asset]\n")
	b.WriteString(TemplateMetaFieldValues + "status: [" + strings.Join(IngestUnitStatuses, ", ") + "]\n")
	b.WriteString(TemplateMetaFieldValues + "executor: [" + strings.Join(IngestExecutors, ", ") + "]\n")
	b.WriteString(TemplateMetaOpenSections + ": true\n")
	b.WriteString("---\n# {{title}}\n\n")
	b.WriteString("Write here the brief an executor needs without reading the plan.\n")
	return b.String()
}
