package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testDashboard = `title: Week
widgets:
  - id: blocked
    type: kanban
    title: Blocked
    width: 2
    filter:
      status: [blocked]
    group_by: field:customer
  - id: howto
    type: note
    text: Read [[notes/howto]] first.
`

// AC-kbdash-1
func TestValidateDashboardReportsEveryProblem(t *testing.T) {
	t.Parallel()
	in := "colour: red\nwidgets:\n  - id: a\n    type: stat\n  - id: a\n    type: stat\n  - id: wide\n    type: stat\n    width: 5\n"
	err := ValidateDashboard([]byte(in))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{`unknown field "colour"`, `widgets[1] (id "a"): duplicate widget id`, `widgets[2] (id "wide"): width 5`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestValidateDashboardAcceptsAndRejects(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{testDashboard, "", "widgets: []\n", "title: Empty\n"} {
		if err := ValidateDashboard([]byte(ok)); err != nil {
			t.Errorf("ValidateDashboard(%q): %v", ok, err)
		}
	}
	cases := []struct{ name, in, want string }{
		{"unknown widget key", "widgets:\n  - id: a\n    type: stat\n    colour: red\n", `unknown field "colour"`},
		{"unknown filter key", "widgets:\n  - id: a\n    type: stat\n    filter:\n      state: x\n", `filter: unknown field "state"`},
		{"error key refused", "widgets:\n  - id: a\n    type: stat\n    error: boom\n", `unknown field "error"`},
		{"bad type", "widgets:\n  - id: a\n    type: pie\n", `type "pie"`},
		{"bad icon", "widgets:\n  - id: a\n    type: stat\n    icon: rocket\n", `icon "rocket"`},
		{"bad accent", "widgets:\n  - id: a\n    type: stat\n    accent: '#fff'\n", `accent "#fff"`},
		{"bad id", "widgets:\n  - id: Bad_ID\n    type: stat\n", "id \"Bad_ID\""},
		{"text outside a note", "widgets:\n  - id: a\n    type: stat\n    text: x\n", "text belongs to a note"},
		{"note without text", "widgets:\n  - id: a\n    type: note\n", "a note needs text"},
		{"bad group_by", "widgets:\n  - id: a\n    type: kanban\n    group_by: colour\n", `group_by "colour"`},
		{"width not a number", "widgets:\n  - id: a\n    type: stat\n    width: wide\n", "widgets[0]"},
		{"text on a stat, blank", "widgets:\n  - id: a\n    type: stat\n    text: \" \"\n", "text belongs to a note"},
		{"duplicate top key", "title: a\ntitle: b\n", `field "title" declared twice`},
		{"duplicate widgets key", "widgets: []\nwidgets: []\n", `field "widgets" declared twice`},
		{"too many columns", "widgets:\n  - id: a\n    type: list\n    columns: [a, b, c, d, e, f, g, h, i]\n", "9 columns, the cap is 8"},
		{"not a mapping", "- a\n- b\n", "not a mapping"},
		{"not yaml", "a: [", "not valid YAML"},
		{"too big", "title: " + strings.Repeat("x", MaxDashboardBytes), "the cap is"},
	}
	for _, c := range cases {
		err := ValidateDashboard([]byte(c.in))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

func TestValidateDashboardCaps(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("widgets:\n")
	for i := 0; i <= MaxDashboardWidgets; i++ {
		b.WriteString("  - id: w" + strings.Repeat("a", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "\n    type: stat\n")
	}
	err := ValidateDashboard([]byte(b.String()))
	if err == nil || !strings.Contains(err.Error(), "the cap is 40") {
		t.Fatalf("want the widget cap, got %v", err)
	}
}

func TestValidDashboardName(t *testing.T) {
	t.Parallel()
	for _, n := range []string{"week", "a", "q3-plan", "0x", strings.Repeat("a", 48)} {
		if !ValidDashboardName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	for _, n := range []string{"", "-a", "A", "a_b", "a.b", "con", "nul", "aux", "prn", "com1", "lpt9", "../x", "a/b", strings.Repeat("a", 49)} {
		if ValidDashboardName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

// AC-kbdash-2
func TestParseDashboardTolerant(t *testing.T) {
	t.Parallel()
	in := "widgets:\n  - id: ok\n    type: stat\n  - id: bad\n    type: pie\n    colour: red\n  - id: fine\n    type: note\n    text: hi\n"
	d, err := ParseDashboard([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Widgets) != 3 {
		t.Fatalf("want 3 widgets, got %d", len(d.Widgets))
	}
	if d.Widgets[0].Problem != "" || d.Widgets[2].Problem != "" || d.Widgets[2].Text != "hi" {
		t.Errorf("good widgets damaged: %+v", d.Widgets)
	}
	if !strings.Contains(d.Widgets[1].Problem, `type "pie"`) || !strings.Contains(d.Widgets[1].Problem, `"colour"`) {
		t.Errorf("bad widget Problem = %q", d.Widgets[1].Problem)
	}
	if _, err := ParseDashboard([]byte("a: [")); err == nil {
		t.Error("a file that is not YAML must be an error")
	}
}

func TestEncodeDashboardRoundTrip(t *testing.T) {
	t.Parallel()
	d, err := ParseDashboard([]byte(testDashboard))
	if err != nil {
		t.Fatal(err)
	}
	d.Widgets[0].Problem = "never written"
	out, err := EncodeDashboard(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "never written") || strings.Contains(string(out), "error") {
		t.Errorf("Problem leaked into the file:\n%s", out)
	}
	if err := ValidateDashboard(out); err != nil {
		t.Fatalf("encoded dashboard is invalid: %v\n%s", err, out)
	}
	again, err := EncodeDashboard(mustParse(t, out))
	if err != nil || string(again) != string(out) {
		t.Errorf("encoding is not canonical:\n%s\n%s (%v)", out, again, err)
	}
	empty, _ := EncodeDashboard(Dashboard{})
	if string(empty) != "widgets: []\n" {
		t.Errorf("empty dashboard encodes as %q", empty)
	}
}

func mustParse(t *testing.T, b []byte) Dashboard {
	t.Helper()
	d, err := ParseDashboard(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// AC-kbdash-4: package-level, no *KB and no temp dir.
func TestDashboardFunctionsNeedNoKB(t *testing.T) {
	t.Parallel()
	data := []byte(testDashboard)
	if err := ValidateDashboard(data); err != nil {
		t.Fatal(err)
	}
	d, err := ParseDashboard(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeDashboard(d); err != nil {
		t.Fatal(err)
	}
	if DashboardHash(data) == "" || DashboardHash(nil) != "" || DashboardHash(data) != DashboardHash([]byte(testDashboard)) {
		t.Error("DashboardHash: want a stable hash and \"\" for nil")
	}
	if !ValidDashboardName("week") {
		t.Error("ValidDashboardName")
	}
}

func TestDashboardListsAreCopies(t *testing.T) {
	t.Parallel()
	DashboardWidgetTypes()[0] = "x"
	DashboardIcons()[0] = "x"
	DashboardAccents()[0] = "x"
	if DashboardWidgetTypes()[0] == "x" || DashboardIcons()[0] == "x" || DashboardAccents()[0] == "x" {
		t.Error("the closed lists must be returned as copies")
	}
}

func TestReadAndListDashboards(t *testing.T) {
	t.Parallel()
	k := &KB{Root: t.TempDir()}
	if names, err := k.ListDashboards(); err != nil || len(names) != 0 {
		t.Fatalf("no folder: %v %v", names, err)
	}
	if _, err := k.ReadDashboard("week"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing dashboard: want ErrNotExist, got %v", err)
	}
	dir := filepath.Join(k.Root, DashboardsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"week.yaml", "alpha.yaml", "Bad.yaml", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(testDashboard), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	names, err := k.ListDashboards()
	if err != nil || strings.Join(names, ",") != "alpha,week" {
		t.Fatalf("ListDashboards = %v %v", names, err)
	}
	got, err := k.ReadDashboard("week")
	if err != nil || string(got) != testDashboard {
		t.Fatalf("ReadDashboard: %q %v", got, err)
	}
	if _, err := k.ReadDashboard("../glossary"); err == nil {
		t.Error("a path-like name must be refused")
	}
}

// AC-kbdash-5
func TestReadDashboardRefusesSymlink(t *testing.T) {
	t.Parallel()
	k := &KB{Root: t.TempDir()}
	dir := filepath.Join(k.Root, DashboardsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "d.yaml")
	if err := os.WriteFile(target, []byte(testDashboard), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.yaml")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if _, err := k.ReadDashboard("link"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("want a symlink error, got %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := k.ReadDashboard("folder"); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Errorf("want a not-regular error, got %v", err)
	}
	if names, _ := k.ListDashboards(); len(names) != 0 {
		t.Errorf("a symlink and a folder are not dashboards, got %v", names)
	}
}
