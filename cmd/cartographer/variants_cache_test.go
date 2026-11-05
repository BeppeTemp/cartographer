package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

const cachedVariant = `schema: 1
name: fork-cli
display_name: Fork CLI
base: crush
detect:
  binaries: [fork-cli]
mcp:
  mode: cli
paths:
  global:
    skill:
      dir: "{home}/.fork-cli/skills"
`

const xdgVariant = `schema: 1
name: fork-cli
display_name: Fork CLI
base: crush
detect:
  binaries: [fork-cli]
mcp:
  mode: cli
paths:
  global:
    skill:
      dir: "{xdg_config}/fork-cli/skills"
`

// variantHome is a fresh home that is also the client directory, with the
// registries emptied before and after.
func variantHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	provisioning.ResetVariants()
	t.Cleanup(provisioning.ResetVariants)
	return home
}

func variantArtifact(desc string) provisioning.Artifact {
	files := []provisioning.ArtifactFile{{Path: "fork-cli.yaml", Content: []byte(desc)}}
	return provisioning.Artifact{Kind: "variant", Name: "fork-cli", Source: "kb:kb-a", ContentHash: provisioning.ContentHashFiles(files), Files: files}
}

func cacheOf(a provisioning.Artifact) clientvariant.Cache {
	return clientvariant.Cache{a.Name: clientvariant.NewCacheEntry(strings.TrimPrefix(a.Source, "kb:"), a.ContentHash, a.Files[0].Content, time.Unix(0, 0))}
}

