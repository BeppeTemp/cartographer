package kb

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// AC-cross-kb-links-4
func TestParseCrossLink(t *testing.T) {
	cases := []struct {
		raw       string
		ok        bool
		want      CrossLink
		malformed bool
	}{
		{"ops/x", false, CrossLink{}, false},
		{"kb:kb-b/ops/x", true, CrossLink{KB: "kb-b", ID: "ops/x"}, false},
		{"kb:kb-b/ops/x.md", true, CrossLink{KB: "kb-b", ID: "ops/x"}, false},
		{"kb:kb-b@host/ops/x", true, CrossLink{Server: "host", KB: "kb-b", ID: "ops/x"}, false},
		{"kb:kb b/x", true, CrossLink{KB: "kb b", ID: "x"}, true},
		{"kb:kb-b/", true, CrossLink{KB: "kb-b"}, true},
		{"kb:kb-b", true, CrossLink{KB: "kb-b"}, true},
		{"kb:kb-b/../x", true, CrossLink{KB: "kb-b", ID: "../x"}, true},
		{"kb:kb-b//x", true, CrossLink{KB: "kb-b", ID: "/x"}, true},
		{"kb:kb-b@/x", true, CrossLink{KB: "kb-b", ID: "x"}, true},
		{"kb:/x", true, CrossLink{ID: "x"}, true},
		{"kb:a@s@t/x", true, CrossLink{Server: "s@t", KB: "a", ID: "x"}, true},
		{"kb:a@ s/x", true, CrossLink{Server: " s", KB: "a", ID: "x"}, true},
	}
	for _, c := range cases {
		got, ok := ParseCrossLink(c.raw)
		if ok != c.ok {
			t.Errorf("%q: ok = %v", c.raw, ok)
			continue
		}
		c.want.Malformed = c.malformed
		if got != c.want {
			t.Errorf("%q: got %+v, want %+v", c.raw, got, c.want)
		}
	}
}

// AC-cross-kb-links-6
func TestExtractCrossLinks(t *testing.T) {
	body := "[[kb:kb-b/a|label]] [[kb:kb-b/a#sec]] [[kb:kb-b/b]] [[kb:bad name/x]] [[kb:bad name/x]]\n" +
		"`[[kb:kb-b/code]]`\n```\n[[kb:kb-b/fenced]]\n```\n[md](kb:kb-b/href) [[plain]]\n[[kb:a@/b]] [[kb:a@/b.md]]\n"
	got := ExtractCrossLinks(body)
	want := []CrossLink{{KB: "kb-b", ID: "a"}, {KB: "kb-b", ID: "b"}, {KB: "bad name", ID: "x", Malformed: true},
		{KB: "a", ID: "b", Malformed: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractCrossLinks = %+v, want %+v", got, want)
	}

	k := mustInitKB(t)
	writeConcept(t, k, "p", "[[kb:kb-b/a]]\n")
	if links, err := k.Links(); err != nil || len(links.Out["p"]) != 0 || len(links.In) != 0 {
		t.Errorf("in-KB links of a page holding only a cross link = %v, %v", links, err)
	}
	cross, err := k.CrossLinks()
	if err != nil || !reflect.DeepEqual(cross["p"], []CrossLink{{KB: "kb-b", ID: "a"}}) {
		t.Errorf("CrossLinks = %v, %v", cross, err)
	}
}

func writeConcept(t *testing.T, k *KB, id, body string) {
	t.Helper()
	p := filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\ntype: Note\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mountedPair(t *testing.T, aliases map[string]string) (a, b *KB) {
	t.Helper()
	a, b = mustInitKB(t), mustInitKB(t)
	m := NewMounts(map[string]*KB{"kb-a": a, "kb-b": b}, []string{"kb-a", "kb-b"}, aliases)
	a.SetMounts(m)
	b.SetMounts(m)
	return a, b
}

func TestCrossLinkedKBs(t *testing.T) {
	a, b := mountedPair(t, map[string]string{"old-b": "kb-b"})
	writeConcept(t, a, "p", "[[kb:old-b/x]] [[kb:kb-b/y]] [[kb:kb-a/z]] [[kb:nope/x]] [[kb:kb-b@host/x]] [[kb:bad name/x]]\n")
	got, err := a.CrossLinkedKBs()
	if err != nil || !reflect.DeepEqual(got, []string{"kb-b"}) {
		t.Errorf("CrossLinkedKBs = %v, %v", got, err)
	}
	if got, _ := b.CrossLinkedKBs(); len(got) != 0 {
		t.Errorf("a KB with no cross link: %v", got)
	}
	if got, _ := mustInitKB(t).CrossLinkedKBs(); len(got) != 0 {
		t.Errorf("no mounts: %v", got)
	}
}
