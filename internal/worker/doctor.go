package worker

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// The doctor task kind (D1261, D1436): the worker maintains a KB on its own, as far
// as tasks[].doctor_allow lets it. Mechanical fixes need no model: kb_repair runs
// first. The worker then reads the kb_review queue itself, at no token cost, and
// each item a rule covers is a short loop of its own with instructions and tools
// fit for it, so the context of one item never reaches the next and a small model
// can run the task. The queue is the signal and the state: a settled item leaves
// kb_review, and an examined one is in the log (doctorLogPath) until
// doctorRetryDays pass. What no rule covers stays for a prompt task or an
// operator's session, and only such a session writes the kb-doctor marker.

const (
	// doctorMinGrant is the least an item may start with.
	doctorMinGrant = 20_000
	// doctorItemTokens, doctorItemSteps and doctorItemResultBytes bound one item:
	// its loop reads a few pages and makes a few small changes.
	doctorItemTokens      = 40_000
	doctorItemSteps       = 8
	doctorItemResultBytes = 8 << 10
	// doctorReviewItems is how many items one run examines.
	doctorReviewItems = 10
	// defaultMaxWrites is the cap of applied writes of one run (tasks[].max_writes).
	defaultMaxWrites = 20
	// doctorMaxModelErrors consecutive model errors end the run.
	doctorMaxModelErrors = 2
	// doctorRetryDays is how long an examined item is left alone, the server's
	// doctor interval (kb.DoctorSuggestionDays; this package imports no internal/kb).
	doctorRetryDays = 14
	// doctorLedgerEntries bounds the log read; a record lost past it only means
	// an item is examined again.
	doctorLedgerEntries = 100
	// doctorQueueLimit and doctorQueuePages bound the kb_review read of one kind: it
	// must reach past the items already examined, which the server still lists.
	doctorQueueLimit = 100
	doctorQueuePages = 5
	// doctorRest is how long a KB with nothing to do is left alone: the queue is
	// worth a few kb_review calls per poll only while it moves.
	doctorRest = time.Hour
	// doctorKeyPerimeter is the key under which Load leaves the perimeter.
	doctorKeyPerimeter = "\x00perimeter"
)

// The words of tasks[].doctor_allow: what a task may do beyond closing or
// suspending work, which is always on.
const (
	doctorAllowRepair    = "repair"    // kb_repair, with no model
	doctorAllowPages     = "pages"     // the body of the pages an item names
	doctorAllowStructure = "structure" // a map's vocabulary and index
	doctorAllowArchive   = "archive"   // a closed journal entry's facts, then the entry
)

var doctorAllowWords = []string{doctorAllowRepair, doctorAllowPages, doctorAllowStructure, doctorAllowArchive}

// doctorRepairs are the checks a run may repair, and the word each needs. The
// server's background repair already applies what is safe to run with no person,
// so these are the fixes it leaves for someone to ask for, and a check not named
// here never runs: reciprocal_link_item can drop hundreds of edges in one commit
// (D309), legacy_skill_name moves skills, and junk_file changes what git tracks.
// invalid_field_value drops values from pages (D1197).
var doctorRepairs = map[string]string{
	"broken_link":               doctorAllowRepair,
	"superseded_target_retired": doctorAllowRepair,
	"unknown_type":              doctorAllowRepair,
	"invalid_field_value":       doctorAllowStructure,
}

// doctorMarker is the substring kb_status reads as "a session closed"
// (mcpserver doctorMarker). The worker never writes it: only an operator's
// session does, so every text this file writes passes through noMarker and a
// write carrying it is refused.
const doctorMarker = "kb-doctor"

//go:embed doctor_instructions.md
var doctorInstructions string

// doctorRule is a review item a model can settle, and how. allow is the
// tasks[].doctor_allow word that turns it on; a rule always on has none and changes
// frontmatter only, since an item can be shaped by whoever writes a page and a body
// edit is what that writer would want. extra is the tools it needs beyond the reads;
// carries lets a body edit reach a page the item links; offers takes only an item
// with a proposal, the server's to make. An item no rule covers stays for a prompt
// task or an operator.
type doctorRule struct {
	kind, check, allow, how string
	extra                   []string
	carries, offers         bool
}

