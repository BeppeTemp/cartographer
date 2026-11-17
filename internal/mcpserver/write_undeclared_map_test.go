package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2185: a write into a folder that is not a declared Map is refused when the
// KB declares Maps, naming the closest ones and map_create.
func TestWriteGate_UndeclaredMapFolderRefused(t *testing.T) {
	t.Parallel()
	k := gateKB(t) // declares "arch" and "ops"
	s := gateSeed(t, k, nil)
	head := gitOut(t, k, "rev-parse", "HEAD")

	text, isErr := callJSON(t, s, adminCtx, "concept_write",
		`{"id":"opz/zz","frontmatter":{"type":"Note","title":"Zz"},"body":"# Zz\n"}`)
	refusedCleanly(t, k, head, text, isErr, "unmapped_folder")
	for _, want := range []string{"ops", "map_create"} {
		if !strings.Contains(text, want) {
			t.Fatalf("refusal does not name %q: %s", want, text)
		}
	}

	// A batch refuses it too.
	text, isErr = callJSON(t, s, adminCtx, "concept_batch", gateBatch(gateWrite("opz/zz", "Zz", "# Zz\n", false, "")))
	refusedCleanly(t, k, head, text, isErr, "unmapped_folder")

	// Declaring the map first lets the same write through.
	if text, isErr = callJSON(t, s, adminCtx, "map_create", `{"name":"opz","title":"Opz","kind":"map"}`); isErr {
		t.Fatalf("map_create: %s", text)
	}
	if text, isErr = callJSON(t, s, adminCtx, "concept_write",
		`{"id":"opz/zz","frontmatter":{"type":"Note","title":"Zz"},"body":"# Zz\n","index_entry":true}`); isErr {
		t.Fatalf("write into the declared map refused: %s", text)
	}
}

// A KB that declares no Map at all (legacy) is not judged.
func TestWriteGate_UndeclaredMapFolderLegacyKBUnaffected(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	s := gateSeed(t, k, nil)
	if text, isErr := callJSON(t, s, adminCtx, "concept_write",
		`{"id":"nomap/zz","frontmatter":{"type":"Note","title":"Zz"},"body":"# Zz\n"}`); isErr {
		t.Fatalf("a KB with no declared Map refused the write: %s", text)
	}
}

// Editing a page that already sits in an unmapped folder is not blocked by the
// folder: the finding is the folder's, not the write's.
func TestWriteGate_UndeclaredMapFolderExistingPageEditable(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"loose/a": "---\ntype: Note\ntitle: A\n---\n# A\n"})
	if text, isErr := callJSON(t, s, adminCtx, "concept_write",
		`{"id":"loose/a","frontmatter":{"type":"Note","title":"A"},"body":"# A\n\nmore\n","if_match":"`+hashOf(t, k, "loose/a")+`"}`); isErr {
		t.Fatalf("edit of an existing page refused: %s", text)
	}
}

// #2185: the whole-KB gate applies the contradiction rule of changed_ids and
// names what blocks.
func TestGateCheck_OpenContradictionBlocksWholeKBAndNamesIt(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{
		"ops/a": "---\ntype: Note\ntitle: A\n---\n# A\n",
		"ops/c": "---\ntype: Contradiction\ntitle: C\nresolution_status: open\ninvolves: [ops/a]\ncontradiction_kind: factual\nreason: x\n---\n# C\n",
	})
	for _, args := range []string{`{"changed_ids":[]}`, `{"changed_ids":["ops/a"]}`} {
		text, isErr := callJSON(t, s, adminCtx, "gate_check", args)
		if isErr {
			t.Fatal(text)
		}
		var out struct {
			Pass     bool                `json:"pass"`
			Blockers []map[string]any    `json:"gate_blockers"`
			By       []map[string]string `json:"blocked_by"`
		}
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		if out.Pass || len(out.Blockers) != 1 {
			t.Fatalf("%s: contradiction did not block: %s", args, text)
		}
		found := false
		for _, b := range out.By {
			if b["source"] == "gate_blockers" && strings.Contains(b["path"], "ops/c") {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: blocked_by does not name the contradiction: %s", args, text)
		}
	}
}

// The tools that name a server-chosen folder (sources, archive) never create
// it: in a KB with Maps but without that Map they fail on their own, and the
// gate's unmapped_folder never fires for them.
func TestWriteGate_ServerNamedFoldersNeverCreatedUndeclared(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"ops/a": "---\ntype: Note\ntitle: A\n---\n# A\n"})
	for _, c := range []struct{ tool, args, folder string }{
		{"source_register", `{"title":"S","locator":"https://example.com/s","kind":"web"}`, "sources"},
		{"concept_archive", `{"id":"ops/a","if_match":"` + hashOf(t, k, "ops/a") + `"}`, "archive"},
	} {
		text, _ := callJSON(t, s, adminCtx, c.tool, c.args)
		if strings.Contains(text, "unmapped_folder") {
			t.Fatalf("%s: gate fired unmapped_folder: %s", c.tool, text)
		}
		if _, err := os.Stat(filepath.Join(k.DataRoot(), c.folder)); err == nil {
			t.Fatalf("%s created the undeclared folder %s: %s", c.tool, c.folder, text)
		}
	}
}
