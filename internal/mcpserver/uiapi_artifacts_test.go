package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// artifactUIHandler mounts the UI fixture KB with one artifact of each kind,
// a binary and an oversized skill file, an invalid skill and an MCP descriptor. Tokens: "whole" reads the whole KB, "narrow" only the
// "visible" Map.
func artifactUIHandler(t *testing.T) http.Handler {
	t.Helper()
	k := uiFixtureKB(t, "docs")
	write := func(rel string, data []byte, mode os.FileMode) {
		t.Helper()
		full := filepath.Join(k.Root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	write("skills/review/SKILL.md", []byte("---\nname: review\ndescription: Reviews a change\n---\n# Review\n"), 0o644)
	write("skills/review/logo.bin", []byte{0xff, 0xfe, 0x00, 0x01}, 0o644)
	write("skills/review/big.md", []byte(strings.Repeat("a", artifactMaxFileSize+1)), 0o644)
	write("skills/broken/SKILL.md", []byte("---\nname: other\ndescription: wrong name\n---\n"), 0o644)
	write("agents/triage.md", []byte("---\nname: triage\ndescription: Sorts incoming issues\n---\nBody\n"), 0o644)
	write("hooks/guard/hook.json", []byte(`{"event":"SessionStart"}`), 0o644)
	write("hooks/guard/run.sh", []byte("#!/bin/sh\n"), 0o755)
	write("mcp/tools.json", []byte(`{"type":"stdio","command":"tool"}`), 0o644)
	write("instructions.md", []byte("# House rules\n"), 0o644)
	write("templates/runbook.md", []byte("---\ntype: Runbook\ntitle: Runbook template\n---\n# {{title}}\n"), 0o644)

	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler())
}

func TestUIAPI_ArtifactsListsWhatTheKBShips(t *testing.T) {
	t.Parallel()
	handler := artifactUIHandler(t)
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/artifacts", "whole")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Artifacts []uiArtifact     `json:"artifacts"`
		Counts    map[string]int   `json:"counts"`
		Kinds     []uiArtifactKind `json:"kinds"`
		Issues    []string         `json:"issues"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range body.Kinds {
		if k.Count != body.Counts[k.Kind] {
			t.Errorf("kind %s: count %d, counts[%s] = %d", k.Kind, k.Count, k.Kind, body.Counts[k.Kind])
		}
	}
	var names []string
	byName := map[string]uiArtifact{}
	for _, a := range body.Artifacts {
		names = append(names, a.Kind+"/"+a.Name)
		byName[a.Kind+"/"+a.Name] = a
		for _, f := range a.Files {
			if f.Content != nil || f.Binary || f.Truncated {
				t.Errorf("%s: the list route must not carry file content", f.Path)
			}
		}
	}
	want := "agent/triage hook/guard instructions/instructions mcp/tools skill/review template/runbook"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("artifacts = %s, want %s (bundled skills and the invalid skill excluded)", got, want)
	}
	if body.Counts["skill"] != 1 || body.Counts["template"] != 1 {
		t.Errorf("counts = %v", body.Counts)
	}
	if len(body.Issues) != 1 || !strings.Contains(body.Issues[0], "skills/broken") || strings.HasPrefix(body.Issues[0], "KB ") {
		t.Errorf("issues = %q, want one about skills/broken without the internal KB key", body.Issues)
	}

	skill := byName["skill/review"]
	if skill.Description != "Reviews a change" || skill.ContentHash == "" || skill.Signed == nil || *skill.Signed {
		t.Errorf("skill metadata = %+v", skill)
	}
	if byName["agent/triage"].Description != "Sorts incoming issues" {
		t.Errorf("agent description = %q", byName["agent/triage"].Description)
	}
	if byName["template/runbook"].Description != "Runbook template" || byName["template/runbook"].Signed != nil {
		t.Errorf("template = %+v", byName["template/runbook"])
	}
	// instructions.md reaches clients inside the generated block (D61);
	// a template never leaves the KB.
	if got := byName["instructions/instructions"]; len(got.Clients) == 0 || got.Signed != nil {
		t.Errorf("instructions = %+v, want clients and no signature", got)
	}
	if got := byName["template/runbook"]; len(got.Clients) != 0 {
		t.Errorf("template clients = %+v, want none", got.Clients)
	}
	hookClients := map[string]bool{}
	for _, c := range byName["hook/guard"].Clients {
		hookClients[c.ID] = true
		if c.Name == "" {
			t.Errorf("client %q has no display name", c.ID)
		}
	}
	if !hookClients[string(configurator.ProviderClaudeCode)] || !hookClients[string(configurator.ProviderKiro)] {
		t.Errorf("hook clients = %v", hookClients)
	}
}

// D864: the page lists every kind a KB can hold, whatever it ships, so a reader
// learns what the KB is able to give to its clients. The descriptors come from
// provisioning.Kinds; the server adds the count and the clients.
func TestUIAPI_ArtifactsListsEveryKindEvenWhenTheKBHasNone(t *testing.T) {
	t.Parallel()
	for name, shipsASkill := range map[string]bool{"one skill": true, "nothing": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			k := uiFixtureKB(t, "docs")
			// A KB is born with its instructions.md (D325); the kind must
			// list with a count of zero too.
			if err := os.Remove(filepath.Join(k.Root, "instructions.md")); err != nil {
				t.Fatal(err)
			}
			if shipsASkill {
				dir := filepath.Join(k.Root, "skills", "review")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: review\ndescription: Reviews a change\n---\n# Review\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ts := auth.NewScopedTokenStore([]auth.ScopedToken{
				{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
			})
			multi := NewMultiKBServer("test")
			multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
			multi.EnableWeb(nil)
			rr := getUI(t, ts.Middleware(multi.Handler()), UIAPIPrefix+"/kbs/docs/artifacts", "whole")
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			var body struct {
				Kinds []uiArtifactKind `json:"kinds"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			var order []string
			byKind := map[string]uiArtifactKind{}
			for _, k := range body.Kinds {
				order = append(order, k.Kind)
				byKind[k.Kind] = k
				if _, ok := uiArtifactKinds[k.Kind]; !ok {
					t.Errorf("kind %q is listed but the detail route would refuse it", k.Kind)
				}
				if k.Title == "" || k.KBPath == "" || !strings.Contains(k.Doc, "#") {
					t.Errorf("kind %q lacks its title, path or doc: %+v", k.Kind, k)
				}
			}
			if got, want := strings.Join(order, " "), "skill agent hook mcp instructions template"; got != want {
				t.Fatalf("kinds = %s, want %s (secrets answer from /secrets)", got, want)
			}
			for kind, kinfo := range byKind {
				want := 0
				if kind == "skill" && shipsASkill {
					want = 1
				}
				if kinfo.Count != want {
					t.Errorf("%s count = %d, want %d", kind, kinfo.Count, want)
				}
			}
			if got := byKind["skill"].KBPath; got != "skills/<name>/SKILL.md" {
				t.Errorf("skill kb_path = %q", got)
			}
			if len(byKind["hook"].Clients) == 0 || byKind["hook"].KBOnly {
				t.Errorf("hook = %+v, want clients and not KB-only", byKind["hook"])
			}
			for _, c := range byKind["hook"].Clients {
				if c.Name == "" {
					t.Errorf("hook client %q has no display name", c.ID)
				}
			}
			if tpl := byKind["template"]; len(tpl.Clients) != 0 || !tpl.KBOnly {
				t.Errorf("template = %+v, want KB-only with no client", tpl)
			}
			// An empty list, not null: the UI maps over it.
			if !strings.Contains(rr.Body.String(), `"kb_only":true,"count":0,"clients":[]`) {
				t.Errorf("a KB-only kind must send clients as [] (and count 0): %s", rr.Body.String())
			}
		})
	}
}

