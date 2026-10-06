package kb

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Artifact usage (D326): which skills and agents the clients actually load.
// The evidence comes from the clients' own session transcripts, reduced to an
// aggregate on the client and posted to the server; this file is the server's
// store for it. It lives in <root>/.cartographer/usage.json, local-only like
// every file there: "is this skill used?" is a question about one operator's
// machines, so it is neither committed nor replicated between servers.

const usageFilename = "usage.json"

// usageMu serialises usage merges (read-modify-write on one small file). One
// package-level lock rather than a field: KB is copied by value in places.
var usageMu sync.Mutex

// UsageEntry is the usage of one artifact by one provider. The JSON tags are
// the wire format of POST /api/usage and match provisioning.ArtifactUsage.
type UsageEntry struct {
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	Provider string    `json:"provider"`
	Source   string    `json:"source,omitempty"`
	LastUsed time.Time `json:"last_used"`
	// Count is the client's activations inside its scan window. It is
	// replaced, never summed: the client already aggregated the window.
	Count int `json:"count"`
}

// UsageKey identifies the artifact an entry is about, across providers.
func UsageKey(kind, name string) string { return kind + "/" + name }

// Key is UsageKey of the entry.
func (e UsageEntry) Key() string { return UsageKey(e.Kind, e.Name) }

func (k *KB) usageFilePath() string {
	return filepath.Join(k.cartographerDirPath(), usageFilename)
}

// LoadUsage reads the usage store. A KB nothing has reported to has none: nil.
func (k *KB) LoadUsage() ([]UsageEntry, error) {
	data, err := os.ReadFile(k.usageFilePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("LoadUsage: %w", err)
	}
	var doc struct {
		Entries []UsageEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("LoadUsage: unmarshal: %w", err)
	}
	return doc.Entries, nil
}

// MergeUsage folds a client's report into the store and returns how many
// entries changed. Per (artifact, provider) the newer LastUsed wins, and its
// Count with it; an entry with no LastUsed is a sighting of nothing, and a bundled
// artifact is not the KB's: both are dropped. Concurrent reports are serialised by usageMu, so two clients
// syncing at once cannot lose each other's rows.
func (k *KB) MergeUsage(in []UsageEntry) (int, error) {
	usageMu.Lock()
	defer usageMu.Unlock()
	cur, err := k.LoadUsage()
	if err != nil {
		return 0, err
	}
	type slot struct{ key, provider string }
	idx := map[slot]int{}
	for i, e := range cur {
		idx[slot{e.Key(), e.Provider}] = i
	}
	changed := 0
	for _, e := range in {
		// A bundled skill ships with the binary: the operator cannot retire it,
		// so a finding about it would have no one to act on it.
		if e.Name == "" || e.Kind == "" || e.LastUsed.IsZero() || e.Source == "bundle" {
			continue
		}
		e.LastUsed = e.LastUsed.UTC()
		s := slot{e.Key(), e.Provider}
		i, ok := idx[s]
		switch {
		case !ok:
			idx[s] = len(cur)
			cur = append(cur, e)
			changed++
		case e.LastUsed.After(cur[i].LastUsed) || (e.LastUsed.Equal(cur[i].LastUsed) && e.Count != cur[i].Count):
			cur[i] = e
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}
	sort.Slice(cur, func(i, j int) bool {
		if cur[i].Key() != cur[j].Key() {
			return cur[i].Key() < cur[j].Key()
		}
		return cur[i].Provider < cur[j].Provider
	})
	if err := k.ensureCartographerDir(); err != nil {
		return 0, err
	}
	data, err := json.MarshalIndent(struct {
		Entries []UsageEntry `json:"entries"`
	}{cur}, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("MergeUsage: marshal: %w", err)
	}
	if err := writeFileAtomic(k.usageFilePath(), data); err != nil {
		return 0, err
	}
	return changed, nil
}

// UsageSummary is what a reader wants per artifact: the latest sighting over
// every provider.
type UsageSummary struct {
	LastUsed time.Time
	Provider string
	// Count is the activations summed over providers; 0 with a non-zero
	// LastUsed means the only signal is a catalogue load (Codex).
	Count int
}

// SummarizeUsage reduces the store to one UsageSummary per artifact key. An
// activation always beats a catalogue sighting, however recent the latter.
func SummarizeUsage(entries []UsageEntry) map[string]UsageSummary {
	out := map[string]UsageSummary{}
	for _, e := range entries {
		s := out[e.Key()]
		activated := s.Count > 0
		switch {
		case e.Count > 0 && !activated:
			s = UsageSummary{LastUsed: e.LastUsed, Provider: e.Provider, Count: e.Count}
		case e.Count > 0:
			s.Count += e.Count
			if e.LastUsed.After(s.LastUsed) {
				s.LastUsed, s.Provider = e.LastUsed, e.Provider
			}
		case !activated && e.LastUsed.After(s.LastUsed):
			s.LastUsed, s.Provider = e.LastUsed, e.Provider
		}
		out[e.Key()] = s
	}
	return out
}
