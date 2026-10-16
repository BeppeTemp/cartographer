package mcpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// referenceUIHandler mounts two KBs: "docs" (the fixture plus placeholders,
// a contradiction, a source, an asset, a journal and a skill) and "work-kb",
// which declares one of docs' keys with another default.
func referenceUIHandler(t *testing.T) http.Handler {
	t.Helper()
	k := uiFixtureKB(t, "docs")
	put := func(root, rel, content string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(k.DataRoot(), "visible/alpha.md", "---\ntype: Runbook\ntitle: Alpha\nstatus: active\nprovenance:\n  - visible/src-a\n---\nClone {{repo:app}} to {{path:work}} and see {{repo:undeclared}}. [beta](beta.md)\n")
	put(k.DataRoot(), "hidden/secret.md", "---\ntype: Runbook\ntitle: Secret\n---\nUses {{repo:hidden-only}}.\n")
	put(k.DataRoot(), "visible/clash.md", "---\ntype: Contradiction\ntitle: Alpha vs Beta\nresolution_status: open\ncontradiction_kind: factual\ninvolves:\n  - visible/alpha\n  - visible/beta\n---\nThey disagree.\n")
	put(k.DataRoot(), "hidden/clash.md", "---\ntype: Contradiction\ntitle: Hidden clash\nresolution_status: open\ncontradiction_kind: factual\n---\nx\n")
	put(k.DataRoot(), "visible/src-a.md", "---\ntype: Source\ntitle: Source A\nsource_kind: document\ningest_status: pending\n---\nx\n")
	put(k.DataRoot(), "visible/owner/notes.txt", "hello\n")
	put(k.DataRoot(), "hidden/pack/index.md", "---\ntype: Note\ntitle: Pack\n---\nx\n")
	put(k.DataRoot(), "hidden/pack/data.txt", "x\n")
	put(k.DataRoot(), "visible/api.md", "---\ntype: Service\ntitle: API\nstatus: active\n---\nx\n")
	put(k.DataRoot(), "hidden/vault.md", "---\ntype: Service\ntitle: Vault\n---\nx\n")
	// No body link to its successor, as the supersede tool leaves it: the
	// relation is the frontmatter alone (D243).
	put(k.DataRoot(), "visible/old.md", "---\ntype: Note\ntitle: Old\nsuperseded_by: visible/new\n---\nReplaced.\n")
	put(k.DataRoot(), "visible/new.md", "---\ntype: Note\ntitle: New\n---\nx\n")
	put(k.DataRoot(), "hidden/older.md", "---\ntype: Note\ntitle: Older\nsuperseded_by: visible/new\n---\nSee [new](../visible/new.md).\n")
	// The visible Map's contract names the hidden one: a narrow caller must
	// not learn that name from it.
	put(k.DataRoot(), "visible/_map.md", "---\ntype: Map\ntitle: Visible\nkind: map\npromote_to: hidden\nwork_map: hidden\n---\n# Visible\n")
	put(k.DataRoot(), "visible/gone.md", "---\ntype: Note\ntitle: Gone\nsuperseded_by: hidden/secret\n---\nSee [secret](../hidden/secret.md).\n")
	put(k.DataRoot(), "log.md", "# Log\n\n## [2026-01-02] second\n\nbody two\n\n## [2026-01-01] first\n\nbody one\n")
	put(k.Root, "paths.yaml", "paths:\n  work:\n    description: Working dir\n    default: $HOME/work\nrepos:\n  app:\n    description: The app\n    default: $HOME/src/app\n    remote: example.com/org/app\n  unused:\n    description: Never cited\n")
	put(k.Root, "skills/probe/SKILL.md", "---\nname: probe\ndescription: Reads the fixture\n---\nRun in {{path:work}} and {{repo:from-skill}}.\n")

	other := uiFixtureKB(t, "work-kb")
	put(other.Root, "paths.yaml", "repos:\n  app:\n    description: Same key\n    default: $HOME/other/app\n")

	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}, {KB: "work-kb"}}}},
		{Principal: "boss", Token: "admin", Policy: auth.Policy{Admin: true}},
		{Principal: "reader", Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{MCPAllowlist: []provisioning.MCPAllowlistEntry{}}) })
	multi.MountKB("work-kb", func(s *Server) { RegisterKBTools(s, other, Deps{MCPAllowlist: []provisioning.MCPAllowlistEntry{}}) })
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler())
}

func placeholderKeys(t *testing.T, body map[string]interface{}) map[string]map[string]interface{} {
	t.Helper()
	out := map[string]map[string]interface{}{}
	for _, raw := range body["keys"].([]interface{}) {
		e := raw.(map[string]interface{})
		out[e["kind"].(string)+":"+e["key"].(string)] = e
	}
	return out
}

func strList(v interface{}) []string {
	var out []string
	for _, x := range v.([]interface{}) {
		out = append(out, x.(string))
	}
	return out
}

