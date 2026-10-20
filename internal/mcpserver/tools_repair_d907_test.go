package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// legacyMap is a _map.md written before D907: a flat required_fields, a flat
// field_values entry, the explicit ontology_mode and two removed escape hatches.
const legacyMap = `---
type: Map
title: Ops
kind: map
# the types this map holds
concept_types: [Note]
ontology_mode: strict
required_fields: [updated]
field_values.status: [open, closed]
lint_ignore: [map_without_templates]
title_max_length: 80
---
# Ops
`

func countFindings(t *testing.T, k *kb.KB, check string) int {
	t.Helper()
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range findings {
		if f.Check == check {
			n++
		}
	}
	return n
}

// One kb_repair commit realigns every removed key of a descriptor, keeps the
// rest (comments included), and the strictness the explicit ontology_mode
// stated survives because it is derived from concept_types. A second run has
// nothing to do.
func TestRepairContractLegacyKey_RealignsTheDescriptorInOneCommit(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	driftWrite(t, k, "ops/_map.md", legacyMap)
	if n := countFindings(t, k, "contract_legacy_key"); n != 1 {
		t.Fatalf("want one contract_legacy_key finding for the descriptor, got %d", n)
	}

	before := commitCount(t, k)
	out := repairApply(t, s, "contract_legacy_key")
	if out["applied"].(float64) != 1 {
		t.Fatalf("applied = %v, want 1: %v", out["applied"], out)
	}
	if got := commitCount(t, k); got != before+1 {
		t.Fatalf("commits = %d, want %d: the realignment is one commit", got, before+1)
	}

	raw, err := os.ReadFile(filepath.Join(k.DataRoot(), "ops", "_map.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, gone := range []string{"ontology_mode", "lint_ignore", "title_max_length", "\nrequired_fields:", "\nfield_values.status:"} {
		if strings.Contains(got, gone) {
			t.Errorf("realigned descriptor still carries %q:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"required_fields.*: [updated]", "field_values.*.status: [open, closed]", "# the types this map holds", "concept_types: [Note]", "# Ops\n"} {
		if !strings.Contains(got, kept) {
			t.Errorf("realigned descriptor lost %q:\n%s", kept, got)
		}
	}
	if n := countFindings(t, k, "contract_legacy_key"); n != 0 {
		t.Fatalf("contract_legacy_key still reported after the repair: %d", n)
	}

	res := callTool(t, s, "concept_batch", `{"operations":[{"op":"write","id":"ops/d","frontmatter":{"type":"Digest","title":"D","updated":"2026-01-02"},"body":"# D\n"}]}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "not allowed in map") {
		t.Fatalf("the map stays strict without ontology_mode: %+v", res.Content)
	}
	// The realigned contract still requires "updated": a page keeps that name.
	res = callTool(t, s, "concept_batch", `{"operations":[{"op":"write","id":"ops/n","frontmatter":{"type":"Note","title":"N","updated":"2026-01-02"},"body":"# N\n\nSee [self](n.md).\n","index_entry":true}]}`)
	if res.IsError {
		t.Fatalf("a page carrying the field the map requires was refused: %+v", res.Content)
	}

	before = commitCount(t, k)
	if out := repairApply(t, s, "contract_legacy_key"); out["applied"].(float64) != 0 {
		t.Fatalf("second run applied %v, want 0", out["applied"])
	}
	if got := commitCount(t, k); got != before {
		t.Fatalf("second run committed: %d -> %d", before, got)
	}
}

// lint_accept in instructions.md is dropped by the same repair, under the
// artifact-write rule: skipped where the KB does not allow artifact writes,
// applied where it does, every other key and the body kept.
func TestRepairContractLegacyKey_DropsLintAcceptOnlyWithArtifactWrite(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	const instr = "---\nperimeter: ops\nlint_accept:\n  skill_git_command: [skills/a]\n---\n# Instructions\n"
	path := filepath.Join(k.Root, "instructions.md")
	if err := os.WriteFile(path, []byte(instr), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("test: instructions.md"); err != nil {
		t.Fatal(err)
	}

	before := commitCount(t, k)
	out := repairApply(t, s, "contract_legacy_key")
	if out["applied"].(float64) != 0 {
		t.Fatalf("applied without allow_artifact_write: %v", out)
	}
	skipped, _ := out["skipped"].([]any)
	if len(skipped) != 1 || !strings.Contains(skipped[0].(map[string]any)["reason"].(string), "allow_artifact_write") {
		t.Fatalf("want instructions.md skipped for allow_artifact_write: %v", out["skipped"])
	}
	if raw, _ := os.ReadFile(path); string(raw) != instr {
		t.Fatalf("instructions.md changed without allow_artifact_write:\n%s", raw)
	}
	if got := commitCount(t, k); got != before {
		t.Fatalf("a skipped repair committed: %d -> %d", before, got)
	}

	setPolicy(k, func(p *kb.Policy) { p.AllowArtifactWrite = true })
	if out := repairApply(t, s, "contract_legacy_key"); out["applied"].(float64) != 1 {
		t.Fatalf("applied = %v, want 1: %v", out["applied"], out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "---\nperimeter: ops\n---\n# Instructions\n"; string(raw) != want {
		t.Fatalf("instructions.md = %q, want %q", raw, want)
	}
	if fi, err := os.Lstat(path); err != nil || (execbit.Supported && fi.Mode().Perm() != 0o640) {
		t.Fatalf("file mode not kept: %v %v", fi, err)
	}
	if n := countFindings(t, k, "contract_legacy_key"); n != 0 {
		t.Fatalf("contract_legacy_key still reported: %d", n)
	}
}

// A map_update that loosens the contract needs a reason, which reaches the
// root log and the response; a tightening needs none.
func TestMapUpdate_LooseningNeedsAReason(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	callOK(t, s, "map_update", `{"map":"ops","required_fields":{"*":["updated"]}}`)

	res := callTool(t, s, "map_update", `{"map":"ops","required_fields":{}}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "needs a reason") {
		t.Fatalf("a loosening without a reason must be refused: %+v", res.Content)
	}
	if c, err := k.ReadMapContract("ops"); err != nil || len(c.RequiredFields) != 1 {
		t.Fatalf("the refused change was written: %+v %v", c, err)
	}

	out := decodeJSON(t, callOK(t, s, "map_update", `{"map":"ops","required_fields":{"*":["updated","owner"]}}`))
	if _, ok := out["loosened"]; ok {
		t.Fatalf("a tightening loosens nothing: %v", out)
	}

	out = decodeJSON(t, callOK(t, s, "map_update", `{"map":"ops","required_fields":{"*":["updated"]},"reason":"owner moved to the team page"}`))
	loosened, _ := out["loosened"].([]any)
	if len(loosened) != 1 || !strings.Contains(loosened[0].(string), "owner") {
		t.Fatalf("loosened = %v, want the dropped owner", out["loosened"])
	}
	log, err := os.ReadFile(filepath.Join(k.DataRoot(), "log.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "loosened") || !strings.Contains(string(log), "owner moved to the team page") {
		t.Fatalf("root log.md lacks the loosening and its reason:\n%s", log)
	}
	if got := headTrailers(t, k.Root, "Reason"); got != "owner moved to the team page" {
		t.Fatalf("Reason trailer = %q", got)
	}
}

// The heartbeat realigns a pre-D907 contract and drops a lint_ignore field
// whatever the KB's auto_repair lists, an explicit empty list included.
func TestAutoRepairAlwaysRealignsAPreD907Contract(t *testing.T) {
	t.Parallel()
	for name, list := range map[string][]string{"empty": {}, "other-checks": {"title_h1_mismatch"}} {
		list := list
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			k, s := repairKB(t, 0)
			driftWrite(t, k, "ops/_map.md", legacyMap)
			driftWrite(t, k, "ops/ig.md", "---\ntype: Note\ntitle: Ig\nupdated: 2026-01-02\nlint_ignore: [orphan]\nstate: open\n---\n# Ig\n")
			setPolicy(k, func(p *kb.Policy) { p.AutoRepair = list; p.AutoRepairDefault = false })
			s.runAutoRepairQuota(context.Background())
			if n := countFindings(t, k, "contract_legacy_key"); n != 0 {
				t.Fatalf("contract_legacy_key still reported: %d", n)
			}
			raw, err := os.ReadFile(filepath.Join(k.DataRoot(), "ops", "ig.md"))
			if err != nil || strings.Contains(string(raw), "lint_ignore") {
				t.Fatalf("lint_ignore not dropped (%v): %s", err, raw)
			}
			// No other unattended page rewrite: the renames stay with the list.
			if !strings.Contains(string(raw), "state: open") || strings.Contains(string(raw), "status:") {
				t.Fatalf("a rename ran unlisted: %s", raw)
			}
		})
	}
}

// A map that requires "updated" keeps the field on its pages: the safe
// nonstandard_field repair does not rename a name the page's own contract
// names, so a write carrying it passes the gate and stays as written.
func TestWriteGate_PageKeepsAFieldItsContractNames(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	driftWrite(t, k, "ops/_map.md", "---\ntype: Map\ntitle: Ops\nkind: map\nrequired_fields.*: [updated]\n---\n# Ops\n")
	res := callTool(t, s, "concept_batch", `{"operations":[{"op":"write","id":"ops/d","frontmatter":{"type":"Note","title":"D","updated":"2026-01-02","state":"x"},"body":"# D\n\nSee [self](d.md).\n","index_entry":true}]}`)
	if res.IsError {
		t.Fatalf("a write into a map requiring the field was refused: %+v", res.Content)
	}
	raw, err := os.ReadFile(filepath.Join(k.DataRoot(), "ops", "d.md"))
	if err != nil {
		t.Fatal(err)
	}
	// "updated" stays (the contract names it); the unnamed synonym is renamed.
	if !strings.Contains(string(raw), "updated:") || strings.Contains(string(raw), "timestamp") || !strings.Contains(string(raw), "status:") {
		t.Fatalf("page = %s", raw)
	}
}
