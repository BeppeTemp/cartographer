package lint

import (
	"strings"
	"testing"
)

func requiresItems(t *testing.T, items []ReviewItem) []ReviewItem {
	t.Helper()
	var out []ReviewItem
	for _, it := range itemsOf(items, ReviewObservation) {
		if strings.HasPrefix(it.Check, "requires_") {
			out = append(out, it)
		}
	}
	return out
}

// An artifact observation names a file, never a concept; artifacts are
// whole-KB resources, so a partial-scope filter drops it and a whole one keeps it.
func TestRequiresObservationItemsNameTheArtifact(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"requires_unresolved", "requires_undeclared"} {
		k, path := obsFixtures[name](t)
		var got *ReviewItem
		for _, it := range requiresItems(t, review(t, k)) {
			if it.Check == name {
				it := it
				got = &it
			}
		}
		if got == nil {
			t.Fatalf("%s: no item", name)
		}
		if got.Artifact != path || len(got.Concepts) != 0 || !got.wholeGraph {
			t.Errorf("%s: item = %+v", name, got)
		}
		if kept := FilterReview([]ReviewItem{*got}, func(string) bool { return true }, false); len(kept) != 0 {
			t.Errorf("%s: a partial-scope caller received an artifact item", name)
		}
		if kept := FilterReview([]ReviewItem{*got}, func(string) bool { return true }, true); len(kept) != 1 {
			t.Errorf("%s: a whole-KB caller lost an artifact item", name)
		}
	}
}

// The heuristics stay quiet where they should: bundle skills, capability ids,
// malformed entries (requires_invalid's), code fences and inline code, and a
// longer name that merely contains the agent's.
func TestRequiresObservationsStayQuiet(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "agents/audit.md", "---\nname: audit\ndescription: A\n---\nx\n")
	writeFile(t, k.Root, "skills/a/SKILL.md", "---\nname: a\ndescription: A\nrequires: [skill:cartographer-ops, skill:b, agent:audit, \"mcp:x\", hook.PreToolUse]\n---\n# A\nSee audit-log and `audit` and:\n```\naudit now\n```\n")
	writeFile(t, k.Root, "skills/b/SKILL.md", skillMD("b", "B", "# B\n"))
	if got := requiresItems(t, review(t, k)); len(got) != 0 {
		t.Fatalf("unexpected items: %+v", got)
	}
}

// Pre-feature KB (D1656): two skills, two agents, one hook, none with
// `requires`. The new detectors add nothing to its lint run or its review, so a
// KB written before the feature reads exactly as before.
func TestOldKBOutputIsUnchanged(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "skills/triage/SKILL.md", skillMD("triage", "Triage notes", "# Triage\nRead the inbox.\n"))
	writeFile(t, k.Root, "skills/report/SKILL.md", skillMD("report", "Write the report", "# Report\nAsk the explorer to gather facts, then the auditor to check them.\n"))
	writeFile(t, k.Root, "agents/explorer.md", "---\nname: explorer\ndescription: Explores\ntools: Read, Grep\n---\nExplore.\n")
	writeFile(t, k.Root, "agents/auditor.md", "---\nname: auditor\ndescription: Audits\ntools: Read\nstrict_tools: true\n---\nAudit.\n")
	writeFile(t, k.Root, "hooks/notify/hook.json", `{"event":"Notification","command":"./notify.sh"}`+"\n")
	writeFile(t, k.Root, "hooks/notify/notify.sh", "#!/bin/sh\ntrue\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if strings.Contains(f.Check, "requires") || f.Check == "degraded_name_collision" {
			t.Errorf("a pre-feature KB gained %s: %+v", f.Check, f)
		}
	}
	// The one thing the heuristic may say about it: report names two agents.
	got := requiresItems(t, review(t, k))
	if len(got) != 1 || got[0].Check != "requires_undeclared" || got[0].Artifact != "skills/report/SKILL.md" ||
		!strings.Contains(got[0].Evidence, "agent:auditor, agent:explorer") {
		t.Fatalf("items = %+v", got)
	}
}

// A skill and an agent with one name: the skill naming itself is no mention.
func TestRequiresUndeclaredIgnoresASkillNamingItself(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "agents/plan.md", "---\nname: plan\ndescription: A\n---\nx\n")
	writeFile(t, k.Root, "skills/plan/SKILL.md", skillMD("plan", "Plans", "# Plan\nThe plan skill runs the plan.\n"))
	if got := requiresItems(t, review(t, k)); len(got) != 0 {
		t.Fatalf("unexpected items: %+v", got)
	}
}
