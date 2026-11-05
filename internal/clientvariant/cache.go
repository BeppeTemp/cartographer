package clientvariant

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// CacheFileName is the cache of accepted variants, in the client directory.
const CacheFileName = ".cartographer-variants.json"

// CacheEntry is one accepted variant. Every entry was approved by hash; the
// descriptor is kept whole so the variant can be registered before any KB is
// reached and retired after the KB stopped declaring it.
type CacheEntry struct {
	SourceKB      string    `json:"source_kb"`
	ContentHash   string    `json:"content_hash"`
	DescriptorB64 string    `json:"descriptor_b64"`
	AcceptedAt    time.Time `json:"accepted_at"`
}

// Descriptor returns the cached descriptor bytes, refusing an entry whose bytes
// do not match its recorded hash (a hand edit is not an approval).
func (e CacheEntry) Descriptor() ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(e.DescriptorB64)
	if err != nil {
		return nil, fmt.Errorf("descriptor is not base64: %w", err)
	}
	if HashOf(data) != e.ContentHash {
		return nil, errors.New("descriptor does not match its recorded hash")
	}
	return data, nil
}

// HashOf is the content hash a cache entry records for descriptor bytes.
func HashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// NewCacheEntry builds the entry for approved descriptor bytes.
func NewCacheEntry(sourceKB string, data []byte, at time.Time) CacheEntry {
	return CacheEntry{SourceKB: sourceKB, ContentHash: HashOf(data), DescriptorB64: base64.StdEncoding.EncodeToString(data), AcceptedAt: at.UTC()}
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

// HostEnv is the Env of this machine.
func HostEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{GOOS: runtime.GOOS, Home: home, Getenv: os.Getenv}
}

// RegisterCached registers every variant of the cache of dir into the
// configurator. It must run before clientconfig.Load: that load drops from the
// config every provider name the registry does not know (D898), and a variant
// registered after it would be deleted from the agents and the lock sections.
//
// The cache is never trusted: each descriptor is hashed, parsed and resolved
// again at every run, and an entry that fails (a hand edit, a schema change, a
// path outside the home) is skipped with a warning and registers nothing. A
// variant that resolves with dropped paths is skipped too: a variant whose
// containment is in doubt writes nowhere. The result is one atomic swap of the
// registered set (configurator.ReplaceVariants), so it is safe to call again.
func RegisterCached(dir string, env Env) (registered []string, warnings []string) {
	c, err := LoadCache(dir)
	if err != nil {
		return nil, []string{"ignoring the client variant cache: " + err.Error()}
	}
	var specs []configurator.VariantSpec
	for _, name := range c.Names() {
		spec, w := specFromEntry(name, c[name], env)
		if w != "" {
			warnings = append(warnings, w)
			continue
		}
		specs = append(specs, spec)
	}
	// One entry the registry refuses (a name that became a built-in) must not
	// take the others down: register one by one into a clean set.
	if err := configurator.ReplaceVariants(nil); err != nil {
		return nil, append(warnings, err.Error())
	}
	for _, s := range specs {
		if err := configurator.RegisterVariant(s); err != nil {
			warnings = append(warnings, "client variant "+s.Name+" skipped: "+err.Error())
			continue
		}
		registered = append(registered, s.Name)
	}
	return registered, warnings
}

func specFromEntry(name string, e CacheEntry, env Env) (configurator.VariantSpec, string) {
	data, err := e.Descriptor()
	if err != nil {
		return configurator.VariantSpec{}, "client variant " + name + " skipped: " + err.Error()
	}
	spec, err := Parse(name, data)
	if err != nil {
		return configurator.VariantSpec{}, "client variant " + name + " skipped: " + err.Error()
	}
	v, reasons := Resolve(spec, env)
	if len(reasons) > 0 {
		return configurator.VariantSpec{}, fmt.Sprintf("client variant %s skipped: %s", name, reasons[0])
	}
	return SpecOf(v), ""
}