func (r doctorRule) tools() []string {
	out := []string{"concept_read", "search"}
	if r.allow != doctorAllowStructure {
		out = append(out, "concept_patch")
	}
	return append(out, r.extra...)
}

const doctorVocabulary = "The item proposes a closed list of values for a field of the map. If the values read as a vocabulary, call `map_update` with `map` and `field_values: {\"<type>\": {\"<field>\": [<values>]}}`, where `<type>` is the proposal's `type` (`*` if it has none), `<field>` its `field` and `<values>` its whole list: give only that field, the worker keeps the others. If the values are page ids, names of one-off things or look accidental, leave the item."

const doctorHarvest = "The entry is closed and old. Pick the facts a reader of a live page would need: causes, traps, what worked. Add each as one line, with a dated link back to the entry, to the live page the entry links that it belongs to, under an existing section. Then set `status` to `archived` and put at the top of the entry `> Archived <today>: lessons in [page](id).`, or `> Archived <today>: no durable lessons.` if there are none. Never delete the entry."

var doctorRules = []doctorRule{
	{kind: "status_reclassify", how: "The page is `active` in a journal that does not read `active` as open work. Set `status` to the value the suggested action names. If it proposes `unknown`, set `waiting_on` to \"operator decision\" and `review_after` to 14 days after today."},
	{kind: "zombie_work", how: "The page is still open but its origin is retired. If the pages show the work ended, set `status` to `done`. Otherwise leave the item."},
	{kind: "duplicate_candidate", allow: doctorAllowPages, how: "The pages look alike. If they are different subjects, add one sentence to each that links the other and says how they differ. If they are the same subject, leave the item: merging is an operator's decision."},
	{kind: "lint_judgement", check: "stale_open", how: "The page is open and has not moved for a long time. If the pages show the work ended, set `status` to `done`. If it waits for someone, set `waiting_on` and `review_after`. Otherwise leave the item. Never change `timestamp`."},
	{kind: "lint_judgement", check: "closed_with_open_items", allow: doctorAllowPages, how: "The page is closed but has unchecked items. Check each item the pages show as done. Otherwise leave the item."},
	{kind: "repeated_fact", allow: doctorAllowPages, how: "The same line stands on several pages. Keep it on the page whose subject it is, and on each other page replace it with a link to that page. If the line is boilerplate rather than a fact, leave the item."},
	{kind: "lint_judgement", check: "template_extra_section", allow: doctorAllowPages, how: "The page has sections its template does not list. For each, do what the evidence offers: rename the heading to a section of the template, or demote it to `###` under the section its text belongs to. Keep the text."},
	{kind: "lint_judgement", check: "template_section_missing", allow: doctorAllowPages, how: "The page lacks sections of its template. Where text on the page belongs in a missing section, add the heading and move that text under it. Add no empty section and no placeholder. If no text fits, leave the item."},
	{kind: "observation", check: "island", allow: doctorAllowPages, how: "These pages link only each other. If one of them says what part of the KB it belongs to, find that page with `search` and add to this page one sentence that links it. Otherwise leave the item."},
	{kind: "observation", check: "facet_sprawl", allow: doctorAllowStructure, how: doctorVocabulary, extra: []string{"map_update"}, offers: true},
	{kind: "observation", check: "missing_value_contract", allow: doctorAllowStructure, how: doctorVocabulary, extra: []string{"map_update"}, offers: true},
	{kind: "index_unclassified", allow: doctorAllowStructure, how: "The map's index keeps pages under `## New pages`. Read it with `index_get` (`with_hash: true`) and, with `index_patch` (`path` the map, `if_match` the hash), move each entry under the section it belongs to. Remove the heading when it is empty. Leave an entry you cannot place.", extra: []string{"index_get", "index_patch"}},
	{kind: "harvest_candidate", allow: doctorAllowArchive, how: doctorHarvest, carries: true},
	{kind: "lesson_unpromoted", allow: doctorAllowArchive, how: doctorHarvest, carries: true},
}

