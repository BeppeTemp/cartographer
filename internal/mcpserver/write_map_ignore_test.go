package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mapIgnoreMD accepts, for the whole map, a stale claim and a synonym field.
// missing_required_field is an error a map cannot accept: lint reports the name
// on the _map.md, a write must not repeat that.
const mapIgnoreMD = "---\ntype: Map\nkind: map\ntitle: Ops\nrequired_fields: [owner]\nlint_ignore: [stale_claim, nonstandard_field, missing_required_field]\n---\n# Ops\n"

// mapIgnoreChecks collects the checks of a findings array, and whether one of
// them is on a map descriptor.
func mapIgnoreChecks(findings any) (checks map[string]bool, onMap bool) {
	checks = map[string]bool{}
	list, _ := findings.([]any)
	for _, f := range list {
		m := f.(map[string]any)
		checks[m["check"].(string)] = true
		if strings.HasSuffix(m["path"].(string), "_map.md") {
			onMap = true
		}
	}
	return checks, onMap
}

// A map's lint_ignore (D306) takes effect on the write path as it does in
// lint: the findings of concept_write/patch/batch and gate_check changed_ids
// leave out what the map accepts, keep what it cannot accept and what it did
// not name, and never repeat lint_ignore_invalid.
func TestWritePathAppliesMapLintIgnore(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, nil)
	k := s.kbRef
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "ops", "_map.md"), []byte(mapIgnoreMD), 0o644); err != nil {
		t.Fatal(err)
	}
	// Frontmatter: a stale review date and a synonym (both accepted by the map),
	// no owner (an error), no title (a warning the map did not name).
	const fm = `{"type":"Note","review_after":"2000-01-01","aggiornato":"2026-01-01"}`

	assertWrite := func(label string, findings any, wantKept bool) {
		t.Helper()
		got, onMap := mapIgnoreChecks(findings)
		for _, accepted := range []string{"stale_claim", "nonstandard_field"} {
			if got[accepted] {
				t.Errorf("%s: findings carry %s, which the map accepts: %v", label, accepted, findings)
			}
		}
		if onMap || got["lint_ignore_invalid"] {
			t.Errorf("%s: findings repeat what lint reports on the _map.md: %v", label, findings)
		}
		if wantKept && (!got["missing_required_field"] || !got["missing_title"]) {
			t.Errorf("%s: an error or an unnamed check was suppressed: %v", label, findings)
		}
	}

	write := decodeJSON(t, mustText(t, s, "concept_write", `{"id":"ops/x","frontmatter":`+fm+`,"body":"b"}`))
	assertWrite("concept_write", write["findings"], true)

	// lint is the reference: the same suppression, and it alone reports the
	// name the map cannot accept.
	lintOut := decodeJSON(t, mustText(t, s, "lint", `{"scope":"ops"}`))
	got, onMap := mapIgnoreChecks(lintOut["findings"])
	if got["stale_claim"] || got["nonstandard_field"] || !got["lint_ignore_invalid"] || !onMap {
		t.Fatalf("lint reference changed: %v", lintOut["findings"])
	}

	patch := decodeJSON(t, mustText(t, s, "concept_patch",
		`{"id":"ops/x","if_match":"`+write["content_hash"].(string)+`","frontmatter":{"review_after":"1999-01-01"}}`))
	assertWrite("concept_patch", patch["findings"], true)

	// A batch refuses a missing required field up front, so its page has an owner.
	const fmOwned = `{"type":"Note","title":"Y","owner":"me","review_after":"2000-01-01","aggiornato":"2026-01-01"}`
	batch := decodeJSON(t, mustText(t, s, "concept_batch",
		`{"operations":[{"op":"write","id":"ops/y","frontmatter":`+fmOwned+`,"body":"b"}]}`))
	assertWrite("concept_batch", batch["results"].([]any)[0].(map[string]any)["findings"], false)

	gate := decodeJSON(t, mustText(t, s, "gate_check", `{"changed_ids":["ops/x"]}`))
	assertWrite("gate_check changed_ids", gate["lint_findings"], true)

	// The same page outside a map that accepts it is still reported.
	if err := k.CreateMap("plain", "Plain", "map", nil, ""); err != nil {
		t.Fatal(err)
	}
	control := decodeJSON(t, mustText(t, s, "concept_write", `{"id":"plain/x","frontmatter":`+fm+`,"body":"b"}`))
	if got, _ := mapIgnoreChecks(control["findings"]); !got["stale_claim"] || !got["nonstandard_field"] {
		t.Errorf("control: a map without lint_ignore lost its findings: %v", control["findings"])
	}
}

// write_gate refuses a write for the findings it introduces: one the map
// accepts is not introduced, one in a map that does not accept it is.
func TestWriteGateHonoursMapLintIgnore(t *testing.T) {
	t.Parallel()
	k, _ := gateKB(t, "warning")
	s := gateSeed(t, k, map[string]string{
		"ops/_map":  "---\ntype: Map\nkind: map\ntitle: Ops\nlint_ignore: [stale_claim]\n---\n# Ops\n",
		"arch/_map": "---\ntype: Map\nkind: map\ntitle: Arch\n---\n# Arch\n",
		"ops/seed":  "---\ntype: Note\ntitle: Seed\n---\n# Seed\n",
		"arch/seed": "---\ntype: Note\ntitle: Seed\n---\n# Seed\n",
	})
	// Linked, so that orphan is not what the gate sees.
	const args = `,"frontmatter":{"type":"Note","title":"X","review_after":"2000-01-01"},"body":"See [seed](seed.md)."}`

	if text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"id":"ops/x"`+args); isErr {
		t.Fatalf("a finding the map accepts refused the write: %s", text)
	}
	if _, err := k.ReadConcept("ops/x"); err != nil {
		t.Fatalf("write did not persist: %v", err)
	}

	text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"id":"arch/x"`+args)
	if !isErr || !strings.Contains(text, "write_gate (warning): refused") || !strings.Contains(text, "stale_claim") {
		t.Fatalf("control: the same write in a map without lint_ignore = %q (error=%v)", text, isErr)
	}
	gateClean(t, k)
}
