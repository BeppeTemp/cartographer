package mcpserver

import (
	"net/http"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/branding"
)

// uiBrandingBody is the 200 body of GET /branding (D2078):
// what the Atlas wears. A name says the image exists (served at /branding/logo-light,
// logo-dark), empty when not declared.
type uiBrandingBody struct {
	Company   string `json:"company"`
	LogoLight string `json:"logo_light"`
	LogoDark  string `json:"logo_dark"`
	Version   string `json:"version"`
}

// SetBranding hands the server the branding of the pinned plugins (nil: none).
// Call it before serving.
func (m *MultiKBServer) SetBranding(b *branding.Active) { m.branding = b }

// GET /branding: 404 without a branding, which the Atlas reads as "no branding".
func (m *MultiKBServer) uiBranding(w http.ResponseWriter, r *http.Request) {
	b := m.branding
	if b == nil {
		writeUINotFound(w)
		return
	}
	writeUIJSON(w, http.StatusOK, uiBrandingBody{Company: b.Meta.Company, LogoLight: b.Meta.LogoLight, LogoDark: b.Meta.LogoDark,
		Version: strings.Trim(b.ETag, `"`)})
}

// GET /branding/logo-light, logo-dark: an image, with the content type the spec judged. An SVG
// gets a CSP that forbids everything but inline style, so a script or a fetch in
// it never runs even if the file is opened on its own.
func (m *MultiKBServer) uiBrandingAsset(w http.ResponseWriter, r *http.Request, name func(*branding.Active) string) {
	b := m.branding
	if b == nil {
		writeUINotFound(w)
		return
	}
	a, ok := b.Assets[name(b)]
	if !ok {
		writeUINotFound(w)
		return
	}
	writeUIHeaders(w)
	h := w.Header()
	h.Set("Content-Type", a.ContentType)
	h.Set("ETag", b.ETag)
	// Revalidated on every use, answered 304 while the branding is unchanged:
	// the ETag is the version the Atlas busts its cache with.
	h.Set("Cache-Control", "private, no-cache")
	if a.ContentType == "image/svg+xml" {
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	}
	if etagMatches(r.Header.Get("If-None-Match"), b.ETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(a.Data)
}