// doctorItem is the part of a kb_review item the doctor reads.
type doctorItem struct {
	Kind            string   `json:"kind"`
	Check           string   `json:"check,omitempty"`
	Concepts        []string `json:"concepts"`
	Evidence        string   `json:"evidence"`
	SuggestedAction string   `json:"suggested_action"`
	// Proposal is what a vocabulary observation suggests, for the model to read.
	Proposal json.RawMessage `json:"proposal,omitempty"`

	rule doctorRule // the rule that matched it, set by queue
}

// key names the item in the log and in the commit reason. The evidence stays out
// of it, as it may carry an age that would make the same item new every day. A
// repeated fact is the exception: the evidence quotes the line, and two facts on
// the same pages are two items.
func (it doctorItem) key() string {
	ids := slices.Clone(it.Concepts)
	sort.Strings(ids)
	identity := it.Kind + "\x00" + it.Check + "\x00" + strings.Join(ids, "\x00")
	if it.Kind == "repeated_fact" {
		identity += "\x00" + it.Evidence
	}
	sum := sha256.Sum256([]byte(identity))
	name := it.Kind
	if it.Check != "" {
		name = it.Check
	}
	return name + "-" + hex.EncodeToString(sum[:4])
}

// doctorPatchKeys, doctorIndexKeys and doctorMapKeys are the only arguments a write
// may carry, and doctorFields the only frontmatter it may set: the three that close
// or suspend work.
var (
	doctorPatchKeys = []string{"id", "if_match", "frontmatter", "old_string", "new_string", "edits", "reason"}
	doctorIndexKeys = []string{"path", "if_match", "old_string", "new_string", "edits", "reason"}
	doctorMapKeys   = []string{"map", "field_values", "reason"}
	doctorFields    = []string{"status", "waiting_on", "review_after"}
)

// errDoctorRefused ends a run whose writes the server keeps refusing.
var errDoctorRefused = errors.New("doctor: consecutive refused writes (is the token read-only?)")

// doctorGuard wraps the tool caller with what the allowlist cannot express: a
// write reaches only the pages and the map of the current item, with a fixed
// commit reason, up to the cap of the run. Consecutive refusals by the server (a
// read-only grant) abort the run instead of spending the whole grant.
type doctorGuard struct {
	inner   ToolCaller
	maxW    int
	applied int
	refused int  // consecutive writes the server refused
	capped  bool // a write met the cap: the item may be half done
	rule    doctorRule
	pages   []string
	key     string
	offer   json.RawMessage // the item's proposal
	targets []string        // what a rule that carries facts may reach
}

// maxRefusedWrites aborts a run whose writes keep being refused.
const maxRefusedWrites = 3

func (g *doctorGuard) ListTools() ([]client.ToolInfo, error) { return g.inner.ListTools() }

func (g *doctorGuard) CallTool(name string, args any) (client.ToolOutput, error) {
	if isRead(name) {
		return g.inner.CallTool(name, args)
	}
	refuse := func(text string) (client.ToolOutput, error) {
		return client.ToolOutput{IsError: true, Text: "refused: " + text}, nil
	}
	if !slices.Contains(g.rule.tools(), name) {
		return refuse("this item may write only with " + strings.Join(g.rule.tools()[2:], ", "))
	}
	if hasMarker(args) {
		return refuse("the text \"" + doctorMarker + "\" is reserved for an operator's session")
	}
	if g.applied >= g.maxW {
		g.capped = true
		return refuse(fmt.Sprintf("this run reached its cap of %d writes; stop now", g.maxW))
	}
	var call json.RawMessage
	var why string
	switch name {
	case "index_patch":
		call, why = g.fenceIndex(rawJSON(args))
	case "map_update":
		call, why = g.fenceMap(rawJSON(args))
	default:
		call, why = g.fence(rawJSON(args))
	}
	if why != "" {
		return refuse(why)
	}
	out, err := g.inner.CallTool(name, call)
	if err != nil || out.IsError {
		if g.refused++; g.refused >= maxRefusedWrites {
			return out, errDoctorRefused
		}
		return out, err
	}
	g.refused = 0
	g.applied++
	return out, nil
}

