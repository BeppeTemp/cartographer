package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// approveProjectionConfig binds kb-a and kb-b, which both ship "shared", to
// one workspace each (disjoint) or to one together, for provider claude.
func approveProjectionConfig(t *testing.T, srvURL string, together bool) *clientconfig.Config {
	t.Helper()
	cfg := &clientconfig.Config{ServerURL: srvURL + "/mcp", KnownKBs: []string{"kb-a", "kb-b"}, Agents: []string{"claude"}}
	if err := cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if together {
		if err := cfg.BindWorkspace("claude", t.TempDir(), []string{"kb-a", "kb-b"}); err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	for _, kb := range []string{"kb-a", "kb-b"} {
		if err := cfg.BindWorkspace("claude", t.TempDir(), []string{kb}); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

// Two KBs in disjoint workspaces may share an artifact name: the D171
// collision is judged per projection, as sync judges it (#2306), so approving
// either one, or listing both, is not refused.
func TestFetchMergedManifestForJudgesCollisionPerProjection(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-b": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "shared")
	})
	defer srv.Close()
	cfg := approveProjectionConfig(t, srv.URL, false)

	for _, kb := range []string{"kb-a", "kb-b", ""} {
		m, err := fetchMergedManifestFor(cfg, kb)
		if err != nil {
			t.Fatalf("kb %q: %v", kb, err)
		}
		got := map[string]bool{}
		for _, a := range m.Artifacts {
			if a.Name == "shared" {
				got[a.Source] = true
			}
		}
		if !got["kb:kb-a"] && kb != "kb-b" || !got["kb:kb-b"] && kb != "kb-a" {
			t.Errorf("kb %q: shared artifacts = %v", kb, got)
		}
	}
}

// Two KBs that share a projection still collide, with or without --kb.
func TestFetchMergedManifestForKeepsCollisionInASharedProjection(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-b": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "shared")
	})
	defer srv.Close()
	cfg := approveProjectionConfig(t, srv.URL, true)

	for _, kb := range []string{"kb-a", ""} {
		_, err := fetchMergedManifestFor(cfg, kb)
		var ce *provisioning.CollisionError
		if !errors.As(err, &ce) || !strings.Contains(err.Error(), "kb:kb-a") || !strings.Contains(err.Error(), "kb:kb-b") {
			t.Errorf("kb %q: error = %v, want the collision of kb-a and kb-b", kb, err)
		}
	}
}

// A collision in a projection that does not receive the KB being approved
// does not stop its approval.
func TestFetchMergedManifestForIgnoresCollisionElsewhere(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-b": "", "kb-c": ""}, "sync_pull", func(kb string) string {
		if kb == "kb-c" {
			return syncPullPayload(t, kb, "only-c")
		}
		return syncPullPayload(t, kb, "shared")
	})
	defer srv.Close()
	cfg := approveProjectionConfig(t, srv.URL, true)
	cfg.KnownKBs = append(cfg.KnownKBs, "kb-c")
	if err := cfg.BindWorkspace("claude", t.TempDir(), []string{"kb-c"}); err != nil {
		t.Fatal(err)
	}
	m, err := fetchMergedManifestFor(cfg, "kb-c")
	if err != nil {
		t.Fatalf("approve kb-c: %v", err)
	}
	if len(m.Artifacts) != 1 || m.Artifacts[0].Source != "kb:kb-c" {
		t.Errorf("artifacts = %+v, want kb-c's only", m.Artifacts)
	}
	if _, err := fetchMergedManifestFor(cfg, ""); err == nil {
		t.Error("without --kb the shared projection's collision must stay")
	}
}

// A server that names no KB has nothing to judge per projection: --kb still
// gets everything it serves, merged once.
func TestFetchMergedManifestForBareServerKeepsArtifactsWithKB(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, "", "bare-skill")
	})
	defer srv.Close()
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", Agents: []string{"claude"}}
	m, err := fetchMergedManifestFor(cfg, "kb-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 1 || m.Artifacts[0].Name != "bare-skill" {
		t.Fatalf("artifacts = %+v, want the bare server's skill", m.Artifacts)
	}
}

// artifacts: false drops a KB's artifacts whether or not a projection
// receives it, as sync does.
func TestFetchMergedManifestForDropsOptedOutKBReachedOrNot(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-b": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "skill-"+kb)
	})
	defer srv.Close()
	off := false
	for _, bind := range []bool{true, false} {
		cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", KnownKBs: []string{"kb-a", "kb-b"}, Agents: []string{"claude"},
			KBOptions: map[string]clientconfig.KBOption{"kb-a": {Artifacts: &off}}}
		if bind {
			if err := cfg.Bind("claude", "kb-b"); err != nil { // kb-a is reached by no provider
				t.Fatal(err)
			}
		} else if err := cfg.Bind("claude", "kb-a"); err != nil { // kb-a reached
			t.Fatal(err)
		}
		m, err := fetchMergedManifestFor(cfg, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range m.Artifacts {
			if a.Source == "kb:kb-a" {
				t.Errorf("bound=%v: opted-out kb-a artifact %s kept", bind, a.Name)
			}
		}
	}
}

// A KB copy shadows the bundle and plugin copy of the same kind and name
// across the union of projections, as it does inside one merge.
func TestKBShadowsBundleAcrossProjections(t *testing.T) {
	arts := []contract.Artifact{
		{Kind: "skill", Name: "ops", Source: "bundle"},
		{Kind: "skill", Name: "ops", Source: "kb:kb-a"},
		{Kind: "skill", Name: "only-bundle", Source: "bundle"},
		{Kind: "skill", Name: "ops", Source: "plugin:p"},
	}
	got := kbShadowsBundle(arts)
	if len(got) != 2 || got[0].Source != "kb:kb-a" || got[1].Name != "only-bundle" {
		t.Fatalf("got %+v", got)
	}
}

// --kb reaches the manifest fetch from the single form, --pending and mcp probe.
func TestApproveCommandsPassKBToTheManifestFetch(t *testing.T) {
	home, _ := approveFixture(t, false)
	_ = home
	var gotKB []string
	approveManifest = func(_ *clientconfig.Config, kb string) (contract.Manifest, error) {
		gotKB = append(gotKB, kb)
		return contract.Manifest{Revision: "r"}, nil
	}
	withStdout(t, func() {
		cmdApprove([]string{"hook", "guard", "--kb", "kb-a", "--yes"})
		cmdApprove([]string{"--pending", "--kb", "kb-b", "--yes"})
		cmdApprove([]string{"hook", "guard", "--yes"})
	})
	if len(gotKB) != 3 || gotKB[0] != "kb-a" || gotKB[1] != "kb-b" || gotKB[2] != "" {
		t.Fatalf("kb passed to the fetch = %q, want [kb-a kb-b \"\"]", gotKB)
	}
}
