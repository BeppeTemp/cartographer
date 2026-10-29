package worker_test

import (
	"slices"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/worker"
)

func TestAllowlist_DenyWinsOverAllowlist(t *testing.T) {
	a := worker.NewAllowlist("concept_read", "concept_delete", "artifact_write", "artifact_zzz", "secret_set")
	for _, n := range []string{"concept_delete", "artifact_write", "artifact_zzz", "secret_set"} {
		if a.Permits(n) {
			t.Errorf("%s permitted", n)
		}
	}
	if !a.Permits("concept_read") {
		t.Error("concept_read refused")
	}
}

func TestAllowlist_DefaultHasNoDeniedName(t *testing.T) {
	for _, n := range worker.DefaultTools() {
		if worker.Denied(n) {
			t.Errorf("default list names denied tool %s", n)
		}
	}
	a := worker.NewAllowlist()
	if a.Permits("concept_delete") || a.Permits("artifact_read") || a.Permits("not_listed_anywhere") {
		t.Error("default allowlist too wide")
	}
}

func TestAllowlist_FilterSortedAndAbsentIsNotAnError(t *testing.T) {
	listed := []client.ToolInfo{{Name: "search"}, {Name: "concept_delete"}, {Name: "atlas_overview"}, {Name: "brand_new_tool"}}
	got := worker.NewAllowlist().Filter(listed)
	if len(got) != 2 || got[0].Name != "atlas_overview" || got[1].Name != "search" {
		t.Fatalf("filter = %+v", got)
	}
}

// A rename on the server fails the build here instead of silently shrinking
// the allowlist or widening what the deny set was meant to stop.
func TestAllowlistNamesExistInTheServerRegistry(t *testing.T) {
	s := mcpserver.New("test")
	mcpserver.RegisterKBTools(s, kbtest.New(t), mcpserver.Deps{BundleFS: fstest.MapFS{}})
	tools := s.Tools()
	var names []string
	names = append(names, worker.DefaultTools()...)
	names = append(names, worker.DenyTools()...)
	for _, n := range names {
		if _, ok := tools[n]; !ok {
			t.Errorf("%s is not a registered tool", n)
		}
	}
	// The default list is read tools plus content writes: no duplicates.
	d := worker.DefaultTools()
	slices.Sort(d)
	if len(slices.Compact(d)) != len(d) {
		t.Error("duplicate in the default list")
	}
}
