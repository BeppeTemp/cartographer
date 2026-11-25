package main

import (
	"encoding/base64"
	"encoding/json"
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

// A KB copy shadows the bundle copy of the same kind and name across the
// union of projections, as it does inside one merge.
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

// shadowPayload is kb's sync_pull answer carrying the bundled skill
// cartographer-ops, and a KB copy of it when kbShips (a bundle copy that
// differs from this client's embedded one is replaced by the embedded one).
func shadowPayload(t *testing.T, kb string, kbShips bool) string {
	t.Helper()
	art := func(source string) map[string]any {
		files := []contract.ArtifactFile{{Path: "SKILL.md", Content: []byte("# ops from " + source)}}
		return map[string]any{
			"kind": "skill", "name": "cartographer-ops", "source": source,
			"content_hash": provisioning.ContentHashFiles(files),
			"files":        []map[string]any{{"path": "SKILL.md", "content_b64": base64.StdEncoding.EncodeToString(files[0].Content)}},
		}
	}
	arts := []map[string]any{art("bundle")}
	if kbShips {
		arts = append(arts, art("kb:"+kb))
	}
	body, err := json.Marshal(map[string]any{"revision": "rev-" + kb, "artifacts": arts})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// End to end: kb-a ships a skill the bundle also ships, kb-b does not, in
// disjoint workspaces. kb-b's projection keeps the bundle copy, kb-a's the KB
// copy; the union must keep the KB copy only, with or without --kb.
func TestFetchMergedManifestForKBShadowsBundleAcrossProjections(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-b": ""}, "sync_pull", func(kb string) string {
		return shadowPayload(t, kb, kb == "kb-a")
	})
	defer srv.Close()
	cfg := approveProjectionConfig(t, srv.URL, false)

	m, err := fetchMergedManifestFor(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, a := range m.Artifacts {
		if a.Name == "cartographer-ops" {
			sources = append(sources, a.Source)
		}
	}
	if len(sources) != 1 || sources[0] != "kb:kb-a" {
		t.Fatalf("cartographer-ops sources = %v, want [kb:kb-a]", sources)
	}
}

// A named KB's artifact that carries no source label (or another KB's) is
// refused with the KB and the artifact named, never merged under a guess.
func TestFetchMergedManifestForRefusesArtifactWithoutSourceLabel(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": ""}, "sync_pull", func(kb string) string {
		return strings.Replace(syncPullPayload(t, kb, "x"), `"source":"kb:kb-a"`, `"source":""`, 1)
	})
	defer srv.Close()
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", KnownKBs: []string{"kb-a"}, Agents: []string{"claude"}}
	for _, kb := range []string{"kb-a", ""} {
		_, err := fetchMergedManifestFor(cfg, kb)
		if err == nil || !strings.Contains(err.Error(), "kb-a") || !strings.Contains(err.Error(), "skill/x") || !strings.Contains(err.Error(), `source ""`) {
			t.Errorf("kb %q: error = %v, want the unlabelled skill/x of kb-a refused", kb, err)
		}
	}
}

// A KB neither bound nor in known_kbs: --kb says to bind it or run sync,
// before any request (#2347). A known KB that ships no such name keeps its
// message; a per-KB endpoint and a bare server skip the guard.
func TestFetchMergedManifestForUnknownKBSaysToSync(t *testing.T) {
	hits := 0
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-b": ""}, "sync_pull", func(kb string) string {
		hits++
		return syncPullPayload(t, kb, "skill-"+kb)
	})
	defer srv.Close()
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", KnownKBs: []string{"kb-a"}, Agents: []string{"claude"}}

	_, err := fetchMergedManifestFor(cfg, "kb-new")
	if err == nil || !strings.Contains(err.Error(), "run cartographer sync") || !strings.Contains(err.Error(), "kb-new") {
		t.Fatalf("unknown KB: error = %v, want the run-sync hint", err)
	}
	if hits != 0 {
		t.Errorf("%d pulls before the guard, want none", hits)
	}

	m, err := fetchMergedManifestFor(cfg, "kb-a")
	if err != nil || len(m.Artifacts) != 1 || m.Artifacts[0].Source != "kb:kb-a" {
		t.Fatalf("known KB: %+v, %v", m.Artifacts, err)
	}

	perKB := &clientconfig.Config{ServerURL: srv.URL + "/mcp/kb-b", KnownKBs: []string{"kb-a"}, Agents: []string{"claude"}}
	if _, err := fetchMergedManifestFor(perKB, "kb-b"); err != nil && strings.Contains(err.Error(), "run cartographer sync") {
		t.Errorf("per-KB endpoint guarded: %v", err)
	}

	bare := prefixAwareMCPServer(t, map[string]string{}, "sync_pull", func(string) string { return syncPullPayload(t, "", "bare-skill") })
	defer bare.Close()
	bcfg := &clientconfig.Config{ServerURL: bare.URL + "/mcp", Agents: []string{"claude"}}
	if _, err := fetchMergedManifestFor(bcfg, "kb-new"); err != nil {
		t.Errorf("bare server guarded: %v", err)
	}
}

