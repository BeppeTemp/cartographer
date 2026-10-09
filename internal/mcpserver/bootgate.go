package mcpserver

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// BootGate is the HTTP handler a server answers with while its KBs are still
// being cloned and indexed (D348). `serve` binds the port first and installs
// the real handler with Open once every KB is mounted, so a liveness probe
// never meets a closed port during a cold start.
//
// Until Open, only /health (liveness, always 200) and /ready (503) answer for
// themselves; every other path is 503 with Retry-After. After Open every
// request goes to the installed handler untouched. State is two atomic values:
// nothing on the request path takes a lock.
type BootGate struct {
	version string
	phase   atomic.Pointer[string]
	handler atomic.Pointer[http.Handler]
}

// NewBootGate returns a gate that reports version in its /health answer.
func NewBootGate(version string) *BootGate {
	return &BootGate{version: version}
}

// SetPhase records what the bootstrap is doing ("cloning", "mounting",
// "indexing"). Diagnostic only: clients must not branch on it.
func (g *BootGate) SetPhase(phase string) { g.phase.Store(&phase) }

// Open installs the real handler; every later request goes to it. Only the
// first call has an effect.
func (g *BootGate) Open(h http.Handler) { g.handler.CompareAndSwap(nil, &h) }

func (g *BootGate) currentPhase() string {
	if p := g.phase.Load(); p != nil {
		return *p
	}
	return ""
}

// Handler returns the gate's http.Handler.
func (g *BootGate) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := g.handler.Load(); h != nil {
			(*h).ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/health", "/ready":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/ready" {
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"ready": false, "kbs": 0, "bootstrapping": true, "phase": g.currentPhase(),
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":        "ok",
				"version":       g.version,
				"started_at":    processStartedAt,
				"kbs":           []string{},
				"ready":         false,
				"bootstrapping": true,
				"phase":         g.currentPhase(),
			})
		default:
			w.Header().Set("Retry-After", "5")
			http.Error(w, "kb bootstrapping, retry", http.StatusServiceUnavailable)
		}
	})
}
