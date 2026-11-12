// Package seam holds the test seams of the Starlark runtime. It sits under
// internal/ of starcore, so no package outside internal/plugin/starcore can
// name its types: the only way to build one is internal/plugin/starcore/startest,
// which only test files may import (TestStarcoreSeamsAreTestOnly holds it).
// A host that links the runtime in production can therefore neither replace
// the network checks nor override a builtin.
package seam

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/netip"
	"net/url"

	"go.starlark.net/starlark"
)

// Gate is the token the test-only entry points of starcore take.
type Gate struct{}

// NetSeams are what a test replaces of http.request: name resolution, the
// proxy function and the TLS configuration.
type NetSeams struct {
	Lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	Proxy  func(*http.Request) (*url.URL, error)
	TLS    *tls.Config
}

// Seams are the seams of one call: lower network seams and extra predeclared
// names (a test's own builtins).
type Seams struct {
	Net   *NetSeams
	Extra starlark.StringDict
}
