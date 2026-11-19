package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gateWarnings(t *testing.T, s *Server, args string) []string {
	t.Helper()
	out := decodeJSON(t, mustText(t, s, "gate_check", args))
	list, ok := out["warnings"].([]interface{})
	if !ok {
		if _, present := out["warnings"]; present {
			t.Fatalf("warnings is not a list: %v", out["warnings"])
		}
		return nil
	}
	var w []string
	for _, v := range list {
		w = append(w, v.(string))
	}
	return w
}

// #2278: a changed id that is no concept and never was is named in warnings;
// pass stays true, a deletion or a move says nothing, an existing id adds no key.
func TestGateCheckWarnsOnUnknownChangedID(t *testing.T) {
	t.Parallel()
	s, k := graphToolKBWithKB(t, map[string]string{
		"ops/alpha.md": "x\n",
		"ops/gone.md":  "y\n",
		"ops/mv.md":    "z\n",
	})
	if _, err := k.CommitOpAs("test: seed", "", ""); err != nil {
		t.Fatal(err)
	}
	mustText(t, s, "concept_delete", `{"id":"ops/gone"}`)
	mustText(t, s, "concept_move", `{"source_id":"ops/mv","target_id":"ops/mv2"}`)

	// a warning is not a refusal: pass stays true
	if out := decodeJSON(t, mustText(t, s, "gate_check", `{"changed_ids":["ops/alfa"]}`)); out["pass"] != true {
		t.Errorf("a typo id failed the gate: %v", out)
	}
	if w := gateWarnings(t, s, `{"changed_ids":["ops/alpha"]}`); w != nil {
		t.Errorf("existing id: warnings %v", w)
	}
	if w := gateWarnings(t, s, `{"changed_ids":["ops/gone","ops/mv"]}`); w != nil {
		t.Errorf("deleted and moved ids: warnings %v", w)
	}
	w := gateWarnings(t, s, `{"changed_ids":["ops/alpha","ops/alfa","ops/gone","ops/betta"]}`)
	if len(w) != 2 || !strings.Contains(w[0], `"ops/alfa"`) || !strings.Contains(w[1], `"ops/betta"`) ||
		!strings.Contains(w[0], "typo") || strings.Contains(strings.Join(w, " "), "ops/gone") {
		t.Errorf("mixed list: %v", w)
	}
}

func TestGateCheckWarnsWithoutGitHistory(t *testing.T) {
	t.Parallel()
	s, k := graphToolKBWithKB(t, map[string]string{"ops/alpha.md": "x\n"})
	if err := os.RemoveAll(filepath.Join(k.Root, ".git")); err != nil {
		t.Fatal(err)
	}
	w := gateWarnings(t, s, `{"changed_ids":["ops/nope"]}`)
	if len(w) != 1 || !strings.Contains(w[0], "without git history") {
		t.Errorf("warnings = %v", w)
	}
}

func TestGateCheckUnknownIDWarningNeedsWholeKBToken(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{"ops/alpha.md": "x\n"})
	text, isErr := callJSON(t, s, narrowCtx, "gate_check", `{"changed_ids":["ops/nope"]}`)
	if !isErr || strings.Contains(text, "warnings") {
		t.Errorf("scoped token: %v %q", isErr, text)
	}
}

// With a scope the ids are not what is linted: the warning still names the
// unknown id but does not claim that nothing was checked.
func TestGateCheckUnknownIDWarningWordingWithAScope(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{"ops/alpha.md": "x\n"})
	w := gateWarnings(t, s, `{"changed_ids":["ops/nope"],"scope":"ops"}`)
	if len(w) != 1 || !strings.Contains(w[0], `"ops/nope"`) || strings.Contains(w[0], "nothing was checked") {
		t.Errorf("warnings = %v", w)
	}
	w = gateWarnings(t, s, `{"changed_ids":["ops/nope"]}`)
	if len(w) != 1 || !strings.Contains(w[0], "nothing was checked for it") {
		t.Errorf("without scope: %v", w)
	}
}
