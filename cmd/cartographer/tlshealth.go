package main

import (
	"log"
	"net"
	"net/http"
	"time"

	"github.com/BeppeTemp/cartographer/internal/mcpserver"
)

// healthOnly lets through GET and HEAD of exactly /health and /ready, to the
// same inner handler as the public listener (so the boot gate answers them
// while the KBs bootstrap and the token rules stay those of the public
// address), and answers 404 to every other method or path. It is the whole of
// what the loopback health listener of a TLS server serves: no HSTS, no
// .well-known, no UI, no API, no MCP.
func healthOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			(r.URL.Path == "/health" || r.URL.Path == "/ready") {
			next.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

// healthShutdownTimeout bounds the drain of the health listener, apart from
// the public one's shutdownHTTPTimeout.
const healthShutdownTimeout = 3 * time.Second

// newHealthServer is the http.Server of the health listener: the public
// server's read timeouts, the probe-only handler. The listener is always
// loopback, so the Host guard is unconditional here: the inner handler's guard
// is decided from the public address and may be absent.
func newHealthServer(addr string, inner http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           mcpserver.LoopbackHostGuard(healthOnly(inner)),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// listenHealth binds the health address; "" means no health listener. The
// address was validated as loopback (config.ValidateTLS) before this runs.
// A failed bind is a listenError, like the public one.
func listenHealth(addr string) (net.Listener, error) {
	if addr == "" {
		return nil, nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, listenError(addr, err)
	}
	return ln, nil
}

func logHealthListener(ln net.Listener) {
	log.Printf("health listener on %s (loopback, /health and /ready only)", ln.Addr())
}
