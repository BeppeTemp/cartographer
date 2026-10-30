package synthesis

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/worker"
)

// Defaults of the perimeter knobs (documented in docs/deployment.md).
const (
	DefaultMaxCandidates = 5
	DefaultMaxNew        = 3
	DefaultContextTokens = 6000
	DefaultReserve       = 0.2
	// AnswerCap is the fixed answer allowance added to the context cap when a
	// candidate's cost is estimated before it starts.
	AnswerCap = 1024
	// logWindow is how far back the closing entries of earlier runs are read
	// to skip the candidates they already examined (D13: losing the log only
	// means a candidate is examined again).
	logWindow = 7 * 24 * time.Hour
)

// Config is what the task reads from its entry; zero values take the defaults.
type Config struct {
	// Map is the contradiction map; empty means the map of the newest existing
	// Contradiction (the task never creates a map).
	Map           string
	MaxCandidates int
	MaxNew        int
	ContextTokens int
	Reserve       float64
}

// WithDefaults fills the zero values.
func (c Config) WithDefaults() Config {
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = DefaultMaxCandidates
	}
	if c.MaxNew <= 0 {
		c.MaxNew = DefaultMaxNew
	}
	if c.ContextTokens <= 0 {
		c.ContextTokens = DefaultContextTokens
	}
	if c.Reserve <= 0 {
		c.Reserve = DefaultReserve
	}
	return c
}

// Budget is the daily budget as the worker sees it (D12: the worker enforces).
type Budget struct{ Daily, Remaining int }

// ErrNoMap ends the task without a write: "synthesis: no contradiction map,
// skipped". The task never creates a map (map_create is outside the allowlist).
var ErrNoMap = errors.New("synthesis: no contradiction map, skipped")

// Candidate is one thing worth the model's attention.
type Candidate struct {
	Source   string // glossary_gap, search_miss, repeated_fact or link_suggest
	Kind     string // the gap kind a hypothesis about it would have
	Subject  string // the term, query or question, as data
	Involves []string
	ID       string // the concept id the hypothesis would be written under
}

// Key is the candidate key listed in the run record: its concept id.
func (c Candidate) Key() string { return c.ID }

// ExaminedPrefix starts the line of a run record that lists the keys examined.
const ExaminedPrefix = "synthesis examined: "

// ExaminedLine is the line the run's closing entry carries.
func ExaminedLine(keys []string) string { return ExaminedPrefix + strings.Join(keys, ",") }

type world struct {
	tools     worker.ToolCaller
	existing  map[string]bool     // every Contradiction id, any status
	synthOpen [][]string          // involves of open synthesis items
	otherOpen map[string][]string // open gaps of the doctor or a person: id -> involves
	examined  map[string]bool
}