// doctorArgs decodes the arguments of a write; why is the refusal.
func doctorArgs(raw json.RawMessage, keys []string) (m map[string]json.RawMessage, why string) {
	if json.Unmarshal(raw, &m) != nil {
		return nil, "arguments must be a JSON object"
	}
	for k := range m {
		if !slices.Contains(keys, k) {
			return nil, "argument " + k + " is not allowed"
		}
	}
	if raw, ok := m["edits"]; ok {
		var edits []map[string]json.RawMessage
		if json.Unmarshal(raw, &edits) != nil {
			return nil, "edits must be a list"
		}
		for _, e := range edits {
			for k := range e {
				if k != "old_string" && k != "new_string" {
					return nil, "an edit may carry only old_string and new_string"
				}
			}
		}
	}
	return m, ""
}

// seal sets the commit reason of a write that may go.
func (g *doctorGuard) seal(m map[string]json.RawMessage) (json.RawMessage, string) {
	m["reason"], _ = json.Marshal("doctor: " + g.key)
	call, err := json.Marshal(m)
	if err != nil {
		return nil, "arguments cannot be encoded"
	}
	return call, ""
}

// linkTargets lists the pages that pages link, read before the model can write:
// a link it added would otherwise open the page it wants next. The server resolves
// the links, so a target is a page and not a piece of text.
func linkTargets(tools ToolCaller, pages []string) []string {
	var out []string
	for _, page := range pages {
		res, err := tools.CallTool("graph_neighbors", map[string]any{"id": page, "direction": "out"})
		var g struct {
			Neighbors []struct {
				ID      string `json:"id"`
				Missing bool   `json:"missing"`
			} `json:"neighbors"`
		}
		if err != nil || res.IsError || json.Unmarshal([]byte(res.Text), &g) != nil {
			continue
		}
		for _, n := range g.Neighbors {
			if !n.Missing {
				out = append(out, n.ID)
			}
		}
	}
	return out
}

