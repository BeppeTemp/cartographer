package mcpserver

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// maxClientFieldLen is the maximum number of bytes for client identity fields.
// Fields longer than this are truncated rune-safe (never split a UTF-8 sequence).
const maxClientFieldLen = 64

// maxClientKeys is the maximum number of distinct keys the roster tracks.
// When exceeded, new distinct keys increment overflow instead of being stored.
const maxClientKeys = 64

// clientKey identifies one distinct client population by name, version,
// protocol version, and protocol era.
type clientKey struct {
	clientName      string
	clientVersion   string
	protocolVersion string
	era             protocolEra
}

// clientEntry is the internal tally for one clientKey.
type clientEntry struct {
	count    int64
	lastSeen time.Time
}

// ClientStat is one roster row, as reported to an operator.
type ClientStat struct {
	KB              string    `json:"kb,omitempty"`
	ClientName      string    `json:"client_name"`
	ClientVersion   string    `json:"client_version"`
	ProtocolVersion string    `json:"protocol_version"`
	Era             string    `json:"era"`
	Count           int64     `json:"count"`
	LastSeen        time.Time `json:"last_seen"`
}

// clientRoster maintains a bounded in-memory tally of requests per client
// identity and protocol era.
type clientRoster struct {
	entries  map[clientKey]*clientEntry
	overflow int64
}

// newClientRoster creates an empty clientRoster.
func newClientRoster() *clientRoster {
	return &clientRoster{
		entries: make(map[clientKey]*clientEntry),
	}
}

// truncateUTF8 truncates s to at most max bytes, never splitting a UTF-8
// sequence. It returns the longest prefix of s whose byte length is ≤ max.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	i := 0
	for i < max {
		_, width := utf8.DecodeRuneInString(s[i:])
		if i+width > max {
			break
		}
		i += width
	}
	return s[:i]
}

// record adds one request to the roster. If the key is new and the roster
// already holds maxClientKeys distinct entries, overflow is incremented instead.
// Fields are truncated to maxClientFieldLen bytes (rune-safe).
func (r *clientRoster) record(clientName, clientVersion, protocolVersion string, era protocolEra, now time.Time) {
	// Unknown identity: record under "unknown" with empty version.
	name := truncateUTF8(clientName, maxClientFieldLen)
	if name == "" {
		name = "unknown"
	}
	version := truncateUTF8(clientVersion, maxClientFieldLen)
	protoVer := truncateUTF8(protocolVersion, maxClientFieldLen)

	k := clientKey{
		clientName:      name,
		clientVersion:   version,
		protocolVersion: protoVer,
		era:             era,
	}

	entry, ok := r.entries[k]
	if !ok {
		// New key: check if we've reached the cap.
		if len(r.entries) >= maxClientKeys {
			r.overflow++
			return
		}
		r.entries[k] = &clientEntry{
			count:    1,
			lastSeen: now,
		}
		return
	}
	entry.count++
	entry.lastSeen = now
}

// stats returns a snapshot of the roster as a sorted slice of ClientStat,
// plus the overflow counter. The slice is sorted by (ClientName, ClientVersion,
// ProtocolVersion, Era) for deterministic output.
func (r *clientRoster) stats() ([]ClientStat, int64) {
	result := make([]ClientStat, 0, len(r.entries))
	for k, e := range r.entries {
		eraStr := k.era.String()
		result = append(result, ClientStat{
			ClientName:      k.clientName,
			ClientVersion:   k.clientVersion,
			ProtocolVersion: k.protocolVersion,
			Era:             eraStr,
			Count:           e.count,
			LastSeen:        e.lastSeen,
		})
	}
	slices.SortFunc(result, func(a, b ClientStat) int {
		if c := strings.Compare(a.ClientName, b.ClientName); c != 0 {
			return c
		}
		if c := strings.Compare(a.ClientVersion, b.ClientVersion); c != 0 {
			return c
		}
		if c := strings.Compare(a.ProtocolVersion, b.ProtocolVersion); c != 0 {
			return c
		}
		return strings.Compare(a.Era, b.Era)
	})
	return result, r.overflow
}
