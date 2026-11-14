package branding

import "testing"

func TestNewActive(t *testing.T) {
	s, probs := Parse(folder("company: Acme\nlogo: {light: l.svg}\n", map[string]string{"l.svg": goodSVG}))
	if probs != nil {
		t.Fatal(probs)
	}
	a := NewActive("acme-brand", s)
	if a.Meta != (Meta{Company: "Acme", LogoLight: "logo-light.svg", LogoDark: "logo-light.svg"}) ||
		a.Assets["logo-light.svg"].ContentType != "image/svg+xml" || a.ETag == "" {
		t.Errorf("active = %+v", a)
	}
	b := NewActive("acme-brand", s)
	if a.ETag != b.ETag {
		t.Error("the ETag must be stable")
	}
}
