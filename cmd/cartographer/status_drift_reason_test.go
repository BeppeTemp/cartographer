package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// #2105: a workspace projection that sync never applied is drift while the
// header (global revisions) is equal and nothing is itemized: the diff says
// which workspace, and a projection that was applied says nothing.
func TestWorkspaceProviderDiffNamesUnitemizedDrift(t *testing.T) {
	ws := "/ws/repo-x"
	sp := syncProjection{Provider: "claude", Workspace: ws, BaseDir: ws, Scope: contract.ScopeProject}
	manifests := map[string]contract.Manifest{projectionKey(sp): {Revision: "rev1"}}

	// The revision judged is the projected manifest's own, not the pulled one.
	rev := resolveProjectionSet([]syncProjection{sp}, manifests, contract.ProjectOptions{})[projectionKey(sp)].Manifest.Revision

	_, d, _, reasons := workspaceProviderDiff(&clientconfig.Config{}, []syncProjection{sp}, manifests, nil, provisioning.LockFile{}, t.TempDir())
	if d.InSync || len(reasons) != 1 || !strings.Contains(reasons[0], "workspace "+ws) || !strings.Contains(reasons[0], "never applied") {
		t.Fatalf("InSync=%v reasons=%q", d.InSync, reasons)
	}

	var lf provisioning.LockFile
	lf.SetProjection(sp.Key(), provisioning.Lock{AppliedRevision: "rev0"})
	_, _, _, reasons = workspaceProviderDiff(&clientconfig.Config{}, []syncProjection{sp}, manifests, nil, lf, t.TempDir())
	if len(reasons) != 1 || !strings.Contains(reasons[0], "manifest "+rev+", lock rev0") {
		t.Errorf("reasons = %q", reasons)
	}

	lf.SetProjection(sp.Key(), provisioning.Lock{AppliedRevision: rev})
	_, d, _, reasons = workspaceProviderDiff(&clientconfig.Config{}, []syncProjection{sp}, manifests, nil, lf, t.TempDir())
	if !d.InSync || len(reasons) != 0 {
		t.Errorf("a clean projection: InSync=%v reasons=%q", d.InSync, reasons)
	}
}

func driftSnapshot(p providerStatus) statusSnapshot {
	p.Connected, p.State, p.Revision, p.LockRevision = true, "drift", "r1", "r1"
	return statusSnapshot{State: "drift", Providers: []providerStatus{p}}
}

// #2105: text and JSON carry the reason, and a drift with nothing at all to say
// gets the generic line instead of silence.
func TestStatusRendersDriftReason(t *testing.T) {
	s := driftSnapshot(providerStatus{Name: "claude", DriftReason: []string{"workspace /ws/repo-x: never applied (manifest r1)"}})
	text := withStdout(t, func() { renderStatus("", s, 1) })
	if !strings.Contains(text, "workspace /ws/repo-x: never applied") || !strings.Contains(text, "cartographer sync") || strings.Contains(text, "no itemized difference") {
		t.Errorf("text = %q", text)
	}
	js := withStdout(t, func() { renderStatus("json", s, 1) })
	var back statusSnapshot
	if err := json.Unmarshal([]byte(js), &back); err != nil || len(back.Providers[0].DriftReason) != 1 || !strings.Contains(js, `"drift_reason"`) {
		t.Errorf("json = %s (%v)", js, err)
	}

	bare := withStdout(t, func() { renderStatus("", driftSnapshot(providerStatus{Name: "claude"}), 1) })
	if !strings.Contains(bare, "no itemized difference: run `cartographer sync`") {
		t.Errorf("bare drift = %q", bare)
	}
}
