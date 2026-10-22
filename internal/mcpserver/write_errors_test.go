package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestApplyPatchEdit_AmbiguousNamesCauseAndWayOut(t *testing.T) {
	t.Parallel()
	body := "one\nsame\ntwo\nsame\n"
	many := strings.Repeat("rep\n", 25)
	for _, tc := range []struct {
		name, body, old string
		want, notWant   []string
	}{
		{"trailing newline", body, "same\n", []string{
			"old_string_ambiguous: old_string matches 2 times (lines 2, 4)",
			"a trailing newline in old_string is the usual reason",
			"extend old_string with the unique text on the line before or after, or pass replace_all=true if all 2 occurrences are meant",
		}, nil},
		{"leading newline", body, "\nsame", []string{"a leading newline in old_string"}, nil},
		{"no newline", body, "same", []string{
			"old_string_ambiguous: old_string matches 2 times (lines 2, 4)",
			"extend old_string with the unique text",
		}, []string{"newline in old_string", "occurrence:", "last"}},
		{"truncated list", many, "rep", []string{
			"matches 25 times (first 10 lines: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)",
			"all 25 occurrences are meant",
		}, []string{"lines 1, 2, 3, 4, 5, 6, 7, 8, 9, 10,"}},
		{"exactly the cap", strings.Repeat("rep\n", 10), "rep", []string{"matches 10 times (lines 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)"}, []string{"first 10"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := applyPatchEdit(tc.body, tc.old, "x", false)
			if err == nil {
				t.Fatal("expected old_string_ambiguous")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%q lacks %q", err, w)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(err.Error(), w) {
					t.Errorf("%q must not contain %q", err, w)
				}
			}
		})
	}
}

// A failed edit i > 1 says it saw the result of the earlier edits; edit 1 and
// the single form see the stored body and say nothing. The page is untouched.
func TestPatch_FailedEditSaysWhatItSaw(t *testing.T) {
	t.Parallel()
	const chain = "matched against the body after edits 1..1"
	okEdit := map[string]any{"old_string": "alpha line", "new_string": "alpha changed"}
	ambiguous := map[string]any{"old_string": "a", "new_string": "x"}
	missing := map[string]any{"old_string": "alpha line", "new_string": "x"} // gone after okEdit

	t.Run("concept_patch", func(t *testing.T) {
		t.Parallel()
		s, k := listsKB(t, "")
		before := readHash(t, k, listsID)
		for _, tc := range []struct {
			name     string
			args     map[string]any
			prefix   string
			wantNote bool
		}{
			{"ambiguous second", map[string]any{"edits": []any{okEdit, ambiguous}}, "edit 2 of 2: old_string_ambiguous", true},
			{"missing second", map[string]any{"edits": []any{okEdit, missing}}, "edit 2 of 2: old_string_not_found", true},
			{"ambiguous first", map[string]any{"edits": []any{ambiguous, okEdit}}, "edit 1 of 2: old_string_ambiguous", false},
			{"single", map[string]any{"old_string": "a", "new_string": "x"}, "old_string_ambiguous", false},
		} {
			_, msg := patchCall(t, s, k, tc.args)
			if !strings.HasPrefix(msg, tc.prefix) || strings.Contains(msg, chain) != tc.wantNote {
				t.Errorf("%s: %q (want prefix %q, chain clause %v)", tc.name, msg, tc.prefix, tc.wantNote)
			}
			if got := readHash(t, k, listsID); got != before {
				t.Errorf("%s: the page changed", tc.name)
			}
		}
	})

	t.Run("index_patch", func(t *testing.T) {
		t.Parallel()
		k := setupTestKB(t)
		s := New("test")
		RegisterKBTools(s, k, Deps{})
		_, hash, err := k.IndexHash("")
		if err != nil {
			t.Fatal(err)
		}
		// The KB name matches twice in the root index (the title and the H1).
		dup := map[string]any{"old_string": filepath.Base(k.Root), "new_string": "Atlas"}
		first := map[string]any{"old_string": "KB initialized.", "new_string": "KB initialized!"}
		for _, tc := range []struct {
			edits    []any
			prefix   string
			wantNote bool
		}{
			{[]any{first, dup}, "edit 2 of 2: old_string_ambiguous", true},
			{[]any{dup, first}, "edit 1 of 2: old_string_ambiguous", false},
		} {
			res := callTool(t, s, "index_patch", mustJSON(t, map[string]any{"if_match": hash, "edits": tc.edits}))
			if msg := res.Content[0].Text; !res.IsError || !strings.HasPrefix(msg, tc.prefix) || strings.Contains(msg, chain) != tc.wantNote {
				t.Errorf("%s: %q", tc.prefix, msg)
			}
		}
		res := callTool(t, s, "index_patch", mustJSON(t, map[string]any{"if_match": hash, "edits": []any{
			first, map[string]any{"old_string": "no such text", "new_string": "x"},
		}}))
		if msg := res.Content[0].Text; !res.IsError || !strings.HasPrefix(msg, "edit 2 of 2: old_string_not_found") || !strings.Contains(msg, chain) {
			t.Errorf("index_patch second edit: %q", msg)
		}
		if _, after, _ := k.IndexHash(""); after != hash {
			t.Error("the index changed")
		}
	})

	t.Run("concept_batch", func(t *testing.T) {
		t.Parallel()
		s, k := listsKB(t, "")
		before := readHash(t, k, listsID)
		batch := func(edits ...any) string {
			res := callTool(t, s, "concept_batch", mustJSON(t, map[string]any{"operations": []any{
				map[string]any{"op": "patch", "id": listsID, "if_match": before, "edits": edits},
			}}))
			if !res.IsError {
				t.Fatalf("expected an error: %s", res.Content[0].Text)
			}
			return res.Content[0].Text
		}
		if msg := batch(okEdit, ambiguous); !strings.Contains(msg, ", edit 2 of 2: old_string_ambiguous") || !strings.Contains(msg, chain) {
			t.Errorf("second edit: %q", msg)
		}
		if msg := batch(ambiguous, okEdit); !strings.Contains(msg, ", edit 1 of 2: old_string_ambiguous") || strings.Contains(msg, chain) {
			t.Errorf("first edit: %q", msg)
		}
		if got := readHash(t, k, listsID); got != before {
			t.Error("the page changed")
		}
	})
}

