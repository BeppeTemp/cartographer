package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// rowKB is a git KB with AutoCommit and repair on write (D349): ops/a holds a
// bare links-section item for ops/b, ops/c is a plain page.
func rowKB(t *testing.T, checks ...string) (*kb.KB, *Server) {
	t.Helper()
	k, _ := setupGitKB(t)
	if err := k.CreateMap("ops", "Ops", "map", nil, ""); err != nil {
		t.Fatal(err)
	}
	seed := map[string]string{
		"a": "# A\n\n## Links\n\n- [B](b.md)\n",
		"b": "# B\n\nBack to [A](a.md).\n",
		"c": "# C\n",
	}
	for id, body := range seed {
		p := filepath.Join(k.DataRoot(), "ops", id+".md")
		if err := os.WriteFile(p, []byte("---\ntype: Note\ntitle: "+strings.ToUpper(id)+"\n---\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	k.AutoCommit = true
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	k.AutoRepair = checks
	k.RepairOnWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return k, s
}

func readBody(t *testing.T, k *kb.KB, id string) string {
	t.Helper()
	cd, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		t.Fatal(err)
	}
	return cd.Body
}

func repairedMap(out map[string]interface{}) map[string]float64 {
	got := map[string]float64{}
	list, _ := out["repaired"].([]interface{})
	for _, r := range list {
		m := r.(map[string]interface{})
		got[m["check"].(string)] = m["count"].(float64)
	}
	return got
}

// patchJSON is a one-edit concept_patch/batch-op body with the current hash.
func patchJSON(t *testing.T, k *kb.KB, id, old, repl string) string {
	t.Helper()
	cd, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{"id":%q,"old_string":%q,"new_string":%q,"if_match":%q}`, id, old, repl, cd.ContentHash)
}

func patchDup(t *testing.T, k *kb.KB) string {
	return patchJSON(t, k, "ops/a", "# A\n", "# A\n\nSee [B](b.md).\n")
}

func TestRepairOnWrite_PatchDropsDuplicateInOneCommit(t *testing.T) {
	k, s := rowKB(t, "duplicate_link", "nonstandard_field")
	before := commitCount(t, k)
	out := decodeJSON(t, mustText(t, s, "concept_patch", patchDup(t, k)))
	if got := repairedMap(out); got["duplicate_link"] != 1 || len(got) != 1 {
		t.Fatalf("repaired = %v", out["repaired"])
	}
	if findingChecks(out)["ops/a.md duplicate_link"] {
		t.Fatalf("finding survived: %v", out["findings"])
	}
	body := readBody(t, k, "ops/a")
	if strings.Contains(body, "- [B](b.md)") || !strings.Contains(body, "See [B](b.md).") {
		t.Fatalf("body:\n%s", body)
	}
	if n := commitCount(t, k) - before; n != 1 {
		t.Fatalf("commits = %d, want 1", n)
	}
	// content_hash is the post-repair hash: an if_match chain works.
	cd, _ := k.ReadConcept("ops/a")
	if out["content_hash"] != cd.ContentHash {
		t.Fatalf("content_hash %v != disk %s", out["content_hash"], cd.ContentHash)
	}
	mustText(t, s, "concept_patch", `{"id":"ops/a","old_string":"See","new_string":"Look","if_match":"`+cd.ContentHash+`"}`)
}

func TestRepairOnWrite_ConceptWriteRenamesNonstandardField(t *testing.T) {
	k, s := rowKB(t, "nonstandard_field")
	out := decodeJSON(t, mustText(t, s, "concept_write",
		`{"id":"ops/n","frontmatter":{"type":"Note","title":"N","keywords":["x"]},"body":"# N\n"}`))
	if got := repairedMap(out); got["nonstandard_field"] != 1 {
		t.Fatalf("repaired = %v", out["repaired"])
	}
	cd, _ := k.ReadConcept("ops/n")
	if strings.Contains(cd.FrontmatterRaw, "keywords") || !strings.Contains(cd.FrontmatterRaw, "tags") {
		t.Fatalf("keywords survived: %s", cd.FrontmatterRaw)
	}
	if out["content_hash"] != cd.ContentHash {
		t.Fatal("content_hash is not the on-disk hash")
	}
}

func TestRepairOnWrite_OffLeavesTheWrite(t *testing.T) {
	for name, set := range map[string]func(k *kb.KB){
		"opt-out":     func(k *kb.KB) { k.RepairOnWrite = false },
		"empty-list":  func(k *kb.KB) { k.AutoRepair = []string{} },
		"only-linkdp": func(k *kb.KB) { k.AutoRepair = []string{"broken_link", "reciprocal_link_item"} },
	} {
		k, s := rowKB(t, "duplicate_link")
		set(k)
		out := decodeJSON(t, mustText(t, s, "concept_patch", patchDup(t, k)))
		if out["repaired"] != nil || !findingChecks(out)["ops/a.md duplicate_link"] {
			t.Errorf("%s: out = %v", name, out)
		}
		if !strings.Contains(readBody(t, k, "ops/a"), "- [B](b.md)") {
			t.Errorf("%s: file was repaired", name)
		}
	}
}

// A neighbour reported by the scoped check is never rewritten, and a listed
// link-dropping check is not applied on write.
func TestRepairOnWrite_NeighboursAndLinkDroppingChecksUntouched(t *testing.T) {
	k, s := rowKB(t, "duplicate_link", "broken_link", "reciprocal_link_item")
	aBefore := readBody(t, k, "ops/a")
	out := decodeJSON(t, mustText(t, s, "concept_write",
		`{"id":"ops/d","frontmatter":{"type":"Note","title":"D"},"body":"See [gone](gone.md) and [A](a.md).\n"}`))
	if !findingChecks(out)["ops/d.md broken_link"] {
		t.Fatalf("findings = %v", out["findings"])
	}
	if out["repaired"] != nil || readBody(t, k, "ops/a") != aBefore {
		t.Fatalf("repaired = %v", out["repaired"])
	}
	if !strings.Contains(readBody(t, k, "ops/d"), "[gone](gone.md)") {
		t.Fatal("broken link was dropped")
	}
	// ops/a carries a duplicate-prone item, but the write did not name it.
	mustText(t, s, "concept_patch", patchJSON(t, k, "ops/c", "# C\n", "# C\n\nSee [A](a.md).\n"))
	if readBody(t, k, "ops/a") != aBefore {
		t.Fatal("neighbour rewritten")
	}
}

func TestRepairOnWrite_BatchMoveSupersede(t *testing.T) {
	k, s := rowKB(t, "duplicate_link")
	out := decodeJSON(t, mustText(t, s, "concept_batch",
		`{"operations":[{"op":"patch",`+patchDup(t, k)[1:]+`,{"op":"patch",`+patchJSON(t, k, "ops/c", "# C\n", "# C2\n")[1:]+`]}`))
	res := out["results"].([]interface{})
	first := res[0].(map[string]interface{})
	if got := repairedMap(first); got["duplicate_link"] != 1 {
		t.Fatalf("entry 0 = %v", first)
	}
	if res[1].(map[string]interface{})["repaired"] != nil {
		t.Fatalf("entry 1 = %v", res[1])
	}
	cd, _ := k.ReadConcept("ops/a")
	if first["content_hash"] != cd.ContentHash {
		t.Fatal("batch content_hash is not post-repair")
	}

	// A moved concept carrying a duplicate.
	p := filepath.Join(k.DataRoot(), "ops", "m.md")
	if err := os.WriteFile(p, []byte("---\ntype: Note\ntitle: M\n---\n# M\n\nSee [B](b.md).\n\n## Links\n\n- [B](b.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mv := decodeJSON(t, mustText(t, s, "concept_move", `{"source_id":"ops/m","target_id":"ops/m2"}`))
	if got := repairedMap(mv); got["duplicate_link"] != 1 {
		t.Fatalf("move = %v", mv)
	}
	if strings.Contains(readBody(t, k, "ops/m2"), "- [B](b.md)") {
		t.Fatal("moved concept not repaired")
	}

	// supersede reports a repaired block.
	if err := os.WriteFile(p, []byte("---\ntype: Note\ntitle: M\n---\n# M\n\nSee [B](b.md).\n\n## Links\n\n- [B](b.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := mustText(t, s, "supersede", `{"source_id":"ops/m","target_id":"ops/c"}`)
	if !strings.Contains(text, "repaired:") || !strings.Contains(text, "duplicate_link") {
		t.Fatalf("supersede = %s", text)
	}
}

func TestRepairOnWrite_CapabilityAndConfig(t *testing.T) {
	k, _ := rowKB(t, "duplicate_link")
	if c := kbCapabilities(k)["repair_on_write"]; c.State != "enabled" {
		t.Fatalf("capability = %+v", c)
	}
	k.AutoRepair = nil
	if c := kbCapabilities(k)["repair_on_write"]; c.State == "enabled" {
		t.Fatalf("capability with no checks = %+v", c)
	}
}

// A repair that cannot be written never fails the call: the content stays as
// written and the finding stays in the report.
func TestRepairOnWrite_WriteErrorKeepsOriginal(t *testing.T) {
	k, _ := rowKB(t, "duplicate_link")
	p := filepath.Join(k.DataRoot(), "ops", "a.md")
	data, _ := os.ReadFile(p)
	dup := strings.Replace(string(data), "# A\n", "# A\n\nSee [B](b.md).\n", 1)
	if err := os.WriteFile(p, []byte(dup), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(p)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
		f.Close()
		t.Skip("directory permissions are not enforced here")
	}
	findings, repaired, hashes := repairWritten(k, []string{"ops/a"}, nil)
	if repaired != nil || hashes != nil {
		t.Fatalf("repaired = %v, hashes = %v", repaired, hashes)
	}
	found := false
	for _, f := range findings {
		found = found || f.Check == "duplicate_link"
	}
	if !found {
		t.Fatalf("finding lost: %v", findings)
	}
	if after, _ := os.ReadFile(p); string(after) != dup {
		t.Fatal("content changed")
	}
}