// fence checks a patch against the current item; why is the refusal, empty when
// the patch may go. A rule that carries facts may edit the body of a page the item
// linked when it started, never its frontmatter.
func (g *doctorGuard) fence(raw json.RawMessage) (call json.RawMessage, why string) {
	m, why := doctorArgs(raw, doctorPatchKeys)
	if why != "" {
		return nil, why
	}
	var p struct {
		ID          string         `json:"id"`
		Frontmatter map[string]any `json:"frontmatter"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return nil, "arguments do not match concept_patch"
	}
	_, old := m["old_string"]
	_, added := m["new_string"]
	_, edits := m["edits"]
	if g.rule.allow == "" && (old || added || edits) {
		return nil, "this item may change only " + strings.Join(doctorFields, ", ")
	}
	if !slices.Contains(g.pages, p.ID) && (len(p.Frontmatter) > 0 || !slices.Contains(g.targets, p.ID)) {
		return nil, "only the pages the item names may be changed"
	}
	for k, v := range p.Frontmatter {
		if _, isText := v.(string); !isText || !slices.Contains(doctorFields, k) {
			return nil, "frontmatter may set only " + strings.Join(doctorFields, ", ") + ", as text"
		}
	}
	return g.seal(m)
}

// onMap reports whether name is the map of a page of the item.
func (g *doctorGuard) onMap(name string) bool {
	for _, p := range g.pages {
		if m, _, _ := strings.Cut(p, "/"); m == name && name != "" {
			return true
		}
	}
	return false
}

func (g *doctorGuard) fenceIndex(raw json.RawMessage) (json.RawMessage, string) {
	m, why := doctorArgs(raw, doctorIndexKeys)
	if why != "" {
		return nil, why
	}
	var p struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &p) != nil || !g.onMap(p.Path) {
		return nil, "only the index of the item's map may be changed"
	}
	return g.seal(m)
}

// fenceMap keeps a contract change to the item's map and its proposal: the model
// decides whether to apply it, never what it says, since the list decides what a
// later repair drops from pages. map_update replaces field_values whole, so the
// worker adds back what the map already declares.
// isOffer reports whether given is exactly the item's proposal.
func (g *doctorGuard) isOffer(given map[string]map[string]any) bool {
	var offer struct {
		Field  string   `json:"field"`
		Type   string   `json:"type"`
		Values []string `json:"values"`
	}
	if json.Unmarshal(g.offer, &offer) != nil || offer.Field == "" {
		return false
	}
	if offer.Type == "" {
		offer.Type = "*"
	}
	values, _ := given[offer.Type][offer.Field].([]any)
	if len(given) != 1 || len(given[offer.Type]) != 1 || len(values) == 0 {
		return false
	}
	got := make([]string, 0, len(values))
	for _, v := range values {
		text, ok := v.(string)
		if !ok {
			return false
		}
		got = append(got, text)
	}
	slices.Sort(got)
	return slices.Equal(got, slices.Sorted(slices.Values(offer.Values)))
}

func (g *doctorGuard) fenceMap(raw json.RawMessage) (json.RawMessage, string) {
	m, why := doctorArgs(raw, doctorMapKeys)
	if why != "" {
		return nil, why
	}
	var p struct {
		Map         string                    `json:"map"`
		FieldValues map[string]map[string]any `json:"field_values"`
	}
	if json.Unmarshal(raw, &p) != nil || !g.onMap(p.Map) {
		return nil, "only field_values of the item's map may be changed"
	}
	if !g.isOffer(p.FieldValues) {
		return nil, "field_values must be the field the item proposes, with its whole list"
	}
	out, err := g.inner.CallTool("concept_read", map[string]any{"id": p.Map + "/_map"})
	var page struct {
		Frontmatter map[string]any `json:"frontmatter"`
	}
	if err != nil || out.IsError || json.Unmarshal([]byte(out.Text), &page) != nil {
		return nil, "the map's contract cannot be read"
	}
	for k, v := range page.Frontmatter { // field_values.<type>.<field>: frontmatter is flat
		rest, ok := strings.CutPrefix(k, "field_values.")
		typ, field, cut := strings.Cut(rest, ".")
		if !ok || !cut {
			continue
		}
		if p.FieldValues[typ] == nil {
			p.FieldValues[typ] = map[string]any{}
		}
		if _, given := p.FieldValues[typ][field]; !given {
			p.FieldValues[typ][field] = v
		}
	}
	m["field_values"], _ = json.Marshal(p.FieldValues)
	return g.seal(m)
}

// hasMarker reports whether any decoded string (key or value, at any depth) of
// the arguments contains the marker: an escaped "kb\u002ddoctor" is the same
// text once the server decodes it. Arguments that do not decode are checked as
// raw text.
func hasMarker(args any) bool {
	raw := rawJSON(args)
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return strings.Contains(string(raw), doctorMarker)
	}
	var walk func(any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case string:
			return strings.Contains(x, doctorMarker)
		case []any:
			for _, e := range x {
				if walk(e) {
					return true
				}
			}
		case map[string]any:
			for k, e := range x {
				if strings.Contains(k, doctorMarker) || walk(e) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}

func rawJSON(args any) []byte {
	if r, ok := args.(json.RawMessage); ok {
		return r
	}
	b, _ := json.Marshal(args)
	return b
}

// DoctorKind is the doctor task kind: one entry, run per KB.
func DoctorKind() Kind {
	return Kind{
		Name:    "doctor",
		Options: []KindOption{{Key: MaxWritesKey}, {Key: DoctorAllowKey}},
		Validate: func(o map[string]any) error {
			if n, _ := o["max_writes"].(int); n < 1 {
				return fmt.Errorf("max_writes must be at least 1")
			}
			allow, _ := o["doctor_allow"].([]string)
			for _, w := range allow {
				if !slices.Contains(doctorAllowWords, w) {
					return fmt.Errorf("doctor_allow: %q is not one of %s", w, strings.Join(doctorAllowWords, ", "))
				}
			}
			return nil
		},
		Load: func(env LoadEnv, o map[string]any) error {
			o[doctorKeyPerimeter] = env.Perimeter
			return nil
		},
		Build: func(o map[string]any) (Instance, error) {
			perimeter, ok := o[doctorKeyPerimeter].(string)
			if !ok {
				return nil, errors.New("a doctor task is built from a loaded config (its log path names the perimeter)")
			}
			n, _ := o["max_writes"].(int)
			if n <= 0 {
				n = defaultMaxWrites
			}
			allow, _ := o["doctor_allow"].([]string)
			return &doctorInstance{now: time.Now, maxWrites: n, perimeter: perimeter, allow: allow}, nil
		},
	}
}

type doctorInstance struct {
	now       func() time.Time
	maxWrites int
	perimeter string
	allow     []string
	restUntil map[string]time.Time // per KB, in memory: a restart only asks again
}

func (d *doctorInstance) allows(word string) bool { return slices.Contains(d.allow, word) }

// rest leaves a KB alone for a while.
func (d *doctorInstance) rest(kb string) {
	if d.restUntil == nil {
		d.restUntil = map[string]time.Time{}
	}
	d.restUntil[kb] = d.now().Add(doctorRest)
}

// doctorStatus is the part of kb_status the doctor reads.
type doctorStatus struct {
	PushState   string `json:"push_state"`
	Conformance struct {
		Repairable map[string]int `json:"repairable"`
	} `json:"conformance"`
}

func readDoctorStatus(tools ToolCaller) (doctorStatus, error) {
	var st doctorStatus
	out, err := tools.CallTool("kb_status", map[string]any{})
	if err != nil {
		return st, err
	}
	if out.IsError {
		return st, fmt.Errorf("kb_status refused: %s", oneLine(out.Text))
	}
	if err := json.Unmarshal([]byte(out.Text), &st); err != nil {
		return st, fmt.Errorf("kb_status: %w", err)
	}
	return st, nil
}

// repairs lists the checks this task may repair on the KB, sorted.
func (d *doctorInstance) repairs(st doctorStatus) []string {
	if !d.allows(doctorAllowRepair) {
		return nil
	}
	var out []string
	for check, n := range st.Conformance.Repairable {
		if need, known := doctorRepairs[check]; known && n > 0 && d.allows(need) {
			out = append(out, check)
		}
	}
	sort.Strings(out)
	return out
}

// repair applies the mechanical fixes in one call, one commit, and returns how
// many it applied. A refusal costs the run nothing: the items can still go.
func (d *doctorInstance) repair(tools ToolCaller, checks []string) int {
	out, err := tools.CallTool("kb_repair", map[string]any{"check": checks, "dry_run": false, "reason": "doctor: repair"})
	if err == nil && out.IsError {
		err = errors.New(oneLine(out.Text))
	}
	var answer struct {
		AppliedTotal int `json:"applied_total"`
	}
	if err == nil {
		err = json.Unmarshal([]byte(out.Text), &answer)
	}
	if err != nil {
		log.Printf("worker: doctor: kb_repair: %v", err)
	}
	return answer.AppliedTotal
}

func (d *doctorInstance) today() string { return d.now().UTC().Format("2006-01-02") }

// doctorLogPath is where a run records what it examined: a path of its own under
// the perimeter's, which the budget's bracketed prefix never matches (markerPath
// has the same reasoning).
func doctorLogPath(perimeter string) string { return "worker/" + perimeter + "/doctor" }

// examined reads the keys recorded in the last doctorRetryDays. A malformed line
// is skipped: the cost is an item examined again.
func (d *doctorInstance) examined(tools ToolCaller) (map[string]bool, error) {
	path := doctorLogPath(d.perimeter)
	out, err := tools.CallTool("log_tail", map[string]any{"path": path, "n": doctorLedgerEntries})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	if out.IsError {
		if strings.Contains(out.Text, "concept not found: log.md") {
			return seen, nil
		}
		return nil, fmt.Errorf("log_tail %s: %s", path, oneLine(out.Text))
	}
	cutoff := d.now().UTC().AddDate(0, 0, -doctorRetryDays).Format("2006-01-02")
	for _, line := range splitEntries(out.Text) {
		rest, ok := strings.CutPrefix(line, "["+path+"] ")
		if !ok {
			continue
		}
		var day string
		var keys []string
		for _, f := range strings.Fields(rest) {
			k, v, _ := strings.Cut(f, "=")
			switch k {
			case "day":
				day = v
			case "examined":
				keys = strings.Split(v, ",")
			}
		}
		if day < cutoff {
			continue
		}
		for _, k := range keys {
			seen[k] = true
		}
	}
	return seen, nil
}

// reviewItems reads the items of one kind, past the first page: the items examined
// are still listed, and a kind mixes the checks of many rules.
func reviewItems(tools ToolCaller, kind string) ([]doctorItem, error) {
	var all []doctorItem
	offset := 0
	for range doctorQueuePages {
		res, err := tools.CallTool("kb_review", map[string]any{"kind": kind, "limit": doctorQueueLimit, "offset": offset})
		if err != nil {
			return nil, err
		}
		if res.IsError {
			return nil, fmt.Errorf("kb_review refused: %s", oneLine(res.Text))
		}
		var page struct {
			Items      []doctorItem `json:"items"`
			NextOffset int          `json:"next_offset"`
		}
		if err := json.Unmarshal([]byte(res.Text), &page); err != nil {
			return nil, fmt.Errorf("kb_review: %w", err)
		}
		all = append(all, page.Items...)
		if page.NextOffset == 0 {
			break
		}
		offset = page.NextOffset
	}
	return all, nil
}

// queue is what this run may work on, at no token cost: the items of the rules
// this task allows, in the rules' order and the server's rank, without those
// already examined.
func (d *doctorInstance) queue(tools ToolCaller) ([]doctorItem, error) {
	seen, err := d.examined(tools)
	if err != nil {
		return nil, err
	}
	var out []doctorItem
	byKind := map[string][]doctorItem{}
	for _, r := range doctorRules {
		if r.allow != "" && !d.allows(r.allow) {
			continue
		}
		items, read := byKind[r.kind]
		if !read {
			if items, err = reviewItems(tools, r.kind); err != nil {
				return nil, err
			}
			byKind[r.kind] = items
		}
		for _, it := range items {
			if it.Check == r.check && len(it.Concepts) > 0 && (!r.offers || len(it.Proposal) > 0) && !seen[it.key()] {
				it.rule = r
				if out = append(out, it); len(out) == doctorReviewItems {
					return out, nil
				}
			}
		}
	}
	return out, nil
}

// Next offers the KB when there is something to repair or an item left to
// examine: a KB with nothing for this task costs no model call and leaves no
// entry. A KB whose kb_status fails is not eligible; the error is the runner's to
// count. Whether a session is due does not matter here: that is the server's
// question to an operator, and the queue already says what is left.
func (d *doctorInstance) Next(_ context.Context, kb string, tools ToolCaller) (*WorkItem, error) {
	if d.now().Before(d.restUntil[kb]) {
		return nil, nil
	}
	st, err := readDoctorStatus(tools)
	if err != nil {
		return nil, err
	}
	if st.PushState == "degraded" { // a degraded KB refuses writes anyway
		return nil, nil
	}
	if len(d.repairs(st)) > 0 {
		return &WorkItem{Key: "doctor"}, nil
	}
	items, err := d.queue(tools)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		d.rest(kb)
		return nil, nil
	}
	return &WorkItem{Key: "doctor"}, nil
}

// doctorSystem is the instructions of one item. Nothing read from a KB is ever
// concatenated here.
func doctorSystem(it doctorItem) string {
	return doctorInstructions + "\n# This item\n\n" + it.rule.how + "\n"
}

// prompt places the item as quoted data.
func (d *doctorInstance) prompt(it doctorItem) string {
	data, _ := json.Marshal(it)
	return fmt.Sprintf("Today is %s. Settle this item, or say in one sentence why you leave it.\n\n%s", d.today(), dataBlock("item", dataToken(), string(data)))
}

// noMarker removes the marker from a text the worker writes: only an operator's
// session may carry it, or a worker's run would silence the proposal with the
// debt still there.
// The replacement has a space where the marker has a hyphen, so no text around
// a replaced marker can rebuild it ("kb-kb-doctor"); the loop is belt and braces.
func noMarker(s string) string {
	for strings.Contains(s, doctorMarker) {
		s = strings.ReplaceAll(s, doctorMarker, "kb doctor")
	}
	return s
}

// record files what the run examined, through the worker's own caller and not
// the model's allowlist; like the closing entry it is retried once and then
// logged, and a record lost means those items are examined again.
func (d *doctorInstance) record(tools ToolCaller, examined []string, writes int) {
	args := map[string]any{"path": doctorLogPath(d.perimeter),
		"entry": noMarker(fmt.Sprintf("day=%s examined=%s writes=%d", d.today(), strings.Join(examined, ","), writes))}
	var last error
	for try := 0; try < 2; try++ {
		out, err := tools.CallTool("log_append", args)
		if err == nil && !out.IsError {
			return
		}
		if err == nil {
			err = errors.New(oneLine(out.Text))
		}
		last = err
	}
	log.Printf("worker: doctor: record not written: %v", last)
}

// Run repairs what needs no model, then examines the queue, one loop per item,
// until a cap or the grant stops it.
func (d *doctorInstance) Run(ctx context.Context, _ *WorkItem, rt Runtime) (Result, error) {
	st, err := readDoctorStatus(rt.Tools)
	if err != nil {
		return Result{}, err
	}
	if st.PushState == "degraded" {
		return Result{Summary: "doctor skipped: KB degraded"}, nil
	}
	var res Result
	repaired := 0
	if checks := d.repairs(st); len(checks) > 0 {
		repaired = d.repair(rt.Tools, checks)
	}
	if repaired > 0 {
		res.Commits = 1
	}
	if rt.Grant < doctorMinGrant {
		res.Summary = fmt.Sprintf("doctor repaired %d findings; budget below the reserve for the items", repaired)
		d.rest(rt.KB)
		return res, nil
	}
	items, err := d.queue(rt.Tools)
	if err != nil {
		return res, err
	}
	if d.maxWrites <= 0 {
		d.maxWrites = defaultMaxWrites
	}
	var (
		examined  []string
		modelErrs int
		runErr    error
	)
	guard := &doctorGuard{inner: rt.Tools, maxW: d.maxWrites}
	for _, it := range items {
		remaining := rt.Grant - res.Tokens
		if ctx.Err() != nil || remaining < doctorMinGrant || guard.applied >= guard.maxW {
			break
		}
		guard.rule, guard.pages, guard.key, guard.offer, guard.targets = it.rule, it.Concepts, it.key(), it.Proposal, nil
		if it.rule.carries {
			guard.targets = linkTargets(rt.Tools, it.Concepts)
		}
		lim := rt.Limits(doctorItemSteps, min(remaining, doctorItemTokens))
		lim.MaxToolResultBytes = doctorItemResultBytes
		agent := NewAgent(rt.Model, guard, NewAllowlist(it.rule.tools()...), lim)
		out, err := agent.Run(ctx, Task{System: doctorSystem(it), User: d.prompt(it)})
		res.measure(out)
		if err != nil {
			runErr = err
			if errors.Is(err, ErrModelAuth) || errors.Is(err, errDoctorRefused) || ctx.Err() != nil {
				break
			}
			if modelErrs++; modelErrs >= doctorMaxModelErrors {
				break
			}
			continue
		}
		modelErrs, runErr = 0, nil
		if guard.capped {
			break // left unrecorded: its other writes come with the next run
		}
		// A cut loop or a barren answer ends the item too: it returns after the
		// retry days, rather than spending the rest of the budget now.
		examined = append(examined, guard.key)
	}
	res.Units = len(examined)
	res.Commits += guard.applied
	res.Summary = fmt.Sprintf("doctor repaired %d findings, examined %d of %d items, %d writes", repaired, len(examined), len(items), guard.applied)
	if ctx.Err() != nil {
		return res, runErr // shutdown: the closing entries have the short timeout
	}
	if len(examined) > 0 {
		d.record(rt.Tools, examined, guard.applied)
	}
	if res.Commits == 0 && len(examined) == 0 {
		d.rest(rt.KB) // nothing moved: the next poll would find the same
	}
	return res, runErr
}