func TestPatch_FrontmatterRemoveGivenAKeyShowsTheShape(t *testing.T) {
	t.Parallel()
	s, k := listsKB(t, "tags: [x, y]\nstatus: draft\n")
	before := readHash(t, k, listsID)
	for _, tc := range []struct {
		key string
		val any
	}{{"tags", nil}, {"status", map[string]any{}}, {"tags", 3}, {"tags", true}, {"tags", ""}} {
		want := fmt.Sprintf(`invalid params: frontmatter_remove: %s`, tc.key)
		hint := fmt.Sprintf(`to remove the key itself use unset: ["%s"] or frontmatter: {"%s": null}`, tc.key, tc.key)

		_, msg := patchCall(t, s, k, map[string]any{"frontmatter_remove": map[string]any{tc.key: tc.val}})
		if !strings.Contains(msg, want) || !strings.Contains(msg, hint) {
			t.Errorf("concept_patch %v: %q", tc.val, msg)
		}
		res := callTool(t, s, "concept_batch", mustJSON(t, map[string]any{"operations": []any{
			map[string]any{"op": "patch", "id": listsID, "if_match": before, "frontmatter_remove": map[string]any{tc.key: tc.val}},
		}}))
		if text := res.Content[0].Text; !res.IsError || !strings.Contains(text, want) || !strings.Contains(text, hint) {
			t.Errorf("concept_batch %v: %q", tc.val, text)
		}
		if got := readHash(t, k, listsID); got != before {
			t.Fatalf("%v: the page changed", tc.val)
		}
	}

	if _, msg := patchCall(t, s, k, map[string]any{"frontmatter_append": map[string]any{"tags": nil}}); !strings.Contains(msg, "frontmatter_append: tags must be a string or an array of strings") || strings.Contains(msg, "unset") {
		t.Errorf("frontmatter_append keeps its text: %q", msg)
	}
	if _, msg := patchCall(t, s, k, map[string]any{"frontmatter_remove": map[string]any{"tags": []string{"x"}}}); msg != "" {
		t.Errorf("a list item still removes: %q", msg)
	}
	if _, msg := patchCall(t, s, k, map[string]any{"frontmatter_remove": map[string]any{"tags": "y"}}); msg != "" {
		t.Errorf("a string item still removes: %q", msg)
	}
}

