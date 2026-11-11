package branding

import (
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// tokensCSS reads web/src/styles/tokens.css, the source of the surfaces.
func tokensCSS(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "web", "src", "styles", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Surface drift guard: the Go constants are a copy of the stylesheet's values,
// so a palette change in CSS fails here instead of silently voiding the check.
func TestSurfacesMatchTokensCSS(t *testing.T) {
	css := tokensCSS(t)
	light := strings.Index(css, `:root[data-theme="light"] {`)
	if light < 0 {
		t.Fatal("no light block in tokens.css")
	}
	val := func(block, name string) string {
		m := regexp.MustCompile(`(?m)^\s*--` + name + `:\s*(#[0-9a-fA-F]{6});`).FindStringSubmatch(block)
		if m == nil {
			t.Fatalf("--%s not found", name)
		}
		return strings.ToLower(m[1])
	}
	dark, lt := css[:light], css[light:]
	for i, n := range []string{"surface-0", "surface-1", "surface-2"} {
		if got := val(dark, n); got != darkPanels[i] {
			t.Errorf("dark --%s = %s, Go has %s", n, got, darkPanels[i])
		}
		if got := val(lt, n); got != lightPanels[i] {
			t.Errorf("light --%s = %s, Go has %s", n, got, lightPanels[i])
		}
	}
	if val(dark, "text-on-primary") != textOn[0] || val(dark, "text-on-accent") != textOn[0] ||
		val(lt, "text-on-primary") != textOn[1] || val(lt, "text-on-accent") != textOn[1] {
		t.Errorf("--text-on-* differ from %v", textOn)
	}
}

// holds recomputes every audited pair of a theme independently of pairs().
func holds(t *testing.T, th Theme, panels [3]string) {
	t.Helper()
	for _, c := range []string{th.Accent, th.Primary} {
		for _, p := range panels {
			if r := ratio(c, p); r < 4.5 {
				t.Errorf("%s on %s = %.2f < 4.5", c, p, r)
			}
		}
		if r := ratio(c, panels[0]); r < 3 {
			t.Errorf("%s on surface-0 = %.2f < 3", c, r)
		}
	}
	if r := ratio(th.TextOnPrimary, th.Primary); r < 4.5 {
		t.Errorf("text on primary %s on %s = %.2f", th.TextOnPrimary, th.Primary, r)
	}
	if r := ratio(th.TextOnAccent, th.Accent); r < 4.5 {
		t.Errorf("text on accent %s on %s = %.2f", th.TextOnAccent, th.Accent, r)
	}
	if r := ratio(th.TextOnPrimary, th.PrimaryDim); r < 4.5 {
		t.Errorf("text on primary %s on dim %s = %.2f", th.TextOnPrimary, th.PrimaryDim, r)
	}
	for _, p := range panels {
		if r := ratio(th.PrimaryDim, p); r < 4.5 {
			t.Errorf("dim %s on %s = %.2f < 4.5", th.PrimaryDim, p, r)
		}
	}
}

func TestDerive(t *testing.T) {
	type tc struct {
		accent                      string
		darkAdjusted, lightAdjusted bool
		wantErr                     string
	}
	// #365d50 is the shipped light accent: it moves on dark. #b5d6bd, the shipped
	// dark accent, needs 43 points on light, within maxShift.
	cases := []tc{
		{"#365d50", true, false, ""},
		{"#91b9a0", false, true, ""},
		{"#808080", true, true, ""},
		{"#ffee00", false, true, ""},
		{"#b5d6bd", false, true, ""},
		{"#ffffcc", false, false, "cannot be shown on the light theme within 45 points of lightness (the nearest colour that passes is #"},
	}
	for _, c := range cases {
		p, err := Derive(c.accent)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) || !strings.Contains(err.Error(), "pick a darker colour") {
				t.Errorf("%s: err = %v", c.accent, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.accent, err)
			continue
		}
		if p.Dark.Adjusted != c.darkAdjusted || p.Light.Adjusted != c.lightAdjusted {
			t.Errorf("%s: adjusted dark %v light %v", c.accent, p.Dark.Adjusted, p.Light.Adjusted)
		}
		holds(t, p.Dark, darkPanels)
		holds(t, p.Light, lightPanels)
	}
}

// The nearest colour the refusal names is itself acceptable.
func TestRefusalNamesAPassingColour(t *testing.T) {
	_, err := Derive("#ffffcc")
	m := regexp.MustCompile(`passes is (#[0-9a-f]{6})\)`).FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatal(err)
	}
	if _, _, _, ok := pairs(m[1], lightPanels, 1); !ok {
		t.Errorf("%s does not pass on the light theme", m[1])
	}
	// A dark-theme refusal asks for a lighter colour.
	if _, err := Derive("#000000"); err == nil || !strings.Contains(err.Error(), "dark theme") || !strings.Contains(err.Error(), "pick a lighter colour") {
		t.Errorf("black: %v", err)
	}
}

func TestDeriveProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	ok, refused := 0, 0
	for i := 0; i < 500; i++ {
		accent := "#" + hex2(rng) + hex2(rng) + hex2(rng)
		p, err := Derive(accent)
		if err != nil {
			refused++
			continue
		}
		ok++
		holds(t, p.Dark, darkPanels)
		holds(t, p.Light, lightPanels)
	}
	if ok == 0 || refused == 0 {
		t.Errorf("a useful sample both accepts and refuses: %d accepted, %d refused", ok, refused)
	}
}

func hex2(r *rand.Rand) string {
	return strings.ToLower(string("0123456789abcdef"[r.Intn(16)]) + string("0123456789abcdef"[r.Intn(16)]))
}

func TestCSSGolden(t *testing.T) {
	p, err := Derive("#365d50")
	if err != nil {
		t.Fatal(err)
	}
	css := string(p.CSS())
	for _, want := range []string{
		"/* generated from the approved branding plugin: do not edit */\n:root:not([data-theme=\"light\"]) {\n  --primary: " + p.Dark.Primary + ";\n  --primary-dim: " + p.Dark.PrimaryDim + ";\n  --accent: " + p.Dark.Accent + ";\n  --text-on-primary: #131416;\n  --text-on-accent: #131416;\n  --graph-edge-active: " + p.Dark.Primary + ";\n}\n",
		":root[data-theme=\"light\"] {\n  --primary: #365d50;\n  --primary-dim: " + p.Light.PrimaryDim + ";\n  --accent: #365d50;\n  --text-on-primary: #fcfaf6;\n  --text-on-accent: #fcfaf6;\n  --graph-edge-active: #365d50;\n  --graph-edge-3d-active: #365d50;\n  --graph-ring: #365d50;\n  --graph-glow-a: rgb(54 93 80 / 0.06);\n}\n",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("css lacks\n%s\nin\n%s", want, css)
		}
	}
	for _, bad := range []string{"url(", "@import", "expression", ";}"} {
		if strings.Contains(css, bad) {
			t.Errorf("css holds %q", bad)
		}
	}
	if string(p.CSS()) != css {
		t.Error("CSS is not deterministic")
	}
}
