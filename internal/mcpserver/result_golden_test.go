package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The tool results are compact JSON (D1185). This golden test is what proves
// the compaction changed nothing but whitespace: every golden under
// testdata/results is the answer of the code that still indented, and the
// current answer must equal json.Compact of it byte for byte.
//
//	go test ./internal/mcpserver -run TestToolResultGolden -update
//
// rewrites the goldens. concept_read-full and -with_content are the old
// answers minus frontmatter_raw, the one intended change (D1185). Only do it for an intended change of a result's
// content, and say so in the PR; the compact form is never the golden.
var (
	goldenSHA  = regexp.MustCompile(`"[0-9a-f]{40}"`)
	goldenTime = regexp.MustCompile(`"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})"`)
)

// normaliseGolden is the one normaliser, applied to both sides: commit SHAs,
// timestamps and the temp KB root become fixed tokens.
func normaliseGolden(text, root string) string {
	text = strings.ReplaceAll(text, strings.ReplaceAll(root, `\`, `\\`), "<root>")
	text = strings.ReplaceAll(text, root, "<root>")
	text = goldenSHA.ReplaceAllString(text, `"<sha>"`)
	return goldenTime.ReplaceAllString(text, `"<time>"`)
}

func goldenKBFiles() map[string]string {
	big := "---\ntype: Note\ntitle: Big\n---\n# Big\n\n## Part one\n\n" + strings.Repeat("filler line about hangars\n", 40) + "\n## Part two\n\nzeppelin details\n"
	files := map[string]string{
		"ops/alpha.md":     "---\ntype: Note\ntitle: Alpha\n---\n# Alpha\n\nZeppelin hangar. [beta](beta.md) [hub](hub.md).\n",
		"ops/beta.md":      "---\ntype: Note\ntitle: Beta\n---\n# Beta\n\nSecond page about the zeppelin. [gamma](gamma.md).\n",
		"ops/gamma.md":     "---\ntype: Note\ntitle: Gamma\n---\n# Gamma\n\nThird page.\n",
		"ops/hub.md":       "---\ntype: Note\ntitle: Hub\n---\n# Hub\n\nEverything links here.\n",
		"ops/big.md":       big,
		"ops/huge.md":      "---\ntype: Note\ntitle: Huge\n---\n# Huge\n\n## Only part\n\n" + strings.Repeat("pad pad pad pad pad pad pad pad\n", conceptReadSizeGuard/32+10),
		"ops/dirigible.md": "---\ntype: Glossary\ntitle: Dirigible\naliases: [airship]\n---\n# Dirigible\n\nSee the zeppelin pages.\n",
	}
	return files
}

type goldenCase struct {
	name string
	tool string
	args string
}

func goldenCases() []goldenCase {
	return []goldenCase{
		{"search-hit", "search", `{"query":"zeppelin"}`}, // the default answer: limit 20, related 5 (D1354)
		{"search-hit-related", "search", `{"query":"zeppelin","related":3,"limit":20}`},
		{"search-miss", "search", `{"query":"nonexistentterm","record_miss":false,"limit":20,"related":5}`},
		{"search-partial", "search", `{"query":"zeppelin nonexistentterm","record_miss":false,"limit":20,"related":5}`},
		{"search-expanded", "search", `{"query":"airship","limit":20,"related":5}`},
		{"concept_read-full", "concept_read", `{"id":"ops/alpha"}`},
		{"concept_read-broken", "concept_read", `{"id":"ops/broken"}`},
		{"concept_read-with_content", "concept_read", `{"id":"ops/alpha","with_content":true}`},
		{"concept_read-section", "concept_read", `{"id":"ops/big","section":"Part two"}`},
		{"concept_read-guard", "concept_read", `{"id":"ops/huge"}`},
		{"concept_read-outline", "concept_read", `{"id":"ops/big","outline":true}`},
		{"concept_read-match", "concept_read", `{"id":"ops/big","match":"zeppelin"}`},
		{"index_get-outline", "index_get", `{"outline":true}`},
		{"index_get-with_hash", "index_get", `{"with_hash":true}`},
		{"concept_list", "concept_list", `{}`},
		{"map_list", "map_list", `{}`},
		{"graph_neighbors", "graph_neighbors", `{"id":"ops/alpha","direction":"both"}`},
		{"graph_context", "graph_context", `{"ids":["ops/beta"]}`},
		{"graph_path", "graph_path", `{"source":"ops/alpha","target":"ops/gamma"}`},
		{"link_suggest", "link_suggest", `{"id":"ops/gamma"}`},
		{"work_list", "work_list", `{}`},
		{"kb_status", "kb_status", `{}`},
		{"kb_review", "kb_review", `{}`},
		{"lint", "lint", `{}`},
		{"validate", "validate", `{}`},
		{"template_list", "template_list", `{}`},
		{"source_list", "source_list", `{}`},
		{"artifact_list", "artifact_list", `{}`},
		{"changes_since", "changes_since", `{"since":"2000-01-01T00:00:00Z"}`},
		{"concept_history", "concept_history", `{"id":"ops/alpha"}`},
		{"concept_write", "concept_write", `{"id":"ops/delta","frontmatter":{"type":"Note","title":"Delta"},"body":"# Delta\n\nNew. [hub](hub.md)\n","index_entry":true}`},
		{"concept_patch", "concept_patch", `{"id":"ops/delta","old_string":"New. ","new_string":"Patched. ","if_match":"{{hash:ops/delta}}"}`},
		{"concept_batch", "concept_batch", `{"operations":[{"op":"write","id":"ops/eps","frontmatter":{"type":"Note","title":"Eps"},"body":"# Eps\n\n[hub](hub.md)\n","index_entry":true}]}`},
		{"log_append", "log_append", `{"entry":"golden"}`},
		{"concept_move", "concept_move", `{"source_id":"ops/eps","target_id":"ops/zeta"}`},
		{"concept_delete", "concept_delete", `{"id":"ops/zeta","if_match":"{{hash:ops/zeta}}"}`},
		{"concept_expand", "concept_expand", `{"id":"ops/gamma"}`},
		{"asset_write", "asset_write", `{"concept_id":"ops/gamma","path":"pic.txt","content":"hello"}`},
		{"asset_list", "asset_list", `{"concept_id":"ops/gamma"}`},
		{"asset_read", "asset_read", `{"concept_id":"ops/gamma","path":"pic.txt"}`},
		{"artifact_write", "artifact_write", `{"path":"skills/demo/SKILL.md","content":"---\nname: demo\ndescription: Use when testing.\n---\n# Demo\n"}`},
		{"map_create", "map_create", `{"name":"archive","title":"Archive"}`},
		{"concept_archive", "concept_archive", `{"id":"ops/delta","if_match":"{{hash:ops/delta}}"}`},
		{"search-archived-fallback", "search", `{"query":"patched","limit":20,"related":5}`},
		{"artifact_read", "artifact_read", `{"path":"skills/demo/SKILL.md"}`},
	}
}

func TestToolResultGolden(t *testing.T) {
	s, k := graphToolKBWithKB(t, goldenKBFiles())
	root := k.DataRoot()
	dir := filepath.Join("testdata", "results")
	if *updateGolden {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range goldenCases() {
		if c.name == "concept_read-broken" {
			// A page whose frontmatter does not parse; kept out of the other cases
			// (lint.Work trips over it, a defect of its own).
			writeKBFile(t, k, "ops/broken.md", "---\ntype: Note\ntitle: [unclosed\n---\n# Broken\n\nbody\n")
		}
		args := c.args
		if i := strings.Index(args, "{{hash:"); i >= 0 {
			id := args[i+len("{{hash:") : i+strings.Index(args[i:], "}}")]
			var rd struct {
				Hash string `json:"content_hash"`
			}
			if err := json.Unmarshal([]byte(mustText(t, s, "concept_read", `{"id":"`+id+`"}`)), &rd); err != nil {
				t.Fatal(err)
			}
			args = strings.Replace(args, "{{hash:"+id+"}}", rd.Hash, 1)
		}
		res := callTool(t, s, c.tool, args)
		checkGolden(t, dir, c.name, res, root)
		if c.name == "concept_read-broken" {
			_ = os.Remove(filepath.Join(root, "ops", "broken.md"))
		}
	}
}

func checkGolden(t *testing.T, dir, name string, res ToolResult, root string) {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("%s: empty result", name)
	}
	text := normaliseGolden(res.Content[0].Text, root)
	if !json.Valid([]byte(text)) {
		// These two answer in plain text, not JSON ("Validation OK: ..." and
		// "deleted <id>"): compaction never touches them, so they have no
		// golden. Any other non-JSON answer is an error or a broken fixture.
		if name != "validate" && name != "concept_delete" {
			t.Fatalf("%s: expected a JSON result, got %.200s", name, text)
		}
		return
	}
	path := filepath.Join(dir, name+".json")
	if *updateGolden {
		// The goldens are stored indented, as the code that indented answered:
		// write that form, never the compact text the tool returns now.
		var indented bytes.Buffer
		if err := json.Indent(&indented, []byte(text), "", "  "); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(indented.Bytes(), '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run with -update on the code that still indents)", name, err)
	}
	want = bytes.TrimRight(want, "\n")
	var compact bytes.Buffer
	if err := json.Compact(&compact, want); err != nil {
		t.Fatalf("%s: golden: %v", name, err)
	}
	if text != compact.String() {
		t.Errorf("%s: not the compact form of the golden\n got: %.300s\nwant: %.300s", name, text, compact.String())
	}
	var a, b any
	_ = json.Unmarshal([]byte(text), &a)
	_ = json.Unmarshal(want, &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("%s: decoded value differs from the golden", name)
	}
}

// The routed connection notice is added by withConnectionNotice, which
// re-marshals the decoded object: it gets its own golden.
func TestToolResultGolden_RoutedConnectionNotice(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-one", "kb-two")
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kb_status","arguments":{"kb":"kb-one"}}}`
	rr := routedPost(t, handler, RoutedMountPath, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	res := decodeToolResult(t, resp)
	// The notice carries the process start time and the temp roots of the KBs;
	// pin the start time through the same normaliser, roots are not in the text.
	checkGolden(t, filepath.Join("testdata", "results"), "routed-kb_status", res, "\x00")
}

// A stored file that happens to be valid JSON is returned byte for byte: the
// compaction lives at construction, not on whatever looks like JSON (D1185).
func TestVerbatimResultsAreNotCompacted(t *testing.T) {
	t.Parallel()
	stored := "{\n  \"a\": 1,\n  \"b\": [1, 2]\n}\n"
	s, k := graphToolKBWithKB(t, map[string]string{"ops/a.md": "x\n"})
	writeKBFile(t, k, "ops/index.md", stored)
	writeKBFile(t, k, "ops/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n")
	if r := callTool(t, s, "concept_expand", `{"id":"ops/a"}`); r.IsError {
		t.Fatal(r.Content[0].Text)
	}
	writeKBFile(t, k, "ops/a/data.json", stored)
	var asset struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(mustText(t, s, "asset_read", `{"concept_id":"ops/a","path":"data.json"}`)), &asset); err != nil || asset.Content != stored {
		t.Errorf("asset_read changed a stored JSON file: %v %q", err, asset.Content)
	}
	res := callTool(t, s, "index_get", `{"path":"ops"}`)
	if res.IsError || res.Content[0].Text != stored {
		t.Errorf("index_get changed a stored file: %q", res.Content[0].Text)
	}
}

// A tool result is built with jsonResult, never with an indenting marshal
// (D1185): the indentation was 18% of a search answer. The one choke point is
// what keeps a later tool from bringing it back.
func TestNoIndentedMarshalInToolResults(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "MarshalIndent") && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				t.Errorf("%s:%d calls MarshalIndent: build a tool result with jsonResult (D1185)", f, i+1)
			}
		}
	}
}
