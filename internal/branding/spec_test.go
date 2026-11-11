package branding

import (
	"strings"
	"testing"
)

const goodSVG = `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><defs><path id="a" d="M0 0"/></defs><use xlink:href="#a"/><rect style="fill:url(#a)"/></svg>`

func svgWith(inner string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg">` + inner + `</svg>`
}

func png() string { return pngSig + "data" }

func folder(decl string, extra map[string]string) map[string]InFile {
	m := map[string]InFile{DeclFile: {Content: []byte(decl)}}
	for k, v := range extra {
		m[k] = InFile{Content: []byte(v)}
	}
	return m
}

func TestParseValid(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]InFile
		check func(*Spec) bool
	}{
		"accent only": {folder("accent: \"#1F6F5C\"\n", nil), func(s *Spec) bool { return s.Accent == "#1f6f5c" && s.LogoLight == nil }},
		"full": {folder("accent: \"#1f6f5c\"\ncompany: Acme\nlogo: {light: l.svg, dark: d.png}\nfavicon: f.ico\n",
			map[string]string{"l.svg": goodSVG, "d.png": png(), "f.ico": icoSig + "x"}),
			func(s *Spec) bool {
				return s.Company == "Acme" && s.LogoLight.Ext == "svg" && s.LogoDark.Ext == "png" && s.Favicon.Ext == "ico"
			}},
		"dark absent":   {folder("logo: {light: l.svg}\n", map[string]string{"l.svg": goodSVG}), func(s *Spec) bool { return s.LogoLight != nil && s.LogoDark == nil }},
		"fragment href": {folder("favicon: f.svg\n", map[string]string{"f.svg": svgWith(`<use href="#a"/>`)}), func(s *Spec) bool { return s.Favicon != nil }},
	} {
		s, probs := Parse(tc.files)
		if len(probs) != 0 || s == nil || !tc.check(s) {
			t.Errorf("%s: %+v %v", name, s, probs)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	big := strings.Repeat("a", 257<<10)
	exec := folder("favicon: f.png\n", map[string]string{"f.png": png()})
	exec["f.png"] = InFile{Content: []byte(png()), Executable: true}
	for name, tc := range map[string]struct {
		files map[string]InFile
		want  string
	}{
		"no declaration":          {map[string]InFile{}, "branding.yaml: missing"},
		"declares nothing":        {folder("company: Acme\n", nil), declNothing},
		"logo without light":      {folder("logo: {dark: d.svg}\n", map[string]string{"d.svg": goodSVG}), "light is required"},
		"unknown key":             {folder("accent: \"#112233\"\nname: X\n", nil), "field name not found"},
		"alias":                   {folder("accent: &a \"#112233\"\ncompany: *a\n", nil), "aliases"},
		"second document":         {folder("accent: \"#112233\"\n---\naccent: \"#223344\"\n", nil), "second YAML document"},
		"hex without hash":        {folder("accent: \"112233\"\n", nil), "not a #rrggbb"},
		"short hex":               {folder("accent: \"#abc\"\n", nil), "not a #rrggbb"},
		"missing file":            {folder("favicon: f.svg\n", nil), "no such file"},
		"stray file":              {folder("accent: \"#112233\"\n", map[string]string{"x.svg": goodSVG}), "a stray file"},
		"executable":              {exec, "execute bit"},
		"ico as a logo":           {folder("logo: {light: l.ico}\n", map[string]string{"l.ico": icoSig}), "not allowed here"},
		"over a file cap":         {folder("favicon: f.svg\n", map[string]string{"f.svg": big}), "over the"},
		"bad png":                 {folder("favicon: f.png\n", map[string]string{"f.png": "GIF89a"}), "wrong signature"},
		"bad ico":                 {folder("favicon: f.ico\n", map[string]string{"f.ico": "MZ"}), "wrong header"},
		"path in the name":        {folder("favicon: ../f.svg\n", nil), "not a file name"},
		"uppercase name":          {folder("favicon: F.svg\n", map[string]string{"F.svg": goodSVG}), "not a file name"},
		"company with markup":     {folder("accent: \"#112233\"\ncompany: a<b\n", nil), "must not contain"},
		"company too long":        {folder("accent: \"#112233\"\ncompany: "+strings.Repeat("a", 61)+"\n", nil), "over 60"},
		"script":                  {svg(svgWith(`<script>alert(1)</script>`)), "element script"},
		"onload":                  {svg(`<svg xmlns="http://www.w3.org/2000/svg" onload="x()"/>`), "event attribute onload"},
		"foreignObject":           {svg(svgWith(`<foreignObject/>`)), "foreignObject"},
		"remote href":             {svg(svgWith(`<image href="https://example.com/x"/>`)), "points outside"},
		"remote xlink href":       {svg(svgWith(`<use xlink:href="https://example.com/x" xmlns:xlink="http://www.w3.org/1999/xlink"/>`)), "points outside"},
		"entity":                  {svg(`<!DOCTYPE svg [<!ENTITY a "b">]><svg xmlns="http://www.w3.org/2000/svg"/>`), "DOCTYPE"},
		"animate":                 {svg(svgWith(`<animateTransform/>`)), "animateTransform"},
		"style import":            {svg(svgWith(`<style>@import "x.css";</style>`)), "style element"},
		"style attribute url":     {svg(svgWith(`<rect style="fill:url(https://example.com/a)"/>`)), "refers outside"},
		"comment splits import":   {svg(svgWith(`<style>@imp<!---->ort "https://example.com/y.css";</style>`)), "style element"},
		"comment in style":        {svg(svgWith(`<style>rect{fill:red}<!-- x --></style>`)), "comment or an instruction"},
		"cdata splits url":        {svg(svgWith(`<style>rect{fill:ur<![CDATA[l(https://example.com/t.png)]]>}</style>`)), "style element"},
		"cdata splits escape":     {svg(svgWith(`<style>rect{fill:u<![CDATA[\]]>72l(https://example.com/a)}</style>`)), "style element"},
		"element in style":        {svg(svgWith(`<style>rect{}<b>x</b></style>`)), "holds the element b"},
		"css escape url":          {svg(svgWith(`<style>rect{fill:u\72l(https://example.com/a)}</style>`)), "style element"},
		"css escape url 2":        {svg(svgWith(`<style>rect{fill:\75rl(https://example.com/a)}</style>`)), "style element"},
		"css escape import":       {svg(svgWith(`<style>@\69mport "x.css";</style>`)), "style element"},
		"image-set":               {svg(svgWith(`<style>rect{fill:image-set("a.png" 1x)}</style>`)), "style element"},
		"src function":            {svg(svgWith(`<style>rect{fill:src("a.png")}</style>`)), "style element"},
		"cross-fade":              {svg(svgWith(`<style>rect{fill:cross-fade(#a, #b)}</style>`)), "style element"},
		"style attr escape":       {svg(svgWith(`<rect style="fill:u\72l(https://example.com/a)"/>`)), "refers outside"},
		"style attr image-set":    {svg(svgWith(`<rect style="fill:image-set(x 1x)"/>`)), "refers outside"},
		"company with quote":      {folder("accent: \"#112233\"\ncompany: a\"b\n", nil), "must not contain"},
		"company with apostrophe": {folder("accent: \"#112233\"\ncompany: \"a'b\"\n", nil), "must not contain"},
		"company with backslash":  {folder("accent: \"#112233\"\ncompany: 'a\\b'\n", nil), "must not contain"},
		"not svg":                 {svg(`<html/>`), "not svg"},
		"not xml":                 {svg(`<svg`), "not valid XML"},
	} {
		s, probs := Parse(tc.files)
		if s != nil || len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), tc.want) {
			t.Errorf("%s: want a problem with %q, got %v", name, tc.want, probs)
			continue
		}
		for _, p := range probs {
			if !strings.HasPrefix(p, "branding/") {
				t.Errorf("%s: problem %q lacks the branding/ prefix", name, p)
			}
		}
	}
}

func svg(content string) map[string]InFile {
	return folder("favicon: f.svg\n", map[string]string{"f.svg": content})
}

func TestParseFolderCap(t *testing.T) {
	files := folder("logo: {light: a.png, dark: b.png}\nfavicon: c.png\n", nil)
	chunk := []byte(pngSig + strings.Repeat("a", 250<<10))
	for _, n := range []string{"a.png", "b.png", "c.png"} {
		files[n] = InFile{Content: chunk}
	}
	files["d.txt"] = InFile{Content: []byte(strings.Repeat("a", 300<<10))}
	if _, probs := Parse(files); !strings.Contains(strings.Join(probs, "\n"), "of the folder") {
		t.Errorf("folder cap: %v", probs)
	}
}
