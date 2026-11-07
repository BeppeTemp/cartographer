package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func kbOrderInstructions(name string) provisioning.Artifact {
	body := name + " section.\n"
	return provisioning.Artifact{
		Kind: "instructions", Name: name, Source: "kb:" + name, Signed: true,
		ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(body))),
		Files:       []provisioning.ArtifactFile{{Path: "instructions.md", Content: []byte(body)}},
	}
}

// projectionKBOrder: a workspace projection follows its folder binding, a
// global one the provider's binding (D182) (#1809).
func TestProjectionKBOrder(t *testing.T) {
	global := map[string][]string{"codex": {"kb-b", "kb-a"}}
	ws := syncProjection{Provider: "codex", Workspace: "/ws", KBs: []string{"kb-a", "kb-b"}}
	if got := projectionKBOrder(ws, global); !reflect.DeepEqual(got, []string{"kb-a", "kb-b"}) {
		t.Errorf("workspace projection order = %v, want the folder binding's", got)
	}
	gl := syncProjection{Provider: "codex"}
	if got := projectionKBOrder(gl, global); !reflect.DeepEqual(got, []string{"kb-b", "kb-a"}) {
		t.Errorf("global projection order = %v, want the provider binding's", got)
	}
}

// Codex and OpenCode bound to one folder with the same KBs but different
// global orders write the same AGENTS.md: the second sync writes nothing.
func TestSharedWorkspaceAgentsMD_NotRewrittenAcrossProviders(t *testing.T) {
	dir := t.TempDir()
	ws := t.TempDir()
	m := provisioning.MergeArtifacts([]provisioning.Artifact{kbOrderInstructions("kb-a"), kbOrderInstructions("kb-b"), kbOrderInstructions("kb-c")})
	mk := func(p string) syncProjection {
		return syncProjection{Provider: p, Workspace: ws, BaseDir: ws, KBs: []string{"kb-a", "kb-b", "kb-c"}, Scope: provisioning.ScopeProject}
	}
	ps := []syncProjection{mk("codex"), mk("opencode")}
	manifests := map[string]provisioning.Manifest{projectionKey(ps[0]): m, projectionKey(ps[1]): m}
	kbOrder := map[string][]string{"codex": {"kb-c", "kb-a", "kb-b"}, "opencode": {"kb-b", "kb-c", "kb-a"}}

	run := func() string {
		res, err := materializeForProviders(manifests, ps, dir, "", false, false, portabilityOptions{}, kbOrder)
		if err != nil {
			t.Fatalf("materialize: %v", err)
		}
		var wrote []string
		for k, r := range res {
			for _, w := range r.Written {
				if w.Kind == "instructions" {
					wrote = append(wrote, k)
				}
			}
		}
		return strings.Join(wrote, ",")
	}
	if run() == "" {
		t.Fatal("first sync wrote no instructions")
	}
	if got := run(); got != "" {
		t.Errorf("second sync rewrote the shared file for %q", got)
	}
	if _, err := os.Stat(filepath.Join(ws, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md: %v", err)
	}
}
