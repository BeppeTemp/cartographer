package main

import (
	"slices"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/surveyor"
)

// The Surveyor does not turn a refusal that opens with an operation's name into a
// report candidate (D1925). Its list of those names is exact, and it is the
// server's registry plus the sub-operations: a test here, because the binary
// links both sides and the Surveyor package must not link the server.
func TestSurveyorOperationNamesAreTheServersToolsAndTheSubOperations(t *testing.T) {
	s := mcpserver.New("test")
	mcpserver.RegisterKBTools(s, kbtest.New(t), mcpserver.Deps{BundleFS: fstest.MapFS{}})
	var tools []string
	for name := range s.Tools() {
		tools = append(tools, name)
	}
	sub := surveyor.SubOperationNames()
	for _, n := range sub {
		if slices.Contains(tools, n) {
			t.Errorf("%q is a registered tool: remove it from the sub-operations", n)
		}
	}
	want := append(tools, sub...)
	slices.Sort(want)
	if got := surveyor.OperationNames(); !slices.Equal(got, want) {
		var missing, extra []string
		for _, n := range want {
			if !slices.Contains(got, n) {
				missing = append(missing, n)
			}
		}
		for _, n := range got {
			if !slices.Contains(want, n) {
				extra = append(extra, n)
			}
		}
		t.Fatalf("surveyor.OperationNames() drifted from the server's tools: add %v, remove %v", missing, extra)
	}
}
