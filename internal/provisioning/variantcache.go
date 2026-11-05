package provisioning

import (
	"fmt"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// VariantContentHash is the manifest hash of a variant descriptor: what the
// server computes for variants/<name>.yaml and what an approval is bound to.
func VariantContentHash(name string, data []byte) string {
	return ContentHashFiles([]ArtifactFile{{Path: name + ".yaml", Content: data}})
}

// VariantCacheArtifact rebuilds the artifact a cache entry stands for, refusing
// bytes that do not hash to the recorded (approved) hash: a hand edit of the
// cache is not an approval.
func VariantCacheArtifact(name string, e clientvariant.CacheEntry) (Artifact, error) {
	data, err := e.Descriptor()
	if err != nil {
		return Artifact{}, err
	}
	if VariantContentHash(name, data) != e.ContentHash {
		return Artifact{}, fmt.Errorf("the descriptor does not match its recorded hash")
	}
	return Artifact{
		Kind: "variant", Name: name, Source: "kb:" + e.SourceKB, ContentHash: e.ContentHash,
		Files: []ArtifactFile{{Path: name + ".yaml", Content: data}},
	}, nil
}

// VariantRegistration is the outcome of RegisterCachedVariants.
type VariantRegistration struct {
	Registered []string
	// Pending are cached variants that the client config no longer authorizes
	// (revoked, descriptor bytes changed, resolved paths moved): cached, never
	// registered.
	Pending []string
	// Warnings are entries that failed a structural check; each is skipped.
	Warnings []string
}

// ResetVariants empties the variant registries (configurator and cells). Tests
// and RegisterCachedVariants use it: a registration is always a whole new set.
func ResetVariants() {
	variantCellsMu.Lock()
	variantCells = map[configurator.Provider]variantEntry{}
	variantCellsMu.Unlock()
	_ = configurator.ReplaceVariants(nil)
}

// RegisterCachedVariants registers the variants of cache that opts authorizes,
// replacing whatever was registered. It is the ONLY path that registers a
// variant from stored bytes, and registration (RegisterVariantCells,
// configurator.RegisterVariant) has no gate of its own, so the gate is here and
// is the one every sync applies: artifactAuthorized, which wants the approval
// bound to the descriptor's hash AND to the fingerprint of the paths it resolves
// to in this environment (D1695). An entry that is not authorized, whose bytes
// do not match its hash, that no longer parses, resolves with paths dropped or
// fails the cell validation registers nothing. opts needs only ApprovedMCP and
// ApprovedVariantPaths.
//
// It must run before clientconfig.Load drops providers the registry does not
// know (D898).
func RegisterCachedVariants(cache clientvariant.Cache, opts ApplyOptions) VariantRegistration {
	var res VariantRegistration
	ResetVariants()
	for _, name := range cache.Names() {
		a, err := VariantCacheArtifact(name, cache[name])
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("client variant %s skipped: %v", name, err))
			continue
		}
		if !artifactAuthorized(a, opts) {
			res.Pending = append(res.Pending, name)
			continue
		}
		v, _, reasons, err := ResolveVariantArtifact(a, opts)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("client variant %s skipped: %v", name, err))
			continue
		}
		if len(reasons) > 0 {
			// A variant whose containment is in doubt writes nowhere.
			res.Warnings = append(res.Warnings, fmt.Sprintf("client variant %s skipped: %s", name, reasons[0]))
			continue
		}
		if _, err := RegisterVariantCells(v); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("client variant %s skipped: %v", name, err))
			continue
		}
		if err := configurator.RegisterVariant(clientvariant.SpecOf(v)); err != nil {
			UnregisterVariantCells(name)
			res.Warnings = append(res.Warnings, fmt.Sprintf("client variant %s skipped: %v", name, err))
			continue
		}
		res.Registered = append(res.Registered, name)
	}
	return res
}

// UnregisterVariant removes one variant from both registries (a revoke, a
// retirement); the others stay registered.
func UnregisterVariant(name string) {
	UnregisterVariantCells(name)
	var keep []configurator.VariantSpec
	for _, v := range configurator.RegisteredVariants() {
		if v.Spec.Name != name {
			keep = append(keep, v.Spec)
		}
	}
	_ = configurator.ReplaceVariants(keep)
}
