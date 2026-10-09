package lint

import (
	"strings"
	"testing"
)

// D357: the drift audit's checks and fixes.

func driftRun(t *testing.T, files map[string]string) []Finding {
	t.Helper()
	k := tempKB(t)
	for rel, content := range files {
		writeFile(t, k.DataRoot(), rel, content)
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return findings
}

func TestTitleH1Mismatch_PicksTheGoodSide(t *testing.T) {
	// The reproduction: a decorative title over a real heading.
	f := findingsOf(driftRun(t, map[string]string{
		"arch/a.md": "---\ntype: Note\ntitle: \"🚀\"\n---\n# Server B real\n",
	}), "title_h1_mismatch")
	if len(f) != 1 || f[0].Fix == nil || f[0].Fix.Kind != FixSetValue || f[0].Fix.Field != "title" || f[0].Fix.To != "Server B real" {
		t.Fatalf("decorative title: %+v", f)
	}
	// Both fail: judgement, no fix.
	f = findingsOf(driftRun(t, map[string]string{
		"arch/a.md": "---\ntype: Note\ntitle: \"🚀\"\n---\n# Deploy 🚀 now\n",
	}), "title_h1_mismatch")
	if len(f) != 1 || f[0].Fix != nil {
		t.Fatalf("both bad: %+v", f)
	}
	// A good title keeps winning.
	f = findingsOf(driftRun(t, map[string]string{
		"arch/a.md": "---\ntype: Note\ntitle: Server B\n---\n# Something else\n",
	}), "title_h1_mismatch")
	if len(f) != 1 || f[0].Fix == nil || f[0].Fix.Kind != FixSyncH1 || f[0].Fix.To != "Server B" {
		t.Fatalf("good title: %+v", f)
	}
}

func TestMissingTitle_Fix(t *testing.T) {
	cases := []struct{ name, page, want string }{
		{"h1", "---\ntype: Note\n---\n# Real heading\n", "Real heading"},
		{"decorative h1 falls to the stem", "---\ntype: Note\n---\n# 🚀\n", "My page"},
		{"stem", "---\ntype: Note\n---\nno heading\n", "My page"},
	}
	for _, c := range cases {
		f := findingsOf(driftRun(t, map[string]string{"arch/my-page.md": c.page}), "missing_title")
		if len(f) != 1 || f[0].Fix == nil || f[0].Fix.Kind != FixSetValue || f[0].Fix.Field != "title" || f[0].Fix.To != c.want {
			t.Errorf("%s: %+v", c.name, f)
		}
	}
	// A non-slug stem and no usable heading: nothing to derive.
	f := findingsOf(driftRun(t, map[string]string{"arch/My Page.md": "---\ntype: Note\n---\nno heading\n"}), "missing_title")
	if len(f) != 1 || f[0].Fix != nil {
		t.Errorf("non-slug stem: %+v", f)
	}
}

func TestRepeatedLink(t *testing.T) {
	files := map[string]string{
		"arch/a.md": "---\ntype: Note\ntitle: A\n---\n# A\n\nSee [b](b.md) and again [the b page](b.md) here; also `[b](b.md)` in code.\n\nA new paragraph may link [b](b.md) once.\n\n```\n[b](b.md) [b](b.md)\n```\n",
		"arch/b.md": "---\ntype: Note\ntitle: B\n---\n# B\n",
	}
	f := findingsOf(driftRun(t, files), "repeated_link")
	if len(f) != 1 || f[0].Severity != SevInfo || f[0].Path != "arch/a.md" || f[0].Fix == nil || f[0].Fix.Kind != FixUnlinkRepeat || f[0].Fix.Field != "b.md" {
		t.Fatalf("findings: %+v", f)
	}
	// Different targets, or one per paragraph or list item: silent.
	for _, body := range []string{"[b](b.md) and [c](c.md)\n", "- [b](b.md)\n- [b](b.md)\n", "[b](b.md)\n\n[b](b.md)\n", "![x](i.png) ![x](i.png)\n"} {
		files["arch/a.md"] = "---\ntype: Note\ntitle: A\n---\n# A\n\n" + body
		if f := findingsOf(driftRun(t, files), "repeated_link"); len(f) != 0 {
			t.Errorf("%q flagged: %+v", body, f)
		}
	}
}

func TestUnlinkRepeats(t *testing.T) {
	body := "x [b](b.md) y [the b page](b.md) z [c](c.md) [c](c.md)\n\n[b](b.md) [b](b.md)\n"
	got, ok := UnlinkRepeats(body, "b.md")
	want := "x [b](b.md) y the b page z [c](c.md) [c](c.md)\n\n[b](b.md) b\n"
	if !ok || got != want {
		t.Fatalf("got %q ok=%v, want %q", got, ok, want)
	}
	if again, ok := UnlinkRepeats(got, "b.md"); ok || again != got {
		t.Errorf("not idempotent: %q", again)
	}
}

func TestUnknownType(t *testing.T) {
	tmpl := "---\ntype: Service\ntitle: Service\n---\n# {{title}}\n"
	page := func(typ string) string { return "---\ntype: " + typ + "\ntitle: P\n---\n# P\n" }
	// No templates and no strict map: no palette, silent.
	if f := findingsOf(driftRun(t, map[string]string{"arch/p.md": page("WhateverIWant")}), "unknown_type"); len(f) != 0 {
		t.Fatalf("no palette: %+v", f)
	}
	k := tempKB(t)
	writeFile(t, k.Root, "templates/service.md", tmpl)
	writeFile(t, k.DataRoot(), "arch/a.md", page("service"))
	writeFile(t, k.DataRoot(), "arch/b.md", page("Gizmo"))
	writeFile(t, k.DataRoot(), "arch/c.md", page("Service"))
	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(f, "unknown_type")
	if len(got) != 2 {
		t.Fatalf("findings: %+v", got)
	}
	if got[0].Path != "arch/a.md" || got[0].Fix == nil || got[0].Fix.Field != "type" || got[0].Fix.To != "Service" {
		t.Errorf("case variant: %+v", got[0])
	}
	if got[1].Path != "arch/b.md" || got[1].Fix != nil {
		t.Errorf("unknown: %+v", got[1])
	}
}

func TestUnknownType_StrictMapPalette(t *testing.T) {
	f := findingsOf(driftRun(t, map[string]string{
		"strict/_map.md": "---\ntype: Map\nkind: map\ntitle: S\nontology_mode: strict\nconcept_types: [Alpha, Beta]\n---\n# S\n",
		"other/x.md":     "---\ntype: Gamma\ntitle: X\n---\n# X\n",
	}), "unknown_type")
	if len(f) != 1 || f[0].Path != "other/x.md" {
		t.Fatalf("findings: %+v", f)
	}
}

func TestValueCaseVariant(t *testing.T) {
	page := func(status string) string {
		return "---\ntype: Note\ntitle: P\nstatus: " + status + "\n---\n# P\n"
	}
	files := map[string]string{
		"arch/a.md": page("active"), "arch/b.md": page("active"), "arch/c.md": page("Active"),
		"arch/d.md": page("done"),
	}
	f := findingsOf(driftRun(t, files), "value_case_variant")
	if len(f) != 1 || f[0].Path != "arch/c.md" || f[0].Fix == nil || f[0].Fix.To != "active" || f[0].Fix.Field != "status" {
		t.Fatalf("majority: %+v", f)
	}
	// A tie: both sides reported, none fixed.
	delete(files, "arch/b.md")
	f = findingsOf(driftRun(t, files), "value_case_variant")
	if len(f) != 2 || f[0].Fix != nil || f[1].Fix != nil {
		t.Fatalf("tie: %+v", f)
	}
	// A map whose contract constrains status owns it (invalid_field_value).
	files["arch/_map.md"] = "---\ntype: Map\nkind: map\ntitle: A\nfield_values.status: [active, done]\n---\n# A\n"
	if f = findingsOf(driftRun(t, files), "value_case_variant"); len(f) != 0 {
		t.Fatalf("constrained: %+v", f)
	}
}

func TestUnmappedFolder(t *testing.T) {
	files := map[string]string{
		"loose/a.md":          "---\ntype: Note\ntitle: A\n---\n# A\n",
		"mapped/_map.md":      "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n",
		"mapped/b.md":         "---\ntype: Note\ntitle: B\n---\n# B\n",
		"mapped/dossier/x.md": "---\ntype: Note\ntitle: X\n---\n# X\n",
		"empty-dir/.keep":     "",
	}
	f := findingsOf(driftRun(t, files), "unmapped_folder")
	if len(f) != 1 || f[0].Path != "loose" || f[0].Fix == nil || f[0].Fix.Kind != FixScaffoldMap || f[0].Fix.Field != "loose" || f[0].Fix.To != "Loose" {
		t.Fatalf("findings: %+v", f)
	}
	if CheckAcceptability("unmapped_folder") != AcceptNone {
		t.Error("unmapped_folder must not be acceptable")
	}
}

func TestStrayFile(t *testing.T) {
	files := map[string]string{
		"arch/a.md":                "---\ntype: Note\ntitle: A\n---\n# A\n",
		"arch/notes.txt":           "stray",
		"arch/dossier/index.md":    "---\ntype: Note\ntitle: D\n---\n# D\n",
		"arch/dossier/diagram.png": "asset",
		"arch/.DS_Store":           "junk",
		"arch/cache.pyc":           "junk",
		"top.txt":                  "stray",
	}
	f := findingsOf(driftRun(t, files), "stray_file")
	var paths []string
	for _, x := range f {
		paths = append(paths, x.Path)
		if x.Fix != nil || x.Severity != SevWarning {
			t.Errorf("%+v", x)
		}
	}
	if strings.Join(paths, ",") != "arch/notes.txt,top.txt" {
		t.Fatalf("paths: %v", paths)
	}
}
