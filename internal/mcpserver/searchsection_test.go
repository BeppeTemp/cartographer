package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// sectionPage is a page of several "##" sections, each padded to ~700 bytes,
// where only the Rollback section holds the term.
func sectionPage(typ, title, term string) string {
	pad := strings.Repeat("filler words about nothing in particular. ", 17)
	return fmt.Sprintf("---\ntype: %s\ntitle: %s\n---\n# %s\n\nIntro.\n\n## Install\n%s\n\n## Rollback\n%s %s\n\n## Contacts\n%s\n\n## Notes\n%s\n",
		typ, title, title, pad, term, pad, pad, pad)
}

type sectionHit struct {
	ID      string `json:"id"`
	Snippet string `json:"snippet"`
	Section string `json:"section"`
	Read    string `json:"read"`
	Bytes   int64  `json:"bytes"`
}

func searchSectionHits(t *testing.T, s *Server, query string) map[string]sectionHit {
	t.Helper()
	var out struct {
		Results []sectionHit `json:"results"`
	}
	if err := json.Unmarshal([]byte(searchText(t, s, query)), &out); err != nil {
		t.Fatal(err)
	}
	byID := map[string]sectionHit{}
	for _, h := range out.Results {
		byID[h.ID] = h
	}
	return byID
}

// A hit names the "##" section that matched, as concept_read accepts it, and
// excerpts it; a page that must be read whole says so and names no section; a
// page where a section saves nothing is today's hit (D1207).
func TestSearchNamesTheSectionThatMatched(t *testing.T) {
	t.Parallel()
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		if err := k.CreateMap("ops", "Ops", "map", nil); err != nil {
			t.Fatal(err)
		}
		if err := k.CreateMap("incidents", "Incidents", "journal", nil); err != nil {
			t.Fatal(err)
		}
		callOK(t, s, "map_update", `{"map":"ops","read_whole":["Runbook"]}`)

		bigSection := "## Only\n" + strings.Repeat("zxqnearly filler text here. ", 100)
		files := map[string]string{
			"ops/guide.md":     sectionPage("Note", "Gateway Guide", "zxqterm"),
			"ops/runbook.md":   sectionPage("Runbook", "Gateway Runbook", "zxqrun"),
			"incidents/e1.md":  sectionPage("Incident", "Outage", "zxqjournal"),
			"ops/short.md":     "---\ntype: Note\ntitle: Short\n---\n## Rollback\nzxqshort is small\n",
			"ops/dominant.md":  "---\ntype: Note\ntitle: Dominant\n---\n" + bigSection + "\n## Tiny\nx\n",
			"ops/titleonly.md": "---\ntype: Note\ntitle: Qwertyuniq Guide\n---\n" + strings.Repeat("## S\nunrelated\n", 1) + strings.Repeat("filler words about nothing. ", 100) + "\n",
		}
		for rel, c := range files {
			writeKBFile(t, k, rel, c)
		}

		// Multi-section page: the section reads back and is cheaper than the page.
		h, ok := searchSectionHits(t, s, "zxqterm")["ops/guide"]
		if !ok || h.Section != "## Rollback" || h.Read != "" {
			t.Fatalf("ops/guide hit = %+v, want section ## Rollback", h)
		}
		if !strings.Contains(h.Snippet, "zxqterm") {
			t.Errorf("snippet %q must excerpt the section", h.Snippet)
		}
		read := callOK(t, s, "concept_read", fmt.Sprintf(`{"id":"ops/guide","section":%q}`, h.Section))
		if !strings.Contains(read, "zxqterm") {
			t.Fatalf("concept_read section %q lacks the matched term: %s", h.Section, read)
		}
		if int64(len(read)) >= h.Bytes {
			t.Errorf("section read costs %d bytes, the page %d: naming it saves nothing", len(read), h.Bytes)
		}
		// The heading is the contract with concept_read: it must resolve.
		body := sectionBodyOf(files["ops/guide.md"])
		if _, ok := okf.ExtractSection(body, h.Section); !ok {
			t.Errorf("section %q does not resolve through okf.ExtractSection", h.Section)
		}

		// Read-whole: a journal entry, and a type listed in the map's read_whole.
		for _, c := range []struct{ id, term string }{{"incidents/e1", "zxqjournal"}, {"ops/runbook", "zxqrun"}} {
			h := searchSectionHits(t, s, c.term)[c.id]
			if h.Read != "whole" || h.Section != "" {
				t.Errorf("%s hit = %+v, want read whole and no section", c.id, h)
			}
		}

		// Nothing saved: a short page, a dominant section, a title-only match.
		for _, c := range []struct{ id, q string }{
			{"ops/short", "zxqshort"}, {"ops/dominant", "zxqnearly"}, {"ops/titleonly", "Qwertyuniq"},
		} {
			h, ok := searchSectionHits(t, s, c.q)[c.id]
			if !ok || h.Section != "" || h.Read != "" {
				t.Errorf("%s hit = %+v ok=%v, want no section and no read", c.id, h, ok)
			}
		}
	})
}

