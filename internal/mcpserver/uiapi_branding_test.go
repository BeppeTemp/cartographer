package mcpserver

import (
	"net/http"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/branding"
)

func brandingHandler(t *testing.T, b *branding.Active) http.Handler {
	t.Helper()
	multi := NewMultiKBServer("v-test")
	multi.SetBranding(b)
	multi.EnableWeb(nil)
	return auth.NewTokenStore(nil).Middleware(multi.Handler())
}

func testBranding(t *testing.T) *branding.Active {
	t.Helper()
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`)
	return branding.NewActive("acme", &branding.Spec{Company: "Example Co",
		LogoLight: &branding.File{Name: "l.svg", Ext: "svg", Data: svg}})
}

func TestUIAPI_BrandingAbsentIs404(t *testing.T) {
	t.Parallel()
	h := brandingHandler(t, nil)
	for _, p := range []string{"/branding", "/branding/logo-light", "/branding/logo-dark"} {
		if rr := getUI(t, h, UIAPIPrefix+p, ""); rr.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", p, rr.Code)
		}
	}
}

func TestUIAPI_BrandingBody(t *testing.T) {
	t.Parallel()
	h := brandingHandler(t, testBranding(t))
	rr := getUI(t, h, UIAPIPrefix+"/branding", "")
	body := decodeUI(t, rr)
	if rr.Code != 200 || body["company"] != "Example Co" || body["logo_light"] == "" || body["logo_dark"] == "" || body["version"] == "" {
		t.Fatalf("%d %v", rr.Code, body)
	}
	if _, has := body["favicon"]; has {
		t.Errorf("the body must not carry a favicon: %v", body)
	}
	if rr := getUI(t, h, UIAPIPrefix+"/branding/favicon", ""); rr.Code != http.StatusNotFound {
		t.Errorf("/branding/favicon = %d, want 404", rr.Code)
	}
	if _, has := body["stylesheet"]; has {
		t.Errorf("the body must not carry a stylesheet: %v", body)
	}
	if rr := getUI(t, h, UIAPIPrefix+"/branding.css", ""); rr.Code != http.StatusNotFound {
		t.Errorf("/branding.css = %d, want 404", rr.Code)
	}
}

func TestUIAPI_BrandingAssetsContentTypeAndCSP(t *testing.T) {
	t.Parallel()
	h := brandingHandler(t, testBranding(t))
	logo := getUI(t, h, UIAPIPrefix+"/branding/logo-light", "")
	if logo.Code != 200 || logo.Header().Get("Content-Type") != "image/svg+xml" || logo.Header().Get("X-Content-Type-Options") != "nosniff" ||
		logo.Header().Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'" {
		t.Fatalf("svg %d %v", logo.Code, logo.Header())
	}
	// Unchanged branding revalidates to a 304 with no body.
	if rr := getUIWith(t, h, UIAPIPrefix+"/branding/logo-light", map[string]string{"If-None-Match": logo.Header().Get("ETag")}); rr.Code != http.StatusNotModified || rr.Body.Len() != 0 || logo.Header().Get("Cache-Control") != "private, no-cache" {
		t.Errorf("revalidation = %d, %d bytes, Cache-Control %q", rr.Code, rr.Body.Len(), logo.Header().Get("Cache-Control"))
	}
	// no dark logo declared: the light one is served for it.
	if dark := getUI(t, h, UIAPIPrefix+"/branding/logo-dark", ""); dark.Code != 200 || dark.Body.String() != logo.Body.String() {
		t.Errorf("logo-dark = %d", dark.Code)
	}
}