// Candidates returns the ordered, bounded list for one run. Read-only; every
// input is a public MCP call. It returns nothing when the reserve blocks the
// start, and never more than the caps.
func Candidates(tools worker.ToolCaller, cfg Config, b Budget, now time.Time) ([]Candidate, Config, error) {
	cfg = cfg.WithDefaults()
	reserve := int(cfg.Reserve * float64(b.Daily))
	per := cfg.ContextTokens + AnswerCap
	n := min(cfg.MaxCandidates, (b.Remaining-reserve)/per)
	if b.Remaining <= reserve || n <= 0 {
		return nil, cfg, nil
	}
	w := &world{tools: tools, existing: map[string]bool{}, otherOpen: map[string][]string{}, examined: map[string]bool{}}
	if err := w.load(&cfg, now); err != nil {
		return nil, cfg, err
	}
	var out []Candidate
	seen := map[string]bool{}
	for _, src := range []func(*Config) ([]Candidate, error){w.glossary, w.misses, w.repeated, w.relations} {
		cands, err := src(&cfg)
		if err != nil {
			return nil, cfg, err
		}
		for _, c := range cands {
			if len(out) == n {
				return out, cfg, nil
			}
			c.ID = ID(cfg.Map, c.Kind, c.Involves)
			if seen[c.ID] || !w.fresh(c) {
				continue
			}
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	return out, cfg, nil
}

func call(tools worker.ToolCaller, name string, args, into any) error {
	out, err := tools.CallTool(name, args)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if out.IsError {
		return fmt.Errorf("%s: %s", name, out.Text)
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal([]byte(out.Text), into); err != nil {
		return fmt.Errorf("%s: unreadable answer: %w", name, err)
	}
	return nil
}

func (w *world) load(cfg *Config, now time.Time) error {
	var list struct {
		Results []struct {
			ID     string         `json:"id"`
			Fields map[string]any `json:"fields"`
		} `json:"results"`
	}
	if err := call(w.tools, "concept_list", map[string]any{"where": []string{"type=Contradiction"}, "fields": []string{"origin", "resolution_status", "involves", "timestamp"}, "limit": 500}, &list); err != nil {
		return err
	}
	newest, newestID := "", ""
	for _, e := range list.Results {
		w.existing[e.ID] = true
		ts, _ := e.Fields["timestamp"].(string)
		if ts >= newest {
			newest, newestID = ts, e.ID
		}
		if e.Fields["resolution_status"] != "open" {
			continue
		}
		inv := strs(e.Fields["involves"])
		if e.Fields["origin"] == Origin {
			w.synthOpen = append(w.synthOpen, inv)
		} else {
			w.otherOpen[e.ID] = inv
		}
	}
	if cfg.Map == "" {
		if newestID == "" {
			return ErrNoMap
		}
		cfg.Map = path.Dir(newestID)
	}
	// The last 7 days of run records: keys already examined are skipped.
	out, err := w.tools.CallTool("log_tail", map[string]any{"n": 50})
	if err != nil {
		return fmt.Errorf("log_tail: %w", err)
	}
	for _, k := range ExaminedKeys(out.Text, now) {
		w.examined[k] = true
	}
	return nil
}

var entryHead = regexp.MustCompile(`(?m)^## (\d{4}-\d{2}-\d{2})`)

// ExaminedKeys reads the candidate keys listed by the run records of the last
// logWindow in a log_tail text. A malformed line is ignored: the cost is a
// candidate examined again.
func ExaminedKeys(logText string, now time.Time) []string {
	var keys []string
	heads := entryHead.FindAllStringSubmatchIndex(logText, -1)
	for i, h := range heads {
		day, err := time.Parse("2006-01-02", logText[h[2]:h[3]])
		if err != nil || now.Sub(day) > logWindow {
			continue
		}
		end := len(logText)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		for _, line := range strings.Split(logText[h[1]:end], "\n") {
			if _, rest, ok := strings.Cut(line, ExaminedPrefix); ok {
				for _, k := range strings.Split(rest, ",") {
					if k = strings.TrimSpace(k); k != "" && !strings.ContainsAny(k, " ;") {
						keys = append(keys, k)
					}
				}
			}
		}
	}
	return keys
}

func strs(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// fresh applies the dedup rules; a candidate costs nothing until here.
func (w *world) fresh(c Candidate) bool {
	if w.existing[c.ID] || w.examined[c.ID] { // a resolved or rejected one included
		return false
	}
	for _, open := range w.synthOpen {
		if subset(c.Involves, open) {
			return false
		}
	}
	if len(w.otherOpen) == 0 || c.Subject == "" {
		return true
	}
	// "Search first" (the doctor's step 5c): an open gap of the doctor or a
	// person on the same concepts means the question is already open.
	var res struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if call(w.tools, "search", map[string]any{"query": c.Subject, "limit": 5, "record_miss": false}, &res) != nil {
		return true
	}
	for _, h := range res.Results {
		if inv, ok := w.otherOpen[h.ID]; ok && intersects(inv, c.Involves) {
			return false
		}
	}
	return true
}

func subset(a, b []string) bool {
	for _, x := range a {
		if !contains(b, x) {
			return false
		}
	}
	return len(a) > 0
}

func intersects(a, b []string) bool {
	for _, x := range a {
		if contains(b, x) {
			return true
		}
	}
	return false
}

type reviewItem struct {
	Concepts []string `json:"concepts"`
	Evidence string   `json:"evidence"`
	Term     string   `json:"term"`
	Weight   int      `json:"weight"`
}

func (w *world) review(kind string) ([]reviewItem, error) {
	var r struct {
		Items []reviewItem `json:"items"`
	}
	if err := call(w.tools, "kb_review", map[string]any{"kind": kind, "limit": 100}, &r); err != nil {
		return nil, err
	}
	sort.SliceStable(r.Items, func(i, j int) bool { return r.Items[i].Weight > r.Items[j].Weight })
	return r.Items, nil
}

// involvesOf is a deterministic, bounded involves set.
func involvesOf(ids []string) []string {
	s := sortedSet(ids)
	return s[:min(len(s), maxInvolves)]
}

func clip(s string, n int) string {
	if r := []rune(strings.Join(strings.Fields(s), " ")); len(r) > n {
		return string(r[:n])
	} else {
		return string(r)
	}
}

// 1. A subject many pages use and no page defines.
func (w *world) glossary(*Config) ([]Candidate, error) {
	items, err := w.review("glossary_gap")
	var out []Candidate
	for _, it := range items {
		out = append(out, Candidate{Source: "glossary_gap", Kind: KindMissingContext, Subject: clip(it.Term, 200), Involves: involvesOf(it.Concepts)})
	}
	return out, err
}

// 2. A question readers asked twice or more and nothing answered (D247, D319).
func (w *world) misses(*Config) ([]Candidate, error) {
	var st struct {
		Misses []struct {
			Query    string `json:"query"`
			Count    int    `json:"count"`
			Resolved bool   `json:"resolved"`
		} `json:"search_misses"`
	}
	if err := call(w.tools, "kb_status", map[string]any{}, &st); err != nil {
		return nil, err
	}
	sort.SliceStable(st.Misses, func(i, j int) bool { return st.Misses[i].Count > st.Misses[j].Count })
	var out []Candidate
	for _, m := range st.Misses {
		if m.Count < 2 || m.Resolved {
			continue
		}
		// A miss names no concept: the pages nearest to the query stand for it.
		var res struct {
			Results []struct {
				ID string `json:"id"`
			} `json:"results"`
		}
		if call(w.tools, "search", map[string]any{"query": m.Query, "limit": 3, "record_miss": false}, &res) != nil {
			continue
		}
		var ids []string
		for _, h := range res.Results {
			ids = append(ids, h.ID)
		}
		if len(ids) > 0 {
			out = append(out, Candidate{Source: "search_miss", Kind: KindMissingContext, Subject: clip(m.Query, 200), Involves: involvesOf(ids)})
		}
	}
	return out, nil
}

// 3. The same fact written in several pages: do they agree?
func (w *world) repeated(*Config) ([]Candidate, error) {
	items, err := w.review("repeated_fact")
	var out []Candidate
	for _, it := range items {
		out = append(out, Candidate{Source: "repeated_fact", Kind: KindOpenQuestion, Subject: clip(it.Evidence, 200), Involves: involvesOf(it.Concepts)})
	}
	return out, err
}

var centralLine = regexp.MustCompile(`^- .*\(([^()\s]+)\)\s*$`)

// 4. Pages the server suggests linking among the most central ones. The
// communities of atlas_overview carry no members in its text, so the "across
// two communities first" refinement is not applied: PageRank order only.
func (w *world) relations(*Config) ([]Candidate, error) {
	out, err := w.tools.CallTool("atlas_overview", map[string]any{"structure": true})
	if err != nil || out.IsError {
		return nil, nil // a missing structure is an empty source, not a failure
	}
	_, rest, _ := strings.Cut(out.Text, "### Most central")
	rest, _, _ = strings.Cut(rest, "### Communities")
	var cands []Candidate
	for _, line := range strings.Split(rest, "\n") {
		m := centralLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		var r struct {
			Candidates []struct {
				ID string `json:"id"`
			} `json:"candidates"`
		}
		if call(w.tools, "link_suggest", map[string]any{"id": m[1], "limit": 3}, &r) != nil {
			continue
		}
		for _, c := range r.Candidates {
			cands = append(cands, Candidate{Source: "link_suggest", Kind: KindOpenQuestion, Subject: fmt.Sprintf("Are %s and %s related?", m[1], c.ID), Involves: involvesOf([]string{m[1], c.ID})})
		}
	}
	return cands, nil
}
