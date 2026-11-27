package mcpserver

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// diffFixture commits visible/page three times, at t1 < t2 < t3, and returns
// the handler plus the three texts and times.
func diffFixture(t *testing.T) (h http.Handler, texts [3]string, at [3]time.Time) {
	t.Helper()
	k := uiFixtureKB(t, "docs")
	if !gitx.IsRepo(k.Root) {
		t.Skip("git not in PATH")
	}
	base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Hour)
	commit := func(i int, body string) {
		t.Helper()
		text := "---\ntype: Note\ntitle: Page\n---\n" + body
		texts[i] = text
		at[i] = base.Add(time.Duration(i) * time.Hour)
		if err := os.WriteFile(filepath.Join(k.DataRoot(), "visible", "page.md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		d := at[i].Format(time.RFC3339)
		if err := gitx.Commit(k.Root, fmt.Sprintf("step %d", i), "Tester", "tester@example.test",
			"GIT_AUTHOR_DATE="+d, "GIT_COMMITTER_DATE="+d); err != nil {
			t.Fatal(err)
		}
	}
	commit(0, "one\n")
	commit(1, "two\n")
	commit(2, "three\n")
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Principal: "reader", Token: "hiddenonly", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"hidden"}}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler()), texts, at
}

func diffURL(id string, from time.Time, to string) string {
	q := url.Values{"id": {id}, "from": {from.Format(time.RFC3339)}}
	if to != "" {
		q.Set("to", to)
	}
	return UIAPIPrefix + "/kbs/docs/diff?" + q.Encode()
}

// diffFile returns the files entry of a response at index i.
func diffFile(t *testing.T, got map[string]interface{}, i int) map[string]interface{} {
	t.Helper()
	files, _ := got["files"].([]interface{})
	if i >= len(files) {
		t.Fatalf("no file %d in %v", i, got)
	}
	return files[i].(map[string]interface{})
}

func TestUIAPI_DiffSpans(t *testing.T) {
	t.Parallel()
	h, texts, at := diffFixture(t)

	// The span covers the third commit only.
	got := decodeUI(t, getUI(t, h, diffURL("visible/page", at[2].Add(-time.Minute), ""), "whole"))
	f := diffFile(t, got, 0)
	if len(got["files"].([]interface{})) != 1 || f["before"] != texts[1] || f["after"] != texts[2] || f["status"] != "modified" ||
		f["path"] != "data/visible/page.md" || got["commits"] != float64(1) || got["truncated"] != false || got["path"] != f["path"] {
		t.Fatalf("2nd edit span = %v", got)
	}
	if got["before"] != nil || got["after"] != nil {
		t.Fatalf("top-level texts are gone: %v", got)
	}
	if b := got["base"].(map[string]interface{}); b["subject"] == "" || b["sha"] == "" {
		t.Fatalf("base = %v", b)
	}

	// A span that starts before the creation has no base.
	got = decodeUI(t, getUI(t, h, diffURL("visible/page", at[0].Add(-time.Hour), at[0].Format(time.RFC3339)), "whole"))
	f = diffFile(t, got, 0)
	if f["before"] != "" || f["after"] != texts[0] || f["status"] != "added" || got["base"] != nil || got["commits"] != float64(1) {
		t.Fatalf("creation span = %v", got)
	}

	// An empty span: no file.
	got = decodeUI(t, getUI(t, h, diffURL("visible/page", at[1].Add(10*time.Minute), at[1].Add(20*time.Minute).Format(time.RFC3339)), "whole"))
	if len(got["files"].([]interface{})) != 0 || got["commits"] != float64(0) || got["base"] == nil {
		t.Fatalf("empty span = %v", got)
	}
}

