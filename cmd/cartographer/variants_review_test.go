package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// variantPayload is the sync_pull answer of a KB that declares fork-cli.
func variantPayload(t *testing.T, kb string) string {
	t.Helper()
	files := []provisioning.ArtifactFile{{Path: "fork-cli.yaml", Content: []byte(cachedVariant)}}
	body, err := json.Marshal(map[string]any{
		"revision": "rev-" + kb,
		"artifacts": []map[string]any{{
			"kind": "variant", "name": "fork-cli", "source": "kb:" + kb,
			"content_hash": provisioning.ContentHashFiles(files),
			"files":        []map[string]any{{"path": "fork-cli.yaml", "content_b64": base64.StdEncoding.EncodeToString([]byte(cachedVariant))}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// A KB whose artifacts the client opted out of still declares its variant: the
// cached variant is not retired, and its entry and files stay.
func TestSyncVariantsKeepsAVariantOfAnOptedOutKB(t *testing.T) {
	home, cfg, skill := retireFixture(t)
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": ""}, "sync_pull", func(kb string) string { return variantPayload(t, kb) })
	defer srv.Close()
	cfg.ServerURL = srv.URL + "/mcp"
	cfg.KnownKBs = []string{"kb-a"}
	cfg.SetKBArtifacts("kb-a", false)
	p := syncProjection{Provider: "claude", BaseDir: home, KBs: []string{"kb-a"}}
	ms, failed, declared, err := buildProjectionManifestsVariants(syncStreams{}, cfg, []syncProjection{p}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range ms[projectionKey(p)].Artifacts {
		if a.Kind == "variant" {
			t.Fatalf("the opt-out left the variant in the manifest: %v", a)
		}
	}
	var out, errw bytes.Buffer
	retired := syncVariants(&out, &errw, cfg, home, []syncProjection{p}, declared, ms, failed, true, false)
	if len(retired) != 0 {
		t.Fatalf("retired %v: %s%s", retired, out.String(), errw.String())
	}
	if c, _ := clientvariant.LoadCache(home); len(c) != 1 || !registered("fork-cli") || len(cfg.Agents) != 1 {
		t.Fatalf("cache %v registered=%v agents=%v", c, registered("fork-cli"), cfg.Agents)
	}
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("the file went: %v", err)
	}
}

// Two KBs declaring one variant name do not stop the sync: it goes through the
// real strict merge, registers the variant from neither and names it.
func TestSyncVariantsCollisionThroughTheStrictMerge(t *testing.T) {
	home := variantHome(t)
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-b": ""}, "sync_pull", func(kb string) string { return variantPayload(t, kb) })
	defer srv.Close()
	a := variantArtifact(cachedVariant)
	cfg := writeConfig(t, home, a)
	cfg.ServerURL = srv.URL + "/mcp"
	cfg.KnownKBs = []string{"kb-a", "kb-b"}
	cfg.Agents = nil
	p := syncProjection{Provider: "claude", BaseDir: home, KBs: []string{"kb-a", "kb-b"}}
	ms, failed, declared, err := buildProjectionManifestsVariants(syncStreams{}, cfg, []syncProjection{p}, true, nil)
	if err != nil {
		t.Fatalf("a variant name two KBs declare stopped the sync: %v", err)
	}
	if len(declared) != 2 {
		t.Fatalf("both declarations must reach the reconcile: %v", declared)
	}
	for _, m := range ms[projectionKey(p)].Artifacts {
		if m.Kind == "variant" {
			t.Fatalf("a claimed variant stayed in the manifest: %v", m)
		}
	}
	var out, errw bytes.Buffer
	syncVariants(&out, &errw, cfg, home, []syncProjection{p}, declared, ms, failed, true, false)
	if !strings.Contains(out.String(), "client variant fork-cli: declared by more than one KB") || registered("fork-cli") {
		t.Fatalf("out=%q registered=%v", out.String(), registered("fork-cli"))
	}
	if c, _ := clientvariant.LoadCache(home); len(c) != 0 {
		t.Fatalf("a collision was cached: %v", c)
	}
}

// The strict merge still refuses a skill two KBs claim and leaves a lone
// variant alone.
func TestMergeArtifactsStrictExemptsOnlyVariants(t *testing.T) {
	v := func(src string) provisioning.Artifact {
		return provisioning.Artifact{Kind: "variant", Name: "fork-cli", Source: src, ContentHash: "h"}
	}
	m, err := provisioning.MergeArtifactsStrict([]provisioning.Artifact{v("kb:kb-a"), v("kb:kb-b"), {Kind: "variant", Name: "solo", Source: "kb:kb-a", ContentHash: "h"}})
	if err != nil || len(m.Artifacts) != 1 || m.Artifacts[0].Name != "solo" {
		t.Fatalf("%v %v", m.Artifacts, err)
	}
	s := func(src string) provisioning.Artifact {
		return provisioning.Artifact{Kind: "skill", Name: "x", Source: src, ContentHash: "h"}
	}
	if _, err := provisioning.MergeArtifactsStrict([]provisioning.Artifact{s("kb:kb-a"), s("kb:kb-b")}); err == nil {
		t.Fatal("a skill collision must still fail")
	}
}

// A disconnect that records a refusal (a managed file behind a symlink, D148)
// returns no error: the variant must stay cached, or nothing owns the files.
func TestReconcileKeepsAVariantWhoseDisconnectWasRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	home, cfg, _ := retireFixture(t)
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(real, "skills", "demo", "SKILL.md")
	if err := os.WriteFile(target, []byte("demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, ".fork-cli")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, ".fork-cli")); err != nil {
		t.Fatal(err)
	}
	res := reconcileVariants(home, cfg, nil, nil, map[string]bool{"kb-a": true}, true, false)
	if len(res.Retired) != 0 || len(res.Warnings) == 0 {
		t.Fatalf("%+v", res)
	}
	if c, _ := clientvariant.LoadCache(home); len(c) != 1 || len(cfg.Agents) != 1 {
		t.Fatalf("cache %v agents %v", c, cfg.Agents)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the file behind the link went: %v", err)
	}
	if lf, _ := provisioning.ReadLockFile(lockFilePath(home)); len(lf.Providers["fork-cli"].Managed) == 0 {
		t.Fatal("the lock section was dropped")
	}
}

// An approved variant of an opted-out KB that is not cached yet is neither
// cached nor registered: the opt-out keeps it out, only retirement and
// collisions read the unfiltered declarations.
func TestSyncVariantsDoesNotCacheAVariantOfAnOptedOutKB(t *testing.T) {
	home := variantHome(t)
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": ""}, "sync_pull", func(kb string) string { return variantPayload(t, kb) })
	defer srv.Close()
	cfg := writeConfig(t, home, variantArtifact(cachedVariant))
	cfg.ServerURL = srv.URL + "/mcp"
	cfg.KnownKBs = []string{"kb-a"}
	cfg.Agents = nil
	cfg.SetKBArtifacts("kb-a", false)
	p := syncProjection{Provider: "claude", BaseDir: home, KBs: []string{"kb-a"}}
	ms, failed, declared, err := buildProjectionManifestsVariants(syncStreams{}, cfg, []syncProjection{p}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	syncVariants(&out, &errw, cfg, home, []syncProjection{p}, declared, ms, failed, true, false)
	if c, _ := clientvariant.LoadCache(home); len(c) != 0 || registered("fork-cli") {
		t.Fatalf("cache %v registered=%v out=%q", c, registered("fork-cli"), out.String())
	}
}

// A cached variant whose KB is then opted out and serves new bytes keeps its
// entry as it is (old hash, not refreshed) and stays registered.
func TestSyncVariantsKeepsACachedVariantOfALaterOptedOutKB(t *testing.T) {
	home, cfg, _ := retireFixture(t)
	old, _ := clientvariant.LoadCache(home)
	oldHash := old["fork-cli"].ContentHash
	changed := strings.Replace(cachedVariant, "Fork CLI", "Fork CLI v2", 1)
	files := []provisioning.ArtifactFile{{Path: "fork-cli.yaml", Content: []byte(changed)}}
	v2 := provisioning.Artifact{Kind: "variant", Name: "fork-cli", Source: "kb:kb-a", ContentHash: provisioning.ContentHashFiles(files), Files: files}
	p := syncProjection{Provider: "claude", BaseDir: home, KBs: []string{"kb-a"}}
	var out, errw bytes.Buffer
	retired := syncVariants(&out, &errw, cfg, home, []syncProjection{p}, []provisioning.Artifact{v2}, map[string]provisioning.Manifest{}, nil, true, false)
	c, _ := clientvariant.LoadCache(home)
	if len(retired) != 0 || c["fork-cli"].ContentHash != oldHash || out.Len() != 0 || !registered("fork-cli") {
		t.Fatalf("retired=%v cache=%v registered=%v", retired, c, registered("fork-cli"))
	}
}