// Command level: approve --kb on an unsynced KB exits 2 with the hint.
func TestApproveKBNotSyncedExitsWithSyncHint(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", KnownKBs: []string{"kb-a"}, Agents: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	oldM := approveManifest
	approveManifest = fetchMergedManifestFor
	t.Cleanup(func() { approveManifest = oldM })
	var code int
	stderr := withStderr(t, func() { code = cmdApprove([]string{"mcp", "x", "--kb", "kb-new", "--yes"}) })
	if code != 2 || !strings.Contains(stderr, "run cartographer sync") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

// boundNotSyncedConfig: kb-a is known, kb-new is bound with it to one
// workspace but not in known_kbs yet (#2349).
func boundNotSyncedConfig(t *testing.T, srvURL string) *clientconfig.Config {
	t.Helper()
	cfg := &clientconfig.Config{ServerURL: srvURL + "/mcp", KnownKBs: []string{"kb-a"}, Agents: []string{"claude"}}
	if err := cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := cfg.BindWorkspace("claude", t.TempDir(), []string{"kb-a", "kb-new"}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A bound KB not yet synced is pulled: its name collision with a known KB of
// the projection is seen before the first sync, and --kb on it works.
func TestFetchMergedManifestForPullsBoundNotSyncedKB(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-new": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "shared")
	})
	defer srv.Close()
	cfg := boundNotSyncedConfig(t, srv.URL)
	for _, kb := range []string{"kb-a", "kb-new", ""} {
		_, err := fetchMergedManifestFor(cfg, kb)
		var ce *provisioning.CollisionError
		if !errors.As(err, &ce) || !strings.Contains(err.Error(), "kb:kb-new") {
			t.Errorf("kb %q: error = %v, want the collision with kb-new", kb, err)
		}
	}

	ok := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-new": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "skill-"+kb)
	})
	defer ok.Close()
	m, err := fetchMergedManifestFor(boundNotSyncedConfig(t, ok.URL), "kb-new")
	if err != nil || len(m.Artifacts) != 2 {
		t.Fatalf("approve --kb kb-new: %+v, %v", m.Artifacts, err)
	}
}

// A bound-only KB the server does not mount leaves out its projections with a
// warning; the others are still judged.
func TestFetchMergedManifestForBoundKBNotMountedIsSkippedWithWarning(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-b": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "skill-"+kb)
	})
	defer srv.Close()
	cfg := boundNotSyncedConfig(t, srv.URL)
	cfg.KnownKBs = []string{"kb-a", "kb-b"}
	if err := cfg.BindWorkspace("claude", t.TempDir(), []string{"kb-b"}); err != nil {
		t.Fatal(err)
	}
	var m contract.Manifest
	var err error
	stderr := withStderr(t, func() { m, err = fetchMergedManifestFor(cfg, "") })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "warning: kb-new is bound but could not be pulled; collisions with it were not checked") {
		t.Errorf("stderr = %q, want the warning", stderr)
	}
	got := map[string]bool{}
	for _, a := range m.Artifacts {
		got[a.Source] = true
	}
	if !got["kb:kb-b"] {
		t.Errorf("sources = %v, want kb-b judged despite the missing kb-new", got)
	}
}

// A known KB the server does not mount stays fatal, bound or not.
func TestFetchMergedManifestForKnownKBFailureStaysFatal(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-new": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "skill-"+kb)
	})
	defer srv.Close()
	_, err := fetchMergedManifestFor(boundNotSyncedConfig(t, srv.URL), "")
	if err == nil || !strings.Contains(err.Error(), "kb-a") {
		t.Fatalf("error = %v, want the failure of known kb-a", err)
	}
}

// A per-KB endpoint pulls its own KB only, as before.
func TestFetchMergedManifestForPerKBEndpointIgnoresBound(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-new": ""}, "sync_pull", func(string) string {
		return syncPullPayload(t, "kb-a", "skill-a")
	})
	defer srv.Close()
	cfg := boundNotSyncedConfig(t, srv.URL)
	cfg.ServerURL = srv.URL + "/mcp/kb-a"
	m, err := fetchMergedManifestFor(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Artifacts {
		if a.Source != "kb:kb-a" {
			t.Errorf("artifact %s from %s, want kb-a only", a.Name, a.Source)
		}
	}
}
