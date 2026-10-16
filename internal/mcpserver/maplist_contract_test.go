package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"required_fields: [owner, status]\nrequired_fields.Runbook: [severity]\nforbidden_fields: [aggiornato]\n"+
		"require_index_entry: true\ntemplates: [runbook, note]\ndefault_template: runbook\ntemplate_sections: true\n"+
		"open_field: phase\nopen_statuses: [draft, doing]\nopen_markers: [TODO]\nwork_map: full-journal\nglossary: true\n"+
		"index: generated\nvalue_synonyms.done: [closed, finished]\nmachine_path_allow_prefixes: [/opt/app]\n"+
		"repeated_fact_min: 4\nhotspot_in_degree: 12\nhotspot_bytes: 9000\noversize_bytes: 50000\noversize_concepts: 80\n"+
		"title_max_length: 0\nforbidden_title_terms: [wip, tmp]\n---\n# Full\n")
	writeMapDescriptor(t, root, "full-journal", "---\ntype: Map\nkind: journal\ntitle: Journal\nharvest_after: 30\npromote_to: full-map\n"+
		"procedure_headings: [Runbook, Fix]\n---\n# Journal\n")
	writeMapDescriptor(t, root, "default-journal", "---\ntype: Map\nkind: journal\ntitle: Default\n---\n# Default\n")
	writeMapDescriptor(t, root, "off-journal", "---\ntype: Map\nkind: journal\ntitle: Off\nharvest_after: 0\n---\n# Off\n")
	writeMapDescriptor(t, root, "optout-map", "---\ntype: Map\nkind: map\ntitle: Opt out\ntemplates: [runbook]\nrequire_template: false\n---\n# Opt out\n")
	writeMapDescriptor(t, root, "broken-map", "---\ntype: Map\nkind: map\ntitle: Broken\nstale_after: soon\nhotspot_bytes: many\n---\n# Broken\n")

	entries := mapListEntries(t, s)

	var full struct {
		Description          string              `json:"description"`
		RequiredFields       []string            `json:"required_fields"`
		RequiredFieldsByType map[string][]string `json:"required_fields_by_type"`
		ForbiddenFields      []string            `json:"forbidden_fields"`
		RequireIndexEntry    bool                `json:"require_index_entry"`
		Templates            []string            `json:"templates"`
		DefaultTemplate      string              `json:"default_template"`
		RequireTemplate      bool                `json:"require_template"`
		TemplateSections     bool                `json:"template_sections"`
		OpenField            string              `json:"open_field"`
		OpenStatuses         []string            `json:"open_statuses"`
		OpenMarkers          []string            `json:"open_markers"`
		WorkMap              string              `json:"work_map"`
		Glossary             bool                `json:"glossary"`
		Index                string              `json:"index"`
		ValueSynonyms        map[string][]string `json:"value_synonyms"`
		MachinePathPrefixes  []string            `json:"machine_path_allow_prefixes"`
		Thresholds           struct {
			RepeatedFactMin     int      `json:"repeated_fact_min"`
			HotspotInDegree     int      `json:"hotspot_in_degree"`
			HotspotBytes        int      `json:"hotspot_bytes"`
			OversizeBytes       int      `json:"oversize_bytes"`
			OversizeConcepts    int      `json:"oversize_concepts"`
			TitleMaxLength      *int     `json:"title_max_length"`
			ForbiddenTitleTerms []string `json:"forbidden_title_terms"`
		} `json:"thresholds"`
	}
	raw, _ := json.Marshal(entries["full-map"])
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatal(err)
	}
	join := func(v []string) string { return strings.Join(v, ",") }
	if full.Description != "Everything the contract can say." ||
		join(full.RequiredFields) != "owner,status" || join(full.RequiredFieldsByType["Runbook"]) != "severity" ||
		join(full.ForbiddenFields) != "aggiornato" || !full.RequireIndexEntry {
		t.Errorf("description / field rules: %+v", full)
	}
	if join(full.Templates) != "note,runbook" || full.DefaultTemplate != "runbook" || !full.RequireTemplate || !full.TemplateSections {
		t.Errorf("templates: %+v", full)
	}
	if full.OpenField != "phase" || join(full.OpenStatuses) != "draft,doing" || join(full.OpenMarkers) != "TODO" ||
		full.WorkMap != "full-journal" || !full.Glossary || full.Index != "generated" {
		t.Errorf("lifecycle / review: %+v", full)
	}
	if join(full.ValueSynonyms["done"]) != "closed,finished" || join(full.MachinePathPrefixes) != "/opt/app" {
		t.Errorf("synonyms / prefixes: %+v", full)
	}
	th := full.Thresholds
	if th.RepeatedFactMin != 4 || th.HotspotInDegree != 12 || th.HotspotBytes != 9000 || th.OversizeBytes != 50000 ||
		th.OversizeConcepts != 80 || th.TitleMaxLength == nil || *th.TitleMaxLength != 0 || join(th.ForbiddenTitleTerms) != "wip,tmp" {
		t.Errorf("thresholds (an explicit title_max_length: 0 must survive): %+v", th)
	}
	for _, key := range []string{"harvest_after", "harvest_after_defaulted", "harvest_off", "promote_to", "procedure_headings", "malformed"} {
		if _, ok := entries["full-map"][key]; ok {
			t.Errorf("a map that is not a journal, or sets no %s, carries it: %s", key, entries["full-map"][key])
		}
	}

	// A journal: the explicit value, the default, and the off switch.
	harvest := func(name string) (days int, defaulted, off bool) {
		e := entries[name]
		_ = json.Unmarshal(e["harvest_after"], &days)
		_ = json.Unmarshal(e["harvest_after_defaulted"], &defaulted)
		_ = json.Unmarshal(e["harvest_off"], &off)
		return days, defaulted, off
	}
	if d, def, off := harvest("full-journal"); d != 30 || def || off {
		t.Errorf("explicit harvest_after: %d %v %v", d, def, off)
	}
	if d, def, off := harvest("default-journal"); d != 45 || !def || off {
		t.Errorf("default harvest_after: %d %v %v", d, def, off)
	}
	if d, def, off := harvest("off-journal"); d != 0 || def || !off {
		t.Errorf("harvest_after: 0 is off: %d %v %v", d, def, off)
	}
	if _, ok := entries["off-journal"]["harvest_after"]; ok {
		t.Errorf("an off journal must omit harvest_after: %s", entries["off-journal"]["harvest_after"])
	}
	var promote string
	var headings []string
	_ = json.Unmarshal(entries["full-journal"]["promote_to"], &promote)
	_ = json.Unmarshal(entries["full-journal"]["procedure_headings"], &headings)
	if promote != "full-map" || join(headings) != "Runbook,Fix" {
		t.Errorf("review keys: %q %v", promote, headings)
	}

	// require_template is the effective value: require_template: false on a map
	// that declares templates is the opt-out (D371), so it is omitted.
	if _, ok := entries["optout-map"]["require_template"]; ok {
		t.Errorf("opt-out map reports require_template: %s", entries["optout-map"]["require_template"])
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
	if !keys["stale_after"] || !keys["hotspot_bytes"] || len(malformed) != 2 {
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
		"description", "required_fields", "required_fields_by_type", "forbidden_fields", "require_index_entry",
		"templates", "default_template", "require_template", "template_sections", "open_field", "open_statuses",
		"open_markers", "harvest_after", "harvest_after_defaulted", "harvest_off", "promote_to", "procedure_headings",
		"glossary", "work_map", "index", "value_synonyms", "machine_path_allow_prefixes", "thresholds", "malformed",
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
