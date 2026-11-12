package mcpserver

import (
	"context"
	"sort"
	"sync"
)

// SkippedKB is a KB the server was told to serve and could not mount: its path
// is missing or moved, or its remote could not be cloned. The server starts
// with the others and reports this one instead of hiding it (#1963).
//
// TRAP: Reason is shown to clients, so it names no server filesystem path
// (the log line has it) and it is only shown to a caller who may see the KB
// (kbRosterVisible), like every other roster entry (D1096).
type SkippedKB struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
	// Retrying is true for a clone that is attempted again with back-off.
	Retrying bool `json:"retrying,omitempty"`
}

// SkippedKBs is the registry of the KBs a start could not mount. The retry of a
// failed clone updates it while requests read it, hence the lock.
type SkippedKBs struct {
	mu   sync.Mutex
	rows map[string]SkippedKB
}

// NewSkippedKBs returns an empty registry.
func NewSkippedKBs() *SkippedKBs { return &SkippedKBs{rows: map[string]SkippedKB{}} }

// Set records (or updates) a skipped KB.
func (s *SkippedKBs) Set(row SkippedKB) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.rows[row.Name] = row
	s.mu.Unlock()
}

// Lookup returns the skipped KB of that name.
func (s *SkippedKBs) Lookup(name string) (SkippedKB, bool) {
	if s == nil {
		return SkippedKB{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[name]
	return row, ok
}

// visible returns, sorted by name, the rows the caller may see.
func (s *SkippedKBs) visible(ctx context.Context) []SkippedKB {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SkippedKB, 0, len(s.rows))
	for _, row := range s.rows {
		if kbRosterVisible(ctx, row.Name) {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SetSkippedKBs attaches the registry of KBs the start could not mount.
func (m *MultiKBServer) SetSkippedKBs(s *SkippedKBs) { m.skipped = s }

// notMountedText is the answer for a KB that is configured but skipped, or ""
// when the caller may not know about it (it then gets the text of an unknown
// KB, so a skipped KB is no existence oracle).
func (m *MultiKBServer) notMountedText(ctx context.Context, name string) string {
	row, ok := m.skipped.Lookup(name)
	if !ok || !kbRosterVisible(ctx, name) {
		return ""
	}
	text := "kb " + quoteJoin([]string{name}) + " is not mounted on this server: " + row.Reason
	if row.Retrying {
		text += " (the server retries the clone; a restart then mounts the KB)"
	}
	return text
}

// Names returns the names of the skipped KBs, sorted.
func (s *SkippedKBs) Names() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.rows))
	for n := range s.rows {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