// staleSetup writes a concept in a git KB, changes it with a second write
// carrying a reason, and returns the hash the first write returned.
func staleSetup(t *testing.T) (*Server, *kb.KB, string) {
	t.Helper()
	k, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	write := func(body string, extra map[string]any) string {
		args := map[string]any{"id": "test/stale", "frontmatter": map[string]any{"type": "Note", "title": "Stale"}, "body": body}
		if h, err := k.ReadConcept("test/stale"); err == nil {
			args["if_match"] = h.ContentHash
		}
		for key, v := range extra {
			args[key] = v
		}
		res := callTool(t, s, "concept_write", mustJSON(t, args))
		if res.IsError {
			t.Fatalf("concept_write: %s", res.Content[0].Text)
		}
		var out struct {
			ContentHash string `json:"content_hash"`
		}
		if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			t.Fatal(err)
		}
		return out.ContentHash
	}
	first := write("# Stale\n\n[[anchor]]\n\nfirst\n", nil)
	write("# Stale\n\n[[anchor]]\n\nsecond\n", map[string]any{"reason": "incident 42"})
	return s, k, first
}

func TestStaleWrite_NamesTheCurrentHashAndTheCommit(t *testing.T) {
	t.Parallel()
	s, k, old := staleSetup(t)
	current := readHash(t, k, "test/stale")
	short := shortSHA(gitOut(t, k, "rev-parse", "HEAD"))
	wantAll := []string{
		"stale_write:", "current content_hash " + current,
		`last changed by ` + short + ` "concept_write: test/stale", reason: incident 42`,
		"re-read the concept (concept_read) and retry with that hash",
	}

	for name, res := range map[string]ToolResult{
		"concept_write": callTool(t, s, "concept_write", mustJSON(t, map[string]any{
			"id": "test/stale", "if_match": old, "frontmatter": map[string]any{"type": "Note", "title": "Stale"}, "body": "x\n\n[[anchor]]\n"})),
		"concept_patch": callTool(t, s, "concept_patch", mustJSON(t, map[string]any{
			"id": "test/stale", "if_match": old, "old_string": "second", "new_string": "third"})),
		"concept_delete": callTool(t, s, "concept_delete", mustJSON(t, map[string]any{"id": "test/stale", "if_match": old})),
		"concept_batch": callTool(t, s, "concept_batch", mustJSON(t, map[string]any{"operations": []any{
			map[string]any{"op": "patch", "id": "test/stale", "if_match": old, "old_string": "second", "new_string": "third"}}})),
	} {
		msg := res.Content[0].Text
		if !res.IsError {
			t.Errorf("%s: expected stale_write", name)
			continue
		}
		for _, w := range wantAll {
			if !strings.Contains(msg, w) {
				t.Errorf("%s: %q lacks %q", name, msg, w)
			}
		}
		if name == "concept_batch" && !strings.Contains(msg, "(test/stale)") {
			t.Errorf("concept_batch lost its label: %q", msg)
		}
	}

	// The hash in the error is the one a retry needs.
	res := callTool(t, s, "concept_patch", mustJSON(t, map[string]any{
		"id": "test/stale", "if_match": current, "old_string": "second", "new_string": "third"}))
	if res.IsError {
		t.Fatalf("retry with the hash of the error: %s", res.Content[0].Text)
	}
}

func TestStaleWrite_AutoRepairCommitShowsAsSuch(t *testing.T) {
	t.Parallel()
	s, k, old := staleSetup(t)
	d, err := k.ReadConcept("test/stale")
	if err != nil {
		t.Fatal(err)
	}
	fm, err := okf.ParseFrontmatter(d.FrontmatterRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.WriteConcept("test/stale", fm, d.Body+"repaired\n", d.ContentHash); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("auto-repair (background)"); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, s, "concept_patch", mustJSON(t, map[string]any{
		"id": "test/stale", "if_match": old, "old_string": "second", "new_string": "third"}))
	if msg := res.Content[0].Text; !res.IsError || !strings.Contains(msg, `"auto-repair (background)"`) {
		t.Errorf("got %q", msg)
	}
}

