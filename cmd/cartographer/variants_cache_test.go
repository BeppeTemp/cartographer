package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/agents"
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
// run: no destructive drop on a name a failure left unknown. The entry is
// approved with the real fingerprint of the descriptor it was cached from, then
// its stored bytes are damaged: registration names the failure.
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

func TestWantsVariantDiscovery(t *testing.T) {
	for _, tc := range []struct {
		target, csv string
		approve     bool
		want        bool
	}{
		{"", "", false, true}, {"all", "", false, true}, {"claude", "", false, false}, {"fork-cli", "", false, true},
		{"", "claude,codex", false, false}, {"", "claude, fork-cli", false, true},
		{"", "claude", true, true}, // --agents <base> --approve-variants discovers
	} {
		if got := wantsVariantDiscovery(tc.target, tc.csv, tc.approve); got != tc.want {
			t.Errorf("wantsVariantDiscovery(%q,%q,%v) = %v, want %v", tc.target, tc.csv, tc.approve, got, tc.want)
		}
	}
}

func TestSetupPlanDefersAgentsWhenAKBSourceExists(t *testing.T) {
	f := baseSetupFacts()
	f.Detected = nil
	f.RemoteProbed = true
	p, err := planSetup(f, setupOptions{Remote: "git@h:me/new.git", ApproveVariants: true})
	if err != nil || !p.AgentsLater || !p.ApproveVariants || len(p.Agents) != 0 {
		t.Fatalf("plan = %+v, err %v: want agents deferred", p, err)
	}
	var b bytes.Buffer
	renderSetupPlan(&b, p, f)
	if !strings.Contains(b.String(), "detected after the KB is mounted") {
		t.Errorf("plan does not say agents are detected after the KB is mounted:\n%s", b.String())
	}
}

func TestReconcileReportsNewlyCachedVariantsOnce(t *testing.T) {
	home := variantHome(t)
	a := variantArtifact(cachedVariant)
	cfg := writeConfig(t, home, a)
	pulled := map[string]bool{"kb-a": true}
	if res := reconcileVariants(home, cfg, []provisioning.Artifact{a}, nil, pulled, true, false); len(res.Added) != 1 || res.Added[0] != "fork-cli" {
		t.Fatalf("first run Added = %v, want [fork-cli]", res.Added)
	}
	if res := reconcileVariants(home, cfg, []provisioning.Artifact{a}, nil, pulled, true, false); len(res.Added) != 0 {
		t.Fatalf("a variant already cached is not new: Added = %v", res.Added)
	}
}

func TestAgentsTableNamesAVariantAfterItsBase(t *testing.T) {
	var b bytes.Buffer
	writeAgentsTable(&b, []agents.Agent{
		{Provider: "claude", Name: "Claude Code", Installed: true},
		{Provider: "fork-cli", Name: "Fork CLI", Installed: true, Variant: true, Base: "crush"},
	}, map[string]bool{"fork-cli": true})
	if !strings.Contains(b.String(), "fork-cli (variant of crush)") {
		t.Errorf("table does not say what the variant forks:\n%s", b.String())
	}
}

func TestDoctorReportsACachedVariantThatIsNotAuthorized(t *testing.T) {
	home := variantHome(t)
	a := variantArtifact(cachedVariant)
	if err := clientvariant.SaveCache(home, cacheOf(a)); err != nil {
		t.Fatal(err)
	}
	got := checkClientVariants(home, &clientconfig.Config{})
	if len(got) != 1 || !strings.Contains(got[0].Fix, "approve variant fork-cli --kb kb-a") {
		t.Fatalf("findings = %+v, want one naming the approve command", got)
	}
}