// writeConfig saves a config listing fork-cli as an agent, approving each artifact
// the way `approve variant` records it in the current environment.
func writeConfig(t *testing.T, home string, approved ...provisioning.Artifact) *clientconfig.Config {
	t.Helper()
	cfg := &clientconfig.Config{ServerURL: "http://localhost:8080/mcp", ServerName: "wiki", Agents: []string{"fork-cli"}}
	for _, a := range approved {
		if err := cfg.ApproveVariant(strings.TrimPrefix(a.Source, "kb:"), "variant/"+a.Name, a.ContentHash, provisioning.VariantPathFingerprint(a, provisioning.ApplyOptions{}), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func registered(name string) bool {
	_, ok := configurator.Lookup(configurator.Provider(name))
	return ok
}

// The cache registers before the config loads: the load drops every provider
// the registry does not know (D898), so the reverse order deletes the variant.
func TestRegisteredVariantSurvivesConfigLoad(t *testing.T) {
	home := variantHome(t)
	a := variantArtifact(cachedVariant)
	writeConfig(t, home, a)
	if err := clientvariant.SaveCache(home, cacheOf(a)); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	registerCachedVariants(&errw)
	if !registered("fork-cli") {
		t.Fatalf("not registered, stderr %q", errw.String())
	}
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0] != "fork-cli" || len(cfg.Unsupported) != 0 {
		t.Fatalf("agents %v, unsupported %v, stderr %q", cfg.Agents, cfg.Unsupported, errw.String())
	}
}

// Registration has no gate of its own: a cached variant the config does not
// authorize is never registered, yet its name stays in the config for the run.
func TestUnauthorizedCachedVariantIsNotRegisteredButNotDropped(t *testing.T) {
	a := variantArtifact(cachedVariant)
	edited := variantArtifact(strings.Replace(cachedVariant, ".fork-cli/skills", ".fork-cli/elsewhere", 1))
	for name, tc := range map[string]struct {
		approved []provisioning.Artifact
		cached   provisioning.Artifact
	}{
		"never approved":                       {nil, a},
		"approved, then the descriptor edited": {[]provisioning.Artifact{a}, edited},
	} {
		t.Run(name, func(t *testing.T) {
			home := variantHome(t)
			writeConfig(t, home, tc.approved...)
			if err := clientvariant.SaveCache(home, cacheOf(tc.cached)); err != nil {
				t.Fatal(err)
			}
			var errw bytes.Buffer
			registerCachedVariants(&errw)
			if registered("fork-cli") {
				t.Fatal("a variant the config does not authorize was registered")
			}
			cfg, err := clientconfig.Load(home)
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Agents) != 1 || len(cfg.Unsupported) != 0 {
				t.Fatalf("agents %v, unsupported %v", cfg.Agents, cfg.Unsupported)
			}
		})
	}
}

// The approval binds the resolved paths: the same approved bytes under another
// XDG_CONFIG_HOME (a service unit, say) are not registered.
func TestApprovedVariantIsNotRegisteredWhenItsPathsMoved(t *testing.T) {
	home := variantHome(t)
	a := variantArtifact(xdgVariant)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-a"))
	writeConfig(t, home, a)
	if err := clientvariant.SaveCache(home, cacheOf(a)); err != nil {
		t.Fatal(err)
	}
	registerCachedVariants(&bytes.Buffer{})
	if !registered("fork-cli") {
		t.Fatal("the variant is not registered in the environment it was approved in")
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-b"))
	registerCachedVariants(&bytes.Buffer{})
	if registered("fork-cli") {
		t.Fatal("a variant approved for other paths was registered")
	}
}

// A cached variant that fails to register keeps its name in the config for the
// run: no destructive drop on a name a failure left unknown.
func TestUnregisteredCachedNameIsNotDropped(t *testing.T) {
	home := variantHome(t)
	good := variantArtifact(cachedVariant)
	writeConfig(t, home, good)
	// Hand-edited bytes under the approved hash.
	c := cacheOf(good)
	e := c["fork-cli"]
	c["fork-cli"] = clientvariant.NewCacheEntry("kb-a", e.ContentHash, []byte("{broken"), time.Unix(0, 0))
	if err := clientvariant.SaveCache(home, c); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	registerCachedVariants(&errw)
	if errw.Len() == 0 {
		t.Fatal("no warning for the broken entry")
	}
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 1 || len(cfg.Unsupported) != 0 {
		t.Fatalf("agents %v, unsupported %v", cfg.Agents, cfg.Unsupported)
	}
}

// First discovery caches and registers; a restart restores it from the cache.
func TestReconcileCachesRegistersAndRestartRestores(t *testing.T) {
	home := variantHome(t)
	a := variantArtifact(cachedVariant)
	cfg := writeConfig(t, home, a)
	res := reconcileVariants(home, cfg, []provisioning.Artifact{a}, nil, map[string]bool{"kb-a": true}, true, false)
	if len(res.Cached) != 1 || len(res.Pending) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("%+v", res)
	}
	if !registered("fork-cli") {
		t.Fatal("not registered after discovery")
	}
	provisioning.ResetVariants() // a restart
	registerCachedVariants(&bytes.Buffer{})
	if !registered("fork-cli") {
		t.Fatal("a restart did not restore the variant")
	}
}

// A declared variant nobody approved is reported with its command, cached
// nothing and never registered.
func TestReconcileLeavesAnUnapprovedVariantPending(t *testing.T) {
	home := variantHome(t)
	a := variantArtifact(cachedVariant)
	cfg := writeConfig(t, home)
	res := reconcileVariants(home, cfg, []provisioning.Artifact{a}, nil, map[string]bool{"kb-a": true}, true, false)
	if len(res.Pending) != 1 || len(res.Cached) != 0 || registered("fork-cli") {
		t.Fatalf("%+v registered=%v", res, registered("fork-cli"))
	}
	if c, _ := clientvariant.LoadCache(home); len(c) != 0 {
		t.Fatalf("an unapproved variant was cached: %v", c)
	}
	var out bytes.Buffer
	printVariantSync(&out, res, false)
	if !strings.Contains(out.String(), "cartographer approve variant fork-cli --kb kb-a") {
		t.Fatalf("%q", out.String())
	}
}

// A KB that now serves other bytes replaces the entry only when those are
// approved; until then the old entry stays registered and the sync reports it.
func TestReconcileRefreshNeedsTheNewApproval(t *testing.T) {
	home := variantHome(t)
	v1 := variantArtifact(cachedVariant)
	v2 := variantArtifact(strings.Replace(cachedVariant, ".fork-cli/skills", ".fork-cli/newer", 1))
	cfg := writeConfig(t, home, v1)
	pulled := map[string]bool{"kb-a": true}
	reconcileVariants(home, cfg, []provisioning.Artifact{v1}, nil, pulled, true, false)

	res := reconcileVariants(home, cfg, []provisioning.Artifact{v2}, nil, pulled, true, false)
	if len(res.Pending) != 1 || len(res.Cached) != 0 {
		t.Fatalf("%+v", res)
	}
	if c, _ := clientvariant.LoadCache(home); c["fork-cli"].ContentHash != v1.ContentHash {
		t.Fatal("unapproved bytes replaced the cached entry")
	}
	if !registered("fork-cli") {
		t.Fatal("the old entry stopped being registered")
	}

	cfg = writeConfig(t, home, v1, v2)
	res = reconcileVariants(home, cfg, []provisioning.Artifact{v2}, nil, pulled, true, false)
	if len(res.Cached) != 1 || len(res.Pending) != 0 {
		t.Fatalf("%+v", res)
	}
	if c, _ := clientvariant.LoadCache(home); c["fork-cli"].ContentHash != v2.ContentHash {
		t.Fatal("approved bytes did not replace the entry")
	}
}

// Two KBs declaring one name: registered from neither, cache untouched.
func TestReconcileCollisionRegistersNeither(t *testing.T) {
	home := variantHome(t)
	a := variantArtifact(cachedVariant)
	b := a
	b.Source = "kb:kb-b"
	cfg := writeConfig(t, home, a)
	res := reconcileVariants(home, cfg, []provisioning.Artifact{a, b}, nil, map[string]bool{"kb-a": true, "kb-b": true}, true, false)
	if len(res.Collisions) != 1 || len(res.Cached) != 0 || registered("fork-cli") {
		t.Fatalf("%+v registered=%v", res, registered("fork-cli"))
	}
	if c, _ := clientvariant.LoadCache(home); len(c) != 0 {
		t.Fatalf("a collision was cached: %v", c)
	}
}

// retireFixture caches fork-cli, registers it and installs one managed skill file.
func retireFixture(t *testing.T) (home string, cfg *clientconfig.Config, skill string) {
	t.Helper()
	home = variantHome(t)
	a := variantArtifact(cachedVariant)
	cfg = writeConfig(t, home, a)
	reconcileVariants(home, cfg, []provisioning.Artifact{a}, nil, map[string]bool{"kb-a": true}, true, false)
	skillRel := filepath.Join(".fork-cli", "skills", "demo", "SKILL.md")
	skill = filepath.Join(home, skillRel)
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{}}
	lf.SetProvider("fork-cli", provisioning.Lock{AppliedRevision: "r", Managed: []provisioning.ManagedFile{{Kind: "skill", Name: "demo", Path: skillRel, ContentHash: "h"}}})
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	return home, cfg, skill
}

