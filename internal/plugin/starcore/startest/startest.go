// Package startest is the door of the tests to the test seams of starcore.
// Only _test.go files may import it (internal/boundaries checks): it hands out
// what would let a host skip the network checks or the source checks.
package startest

import (
	"go.starlark.net/starlark"

	"github.com/BeppeTemp/cartographer/internal/plugin/starcore"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/internal/seam"
)

type (
	// NetSeams replace the name resolution, the proxy and the TLS of http.request.
	NetSeams = seam.NetSeams
	// Seams are the seams of one call.
	Seams = seam.Seams
)

// LoadSources runs plugin.star over srcs without the checks of a tree.
func LoadSources(srcs map[string][]byte, opts starcore.Options) (*starcore.Script, error) {
	return starcore.LoadSources(seam.Gate{}, srcs, opts)
}

// ReplaceSource swaps a source the next call of s runs again.
func ReplaceSource(s *starcore.Script, path string, src []byte) {
	s.ReplaceSource(seam.Gate{}, path, src)
}

// InCallPhase puts a bare thread in the CALL phase.
func InCallPhase(t *starlark.Thread) { starcore.InCallPhase(seam.Gate{}, t) }