func sectionBodyOf(content string) string {
	_, body, _ := okf.SplitFrontmatter(content)
	return body
}

// A concept the caller cannot see leaks neither a section nor a snippet.
func TestSearchSectionNeverLeaksAHiddenConcept(t *testing.T) {
	s := graphToolKB(t, map[string]string{
		"ops/open.md":      sectionPage("Note", "Open", "zxqshared"),
		"hidden/secret.md": sectionPage("Note", "Secret", "zxqshared zxqsecretonly"),
	})
	text, isErr := callJSON(t, s, narrowCtx, "search", `{"query":"zxqshared"}`)
	if isErr {
		t.Fatal(text)
	}
	if strings.Contains(text, "hidden/") || strings.Contains(text, "zxqsecretonly") {
		t.Fatalf("narrowed search leaked a hidden concept: %s", text)
	}
	if !strings.Contains(text, `"section":"## Rollback"`) {
		t.Fatalf("the visible hit should name its section: %s", text)
	}
	text, _ = callJSON(t, s, adminCtx, "graph_context", `{"ids":["ops/open"]}`)
	if strings.Contains(text, `"section"`) || strings.Contains(text, `"read":"whole"`) {
		t.Errorf("graph_context must not carry search's section fields: %s", text)
	}
}

func TestMapReadWholeRoundTrip(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	callOK(t, s, "map_create", `{"name":"proc","title":"Proc","read_whole":["Runbook","Procedure"]}`)
	list := func() []string {
		var infos []struct {
			Name      string   `json:"name"`
			ReadWhole []string `json:"read_whole"`
		}
		if err := json.Unmarshal([]byte(callOK(t, s, "map_list", `{}`)), &infos); err != nil {
			t.Fatal(err)
		}
		for _, i := range infos {
			if i.Name == "proc" {
				return i.ReadWhole
			}
		}
		t.Fatal("proc missing from map_list")
		return nil
	}
	if got := strings.Join(list(), ","); got != "Procedure,Runbook" {
		t.Fatalf("after map_create read_whole = %q", got)
	}
	callOK(t, s, "map_update", `{"map":"proc","read_whole":["Runbook"]}`)
	if got := strings.Join(list(), ","); got != "Runbook" {
		t.Fatalf("after map_update (no reason: dropping an entry is no loosening) = %q", got)
	}
	callOK(t, s, "map_update", `{"map":"proc","read_whole":[]}`)
	if got := list(); len(got) != 0 {
		t.Fatalf("after [] read_whole = %v, want removed", got)
	}
}

func TestMapReadWholeMalformedIsReported(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	writeMapDescriptor(t, k.DataRoot(), "bad", "---\ntype: Map\nkind: map\ntitle: Bad\nread_whole: Runbook\n---\n# Bad\n")
	c, err := k.ReadMapContract("bad")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.ReadWhole) != 0 || len(c.Malformed) != 1 || c.Malformed[0].Key != "read_whole" {
		t.Fatalf("contract = %+v, want read_whole reported as malformed", c)
	}
}
