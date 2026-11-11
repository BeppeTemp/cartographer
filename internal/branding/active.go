package branding

import (
	"crypto/sha256"
	"fmt"
	"sort"
)

// Asset is a served image: the content type is fixed from the extension.
type Asset struct {
	ContentType string
	Data        []byte
}

// Meta is what the shell tells the UI: the served names of the images and the
// logo's alt text. A name is empty when not declared.
type Meta struct{ Company, LogoLight, LogoDark, Favicon string }

// Active is the branding the server wears: the derived palette and its
// stylesheet, and the images keyed by the name they are served under.
type Active struct {
	Plugin  string
	Palette *Palette // nil without an accent
	CSS     []byte   // nil without an accent
	Assets  map[string]Asset
	Meta    Meta
	ETag    string
}

var contentTypes = map[string]string{"svg": "image/svg+xml", "png": "image/png", "ico": "image/x-icon"}

// NewActive derives the palette and names the assets of a valid Spec. An
// accent that cannot be shown is an error naming the key.
func NewActive(plugin string, s *Spec) (*Active, error) {
	a := &Active{Plugin: plugin, Assets: map[string]Asset{}, Meta: Meta{Company: s.Company}}
	if s.Accent != "" {
		p, err := Derive(s.Accent)
		if err != nil {
			return nil, fmt.Errorf("plugin %s, branding/%s, accent: %w", plugin, DeclFile, err)
		}
		a.Palette, a.CSS = &p, p.CSS()
	}
	put := func(base string, f *File) string {
		if f == nil {
			return ""
		}
		name := base + "." + f.Ext
		a.Assets[name] = Asset{ContentType: contentTypes[f.Ext], Data: f.Data}
		return name
	}
	a.Meta.LogoLight = put("logo-light", s.LogoLight)
	if s.LogoDark != nil {
		a.Meta.LogoDark = put("logo-dark", s.LogoDark)
	} else {
		a.Meta.LogoDark = a.Meta.LogoLight
	}
	a.Meta.Favicon = put("favicon", s.Favicon)
	h := sha256.New()
	h.Write(a.CSS)
	names := make([]string, 0, len(a.Assets))
	for n := range a.Assets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h.Write([]byte{0})
		h.Write([]byte(n))
		h.Write(a.Assets[n].Data)
	}
	a.ETag = fmt.Sprintf(`"%x"`, h.Sum(nil)[:16])
	return a, nil
}
