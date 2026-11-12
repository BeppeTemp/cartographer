package mcpserver

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzip for the Atlas's JSON. A 2,000-node graph or a long lint list is
// hundreds of kilobytes of repetitive JSON; compressing it is the cheapest
// cut of what a page load moves (#1962).
//
// The allow-list is the response itself: only a body the handler marked
// application/json is compressed, so the event stream (text/event-stream,
// flushed event by event) and anything added later pass through untouched.
// The wrapper forwards Flush and Unwrap, which is how the stream clears its
// read deadline through http.ResponseController.

var uiGzipPool = sync.Pool{New: func() any {
	gz, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
	return gz
}}

// acceptsGzip reports whether an Accept-Encoding header allows gzip: the token
// is listed and its q-value is not zero ("*" is not taken as consent: a client
// that wants gzip names it).
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		params = strings.ReplaceAll(params, " ", "")
		if q, ok := strings.CutPrefix(strings.ToLower(params), "q="); ok {
			if v, err := strconv.ParseFloat(q, 64); err == nil && v <= 0 {
				return false
			}
		}
		return true
	}
	return false
}

// uiGzip serves next with its JSON compressed when the client negotiated it.
func uiGzip(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next(w, r)
			return
		}
		g := &uiGzipWriter{ResponseWriter: w}
		defer g.finish()
		next(g, r)
	}
}

type uiGzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
}

func (g *uiGzipWriter) WriteHeader(code int) {
	if g.decided {
		g.ResponseWriter.WriteHeader(code)
		return
	}
	g.decided = true
	h := g.Header()
	h.Add("Vary", "Accept-Encoding")
	if code >= 200 && code != http.StatusNoContent && code != http.StatusNotModified &&
		h.Get("Content-Encoding") == "" && strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		g.gz = uiGzipPool.Get().(*gzip.Writer)
		g.gz.Reset(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *uiGzipWriter) Write(b []byte) (int, error) {
	if !g.decided {
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *uiGzipWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the connection underneath.
func (g *uiGzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *uiGzipWriter) finish() {
	if g.gz != nil {
		_ = g.gz.Close()
		uiGzipPool.Put(g.gz)
		g.gz = nil
	}
}
