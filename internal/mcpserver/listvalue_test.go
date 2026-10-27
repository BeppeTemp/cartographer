package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// tagsKB is a map whose contract closes the tags vocabulary (D1197).
func tagsKB(t *testing.T) (*kb.KB, *Server) {
	t.Helper()
	k, s := repairKB(t, 0)
	_, err := k.UpdateMapContract("ops", kb.MapContractUpdate{
		FieldValues:   map[string]map[string][]string{kb.AllTypes: {"tags": {"foo-bar", "alpha"}}},
		ValueSynonyms: map[string][]string{"foo-bar": {"foobar"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return k, s
}

func writeTagged(t *testing.T, k *kb.KB, id string, tags ...string) {
	t.Helper()
	fm := newFM()
	fm.Set("type", "Note")
	fm.Set("title", "T")
	fm.Set("updated", "2026-01-02")
	fm.Set("tags", tags)
	if _, err := k.WriteConcept(okf.ConceptID(id), fm, "# T\n", ""); err != nil {
		t.Fatal(err)
	}
}

func tagsOf(t *testing.T, k *kb.KB, id string) string {
	t.Helper()
	cd, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		t.Fatal(err)
	}
	return cd.FrontmatterRaw
}

func TestKBRepairListValue(t *testing.T) {
	t.Parallel()
	k, s := tagsKB(t)
	writeTagged(t, k, "ops/a", "Foo_Bar", "foobar", "junk", "alpha")
	writeTagged(t, k, "ops/gone", "junk")
	dry := repairCall(t, s, `{"check":"invalid_field_value"}`)
	if dry["dry_run"] != true || dry["planned_total"].(float64) == 0 {
		t.Fatalf("dry run = %v", dry)
	}
	repairCall(t, s, `{"check":"invalid_field_value","dry_run":false}`)
	if raw := tagsOf(t, k, "ops/a"); !strings.Contains(raw, "tags: [foo-bar, alpha]") && !strings.Contains(raw, "- foo-bar\n- alpha") {
		t.Errorf("want [foo-bar, alpha] (renamed, deduplicated, junk dropped):\n%s", raw)
	}
	if raw := tagsOf(t, k, "ops/gone"); strings.Contains(raw, "tags") {
		t.Errorf("a list left empty must lose the key:\n%s", raw)
	}
	if out := repairCall(t, s, `{"check":"invalid_field_value"}`); out["planned_total"].(float64) != 0 {
		t.Errorf("second run must plan nothing: %v", out)
	}
}

// A removal is never applied unattended (D1197): a tag written before the
// map_update that declares it must survive the write and the background
// repair, while a case variant is renamed.
func TestListValueDropIsNeverUnattended(t *testing.T) {
	t.Parallel()
	k, s := tagsKB(t)
	// The write gate refuses a new page that introduces the finding, so the
	// page predates the call, as one written before the contract was closed.
	writeTagged(t, k, "ops/w", "Foo_Bar", "brand-new")
	out := decodeJSON(t, mustText(t, s, "concept_write",
		`{"id":"ops/w","frontmatter":{"type":"Note","title":"W","updated":"2026-01-02","tags":["Foo_Bar","brand-new"]},"body":"# W\n\nedited\n"}`))
	raw := tagsOf(t, k, "ops/w")
	if !strings.Contains(raw, "brand-new") || strings.Contains(raw, "Foo_Bar") || !strings.Contains(raw, "foo-bar") {
		t.Fatalf("the write must rename the variant and keep the unknown tag:\n%s\n%v", raw, out)
	}
	if fs, _ := out["findings"].([]interface{}); len(fs) == 0 {
		t.Errorf("the unknown tag must stay a finding: %v", out)
	}
	writeTagged(t, k, "ops/b", "Alpha", "stray")
	stagedAutoRepair(context.Background(), k, lint.AutoRepairChecks(), autoRepairLimit)
	raw = tagsOf(t, k, "ops/b")
	if !strings.Contains(raw, "stray") || strings.Contains(raw, "Alpha") {
		t.Fatalf("the background stage must rename and keep the unknown tag:\n%s", raw)
	}
}

// End to end (D1197): sprawl -> kb_review proposal -> map_update -> kb_repair.
func TestFacetSprawlProposalClosesEndToEnd(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	for i := 0; i < 40; i++ {
		tags := []string{"common", fmt.Sprintf("t%d", i)}
		if i < 3 {
			tags = append(tags, "Common-Tag")
		}
		if i == 3 {
			tags = append(tags, "commontag")
		}
		writeTagged(t, k, fmt.Sprintf("ops/p%d", i), tags...)
	}
	var out struct {
		Items []struct {
			Check    string `json:"check"`
			Proposal *struct {
				Key     string            `json:"key"`
				Values  []string          `json:"values"`
				Mapping map[string]string `json:"mapping"`
				Drop    []string          `json:"drop"`
			} `json:"proposal"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(callOK(t, s, "kb_review", `{"kind":"observation"}`)), &out); err != nil {
		t.Fatal(err)
	}
	var vals []string
	var syn map[string]string
	for _, it := range out.Items {
		if it.Check == "facet_sprawl" {
			if it.Proposal == nil || it.Proposal.Key != "field_values.tags" || len(it.Proposal.Drop) != 40 {
				t.Fatalf("proposal = %+v", it.Proposal)
			}
			vals, syn = it.Proposal.Values, it.Proposal.Mapping
		}
	}
	if len(vals) != 2 {
		t.Fatalf("facet_sprawl item missing or wrong values: %+v", out.Items)
	}
	synonyms := map[string][]string{}
	for from, to := range syn {
		synonyms[to] = append(synonyms[to], from)
	}
	args, _ := json.Marshal(map[string]any{"map": "ops", "field_values": map[string]any{"*": map[string]any{"tags": vals}}, "value_synonyms": synonyms})
	callOK(t, s, "map_update", string(args))
	repairCall(t, s, `{"check":"invalid_field_value","dry_run":false}`)
	for _, f := range mustLint(t, k) {
		if f.Check == "invalid_field_value" {
			t.Fatalf("still invalid after the repair: %+v", f)
		}
	}
	if !strings.Contains(callOK(t, s, "kb_review", `{"kind":"observation"}`), `"facet_sprawl"`) {
		return
	}
	t.Fatal("facet_sprawl still reported after the vocabulary was applied")
}
