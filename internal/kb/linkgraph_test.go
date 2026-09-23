package kb

import (
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func TestLinkGraphProjectsTheVisibleConcepts(t *testing.T) {
	f := newGraphFixture(t)
	calls := map[string]int{}
	include := func(id string) bool {
		calls[id]++
		return !strings.HasPrefix(id, "notes/")
	}
	lg, err := f.k.LinkGraph(include)
	if err != nil {
		t.Fatal(err)
	}
	want := []okf.ConceptID{"infra/cluster", "infra/dns", "infra/gateway"}
	if !reflect.DeepEqual(lg.IDs, want) {
		t.Fatalf("ids = %v, want %v", lg.IDs, want)
	}
	for id, n := range calls {
		if n != 1 {
			t.Errorf("include(%s) called %d times", id, n)
		}
	}
	idx := func(id okf.ConceptID) int { return lg.Index[id] }
	// gateway → dns, cluster (missing.md dropped); dns → gateway (the
	// extensionless diagram is no concept); cluster → itself (dropped) and
	// notes/log-review (excluded).
	if got := lg.Graph.Out[idx("infra/gateway")]; !reflect.DeepEqual(got, []int{idx("infra/cluster"), idx("infra/dns")}) {
		t.Errorf("gateway out = %v", got)
	}
	if got := lg.Graph.Out[idx("infra/cluster")]; len(got) != 0 {
		t.Errorf("cluster out = %v, want none (self and excluded targets dropped)", got)
	}
	if got := lg.Graph.In[idx("infra/gateway")]; !reflect.DeepEqual(got, []int{idx("infra/dns")}) {
		t.Errorf("gateway in = %v", got)
	}
	if fa := lg.Facets[idx("infra/gateway")]; fa != (NodeFacets{Title: "Gateway", Type: "Service", Status: "active", Collection: "infra"}) {
		t.Errorf("facets = %+v", fa)
	}

	all, err := f.k.LinkGraph(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.IDs) != 5 {
		t.Fatalf("nil include: %v", all.IDs)
	}
}
