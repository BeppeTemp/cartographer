package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// D357: the repairs of the drift audit, end to end through kb_repair.

func driftWrite(t *testing.T, k *kb.KB, rel, content string) {
	t.Helper()
	abs := filepath.Join(k.DataRoot(), rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("test: " + rel); err != nil {
		t.Fatal(err)
	}
}

func repairApply(t *testing.T, s *Server, check string) map[string]any {
	t.Helper()
	return repairCall(t, s, `{"check":"`+check+`","dry_run":false}`)
}

func readRaw(t *testing.T, k *kb.KB, id string) string {
	t.Helper()
	cd, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		t.Fatal(err)
	}
	return cd.FrontmatterRaw + "\n---\n" + cd.Body
}

// The reproduction of D357 decision 1: the decorative title no longer wins.
func TestRepairTitleH1_DecorativeTitleTakesTheHeading(t *testing.T) {
	k, s := repairKB(t, 0)
	driftWrite(t, k, "ops/rocket.md", "---\ntype: Note\ntitle: \"🚀\"\n---\n# Server B real\n")
	out := repairApply(t, s, "title_h1_mismatch")
	if out["applied"].(float64) != 1 {
		t.Fatalf("out = %v", out)
	}
	got := readRaw(t, k, "ops/rocket")
	if !strings.Contains(got, "title: Server B real") || !strings.Contains(got, "# Server B real") || strings.Contains(got, "🚀") {
		t.Fatalf("page:\n%s", got)
	}
	if again := repairApply(t, s, "title_h1_mismatch"); again["applied"].(float64) != 0 {
		t.Fatalf("not idempotent: %v", again)
	}
}

func TestRepairMissingTitle(t *testing.T) {
	k, s := repairKB(t, 0)
	driftWrite(t, k, "ops/with-heading.md", "---\ntype: Note\n---\n# The heading\n")
	driftWrite(t, k, "ops/from-stem.md", "---\ntype: Note\n---\nno heading\n")
	out := repairApply(t, s, "missing_title")
	if out["applied"].(float64) != 2 {
		t.Fatalf("out = %v", out)
	}
	if got := readRaw(t, k, "ops/with-heading"); !strings.Contains(got, "title: The heading") {
		t.Fatalf("page:\n%s", got)
	}
	if got := readRaw(t, k, "ops/from-stem"); !strings.Contains(got, "title: From stem") {
		t.Fatalf("page:\n%s", got)
	}
	if again := repairApply(t, s, "missing_title"); again["applied"].(float64) != 0 {
		t.Fatalf("not idempotent: %v", again)
	}
}

func TestRepairStringifiedList_BareScalars(t *testing.T) {
	k, s := repairKB(t, 0)
	driftWrite(t, k, "ops/p.md", "---\ntype: Note\ntitle: P\ntags: \"x, y\"\nprovenance: \"Talk, 2024\"\nrelated: backup\n---\n# P\n")
	if out := repairApply(t, s, "stringified_list"); out["applied"].(float64) != 1 {
		t.Fatalf("out = %v", out)
	}
	cd, _ := k.ReadConcept("ops/p")
	fm, _ := okf.ParseFrontmatter(cd.FrontmatterRaw)
	for field, want := range map[string]string{"tags": "x|y", "provenance": "Talk, 2024", "related": "backup"} {
		v, _ := fm.Get(field)
		list, ok := v.([]string)
		if !ok || strings.Join(list, "|") != want {
			t.Errorf("%s = %#v, want [%s]", field, v, want)
		}
	}
}

func TestRepairRepeatedLink(t *testing.T) {
	k, s := repairKB(t, 0)
	driftWrite(t, k, "ops/b.md", "---\ntype: Note\ntitle: B\n---\n# B\n")
	driftWrite(t, k, "ops/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n\nSee [b](b.md) and [the b note](b.md).\n")
	if out := repairApply(t, s, "repeated_link"); out["applied"].(float64) != 1 {
		t.Fatalf("out = %v", out)
	}
	if got := readRaw(t, k, "ops/a"); !strings.Contains(got, "See [b](b.md) and the b note.") {
		t.Fatalf("page:\n%s", got)
	}
	if again := repairApply(t, s, "repeated_link"); again["applied"].(float64) != 0 {
		t.Fatalf("not idempotent: %v", again)
	}
}

