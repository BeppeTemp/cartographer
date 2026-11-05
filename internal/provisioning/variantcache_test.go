package provisioning

import (
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func cacheFor(a Artifact) clientvariant.Cache {
	return clientvariant.Cache{a.Name: clientvariant.NewCacheEntry("kb-a", a.ContentHash, a.Files[0].Content, time.Unix(0, 0))}
}

// registeredEverywhere reports whether the variant is visible to the configurator
// registry and to the destination cells: both must agree.
func registeredEverywhere(name string) (reg, cells bool) {
	_, reg = configurator.Lookup(configurator.Provider(name))
	return reg, destDirScoped("skill", "x", configurator.Provider(name), ScopeGlobal) != ""
}

func cleanVariants(t *testing.T) {
	t.Helper()
	ResetVariants()
	t.Cleanup(ResetVariants)
}

// Registration has no gate of its own, so RegisterCachedVariants is the gate
// (PR 5 security review): hash AND resolved-path fingerprint, or nothing.
func TestRegisterCachedVariantsRegistersOnlyWhatIsAuthorized(t *testing.T) {
	cleanVariants(t)
	fixedEnv(t, "")
	a := variantWith(variantDescriptor)
	approved, paths := approvalFor(a)
	opts := ApplyOptions{ApprovedMCP: approved, ApprovedVariantPaths: paths}

	res := RegisterCachedVariants(cacheFor(a), opts)
	if len(res.Registered) != 1 || len(res.Warnings) != 0 {
		t.Fatalf("approved: %+v", res)
	}
	if reg, cells := registeredEverywhere("fork-cli"); !reg || !cells {
		t.Fatalf("approved variant: registry %v, cells %v", reg, cells)
	}

	for name, tc := range map[string]struct {
		cache clientvariant.Cache
		opts  ApplyOptions
	}{
		"unapproved":                  {cacheFor(a), ApplyOptions{}},
		"hash without paths":          {cacheFor(a), ApplyOptions{ApprovedMCP: approved}},
		"another approved hash":       {cacheFor(a), ApplyOptions{ApprovedMCP: map[string]string{"kb:kb-a\x00variant/fork-cli": "other"}, ApprovedVariantPaths: paths}},
		"descriptor edited, rehashed": {cacheFor(variantWith(strings.Replace(variantDescriptor, ".fork-cli/skills", ".fork-cli/elsewhere", 1))), opts},
	} {
		t.Run(name, func(t *testing.T) {
			res := RegisterCachedVariants(tc.cache, tc.opts)
			if len(res.Registered) != 0 || len(res.Pending) != 1 {
				t.Fatalf("%+v", res)
			}
			// The previous registration is gone too: a registration is a whole new set.
			if reg, cells := registeredEverywhere("fork-cli"); reg || cells {
				t.Fatalf("not authorized but registered: registry %v, cells %v", reg, cells)
			}
		})
	}
}

// An approved descriptor whose cached bytes were edited by hand is skipped with a
// warning, and one the operator never approved is pending even if its hash matches.
func TestRegisterCachedVariantsRefusesAHandEditedEntry(t *testing.T) {
	cleanVariants(t)
	fixedEnv(t, "")
	a := variantWith(variantDescriptor)
	approved, paths := approvalFor(a)
	c := cacheFor(a)
	e := c["fork-cli"]
	e2 := clientvariant.NewCacheEntry("kb-a", e.ContentHash, []byte(strings.Replace(variantDescriptor, ".fork-cli/skills", "../x", 1)), time.Unix(0, 0))
	c["fork-cli"] = e2
	res := RegisterCachedVariants(c, ApplyOptions{ApprovedMCP: approved, ApprovedVariantPaths: paths})
	if len(res.Registered) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "hash") {
		t.Fatalf("%+v", res)
	}
	if reg, cells := registeredEverywhere("fork-cli"); reg || cells {
		t.Fatal("a hand-edited entry registered")
	}
}

// The same approved bytes resolved under another XDG_CONFIG_HOME land in
// directories the operator never saw: not registered (D1695).
func TestRegisterCachedVariantsRefusesMovedPaths(t *testing.T) {
	cleanVariants(t)
	a := variantWith(xdgDescriptor)
	fixedEnv(t, "")
	approved, paths := approvalFor(a)
	opts := ApplyOptions{ApprovedMCP: approved, ApprovedVariantPaths: paths}
	if res := RegisterCachedVariants(cacheFor(a), opts); len(res.Registered) != 1 {
		t.Fatalf("same environment: %+v", res)
	}
	fixedEnv(t, "/srv/elsewhere")
	res := RegisterCachedVariants(cacheFor(a), opts)
	if len(res.Registered) != 0 || len(res.Pending) != 1 {
		t.Fatalf("moved paths: %+v", res)
	}
	if reg, cells := registeredEverywhere("fork-cli"); reg || cells {
		t.Fatal("a variant approved for other paths is registered")
	}
}

// A variant that resolves with a dropped path (here an escape of the home) is
// refused even with a matching approval: containment is re-proved at every start.
func TestRegisterCachedVariantsReprovesContainment(t *testing.T) {
	cleanVariants(t)
	fixedEnv(t, "")
	a := variantWith(strings.Replace(variantDescriptor, "{home}/.fork-cli/skills", "{home}/../x", 1))
	approved, paths := approvalFor(a)
	res := RegisterCachedVariants(cacheFor(a), ApplyOptions{ApprovedMCP: approved, ApprovedVariantPaths: paths})
	if len(res.Registered) != 0 {
		t.Fatalf("%+v", res)
	}
	if reg, cells := registeredEverywhere("fork-cli"); reg || cells {
		t.Fatal("an escaping variant registered")
	}
}

func TestUnregisterVariantLeavesTheOthers(t *testing.T) {
	cleanVariants(t)
	fixedEnv(t, "")
	a := variantWith(variantDescriptor)
	approved, paths := approvalFor(a)
	RegisterCachedVariants(cacheFor(a), ApplyOptions{ApprovedMCP: approved, ApprovedVariantPaths: paths})
	UnregisterVariant("fork-cli")
	if reg, cells := registeredEverywhere("fork-cli"); reg || cells {
		t.Fatal("still registered")
	}
}
