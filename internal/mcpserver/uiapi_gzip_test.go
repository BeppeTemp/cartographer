package mcpserver

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func getUIWith(t *testing.T, handler http.Handler, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// The Atlas's JSON is compressed when the client asks, byte for byte the same
// document, and left alone when it does not.
func TestUIAPI_JSONIsGzippedWhenNegotiated(t *testing.T) {
	t.Parallel()
	handler := newUIHandler(t, auth.NewTokenStore(nil), "docs")
	for _, path := range []string{"/kbs", "/kbs/docs/overview", "/kbs/docs/graph", "/kbs/docs/lint", "/kbs/docs/concept?id=nope"} {
		plain := getUIWith(t, handler, UIAPIPrefix+path, nil)
		if plain.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s: compressed without Accept-Encoding", path)
		}
		zipped := getUIWith(t, handler, UIAPIPrefix+path, map[string]string{"Accept-Encoding": "br, gzip;q=0.8"})
		if zipped.Code != plain.Code {
			t.Errorf("%s: status %d vs %d", path, zipped.Code, plain.Code)
		}
		if zipped.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(zipped.Header().Get("Vary"), "Accept-Encoding") {
			t.Errorf("%s: headers %v", path, zipped.Header())
		}
		if got := gunzip(t, zipped.Body.Bytes()); got != plain.Body.String() {
			t.Errorf("%s: compressed body differs from the plain one", path)
		}
		if zipped.Header().Get("Content-Type") != "application/json" || !cacheControlOK(zipped.Header().Get("Cache-Control")) {
			t.Errorf("%s: shared headers lost: %v", path, zipped.Header())
		}
	}
}

func TestAcceptsGzip(t *testing.T) {
	t.Parallel()
	for header, want := range map[string]bool{
		"": false, "gzip": true, "GZIP": true, "deflate, gzip": true, "gzip;q=0": false, "gzip; q=0.0": false,
		"gzip;q=0.5": true, "identity": false, "*": false, "br": false, "xgzip": false,
	} {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}

// The allow-list is the response's own type: an event stream is flushed event
// by event and never compressed, and the wrapper still reaches the connection.
func TestUIGzip_LeavesAnEventStreamAlone(t *testing.T) {
	t.Parallel()
	h := uiGzip(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: hello\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush through the wrapper: %v", err)
		}
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Header().Get("Content-Encoding") != "" || rr.Body.String() != "event: hello\n\n" || !rr.Flushed {
		t.Errorf("stream altered: %v %q flushed=%v", rr.Header(), rr.Body.String(), rr.Flushed)
	}
}

// The compression is worth having: the graph of a KB with a few hundred
// concepts shrinks several-fold.
func TestUIAPI_GzipShrinksTheGraph(t *testing.T) {
	t.Parallel()
	k := uiFixtureKB(t, "docs")
	for i := 0; i < 300; i++ {
		page := fmt.Sprintf("---\ntype: Note\ntitle: Page %d\nstatus: active\n---\n[prev](c%03d.md)\n", i, max(i-1, 0))
		if err := os.WriteFile(filepath.Join(k.DataRoot(), "visible", fmt.Sprintf("c%03d.md", i)), []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	handler := auth.NewTokenStore(nil).Middleware(multi.Handler())
	plain := getUIWith(t, handler, UIAPIPrefix+"/kbs/docs/graph", nil)
	zipped := getUIWith(t, handler, UIAPIPrefix+"/kbs/docs/graph", map[string]string{"Accept-Encoding": "gzip"})
	if zipped.Body.Len()*3 > plain.Body.Len() {
		t.Errorf("graph %d bytes plain, %d gzipped: less than 3x", plain.Body.Len(), zipped.Body.Len())
	}
}
