package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// mapListEntries runs map_list and decodes every entry as a raw key set, so a
// test can tell an omitted key from a zero value.
func mapListEntries(t *testing.T, s *Server) map[string]map[string]json.RawMessage {
	t.Helper()
	var infos []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(callOK(t, s, "map_list", `{}`)), &infos); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]json.RawMessage{}
	for _, info := range infos {
		var name string
		if err := json.Unmarshal(info["name"], &name); err != nil {
			t.Fatal(err)
		}
		out[name] = info
	}
	return out
}

func writeMapDescriptor(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "_map.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMapListCarriesTheWholeContract: map_list returns every key of _map.md, the
// effective value where the server derives one, and nothing for a key that is
// unset.
func TestMapListCarriesTheWholeContract(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	root := k.DataRoot()

	writeMapDescriptor(t, root, "full-map", "---\ntype: Map\nkind: map\ntitle: Full\ndescription: Everything the contract can say.\n"+
		"required_fields.*: [owner, status]\nrequired_fields.Runbook: [severity]\nforbidden_fields: [aggiornato]\n"+
		"templates: [runbook, note]\ndefault_template: runbook\n"+
		"open_field: phase\nopen_statuses: [draft, doing]\nopen_markers: [TODO]\nwork_map: full-journal\nglossary: true\n"+
		"index: generated\nvalue_synonyms.done: [closed, finished]\n"+
		"forbidden_title_terms: [wip, tmp]\n---\n# Full\n")
	writeMapDescriptor(t, root, "full-journal", "---\ntype: Map\nkind: journal\ntitle: Journal\nharvest_after: 30\npromote_to: full-map\n"+
		"procedure_headings: [Runbook, Fix]\n---\n# Journal\n")
	writeMapDescriptor(t, root, "default-journal", "---\ntype: Map\nkind: journal\ntitle: Default\n---\n# Default\n")
	writeMapDescriptor(t, root, "legacy-journal", "---\ntype: Map\nkind: journal\ntitle: Legacy\nharvest_after: 0\nrequire_index_entry: true\nhotspot_bytes: 9000\n---\n# Legacy\n")
	writeMapDescriptor(t, root, "broken-map", "---\ntype: Map\nkind: map\ntitle: Broken\nstale_after: soon\nharvest_after: soon\n---\n# Broken\n")

	entries := mapListEntries(t, s)

	var full struct {
		Description         string              `json:"description"`
		RequiredFields      map[string][]string `json:"required_fields"`
		ForbiddenFields     []string            `json:"forbidden_fields"`
		Templates           []string            `json:"templates"`
		DefaultTemplate     string              `json:"default_template"`
		OpenField           string              `json:"open_field"`
		OpenStatuses        []string            `json:"open_statuses"`
		OpenMarkers         []string            `json:"open_markers"`
		WorkMap             string              `json:"work_map"`
		Glossary            bool                `json:"glossary"`
		Index               string              `json:"index"`
		ValueSynonyms       map[string][]string `json:"value_synonyms"`
		ForbiddenTitleTerms []string            `json:"forbidden_title_terms"`
	}
	raw, _ := json.Marshal(entries["full-map"])
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatal(err)
	}
	join := func(v []string) string { return strings.Join(v, ",") }
	if full.Description != "Everything the contract can say." ||
		join(full.RequiredFields[kb.AllTypes]) != "owner,status" || join(full.RequiredFields["Runbook"]) != "severity" ||
		join(full.ForbiddenFields) != "aggiornato" {
		t.Errorf("description / field rules: %+v", full)
	}
	if join(full.Templates) != "note,runbook" || full.DefaultTemplate != "runbook" {
		t.Errorf("templates: %+v", full)
	}
	if full.OpenField != "phase" || join(full.OpenStatuses) != "draft,doing" || join(full.OpenMarkers) != "TODO" ||
		full.WorkMap != "full-journal" || !full.Glossary || full.Index != "generated" {
		t.Errorf("lifecycle / review: %+v", full)
	}
	if join(full.ValueSynonyms["done"]) != "closed,finished" || join(full.ForbiddenTitleTerms) != "wip,tmp" {
		t.Errorf("synonyms / title terms: %+v", full)
	}
	for _, key := range []string{"harvest_after", "harvest_after_defaulted", "promote_to", "procedure_headings", "malformed", "legacy_keys"} {
		if _, ok := entries["full-map"][key]; ok {
			t.Errorf("a map that is not a journal, or sets no %s, carries it: %s", key, entries["full-map"][key])
		}
	}

	// A journal: the explicit value and the default (there is no off switch).
	harvest := func(name string) (days int, defaulted bool) {
		e := entries[name]
		_ = json.Unmarshal(e["harvest_after"], &days)
		_ = json.Unmarshal(e["harvest_after_defaulted"], &defaulted)
		return days, defaulted
	}
	if d, def := harvest("full-journal"); d != 30 || def {
		t.Errorf("explicit harvest_after: %d %v", d, def)
	}
	if d, def := harvest("default-journal"); d != 45 || !def {
		t.Errorf("default harvest_after: %d %v", d, def)
	}
	if d, def := harvest("legacy-journal"); d != 45 || !def {
		t.Errorf("harvest_after: 0 means the default: %d %v", d, def)
	}
	var promote string
	var headings []string
	_ = json.Unmarshal(entries["full-journal"]["promote_to"], &promote)
	_ = json.Unmarshal(entries["full-journal"]["procedure_headings"], &headings)
	if promote != "full-map" || join(headings) != "Runbook,Fix" {
		t.Errorf("review keys: %q %v", promote, headings)
	}

	// A removed key is named, not silently dropped, and is not part of the contract.
	var legacy []string
	if err := json.Unmarshal(entries["legacy-journal"]["legacy_keys"], &legacy); err != nil {
		t.Fatalf("legacy_keys: %v (%s)", err, entries["legacy-journal"]["legacy_keys"])
	}
	if join(legacy) != "harvest_after,hotspot_bytes,require_index_entry" {
		t.Errorf("legacy_keys = %v", legacy)
	}

	// A malformed entry is named, not silently dropped.
	var malformed []struct {
		Key    string `json:"key"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(entries["broken-map"]["malformed"], &malformed); err != nil {
		t.Fatalf("malformed: %v (%s)", err, entries["broken-map"]["malformed"])
	}
	keys := map[string]bool{}
	for _, m := range malformed {
		keys[m.Key] = m.Reason != ""
	}
	if !keys["stale_after"] || !keys["harvest_after"] || len(malformed) != 2 {
		t.Errorf("malformed = %+v", malformed)
	}
}

// TestMapListMinimalMapAddsNoKey: a map whose _map.md sets no contract keeps the
// output it had before the full contract was added.
func TestMapListMinimalMapAddsNoKey(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	writeMapDescriptor(t, k.DataRoot(), "bare", "---\ntype: Map\nkind: map\ntitle: Bare\n---\n# Bare\n")

	added := []string{
		"description", "required_fields", "field_values", "forbidden_fields",
		"templates", "default_template", "open_field", "open_statuses",
		"open_markers", "harvest_after", "harvest_after_defaulted", "promote_to", "procedure_headings",
		"glossary", "work_map", "index", "value_synonyms", "forbidden_title_terms", "read_whole", "legacy_keys", "malformed",
	}
	entries := mapListEntries(t, s)
	for _, name := range []string{"bare", "manutenzione"} {
		e, ok := entries[name]
		if !ok {
			t.Fatalf("%s missing from map_list", name)
		}
		for _, key := range added {
			if v, present := e[key]; present {
				t.Errorf("%s: minimal map carries %q = %s", name, key, v)
			}
		}
	}
}
