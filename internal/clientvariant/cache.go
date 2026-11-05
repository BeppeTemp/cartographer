package clientvariant

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// CacheFileName is the cache of accepted variants, in the client directory.
const CacheFileName = ".cartographer-variants.json"

// CacheEntry is one accepted variant. The descriptor is kept whole so the
// variant can be registered before any KB is reached and retired after the KB
// stopped declaring it. ContentHash is the manifest hash the operator approved
// (provisioning.VariantContentHash), not a hash of the raw bytes: nothing here
// is consent, the approval in the client config is, and it is re-checked at
// every registration (provisioning.RegisterCachedVariants).
type CacheEntry struct {
	SourceKB      string    `json:"source_kb"`
	ContentHash   string    `json:"content_hash"`
	DescriptorB64 string    `json:"descriptor_b64"`
	AcceptedAt    time.Time `json:"accepted_at"`
}

// Descriptor returns the cached descriptor bytes.
func (e CacheEntry) Descriptor() ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(e.DescriptorB64)
	if err != nil {
		return nil, fmt.Errorf("descriptor is not base64: %w", err)
	}
	return data, nil
}

// NewCacheEntry builds the entry for descriptor bytes the operator approved
// under the manifest hash hash.
func NewCacheEntry(sourceKB, hash string, data []byte, at time.Time) CacheEntry {
	return CacheEntry{SourceKB: sourceKB, ContentHash: hash, DescriptorB64: base64.StdEncoding.EncodeToString(data), AcceptedAt: at.UTC()}
}

// Cache maps a variant name to its accepted entry.
type Cache map[string]CacheEntry

// CachePath is where the cache of the client directory dir lives.
func CachePath(dir string) string { return filepath.Join(dir, CacheFileName) }

// LoadCache reads the cache; an absent file is an empty cache. A file that does
// not parse is an error the caller reports once: the cache holds nothing that
// cannot be rebuilt by the next approved sync, so it never blocks a command.
func LoadCache(dir string) (Cache, error) {
	data, err := os.ReadFile(CachePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return Cache{}, nil
	}
	if err != nil {
		return Cache{}, err
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return Cache{}, fmt.Errorf("%s: %w", CachePath(dir), err)
	}
	if c == nil {
		c = Cache{}
	}
	return c, nil
}

// SaveCache writes the cache atomically, refusing a link. The caller holds the
// client lock (provisioning.LockClientState), as for the state file.
func SaveCache(dir string, c Cache) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return safewrite.WriteFileAtomicNoFollow(dir, CachePath(dir), append(data, '\n'), 0o644)
}

// Names returns the cached variant names, sorted.
func (c Cache) Names() []string {
	out := make([]string, 0, len(c))
	for n := range c {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Retired returns the cached names no KB declares any more. complete says the
// pull of every bound KB finished and every KB was reachable: when it is false
// nothing is retired, a partial view proves no absence.
func (c Cache) Retired(declared map[string]bool, complete bool) []string {
	if !complete {
		return nil
	}
	var out []string
	for _, n := range c.Names() {
		if !declared[n] {
			out = append(out, n)
		}
	}
	return out
}

// CachedNames returns the names in the cache of dir, empty when it is absent or
// unreadable. clientconfig uses it so the unsupported-provider drop (D898) never
// deletes a variant that failed to register this run.
func CachedNames(dir string) map[string]bool {
	c, err := LoadCache(dir)
	if err != nil {
		return nil
	}
	m := make(map[string]bool, len(c))
	for n := range c {
		m[n] = true
	}
	return m
}

// SpecOf is the registry spec of a resolved variant.
func SpecOf(v Variant) configurator.VariantSpec {
	return configurator.VariantSpec{
		Name: v.Name, DisplayName: v.DisplayName, Base: v.Base,
		Binaries: v.Binaries, DetectDirs: v.DetectDirs, MCPFile: v.MCPFile,
		MCPMode: v.MCPMode, SeedTemplate: v.SeedTemplate, SeedDerived: v.SeedDerived,
	}
}

// Without returns a copy of the cache with name dropped.
func (c Cache) Without(name string) Cache {
	out := make(Cache, len(c))
	for n, e := range c {
		if n != name {
			out[n] = e
		}
	}
	return out
}