// The KB-only kinds have no row in the destination matrix, so BuildManifest
// does not scan them: their layout is held by the scanner the UI and the tools
// use. A KBPath that stops matching it would send readers to a folder nothing
// reads (D864). The synced kinds are checked in internal/provisioning.
func TestKinds_KBOnlyPathsAreWhatTheScannersFind(t *testing.T) {
	t.Parallel()
	for _, info := range provisioning.Kinds() {
		if !info.KBOnly {
			continue
		}
		t.Run(info.Kind, func(t *testing.T) {
			k := uiFixtureKB(t, "docs")
			rel := strings.NewReplacer("<name>", "sample", "<slug>", "sample").Replace(info.KBPath)
			full := filepath.Join(k.Root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("---\ntype: Note\ntitle: Sample\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			switch info.Kind {
			case "template":
				slugs, err := listTemplateSlugs(k)
				if err != nil || strings.Join(slugs, ",") != "sample" {
					t.Fatalf("a file at %s: listTemplateSlugs = %v, %v", info.KBPath, slugs, err)
				}
			case "secret":
				files, err := uiSecretsList(k)
				if err != nil || len(files) != 1 || files[0].Path != rel {
					t.Fatalf("a file at %s: uiSecretsList = %+v, %v", info.KBPath, files, err)
				}
			default:
				t.Fatalf("kind %q is KB-only and has no scanner in this test: add one", info.Kind)
			}
		})
	}
}

func TestUIAPI_ArtifactCarriesBoundedTextContent(t *testing.T) {
	t.Parallel()
	handler := artifactUIHandler(t)
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/artifact?kind=skill&name=review", "whole")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var a uiArtifact
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	files := map[string]uiArtifactFile{}
	for _, f := range a.Files {
		files[f.Path] = f
	}
	if f := files["skills/review/SKILL.md"]; f.Content == nil || !strings.Contains(*f.Content, "# Review") {
		t.Errorf("SKILL.md = %+v", f)
	}
	if f := files["skills/review/logo.bin"]; !f.Binary || f.Content != nil || f.Size != 4 {
		t.Errorf("binary file = %+v", f)
	}
	if f := files["skills/review/big.md"]; !f.Truncated || f.Content != nil || f.Size != artifactMaxFileSize+1 {
		t.Errorf("oversized file = %+v", f)
	}
}

func TestUIAPI_ArtifactsAreWholeKBResources(t *testing.T) {
	t.Parallel()
	handler := artifactUIHandler(t)
	for _, path := range []string{"/kbs/docs/artifacts", "/kbs/docs/artifact?kind=skill&name=review"} {
		if rr := getUI(t, handler, UIAPIPrefix+path, "narrow"); rr.Code != http.StatusNotFound {
			t.Errorf("narrow %s: status %d, want 404", path, rr.Code)
		}
	}
	flag := func(token string) interface{} {
		kbs := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs", token))["kbs"].([]interface{})
		return kbs[0].(map[string]interface{})["artifacts"]
	}
	if got := flag("narrow"); got != false {
		t.Errorf("narrow artifacts flag = %v, want false", got)
	}
	if got := flag("whole"); got != true {
		t.Errorf("whole artifacts flag = %v, want true", got)
	}
}

func TestUIAPI_ArtifactRequestErrors(t *testing.T) {
	t.Parallel()
	handler := artifactUIHandler(t)
	for _, tc := range []struct {
		query, field string
		status       int
	}{
		{"name=review", "kind", http.StatusBadRequest},
		{"kind=widget&name=review", "kind", http.StatusBadRequest},
		{"kind=skill", "name", http.StatusBadRequest},
		{"kind=skill&name=nope", "", http.StatusNotFound},
		{"kind=mcp&name=absent", "", http.StatusNotFound},
		{"kind=skill&name=broken", "", http.StatusNotFound},
	} {
		rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/artifact?"+tc.query, "whole")
		if rr.Code != tc.status {
			t.Errorf("%s: status %d, want %d", tc.query, rr.Code, tc.status)
			continue
		}
		if tc.field != "" {
			if got := decodeUI(t, rr)["error"].(map[string]interface{})["field"]; got != tc.field {
				t.Errorf("%s: field %v, want %s", tc.query, got, tc.field)
			}
		}
	}
	req := httptest.NewRequest(http.MethodPost, UIAPIPrefix+"/kbs/docs/artifacts", nil)
	req.Header.Set("Authorization", "Bearer whole")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", rr.Code)
	}
}