func TestRepairUnknownType_OnlyWhenOneKnownTypeMatches(t *testing.T) {
	k, s := repairKB(t, 0)
	driftWrite(t, k, "../templates/service.md", "---\ntype: Service\ntitle: Service\n---\n# {{title}}\n")
	driftWrite(t, k, "ops/svc.md", "---\ntype: service\ntitle: S\n---\n# S\n")
	driftWrite(t, k, "ops/odd.md", "---\ntype: Gadget\ntitle: G\n---\n# G\n")
	out := repairApply(t, s, "unknown_type")
	if out["applied"].(float64) != 1 {
		t.Fatalf("out = %v", out)
	}
	if got := readRaw(t, k, "ops/svc"); !strings.Contains(got, "type: Service") {
		t.Fatalf("page:\n%s", got)
	}
	if got := readRaw(t, k, "ops/odd"); !strings.Contains(got, "type: Gadget") {
		t.Fatalf("a judgement was guessed:\n%s", got)
	}
}

func TestRepairValueCaseVariant(t *testing.T) {
	k, s := repairKB(t, 0)
	for _, id := range []string{"a", "b"} {
		driftWrite(t, k, "free/"+id+".md", "---\ntype: Note\ntitle: "+id+"\nstatus: active\n---\n# x\n")
	}
	driftWrite(t, k, "free/c.md", "---\ntype: Note\ntitle: c\nstatus: Active\n---\n# c\n")
	if out := repairApply(t, s, "value_case_variant"); out["applied"].(float64) != 1 {
		t.Fatalf("out = %v", out)
	}
	if got := readRaw(t, k, "free/c"); !strings.Contains(got, "status: active") {
		t.Fatalf("page:\n%s", got)
	}
}

func TestRepairUnmappedFolder_ScaffoldsWhatMapCreateWrites(t *testing.T) {
	k, s := repairKB(t, 0)
	driftWrite(t, k, "loose-notes/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n")
	before := commitCount(t, k)
	dry := repairCall(t, s, `{"check":"unmapped_folder"}`)
	if dry["planned_total"].(float64) != 1 || commitCount(t, k) != before {
		t.Fatalf("dry run = %v", dry)
	}
	out := repairApply(t, s, "unmapped_folder")
	if out["applied"].(float64) != 1 || commitCount(t, k) != before+1 {
		t.Fatalf("out = %v", out)
	}
	raw, err := os.ReadFile(filepath.Join(k.DataRoot(), "loose-notes", "_map.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The descriptor map_create writes for a plain map, with no contract.
	want := "---\ntype: Map\ntitle: Loose notes\nkind: map\nontology_mode: flexible\n---\n# Loose notes\n"
	if string(raw) != want {
		t.Fatalf("_map.md:\n%s\nwant:\n%s", raw, want)
	}
	c, err := k.ReadMapContract("loose-notes")
	if err != nil || c.Kind != "map" {
		t.Fatalf("contract %+v, err %v", c, err)
	}
	if again := repairApply(t, s, "unmapped_folder"); again["applied"].(float64) != 0 || commitCount(t, k) != before+1 {
		t.Fatalf("not idempotent: %v", again)
	}
}

// repair-on-write handles concept checks only: a folder check never runs there.
func TestRepairOnWriteSkipsFolderChecks(t *testing.T) {
	k, _ := repairKB(t, 0)
	k.RepairOnWrite = true
	k.AutoRepair = []string{"unmapped_folder", "missing_title"}
	got := repairOnWriteChecks(k)
	if len(got) != 1 || got[0] != "missing_title" {
		t.Fatalf("checks = %v", got)
	}
}

// The heartbeat repairs a folder with no descriptor, and the pages in it, in
// one run: the scaffold comes first so the pages are fixed inside a map.
func TestStagedAutoRepair_ScaffoldsFoldersAndFixesTheirPages(t *testing.T) {
	k, _ := repairKB(t, 0)
	driftWrite(t, k, "loose/untitled.md", "---\ntype: Note\n---\n# Untitled one\n")
	r := stagedAutoRepair(context.Background(), k, config.DefaultAutoRepair, 50)
	if r.applied["unmapped_folder"] != 1 || r.applied["missing_title"] != 1 {
		t.Fatalf("applied = %v, skipped = %v, errs = %v", r.applied, r.skipped, r.errs)
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "loose", "_map.md")); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, k, "loose/untitled"); !strings.Contains(got, "title: Untitled one") {
		t.Fatalf("page:\n%s", got)
	}
}