func TestVariantKBsIsTheDeclaringKBNarrowedBySelection(t *testing.T) {
	for _, tc := range []struct {
		sel  []string
		want []string
	}{
		{nil, []string{"kb-a"}}, {[]string{"all"}, []string{"kb-a"}},
		{[]string{"kb-a", "kb-b"}, []string{"kb-a"}}, {[]string{"kb-b"}, nil},
	} {
		if got := variantKBs("kb-a", tc.sel); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("variantKBs(kb-a, %v) = %v, want %v", tc.sel, got, tc.want)
		}
	}
	if variantKBs("", nil) != nil {
		t.Error("a variant with no known source connects to nothing")
	}
}

// sync connects a newly cached variant bound to its source KB alone, whatever
// else the server mounts (D1702).
func TestConnectNewVariantsBindsOnlyTheSourceKB(t *testing.T) {
	home := variantHome(t)
	a := variantArtifact(cachedVariant)
	cfg := writeConfig(t, home, a)
	cfg.Agents = nil
	cfg.ServerURL = "http://127.0.0.1:1/mcp" // nobody listens: the binding is still recorded
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	if res := reconcileVariants(home, cfg, []provisioning.Artifact{a}, nil, map[string]bool{"kb-a": true}, true, false); len(res.Added) != 1 {
		t.Fatalf("not cached: %+v", res)
	}
	var out, errw bytes.Buffer
	connectNewVariants(&out, &errw, cfg, home, []string{"fork-cli"})
	if !strings.Contains(out.String(), "connected") {
		t.Fatalf("out %q err %q", out.String(), errw.String())
	}
	got, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if kbs, explicit := got.BoundKBs("fork-cli"); !explicit || !reflect.DeepEqual(kbs, []string{"kb-a"}) {
		t.Fatalf("bound to %v (explicit %v), want exactly [kb-a]", kbs, explicit)
	}
}

func TestDiscoverVariantsSaysWhyItFoundNothing(t *testing.T) {
	home := variantHome(t)
	var out, errw bytes.Buffer
	got := discoverVariants(&clientconfig.Config{ServerURL: "http://127.0.0.1:1/mcp"}, home, variantDiscovery{Out: &out, Err: &errw})
	if got != nil || !strings.Contains(errw.String(), "not discovered") {
		t.Fatalf("found %v, stderr %q: want the reason printed", got, errw.String())
	}
}

// setup connects the variant it approved, though nothing was detected when the
// plan was made (D1702).
func TestRunSetup_ConnectsTheVariantItApproved(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	setupDetected = func() []string { return nil }
	saved := setupDiscover
	t.Cleanup(func() { setupDiscover = saved })
	setupDiscover = func(_ string, approve bool, kbs []string) []string {
		*calls = append(*calls, fmt.Sprintf("discover approve=%v kbs=%v", approve, kbs))
		return []string{"fork-cli"}
	}
	if code := runSetup(setupOptions{Remote: "git@h:me/wiki.git", ApproveVariants: true}, false, true, false, noPrompt()); code != 0 {
		t.Fatalf("runSetup = %d", code)
	}
	want := []string{"discover approve=true kbs=[]", "connect --no-input --agents fork-cli --approve-variants"}
	got := 0
	for _, c := range *calls {
		if got < len(want) && strings.HasPrefix(c, want[got]) {
			got++
		}
	}
	if got != len(want) {
		t.Fatalf("calls %q: want them to contain, in order, %q", *calls, want)
	}
}

// scopedFixture caches, approves and registers fork-cli (declared by kb-a) on a
// server nobody listens on: the bindings are still recorded.
func scopedFixture(t *testing.T) string {
	t.Helper()
	home := variantHome(t)
	a := variantArtifact(cachedVariant)
	cfg := writeConfig(t, home, a)
	cfg.Agents = nil
	cfg.ServerURL = "http://127.0.0.1:1/mcp"
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	if res := reconcileVariants(home, cfg, []provisioning.Artifact{a}, nil, map[string]bool{"kb-a": true}, true, false); len(res.Cached) != 1 {
		t.Fatalf("not cached: %+v", res)
	}
	return home
}