func TestUIAPI_PlaceholdersWholeKB(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	body := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/placeholders", "whole"))
	if body["registry"] != true {
		t.Fatalf("registry = %v, want true", body["registry"])
	}
	keys := placeholderKeys(t, body)

	app := keys["repo:app"]
	if app == nil || app["declared"] != true || app["remote"] != "example.com/org/app" || app["default"] != "$HOME/src/app" || app["description"] != "The app" {
		t.Fatalf("repo:app = %v", app)
	}
	if got := strList(app["concepts"]); len(got) != 1 || got[0] != "visible/alpha" {
		t.Fatalf("repo:app concepts = %v", got)
	}
	// A cross-KB conflict: work-kb declares the same key with another default.
	conf := app["conflicts"].([]interface{})
	if len(conf) != 1 || conf[0].(map[string]interface{})["kb"] != "work-kb" || conf[0].(map[string]interface{})["default"] != "$HOME/other/app" {
		t.Fatalf("conflicts = %v", app["conflicts"])
	}
	// An artifact citation.
	work := keys["path:work"]
	if got := strList(work["artifacts"]); len(got) != 1 || got[0] != "skill/probe" {
		t.Fatalf("path:work artifacts = %v", got)
	}
	// Cited, never declared.
	for _, id := range []string{"repo:undeclared", "repo:from-skill", "repo:hidden-only"} {
		if e := keys[id]; e == nil || e["declared"] != false {
			t.Fatalf("%s = %v, want declared:false", id, e)
		}
	}
	// Declared, never cited: still listed, with empty (not null) lists.
	if e := keys["repo:unused"]; e == nil || len(e["concepts"].([]interface{})) != 0 || len(e["artifacts"].([]interface{})) != 0 {
		t.Fatalf("repo:unused = %v", e)
	}
	// Sorted by kind, then key.
	var order []string
	for _, raw := range body["keys"].([]interface{}) {
		e := raw.(map[string]interface{})
		order = append(order, e["kind"].(string)+":"+e["key"].(string))
	}
	if strings.Join(order, ",") != "path:work,repo:app,repo:from-skill,repo:hidden-only,repo:undeclared,repo:unused" {
		t.Fatalf("order = %v", order)
	}
}

func TestUIAPI_PlaceholdersNarrowedTokenSeesOnlyItsOwnCitations(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/placeholders", "narrow")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	for _, leak := range []string{"hidden-only", "hidden/secret", "from-skill", "skill/probe", "Working dir", "example.com", "work-kb"} {
		if strings.Contains(rr.Body.String(), leak) {
			t.Fatalf("narrowed placeholders leaked %q: %s", leak, rr.Body.String())
		}
	}
	body := decodeUI(t, rr)
	keys := placeholderKeys(t, body)
	if body["registry"] != false || keys["repo:app"] == nil || keys["repo:app"]["declared"] != false {
		t.Fatalf("narrowed body = %v", body)
	}
	if got := strList(keys["repo:app"]["concepts"]); len(got) != 1 || got[0] != "visible/alpha" {
		t.Fatalf("concepts = %v", got)
	}
}

func TestUIAPI_Contradictions(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	for token, want := range map[string]int{"whole": 2, "narrow": 1} {
		body := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/contradictions", token))
		list := body["contradictions"].([]interface{})
		if len(list) != want {
			t.Fatalf("%s: %d contradictions, want %d: %v", token, len(list), want, list)
		}
	}
	e := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/contradictions?status=all", "narrow"))["contradictions"].([]interface{})[0].(map[string]interface{})
	if e["id"] != "visible/clash" || e["resolution_status"] != "open" || len(e["involves"].([]interface{})) != 2 {
		t.Fatalf("entry = %v", e)
	}
	if body := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/contradictions?status=resolved", "whole")); len(body["contradictions"].([]interface{})) != 0 {
		t.Fatalf("resolved = %v", body)
	}
}

func TestUIAPI_Maps(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	maps := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/maps", "narrow"))["maps"].([]interface{})
	if len(maps) != 1 || maps[0].(map[string]interface{})["name"] != "visible" {
		t.Fatalf("narrow maps = %v", maps)
	}
	if m := maps[0].(map[string]interface{}); m["promote_to"] != nil || m["work_map"] != nil {
		t.Fatalf("narrow caller learns a hidden Map's name: %v", m)
	}
	for _, m := range decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/maps", "whole"))["maps"].([]interface{}) {
		if m := m.(map[string]interface{}); m["name"] == "visible" && (m["promote_to"] != "hidden" || m["work_map"] != "hidden") {
			t.Fatalf("whole caller misses the contract: %v", m)
		}
	}
	if maps := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/maps", "whole"))["maps"].([]interface{}); len(maps) != 3 {
		t.Fatalf("whole maps = %v", maps)
	}
}

