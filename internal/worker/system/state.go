package system

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

// StateFile is the asset of the system Source that holds the state. Its
// extension is outside the list the server indexes into the owner's search
// document (D277): a .json would put the whole system into search hits.
const StateFile = "system-state.jsonl"

// Item is one group as last observed.
type Item struct {
	Key        string `json:"key"`
	Title      string `json:"title"`
	Revision   string `json:"revision"`
	ObservedAt string `json:"observed_at"`
	Snapshot   string `json:"snapshot"`
}

// State is group key -> last handled observation.
type State map[string]Item

// ErrEmptyListing: zero groups while the state has some never means "everything
// was removed" (decision 8): a lost identity or a filter that matched nothing
// would otherwise mark every page absent.
var ErrEmptyListing = errors.New("system: the listing is empty but the state is not")

// Digest is the sha256 hex of bytes, the value asset_read returns as sha256 and
// the Source holds in feed_cursor.
func Digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

// Encode is deterministic: a header line, then one JSON line per group sorted by
// key, LF endings (one line per group keeps git diffs per group).
func (s State) Encode() (data []byte, digest string, err error) {
	var b bytes.Buffer
	b.WriteString("{\"version\":1}\n")
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		it := s[k]
		it.Key = k
		line, err := marshal(it)
		if err != nil {
			return nil, "", err
		}
		b.Write(line)
		b.WriteByte('\n')
		if b.Len() > MaxStateBytes {
			return nil, "", fmt.Errorf("system: the state is over %d bytes", MaxStateBytes)
		}
	}
	return b.Bytes(), Digest(b.Bytes()), nil
}

func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// Decode is strict: an unknown version, a malformed line, a duplicate key or a
// bad revision is an error, which the caller treats as an untrusted state.
func Decode(data []byte) (State, error) {
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) < 2 || string(lines[0]) != `{"version":1}` || len(lines[len(lines)-1]) != 0 {
		return nil, errors.New("system: the state has no version 1 header or no final newline")
	}
	s := State{}
	for i, l := range lines[1 : len(lines)-1] {
		var it Item
		d := json.NewDecoder(bytes.NewReader(l))
		d.DisallowUnknownFields()
		if err := d.Decode(&it); err != nil || d.More() {
			return nil, fmt.Errorf("system: state line %d is malformed", i+2)
		}
		switch {
		case !ValidKey(it.Key):
			return nil, fmt.Errorf("system: state line %d has an invalid key", i+2)
		case !revisionRE.MatchString(it.Revision):
			return nil, fmt.Errorf("system: state line %d has a bad revision", i+2)
		}
		if _, dup := s[it.Key]; dup {
			return nil, fmt.Errorf("system: state line %d repeats a key", i+2)
		}
		s[it.Key] = it
	}
	return s, nil
}

// Handled is one group whose unit exists: Group is the new observation, or for
// a removal only the key matters.
type Handled struct {
	Key     string
	Removed bool
	Group   Group
	At      string // RFC3339 UTC of the poll
}

// Apply returns the old state with the handled groups applied (decision 5): an
// added or modified group takes the new revision and snapshot, a removed one is
// dropped, and a group that failed or was cut keeps its old entry. This is the
// one deliberate difference from a feed, whose opaque cursor cannot express
// partial progress.
func (s State) Apply(handled []Handled) State {
	n := make(State, len(s))
	for k, v := range s {
		n[k] = v
	}
	for _, h := range handled {
		if h.Removed {
			delete(n, h.Key)
			continue
		}
		n[h.Key] = Item{Key: h.Key, Title: h.Group.Title, Revision: h.Group.Revision, ObservedAt: h.At, Snapshot: h.Group.Content}
	}
	return n
}

// Change is one group that differs from the state.
type Change struct {
	Kind     string // recipe.Added, recipe.Modified or recipe.Removed
	Key      string
	Group    Group // the current observation; zero for a removal
	Previous Item  // the state's entry; zero for an added group
}

// Changes are the groups that differ, each list sorted by key.
type Changes struct {
	Added, Modified, Removed []Change
	Unchanged                int
}

// Ordered is every change in key order.
func (c Changes) Ordered() []Change {
	all := append(append(append([]Change{}, c.Added...), c.Modified...), c.Removed...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Key < all[j].Key })
	return all
}

// Plan compares a validated listing (sorted by key) with the state.
func Plan(previous State, current []Group) (Changes, error) {
	var c Changes
	if len(current) == 0 && len(previous) > 0 {
		return c, ErrEmptyListing
	}
	now := map[string]bool{}
	for _, g := range current {
		now[g.Key] = true
		p, had := previous[g.Key]
		switch {
		case !had:
			c.Added = append(c.Added, Change{Kind: recipe.Added, Key: g.Key, Group: g})
		case p.Revision != g.Revision:
			c.Modified = append(c.Modified, Change{Kind: recipe.Modified, Key: g.Key, Group: g, Previous: p})
		default:
			c.Unchanged++
		}
	}
	keys := make([]string, 0)
	for k := range previous {
		if !now[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		c.Removed = append(c.Removed, Change{Kind: recipe.Removed, Key: k, Previous: previous[k]})
	}
	return c, nil
}