func scopedOpts(home string, providers, kbs []string) connectOptions {
	return connectOptions{Providers: providers, Dir: home, ServerURL: "http://127.0.0.1:1/mcp", Name: "wiki", KBs: kbs}
}

func TestDoConnectScopedBindsAVariantToItsSourceKBOnly(t *testing.T) {
	for _, sel := range [][]string{{"kb-a", "kb-b"}, {"all"}, {"kb-b", "all"}} {
		home := scopedFixture(t)
		if _, err := doConnectScoped(scopedOpts(home, []string{"fork-cli"}, sel)); err != nil {
			t.Fatalf("selection %v: %v", sel, err)
		}
		got, _ := clientconfig.Load(home)
		if kbs, explicit := got.BoundKBs("fork-cli"); !explicit || !reflect.DeepEqual(kbs, []string{"kb-a"}) {
			t.Errorf("selection %v: bound to %v (explicit %v), want [kb-a]", sel, kbs, explicit)
		}
	}
}

func TestDoConnectScopedSkipsAVariantTheSelectionLeavesOut(t *testing.T) {
	home := scopedFixture(t)
	// Only the variant, and its KB is not selected: an error, not a quiet success.
	if _, err := doConnectScoped(scopedOpts(home, []string{"fork-cli"}, []string{"kb-b"})); err == nil || !strings.Contains(err.Error(), "kb-a") {
		t.Fatalf("err = %v, want one naming the declaring KB", err)
	}
	// Mixed with a built-in: the built-in connects with the selection, the variant is left out.
	res, err := doConnectScoped(scopedOpts(home, []string{"crush", "fork-cli"}, []string{"kb-b"}))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := clientconfig.Load(home)
	if kbs, _ := got.BoundKBs("crush"); !reflect.DeepEqual(kbs, []string{"kb-b"}) {
		t.Errorf("crush bound to %v, want [kb-b]", kbs)
	}
	if slices.Contains(got.Agents, "fork-cli") || slices.Contains(res.Providers, "fork-cli") {
		t.Errorf("fork-cli was connected: agents %v", got.Agents)
	}
}

func TestMergeDiscoveredJoinsAnExplicitList(t *testing.T) {
	if got := mergeDiscovered([]string{"crush"}, []string{"fork-cli", "crush"}); !reflect.DeepEqual(got, []string{"crush", "fork-cli"}) {
		t.Errorf("merged = %v", got)
	}
}

// A KB that turned artifacts off installs nothing through sync, so discovery
// neither approves nor caches its variants either; the same KB with artifacts on
// is the control.
func TestDiscoverVariantsLeavesOutAKBWithArtifactsOff(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name string
		opts map[string]clientconfig.KBOption
		want bool // cached
	}{
		{"artifacts on", nil, true},
		{"artifacts off", map[string]clientconfig.KBOption{"kb-a": {Artifacts: &off}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := variantHome(t)
			a := variantArtifact(cachedVariant)
			srv := prefixAwareMCPServer(t, map[string]string{"kb-a": ""}, "sync_pull", func(kb string) string {
				return pulledHookPayload(t, kb, a)
			})
			defer srv.Close()
			cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", ServerName: "wiki", KBOptions: tc.opts}
			if err := clientconfig.Save(home, cfg); err != nil {
				t.Fatal(err)
			}
			var out, errw bytes.Buffer
			discoverVariants(cfg, home, variantDiscovery{Approve: true, Out: &out, Err: &errw})
			c, _ := clientvariant.LoadCache(home)
			got, _ := clientconfig.Load(home)
			approved := len(got.MCPApprovals["kb-a"]) != 0
			if (len(c) != 0) != tc.want || approved != tc.want {
				t.Fatalf("cached %v approved %v, want %v\nout %q err %q", c, approved, tc.want, out.String(), errw.String())
			}
			if !tc.want && !strings.Contains(out.String(), "kb-a") {
				t.Errorf("no note naming the KB: %q", out.String())
			}
		})
	}
}