// An expanded concept lists every file of its folder that changed, main first.
func TestUIAPI_DiffExpandedConcept(t *testing.T) {
	t.Parallel()
	k := uiFixtureKB(t, "docs") // visible/owner is expanded (index.md)
	if !gitx.IsRepo(k.Root) {
		t.Skip("git not in PATH")
	}
	base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Hour)
	dir := filepath.Join(k.DataRoot(), "visible", "owner")
	step := func(i int, files map[string][]byte) {
		t.Helper()
		for name, data := range files {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		d := base.Add(time.Duration(i) * time.Hour).Format(time.RFC3339)
		if err := gitx.Commit(k.Root, fmt.Sprintf("step %d", i), "Tester", "tester@example.test",
			"GIT_AUTHOR_DATE="+d, "GIT_COMMITTER_DATE="+d); err != nil {
			t.Fatal(err)
		}
	}
	step(0, map[string][]byte{"index.md": []byte("---\ntype: Note\ntitle: Owner\n---\nv1\n")})
	step(1, map[string][]byte{
		"index.md":           []byte("---\ntype: Note\ntitle: Owner\n---\nv2\n"),
		"system-state.jsonl": []byte("{\"a\":1}\n"),
		"shot.bin":           {0x89, 'P', 0, 1, 2},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	h := auth.NewTokenStore(nil).Middleware(multi.Handler())

	got := decodeUI(t, getUI(t, h, diffURL("visible/owner", base.Add(30*time.Minute), ""), ""))
	files := got["files"].([]interface{})
	if len(files) != 3 || got["path"] != "data/visible/owner/index.md" {
		t.Fatalf("files = %v", got)
	}
	main, state, bin := diffFile(t, got, 0), diffFile(t, got, 1), diffFile(t, got, 2)
	if main["path"] != "data/visible/owner/index.md" || main["status"] != "modified" ||
		main["before"] == main["after"] || main["binary"] != false {
		t.Fatalf("main = %v", main)
	}
	if state["path"] != "data/visible/owner/shot.bin" && state["path"] != "data/visible/owner/system-state.jsonl" {
		t.Fatalf("satellites unsorted: %v", state)
	}
	if bin["path"] != "data/visible/owner/system-state.jsonl" || bin["status"] != "added" || bin["after"] != "{\"a\":1}\n" {
		t.Fatalf("jsonl = %v", bin)
	}
	if state["path"] != "data/visible/owner/shot.bin" || state["status"] != "added" || state["binary"] != true || state["after"] != "" {
		t.Fatalf("binary asset = %v", state)
	}
}

func TestUIAPI_DiffRefusals(t *testing.T) {
	t.Parallel()
	h, _, at := diffFixture(t)
	for _, path := range []string{
		UIAPIPrefix + "/kbs/docs/diff?id=visible/page&from=yesterday",
		UIAPIPrefix + "/kbs/docs/diff?id=visible/page",
		UIAPIPrefix + "/kbs/docs/diff?from=" + url.QueryEscape(at[0].Format(time.RFC3339)),
		diffURL("visible/page", at[2], at[0].Format(time.RFC3339)),
	} {
		if rr := getUI(t, h, path, "whole"); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", path, rr.Code)
		}
	}
	missing := getUI(t, h, diffURL("visible/nope", at[0], ""), "whole")
	hidden := getUI(t, h, diffURL("visible/page", at[0], ""), "hiddenonly")
	if missing.Code != http.StatusNotFound || hidden.Code != http.StatusNotFound || missing.Body.String() != hidden.Body.String() {
		t.Fatalf("missing %d %q vs hidden %d %q", missing.Code, missing.Body, hidden.Code, hidden.Body)
	}
}

// A token narrowed by types sees neither the satellites of another type nor the
// assets of their folders, and a map's index.md owns no folder.
func TestUIAPI_DiffFootprintIsFilteredPerFile(t *testing.T) {
	t.Parallel()
	k := uiFixtureKB(t, "docs")
	if !gitx.IsRepo(k.Root) {
		t.Skip("git not in PATH")
	}
	base := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Hour)
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(k.DataRoot(), rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("visible/owner/index.md", "---\ntype: Note\ntitle: Owner\n---\nv2\n")
	write("visible/owner/open.md", "---\ntype: Note\ntitle: Open child\n---\nopen\n")
	write("visible/owner/plain.bin", "plain asset")
	write("visible/owner/hid/index.md", "---\ntype: Runbook\ntitle: Hidden child\n---\nsecret child\n")
	write("visible/owner/hid/pic.bin", "hidden asset")
	write("visible/owner/hid2.md", "---\ntype: Runbook\ntitle: Hidden flat\n---\nsecret flat\n")
	d := base.Format(time.RFC3339)
	if err := gitx.Commit(k.Root, "step", "Tester", "tester@example.test", "GIT_AUTHOR_DATE="+d, "GIT_COMMITTER_DATE="+d); err != nil {
		t.Fatal(err)
	}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Principal: "notes", Token: "notesonly", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Types: []string{"Note"}}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	h := ts.Middleware(multi.Handler())

	paths := func(token string) map[string]bool {
		got := decodeUI(t, getUI(t, h, diffURL("visible/owner", base.Add(-time.Hour), ""), token))
		out := map[string]bool{}
		for _, f := range got["files"].([]interface{}) {
			out[f.(map[string]interface{})["path"].(string)] = true
		}
		return out
	}
	all := paths("whole")
	if len(all) != 6 {
		t.Fatalf("whole token: %v", all)
	}
	got := paths("notesonly")
	want := map[string]bool{
		"data/visible/owner/index.md":  true,
		"data/visible/owner/open.md":   true,
		"data/visible/owner/plain.bin": true,
	}
	if len(got) != len(want) {
		t.Fatalf("type-restricted token sees %v, want %v", got, want)
	}
	for p := range want {
		if !got[p] {
			t.Fatalf("missing %s in %v", p, got)
		}
	}
	// No sibling of the map leaks in either.
	for p := range all {
		if p == "data/visible/alpha.md" || p == "data/visible/beta.md" {
			t.Fatalf("sibling concept in the footprint: %s", p)
		}
	}
}

func TestUIDiffFootprintOfAMapIndexIsEmpty(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]string{
		"data/visible/index.md":           "",
		"data/visible/alpha.md":           "",
		"data/index.md":                   "",
		"data/visible/owner/index.md":     "data/visible/owner/",
		"data/visible/owner/hid/index.md": "data/visible/owner/hid/",
	} {
		if got := uiDiffFootprint(p); got != want {
			t.Errorf("uiDiffFootprint(%q) = %q, want %q", p, got, want)
		}
	}
}

// The span is [from, to]: a change committed exactly at from counts, and the
// base is the newest revision strictly before it.
func TestUIAPI_DiffSpanBoundaryIsInclusive(t *testing.T) {
	t.Parallel()
	h, texts, at := diffFixture(t)
	got := decodeUI(t, getUI(t, h, diffURL("visible/page", at[1], at[1].Format(time.RFC3339)), "whole"))
	f := diffFile(t, got, 0)
	if got["commits"] != float64(1) || f["before"] != texts[0] || f["after"] != texts[1] {
		t.Fatalf("[at1, at1] = %v", got)
	}
}
