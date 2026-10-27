package provisioning

import (
	"strings"
	"testing"
)

// The routed clause is paid once per KB on every model call of every session
// (#1246/D1246): 9 KBs multiply it by nine, which is what took the routed
// block over D1182's 8192 bytes. 105 bytes measured for "kb-a", plus at most
// 10 %.
func TestRoutingSentence_RoutedClauseBudget(t *testing.T) {
	const budget = 115
	extra := len(routingSentence("kb-a", true)) - len(routingSentence("kb-a", false))
	if extra > budget {
		t.Errorf("the routed clause is %d bytes per KB, budget %d (#1246/D1246): nine KBs pay it nine times (%d bytes) on every model call, shrink it or amend D1246 deliberately", extra, budget, extra*9)
	}
	got := routingSentence("kb-a", true)
	for _, piece := range []string{`"kb-a"`, `"cartographer"`, "not prefixed", "where a tool takes a `kb` argument", "pass `kb: \"kb-a\"`", "never inferred"} {
		if !strings.Contains(got, piece) {
			t.Errorf("the routed sentence lost %q: %s", piece, got)
		}
	}
}