// A KB that answered and no longer declares the variant retires it: files
// pruned through the lock, cache entry, registration and agent name gone.
func TestReconcileRetiresAVariantItsKBDropped(t *testing.T) {
	home, cfg, skill := retireFixture(t)
	res := reconcileVariants(home, cfg, nil, nil, map[string]bool{"kb-a": true}, true, false)
	if len(res.Retired) != 1 || len(res.Warnings) != 0 {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(skill); !os.IsNotExist(err) {
		t.Fatalf("the variant's file stayed: %v", err)
	}
	if c, _ := clientvariant.LoadCache(home); len(c) != 0 {
		t.Fatalf("cache %v", c)
	}
	if registered("fork-cli") || len(cfg.Agents) != 0 {
		t.Fatalf("registered=%v agents=%v", registered("fork-cli"), cfg.Agents)
	}
	if lf, _ := provisioning.ReadLockFile(lockFilePath(home)); len(lf.Providers["fork-cli"].Managed) != 0 {
		t.Fatal("the lock section stayed")
	}
}

// Nothing is retired on a partial view: server unreachable, or the variant's own
// KB not among those that answered; a dry run reports and changes nothing.
func TestReconcileRetiresNothingOnAPartialView(t *testing.T) {
	for name, tc := range map[string]struct {
		pulled   map[string]bool
		complete bool
		dry      bool
	}{
		"server unreachable":  {map[string]bool{"kb-a": true}, false, false},
		"its KB not answered": {map[string]bool{}, true, false},
		"dry run":             {map[string]bool{"kb-a": true}, true, true},
	} {
		t.Run(name, func(t *testing.T) {
			home, cfg, skill := retireFixture(t)
			res := reconcileVariants(home, cfg, nil, nil, tc.pulled, tc.complete, tc.dry)
			if tc.dry != (len(res.Retired) == 1) {
				t.Fatalf("%+v", res)
			}
			if _, err := os.Stat(skill); err != nil {
				t.Fatalf("the file went: %v", err)
			}
			if c, _ := clientvariant.LoadCache(home); len(c) != 1 || !registered("fork-cli") || len(cfg.Agents) != 1 {
				t.Fatalf("cache %v registered=%v agents=%v", c, registered("fork-cli"), cfg.Agents)
			}
		})
	}
}

// approve revoke variant: out of the cache and the registry; files stay.
func TestForgetCachedVariant(t *testing.T) {
	home, _, skill := retireFixture(t)
	if err := forgetCachedVariant(home, "fork-cli"); err != nil {
		t.Fatal(err)
	}
	if c, _ := clientvariant.LoadCache(home); len(c) != 0 || registered("fork-cli") {
		t.Fatalf("cache %v registered=%v", c, registered("fork-cli"))
	}
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("revoke removed what was installed: %v", err)
	}
}