// Outside a git repository the error carries the hash and no commit part; a
// stale if_match on a page that does not exist has no current hash at all.
func TestStaleWrite_NoGitHashOnly_MissingPageUnchanged(t *testing.T) {
	t.Parallel()
	s, k := listsKB(t, "")
	if gitx.IsRepo(k.Root) {
		t.Skip("fixture is a git repository")
	}
	current := readHash(t, k, listsID)
	_, msg := patchCall(t, s, k, map[string]any{"old_string": "alpha line", "new_string": "x", "if_match": "0000"})
	if !strings.HasPrefix(msg, "stale_write: ") || !strings.Contains(msg, "current content_hash "+current) || strings.Contains(msg, "last changed by") {
		t.Errorf("non-git: %q", msg)
	}

	res := callTool(t, s, "concept_write", mustJSON(t, map[string]any{
		"id": "notes/missing", "if_match": "0000", "frontmatter": map[string]any{"type": "Note"}, "body": "x"}))
	if msg := res.Content[0].Text; !res.IsError || !strings.HasPrefix(msg, "stale_write: ") || strings.Contains(msg, "current content_hash") {
		t.Errorf("missing page: %q", msg)
	}
}

func TestStaleWrite_IndexPatchAndArtifactNameTheCurrentHash(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	_, hash, err := k.IndexHash("")
	if err != nil {
		t.Fatal(err)
	}
	res := callTool(t, s, "index_patch", mustJSON(t, map[string]any{"if_match": "0000", "old_string": "KB initialized.", "new_string": "x"}))
	if msg := res.Content[0].Text; !res.IsError || !strings.HasPrefix(msg, "stale_write: ") || !strings.Contains(msg, "current content_hash "+hash) {
		t.Errorf("index_patch: %q", msg)
	}

	const path = "skills/stale-test/assets/note.txt"
	if res := callTool(t, s, "artifact_write", mustJSON(t, map[string]any{"path": path, "content": "one\n"})); res.IsError {
		t.Fatalf("artifact_write: %s", res.Content[0].Text)
	}
	sum := sha256Hex([]byte("one\n"))
	for _, name := range []string{"artifact_write", "artifact_delete"} {
		res := callTool(t, s, name, mustJSON(t, map[string]any{"path": path, "if_match": "0000", "content": "two\n"}))
		if msg := res.Content[0].Text; !res.IsError || !strings.Contains(msg, "stale_write: ") || !strings.Contains(msg, "current sha256 "+sum) {
			t.Errorf("%s: %q", name, msg)
		}
	}
}

func TestBatchWriteError_RollbackFailureCarriesTheRecoveryHint(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	ops := []kb.BatchWriteOp{{ID: "manutenzione/test-runbook"}}

	failed := fmt.Errorf("batch op 0 (manutenzione/test-runbook): disk full (%s: log.md: permission denied)", kb.RollbackFailedMarker)
	if msg := batchWriteError(k, ops, failed); !strings.HasPrefix(msg, "concept_batch: ") || !strings.Contains(msg, "concept_history(id)") || !strings.Contains(msg, "rev=<sha>") {
		t.Errorf("rollback failure: %q", msg)
	}
	if msg := batchWriteError(k, ops, errors.New("batch op 0 (x): disk full")); strings.Contains(msg, "concept_history") {
		t.Errorf("a rollback that worked has nothing to undo: %q", msg)
	}

	stale := fmt.Errorf("batch op 0 (manutenzione/test-runbook): %w", okf.ErrStaleWrite)
	if msg := batchWriteError(k, ops, stale); !strings.HasPrefix(msg, "stale_write: ") || !strings.Contains(msg, "current content_hash "+readHash(t, k, "manutenzione/test-runbook")) {
		t.Errorf("stale batch: %q", msg)
	}
}

func TestConceptMove_PartialFailureCarriesTheRecoveryHint(t *testing.T) {
	k, s := namespaceServer(t)
	writeService(t, k, "services/sample", "svc\n")
	failingFS(t, errors.New("injected remove failure"), nil)

	res := callTool(t, s, "concept_move", `{"source_id":"services/sample","target_id":"application-services/sample"}`)
	msg := res.Content[0].Text
	if !res.IsError || !strings.Contains(msg, "already written") || !strings.Contains(msg, "concept_history(id)") || !strings.Contains(msg, "rev=<sha>") {
		t.Errorf("got %q", msg)
	}
}

// The hint names two tools: both must be callable by a client of the agent
// profile, or a message would prescribe a hidden tool.
func TestRecoveryHint_NamesAgentProfileTools(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	s.SetToolsProfile("agent")
	names := listToolNames(t, s)
	for _, tool := range []string{"concept_history", "concept_read", "concept_write"} {
		if !names[tool] {
			t.Errorf("%s is named by recoveryHint but hidden in the agent profile", tool)
		}
		if !strings.Contains(recoveryHint, tool) {
			t.Errorf("recoveryHint no longer names %s: update this test", tool)
		}
	}
}