func TestUIAPI_ArtifactsRefuseASymlinkedTree(t *testing.T) {
	t.Parallel()
	k := uiFixtureKB(t, "docs")
	if err := os.MkdirAll(filepath.Join(k.Root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(k.Root, "skills", "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	handler := auth.NewTokenStore(nil).Middleware(multi.Handler())
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/artifacts", "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", rr.Code, rr.Body.String())
	}
	if code := decodeUI(t, rr)["error"].(map[string]interface{})["code"]; code != uiCodeInternal {
		t.Errorf("error code = %v", code)
	}
}

// TestUIAPI_ArtifactsIncludesFindings: the Artifacts panel carries the artifact
// lint findings (D316) — counted for the whole KB, attached to the artifact
// whose file they name.
func TestUIAPI_ArtifactsIncludesFindings(t *testing.T) {
	t.Parallel()
	handler := artifactUIHandler(t)
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/artifacts", "whole")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Artifacts     []uiArtifact   `json:"artifacts"`
		FindingCounts map[string]int `json:"finding_counts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// skills/broken is left out of the list, but its finding still counts.
	if body.FindingCounts["skill_invalid"] < 1 {
		t.Errorf("finding_counts = %v, want skill_invalid >= 1", body.FindingCounts)
	}
	for _, a := range body.Artifacts {
		if a.Kind == "hook" && a.Name == "guard" {
			if len(a.Findings) == 0 || a.Findings[0].Check != "hook_invalid" {
				t.Errorf("hook/guard findings = %+v, want hook_invalid", a.Findings)
			}
		}
		if a.Kind == "skill" && a.Name == "review" && len(a.Findings) != 0 {
			t.Errorf("a healthy skill carries no findings: %+v", a.Findings)
		}
	}
	// The detail route carries them too.
	rr = getUI(t, handler, UIAPIPrefix+"/kbs/docs/artifact?kind=hook&name=guard", "whole")
	var detail uiArtifact
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Findings) == 0 {
		t.Errorf("detail findings = %+v", detail.Findings)
	}
}
