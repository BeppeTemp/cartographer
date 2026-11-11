package atlasext

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

type fakeRoutes map[string]bool

func (f fakeRoutes) IsPublicGET(p string) bool { return f[p] }

var public = fakeRoutes{"/api/v1/kbs/kb-a/sources": true, "/api/v1/kbs": true}

const goodView = `title: Sources
icon: sources
source:
  route: /api/v1/kbs/{kb}/sources
  items: sources
renderer: table
columns:
  - {key: name, label: Name}
  - {key: updated, label: Updated, format: relative}
`

const goodApp = "title: Board\nurl: https://board.example.com/\n"

func TestGoodDeclarations(t *testing.T) {
	v, probs := ParseView([]byte(goodView))
	if len(probs) != 0 || v.Scope != "kb" || v.Limit != 100 || v.Columns[0].Format != "text" {
		t.Fatalf("%v %+v", probs, v)
	}
	if p := Validate("views/s.yaml", v, public); len(p) != 0 {
		t.Fatal(p)
	}
	for _, u := range []string{"http://localhost:8080/x", "http://127.0.0.1/", "http://[::1]:3000/"} {
		if _, probs := ParseApp([]byte("title: A\nurl: " + u + "\n")); len(probs) != 0 {
			t.Errorf("%s: %v", u, probs)
		}
	}
}

func TestViewProblems(t *testing.T) {
	with := func(old, new string) string { return strings.Replace(goodView, old, new, 1) }
	cases := []struct{ name, in, want string }{
		{"unknown key", goodView + "extra: 1\n", `unknown key "extra"`},
		{"alias", "title: &a x\nother: *a\n", "aliases"},
		{"two documents", goodView + "---\ntitle: b\n", "more than one"},
		{"9 KiB", goodView + "description: " + strings.Repeat("x", 9<<10) + "\n", "over the"},
		{"route absolute", with("/api/v1/kbs/{kb}/sources", "https://example.com/x"), "source.route"},
		{"route query", with("/api/v1/kbs/{kb}/sources", "/api/v1/kbs?x=1"), "source.route"},
		{"route dotdot", with("/api/v1/kbs/{kb}/sources", "/api/v1/../x"), "source.route"},
		{"route placeholder", with("/api/v1/kbs/{kb}/sources", "/api/v1/{id}"), "source.route"},
		{"renderer", with("renderer: table", "renderer: iframe"), "renderer"},
		{"format html", with("format: relative", "format: html"), "format"},
		{"no columns", strings.Split(goodView, "columns:")[0], "columns"},
		{"column slash", with("key: name", "key: a/b"), "columns[0].key"},
		{"column bracket", with("key: name", "key: \"a[0]\""), "columns[0].key"},
		{"icon", with("icon: sources", "icon: skull"), "icon"},
		{"scope", goodView + "scope: all\n", "scope"},
		{"limit", goodView + "limit: 201\n", "limit"},
		{"query brace", goodView[:strings.Index(goodView, "renderer")] + "  query: {a: \"{x}\"}\n" + goodView[strings.Index(goodView, "renderer"):], "source.query.a"},
		{"limit zero", goodView + "limit: 0\n", "limit: 1 to 200"},
		{"limit type", goodView + "limit: abc\n", "limit: "},
		{"nested type", with("{key: name, label: Name}", "\n    key: name\n    label: [x]"), "columns[0].label"},
		{"title multiline", with("title: Sources", "title: \"a\\nb\""), "title"},
	}
	for _, tc := range cases {
		_, probs := ParseView([]byte(tc.in))
		if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, probs, tc.want)
		}
	}
}

func TestListTakesOneColumn(t *testing.T) {
	_, probs := ParseView([]byte(strings.Replace(goodView, "renderer: table", "renderer: list", 1)))
	if len(probs) != 1 || !strings.Contains(probs[0], "columns") {
		t.Fatal(probs)
	}
}

