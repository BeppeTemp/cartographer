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
type Meta struct{ Company, LogoLight, LogoDark string }

// Active is the branding the server wears: the images keyed by the name they
// are served under.
type Active struct {
	Plugin string
	Assets map[string]Asset
	Meta   Meta
	ETag   string
}

var contentTypes = map[string]string{"svg": "image/svg+xml", "png": "image/png"}

// NewActive names the assets of a valid Spec.
func NewActive(plugin string, s *Spec) *Active {
	a := &Active{Plugin: plugin, Assets: map[string]Asset{}, Meta: Meta{Company: s.Company}}
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
	h := sha256.New()
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
	return a
}
