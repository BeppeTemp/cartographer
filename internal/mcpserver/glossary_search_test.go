package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D276: search expands a query through the KB's glossary.yaml.

const searchGlossary = `terms:
  - canonical: Zephyr Controller
    aliases: [ZCL, zephyrctl]
    forbidden: [ZephyrController]
`

func writeSearchGlossary(t *testing.T, k *kb.KB) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(k.Root, kb.GlossaryFile), []byte(searchGlossary), 0o644); err != nil {
		t.Fatal(err)
	}
}

type searchOut struct {
	Count      int      `json:"count"`
	ExpandedTo []string `json:"expanded_to"`
	Results    []struct {
		ID string `json:"id"`
	} `json:"results"`
}

func decodeSearch(t *testing.T, s *Server, query string) searchOut {
	t.Helper()
	var out searchOut
	if err := json.Unmarshal([]byte(searchText(t, s, query)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (o searchOut) has(id string) bool {
	for _, r := range o.Results {
		if r.ID == id {
			return true
		}
	}
	return false
}

func TestSearch_GlossaryExpandsAliasesBothWays(t *testing.T) {
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		writeTestConcept(t, k, "arch/canonical-page", "The Zephyr Controller schedules the nightly jobs.\n")
		writeTestConcept(t, k, "arch/alias-page", "Restart zcl after an upgrade.\n")
		writeSearchGlossary(t, k)

		byAlias := decodeSearch(t, s, "zephyrctl")
		if !byAlias.has("arch/canonical-page") || !byAlias.has("arch/alias-page") {
			t.Fatalf("alias query: %+v", byAlias)
		}
		if strings.Join(byAlias.ExpandedTo, "|") != "zephyr controller|zcl" {
			t.Fatalf("expanded_to = %q", byAlias.ExpandedTo)
		}

		byCanonical := decodeSearch(t, s, "Zephyr Controller")
		if !byCanonical.has("arch/alias-page") {
			t.Fatalf("canonical query did not reach the alias page: %+v", byCanonical)
		}
	})
}

func TestSearch_GlossaryForbiddenDoesNotExpand(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	writeTestConcept(t, k, "arch/canonical-page", "The Zephyr Controller schedules the nightly jobs.\n")
	writeSearchGlossary(t, k)
	out := decodeSearch(t, s, "ZephyrController")
	if len(out.ExpandedTo) != 0 || out.has("arch/canonical-page") {
		t.Fatalf("a forbidden term expanded: %+v", out)
	}
}

// No glossary: the response has no expanded_to, exactly as before D276.
func TestSearch_NoGlossaryNoExpansion(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	writeTestConcept(t, k, "arch/alias-page", "Restart zcl after an upgrade.\n")
	text := searchText(t, s, "zcl")
	if strings.Contains(text, "expanded_to") {
		t.Fatalf("expanded_to without a glossary: %s", text)
	}
	if out := decodeSearch(t, s, "zephyrctl"); out.Count != 0 {
		t.Fatalf("no glossary, yet the alias matched: %+v", out)
	}
}

// A miss is recorded only when every variant came back empty.
func TestSearch_GlossaryMissOnlyWhenEveryVariantMisses(t *testing.T) {
	k, s, l := missFixture(t)
	writeSearchGlossary(t, k)
	// No page names the term in any form: every variant misses.
	if out := decodeSearch(t, s, "zephyrctl"); out.Count != 0 || len(out.ExpandedTo) == 0 {
		t.Fatalf("expected an expanded miss: %+v", out)
	}
	// Only the canonical is written: the alias query now hits through it.
	writeTestConcept(t, k, "arch/canonical-page", "The Zephyr Controller schedules the nightly jobs.\n")
	if out := decodeSearch(t, s, "zephyrctl"); !out.has("arch/canonical-page") {
		t.Fatalf("variant did not hit: %+v", out)
	}
	got := missLines(t, l)
	if len(got) != 1 || got[0].Query != "zephyrctl" {
		t.Fatalf("recorded = %+v, want one miss, from before the page existed", got)
	}
}

// glossary.yaml is authored through the artifact tools, strictly validated.
func TestArtifactTools_GlossaryWrite(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	resps := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_write", map[string]any{"path": "glossary.yaml", "content": searchGlossary})})
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("valid glossary.yaml rejected: %+v", tr.Content)
	}
	resps = runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_write", map[string]any{
		"path": "glossary.yaml", "content": "terms:\n  - canonical: A\n    aliases: [x]\n    forbidden: [x]\n",
		"if_match": sha256Hex([]byte(searchGlossary)),
	})})
	if tr := decodeToolResult(t, resps[1]); !tr.IsError || !containsText(tr, "both an alias and forbidden") {
		t.Fatalf("invalid glossary.yaml accepted: %+v", tr.Content)
	}
	if data, _ := os.ReadFile(filepath.Join(k.Root, kb.GlossaryFile)); string(data) != searchGlossary {
		t.Fatalf("a rejected write changed glossary.yaml: %q", data)
	}
}