func TestValidateRefusesWhatIsNotPublic(t *testing.T) {
	for _, r := range []string{"/api/ui/v1/kbs", "/mcp", "/api/v1/kbs/{kb}/secrets"} {
		v, probs := ParseView([]byte(strings.Replace(goodView, "/api/v1/kbs/{kb}/sources", r, 1)))
		if len(probs) != 0 {
			t.Fatal(probs)
		}
		got := Validate("views/x.yaml", v, public)
		if len(got) != 1 || !strings.HasPrefix(got[0], "views/x.yaml: source.route:") {
			t.Errorf("%s: %v", r, got)
		}
	}
	v, _ := ParseView([]byte(goodView))
	if len(Validate("f", v, nil)) != 1 {
		t.Error("no route set must refuse")
	}
}

func TestAppProblems(t *testing.T) {
	for _, c := range []struct{ u, want string }{
		{"javascript:alert(1)", "https"},
		{"http://example.com", "loopback"},
		{"https://user:pw@example.com/", "user information"},
		{"https://example.com/#x", "fragment"},
		{"https:///x", "host"},
		{"ftp://example.com/", "https"},
	} {
		u, want := c.u, c.want
		_, probs := ParseApp([]byte("title: A\nurl: \"" + u + "\"\n"))
		if len(probs) == 0 || !strings.Contains(strings.Join(probs, ""), want) {
			t.Errorf("%s: %v want %q", u, probs, want)
		}
	}
	if _, probs := ParseApp([]byte("title: A\nurl: https://example.com/\nicon: skull\n")); len(probs) != 1 {
		t.Errorf("%v", probs)
	}
	if _, probs := ParseApp([]byte("title: " + strings.Repeat("x", 41) + "\nurl: https://example.com/\n")); len(probs) != 1 {
		t.Errorf("%v", probs)
	}
}

const manifest = "schema: 2\nname: team-board\nversion: 1.0.0\ndisplay_name: Team board\ndescription: Views.\ncartographer: \">=0.29.0\"\ncomponents:\n  atlas:\n    sdk: {views: 1, apps: 1}\n"

func tf(p, c string) core.TreeFile { return core.TreeFile{Path: p, Content: []byte(c)} }

func read(files ...core.TreeFile) (core.Plugin, []string) {
	return core.ReadTree(append([]core.TreeFile{tf(core.ManifestFile, manifest)}, files...), core.Options{RunningVersion: "0.29.0", Side: core.SideAtlas})
}

func TestTreeLoadsAndPreviews(t *testing.T) {
	p, probs := read(tf("views/sources.yaml", goodView), tf("apps/board.yaml", goodApp))
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	var b strings.Builder
	previewView(&b, p.Contributions["views"][0])
	previewApp(&b, p.Contributions["apps"][0])
	for _, w := range []string{`view "sources" (table) reads GET /api/v1/kbs/{kb}/sources`, `app "board" opens https://board.example.com/`, "data only: nothing runs"} {
		if !strings.Contains(b.String(), w) {
			t.Errorf("preview lacks %q: %s", w, b.String())
		}
	}
}

func TestTreeProblems(t *testing.T) {
	_, probs := read(tf("views/a.yaml", goodView), tf("views/b.yaml", goodView), tf("views/Bad.yaml", goodView),
		core.TreeFile{Path: "apps/x.yaml", Content: []byte(goodApp), Executable: true}, tf("apps/sub/y.yaml", goodApp))
	j := strings.Join(probs, "\n")
	for _, w := range []string{"views/b.yaml: title:", "views/Bad.yaml: expected a file", "apps/x.yaml: the atlas side holds data", "apps/sub/y.yaml: expected a file"} {
		if !strings.Contains(j, w) {
			t.Errorf("missing %q in\n%s", w, j)
		}
	}
}

// The atlas side's point set is exactly {views, apps}.
func TestAtlasPointSet(t *testing.T) {
	var got []string
	for _, p := range core.RegisteredPoints() {
		if p.Side == core.SideAtlas {
			got = append(got, p.Dir)
		}
	}
	if strings.Join(got, ",") != "apps,views" {
		t.Fatalf("atlas points = %v", got)
	}
}

func TestIconsExistInTheAtlas(t *testing.T) {
	src, err := os.ReadFile("../../web/src/components/Icon.tsx")
	if err != nil {
		t.Skip("frontend sources are not present in this tree")
	}
	for _, i := range Icons {
		if !regexp.MustCompile(`\| "` + i + `"`).Match(src) {
			t.Errorf("icon %q is not an IconName in Icon.tsx", i)
		}
	}
}