func TestUIAPI_HistoryAndAssetsAreVisibilityChecked(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	hist := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/history?id=visible/alpha", "narrow"))
	if hist["id"] != "visible/alpha" || hist["revisions"] == nil {
		t.Fatalf("history = %v", hist)
	}
	if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/history", "whole"); rr.Code != http.StatusBadRequest {
		t.Fatalf("history without id: %d", rr.Code)
	}
	missing := getUI(t, h, UIAPIPrefix+"/kbs/docs/history?id=visible/nope", "whole")
	hidden := getUI(t, h, UIAPIPrefix+"/kbs/docs/history?id=hidden/secret", "narrow")
	if missing.Code != http.StatusNotFound || hidden.Code != http.StatusNotFound || missing.Body.String() != hidden.Body.String() {
		t.Fatalf("missing %d %q vs hidden %d %q", missing.Code, missing.Body, hidden.Code, hidden.Body)
	}

	assets := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/assets?id=visible/owner", "narrow"))
	list := assets["assets"].([]interface{})
	if assets["id"] != "visible/owner" || len(list) != 1 || list[0].(map[string]interface{})["path"] != "notes.txt" {
		t.Fatalf("assets = %v", assets)
	}
	if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/assets?id=visible/beta", "narrow"); rr.Code != http.StatusBadRequest {
		t.Fatalf("non-expanded assets: %d", rr.Code)
	}
	if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/assets?id=hidden/pack", "narrow"); rr.Code != http.StatusNotFound {
		t.Fatalf("hidden assets: %d", rr.Code)
	}
	if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/assets?id=hidden/pack", "whole"); rr.Code != http.StatusOK {
		t.Fatalf("whole assets: %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUIAPI_JournalIsAWholeKBResource(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/journal", "narrow"); rr.Code != http.StatusNotFound {
		t.Fatalf("narrow journal: %d", rr.Code)
	}
	body := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/journal", "whole"))
	text, _ := body["text"].(string)
	if !strings.Contains(text, "first") || !strings.Contains(text, "second") {
		t.Fatalf("journal text = %q", text)
	}
	one := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/journal?n=1", "whole"))["text"].(string)
	if strings.Contains(one, "first") || !strings.Contains(one, "second") {
		t.Fatalf("n=1 text = %q", one)
	}
}

func TestUIAPI_Sources(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	list := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/sources", "narrow"))["sources"].([]interface{})
	if len(list) != 1 {
		t.Fatalf("sources = %v", list)
	}
	s := list[0].(map[string]interface{})
	if s["id"] != "visible/src-a" || s["ingest_status"] != "pending" || s["cited_by"].(float64) != 1 {
		t.Fatalf("source = %v", s)
	}
	if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/sources?status=bogus", "whole"); rr.Code != http.StatusBadRequest {
		t.Fatalf("bogus status: %d", rr.Code)
	}
}

func TestUIAPI_ServicesAreFilteredByVisibility(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	ids := func(token string) []string {
		var out []string
		for _, raw := range decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/services", token))["services"].([]interface{}) {
			out = append(out, raw.(map[string]interface{})["id"].(string))
		}
		return out
	}
	if got := ids("narrow"); len(got) != 1 || got[0] != "visible/api" {
		t.Fatalf("narrow services = %v", got)
	}
	if got := ids("whole"); len(got) != 2 {
		t.Fatalf("whole services = %v", got)
	}
}

func TestUIAPI_ConflictsAreWholeKB(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/conflicts", "narrow"); rr.Code != http.StatusNotFound {
		t.Fatalf("narrow conflicts: %d", rr.Code)
	}
	if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/conflicts", "whole"); rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatalf("whole conflicts: %d %q", rr.Code, rr.Body.String())
	}
}

func TestUIAPI_AuditIsAdminOnly(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	for _, token := range []string{"whole", "narrow"} {
		if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/audit", token); rr.Code != http.StatusNotFound {
			t.Fatalf("%s audit: %d", token, rr.Code)
		}
	}
	body := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/audit", "admin"))
	if body["enabled"] != false {
		t.Fatalf("admin audit without a sink = %v", body)
	}
}

func TestUIAPI_Whoami(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	who := decodeUI(t, getUI(t, h, UIAPIPrefix+"/whoami", "narrow"))
	rules := who["permissions"].([]interface{})
	if who["id"] != "reader" || who["admin"] != false || len(rules) != 1 || rules[0].(map[string]interface{})["kb"] != "docs" {
		t.Fatalf("whoami = %v", who)
	}
	if who := decodeUI(t, getUI(t, h, UIAPIPrefix+"/whoami", "admin")); who["admin"] != true {
		t.Fatalf("admin whoami = %v", who)
	}
}

func TestUIAPI_ConceptCarriesTheSupersedeChain(t *testing.T) {
	t.Parallel()
	h := referenceUIHandler(t)
	next := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/concept?id=visible/new", "narrow"))
	// The hidden predecessor is not disclosed.
	if got := next["supersedes"].([]interface{}); len(got) != 1 || got[0] != "visible/old" {
		t.Fatalf("supersedes = %v, want [visible/old]", got)
	}
	old := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/concept?id=visible/old", "narrow"))
	if old["superseded_by"] != "visible/new" {
		t.Fatalf("superseded_by = %v", old["superseded_by"])
	}
	gone := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/concept?id=visible/gone", "narrow"))
	if gone["superseded_by"] != "" {
		t.Fatalf("hidden successor named: %v", gone["superseded_by"])
	}
}
