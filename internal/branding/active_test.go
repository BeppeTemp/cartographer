package branding

import (
	"strings"
	"testing"
)

func TestNewActive(t *testing.T) {
	s, probs := Parse(folder("accent: \"#365d50\"\ncompany: Acme\nlogo: {light: l.svg}\nfavicon: f.png\n", map[string]string{"l.svg": goodSVG, "f.png": png()}))
	if probs != nil {
		t.Fatal(probs)
	}
	a, err := NewActive("acme-brand", s)
	if err != nil {
		t.Fatal(err)
	}
	if a.Meta != (Meta{Company: "Acme", LogoLight: "logo-light.svg", LogoDark: "logo-light.svg", Favicon: "favicon.png"}) ||
		a.Assets["logo-light.svg"].ContentType != "image/svg+xml" || a.Assets["favicon.png"].ContentType != "image/png" || len(a.CSS) == 0 || a.ETag == "" {
		t.Errorf("active = %+v", a)
	}
	b, _ := NewActive("acme-brand", s)
	if a.ETag != b.ETag {
		t.Error("the ETag must be stable")
	}
	bad := &Spec{Accent: "#ffffcc"}
	if _, err := NewActive("acme-brand", bad); err == nil || !strings.Contains(err.Error(), "acme-brand") || !strings.Contains(err.Error(), "accent") {
		t.Errorf("an unusable accent must name the plugin and the key: %v", err)
	}
}
