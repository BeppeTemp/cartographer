package kb

import (
	"fmt"
	"testing"
	"time"
)

// BenchmarkCrossInboundDigestWarm records the cost of the digest while no
// KB moved; TestCrossInboundDigestIsMemoized is the gate.
func BenchmarkCrossInboundDigestWarm(b *testing.B) {
	old := crossMaxAge
	crossMaxAge = time.Hour
	defer func() { crossMaxAge = old }()
	byName, names := map[string]*KB{}, []string{}
	for i := 0; i < 30; i++ {
		n := fmt.Sprintf("kb-%d", i)
		names = append(names, n)
		byName[n] = newTestKB(b)
	}
	m := NewMounts(byName, names, nil)
	for _, k := range byName {
		k.SetMounts(m)
	}
	a := byName["kb-0"]
	a.CrossInboundDigest()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.CrossInboundDigest()
	}
}
