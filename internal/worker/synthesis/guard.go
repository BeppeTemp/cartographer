package synthesis

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/worker"
)

// The tools the model is offered: reads, and the two guarded writes.
var (
	readTools  = []string{"search", "concept_read", "concept_list", "work_list", "graph_context", "link_suggest", "graph_neighbors", "kb_review", "kb_status", "atlas_overview", "log_tail"}
	writeTools = []string{"concept_write", "concept_patch"}
)

// Tools is the synthesis allowlist, a subset of the default one (D9).
func Tools() []string { return append(append([]string{}, readTools...), writeTools...) }

// Allowlist is Tools as the agent loop's allowlist.
func Allowlist() *worker.Allowlist { return worker.NewAllowlist(Tools()...) }

// refusal is what the model is told when the guard refuses a call.
const refusal = "synthesis creates new gaps only"

// Guard wraps the MCP caller of one synthesis candidate and refuses every call
// outside the allowlist (D11 is enforced here, because the server cannot see
// "a claim on an active page": concept_write overwrites an existing id). A
// refusal is a tool error the model reads, not a transport error.
type Guard struct {
	Inner worker.ToolCaller
	// Target is the id the shape computed for the candidate in hand: the only id
	// concept_write may create.
	Target string
	// Refused counts the calls the guard turned down.
	Refused int
	// Written holds the id created, if any.
	Written string
}

// ListTools offers the allowlisted tools only.
func (g *Guard) ListTools() ([]client.ToolInfo, error) {
	listed, err := g.Inner.ListTools()
	if err != nil {
		return nil, err
	}
	a := Allowlist()
	var out []client.ToolInfo
	for _, t := range listed {
		if a.Permits(t.Name) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (g *Guard) refuse(why string) (client.ToolOutput, error) {
	g.Refused++
	if why != "" {
		why = refusal + ": " + why
	} else {
		why = refusal
	}
	return client.ToolOutput{IsError: true, Text: why}, nil
}

// CallTool runs the call when the guard allows it.
func (g *Guard) CallTool(name string, args any) (client.ToolOutput, error) {
	raw, err := toRaw(args)
	if err != nil {
		return g.refuse("arguments must be a JSON object")
	}
	// The caller is already scoped to the session's KB: a routed `kb` or
	// `target_kb` argument could send a read or a write to another one.
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil && string(raw) != "null" {
		return g.refuse("arguments must be a JSON object")
	}
	for _, k := range []string{"kb", "target_kb"} {
		if _, ok := top[k]; ok {
			return g.refuse("argument " + k + " is not allowed")
		}
	}
	switch {
	case name == "concept_write":
		return g.write(raw)
	case name == "concept_patch":
		return g.patch(raw)
	case name == "search":
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if v, ok := m["record_miss"]; ok && v != false {
			return g.refuse("search probes pass record_miss false")
		}
		if m == nil {
			m = map[string]any{}
		}
		m["record_miss"] = false // a probe must not feed the log it reads (D319)
		return g.Inner.CallTool(name, m)
	case contains(readTools, name):
		return g.Inner.CallTool(name, raw)
	}
	return g.refuse("") // by name, whatever the model asks (defence in depth)
}

func toRaw(args any) (json.RawMessage, error) {
	if r, ok := args.(json.RawMessage); ok {
		return r, nil
	}
	return json.Marshal(args)
}

// read returns the parsed concept_read of id; exists is false when the server
// answers a tool error (not found), err only on a transport failure.
func (g *Guard) read(id string) (m map[string]any, exists bool, err error) {
	out, err := g.Inner.CallTool("concept_read", map[string]any{"id": id})
	if err != nil {
		return nil, false, err
	}
	if out.IsError {
		if strings.Contains(out.Text, "concept not found") { // okf.ErrNotFound
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("concept_read %q: %s", id, out.Text) // any other error: unknown, so no write
	}
	if json.Unmarshal([]byte(out.Text), &m) != nil {
		return nil, false, fmt.Errorf("concept_read %q: unreadable answer", id)
	}
	return m, true, nil
}

func (g *Guard) write(raw json.RawMessage) (client.ToolOutput, error) {
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(raw, &keys)
	for k := range keys {
		if k != "id" && k != "frontmatter" && k != "body" && k != "index_entry" {
			return g.refuse("argument " + k + " is not allowed")
		}
	}
	var p struct {
		ID          string         `json:"id"`
		Frontmatter map[string]any `json:"frontmatter"`
		Body        string         `json:"body"`
		IfMatch     string         `json:"if_match"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ID == "" || p.ID != g.Target || p.IfMatch != "" {
		return g.refuse("the id is not the one computed for this candidate")
	}
	if err := checkFrontmatter(p.Frontmatter, g.Target); err != nil {
		return g.refuse(err.Error())
	}
	if _, exists, err := g.read(p.ID); err != nil || exists {
		return g.refuse("the concept exists") // fail closed on a transport error too
	}
	out, err := g.Inner.CallTool("concept_write", json.RawMessage(raw))
	if err == nil && !out.IsError {
		g.Written = p.ID
	}
	return out, err
}

// checkFrontmatter is the shape a created concept must have.
func checkFrontmatter(fm map[string]any, id string) error {
	str := func(k string) string { s, _ := fm[k].(string); return s }
	list := func(k string) []string {
		var out []string
		l, _ := fm[k].([]any)
		for _, v := range l {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	switch {
	case str("type") != "Contradiction":
		return fmt.Errorf("type must be Contradiction")
	case !IsGapKind(str("contradiction_kind")):
		return fmt.Errorf("contradiction_kind must be a gap kind")
	case str("resolution_status") != "open":
		return fmt.Errorf("resolution_status must be open")
	case str("origin") != Origin:
		return fmt.Errorf("origin must be %s", Origin)
	case !contains(confidences, str("confidence")):
		return fmt.Errorf("confidence must be one of %v", confidences)
	case len(list("provenance")) == 0:
		return fmt.Errorf("provenance needs at least one cited concept")
	case len(list("involves")) == 0 || len(list("involves")) > maxInvolves:
		return fmt.Errorf("involves must list 1..%d concepts", maxInvolves)
	case ID(path.Dir(id), str("contradiction_kind"), list("involves")) != id:
		return fmt.Errorf("the id does not match kind and involves")
	}
	for k := range fm {
		if strings.HasPrefix(k, "resolution") && k != "resolution_status" {
			return fmt.Errorf("%s is the confirmer's, not the worker's", k)
		}
	}
	return nil
}

// patchKeys are the only arguments a synthesis patch may carry.
var patchKeys = []string{"id", "if_match", "frontmatter", "frontmatter_append", "old_string", "new_string", "edits", "reason"}

func (g *Guard) patch(raw json.RawMessage) (client.ToolOutput, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return g.refuse("arguments must be a JSON object")
	}
	for k := range m {
		if !contains(patchKeys, k) {
			return g.refuse("argument " + k + " is not allowed")
		}
	}
	var p struct {
		ID          string         `json:"id"`
		IfMatch     string         `json:"if_match"`
		Frontmatter map[string]any `json:"frontmatter"`
		Append      map[string]any `json:"frontmatter_append"`
		patchEdit
		Edits []json.RawMessage `json:"edits"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ID == "" || p.IfMatch == "" {
		return g.refuse("a patch needs id and if_match")
	}
	for k, v := range p.Frontmatter {
		c, isStr := v.(string)
		if k != "confidence" || !isStr || !contains(confidences, c) { // null deletes a key: refused too
			return g.refuse("only confidence may be set, to low, medium or high")
		}
	}
	doc, exists, err := g.read(p.ID)
	if err != nil || !exists {
		return g.refuse("the concept is not readable")
	}
	fm, _ := doc["frontmatter"].(map[string]any)
	if fm["origin"] != Origin || fm["resolution_status"] != "open" || fm["type"] != "Contradiction" {
		return g.refuse("only an open synthesis hypothesis may be patched")
	}
	for k, v := range p.Append {
		add := strs(v)
		if s, ok := v.(string); ok {
			add = []string{s}
		}
		if (k != "provenance" && k != "involves") || len(add) == 0 {
			return g.refuse("only provenance and involves may be appended to, with concept ids")
		}
		for _, id := range add {
			if !validID(id) {
				return g.refuse(fmt.Sprintf("%q is not a concept id", id))
			}
		}
		if k == "involves" && len(sortedSet(append(strs(fm["involves"]), add...))) > maxInvolves {
			return g.refuse(fmt.Sprintf("involves is capped at %d", maxInvolves))
		}
	}
	var edits []patchEdit
	for _, r := range p.Edits {
		var keys map[string]json.RawMessage
		var e patchEdit
		if json.Unmarshal(r, &keys) != nil || json.Unmarshal(r, &e) != nil {
			return g.refuse("an edit must be an object of old_string and new_string")
		}
		for k := range keys { // replace_all would rewrite outside the Evidence section
			if k != "old_string" && k != "new_string" {
				return g.refuse("an edit takes old_string and new_string only")
			}
		}
		edits = append(edits, e)
	}
	if p.Old != "" || p.New != "" {
		edits = append(edits, p.patchEdit)
	}
	if len(edits) > 0 {
		body, _ := doc["body"].(string)
		if !editsStayInEvidence(body, edits) {
			return g.refuse("only the ## Evidence section may be edited")
		}
	}
	return g.Inner.CallTool("concept_patch", json.RawMessage(raw))
}

// evidenceBounds locates the content of the body's "## Evidence" section: the
// last H2 line of exactly that title (the shape writes it after the statement),
// up to the next heading line. ok is false when there is none.
func evidenceBounds(body string) (start, end int, ok bool) {
	const h = "\n## Evidence\n"
	i := strings.LastIndex("\n"+body, h)
	if i < 0 {
		return 0, 0, false
	}
	start = i + len(h) - 1 // offsets in body: the sentinel "\n" shifts by one
	end = len(body)
	if j := strings.Index(body[start:], "\n#"); j >= 0 {
		end = start + j
	}
	return start, end, true
}

// patchEdit is one old_string/new_string pair of a patch.
type patchEdit struct {
	Old string `json:"old_string"`
	New string `json:"new_string"`
}

// editsStayInEvidence replays the edits in order on the evolving body, as the
// server applies them (each old_string must match exactly once), and requires
// every change to lie entirely inside the Evidence section as it is at that
// step: checking against the original section would let edit 1 make a text
// unique elsewhere for edit 2.
func editsStayInEvidence(body string, edits []patchEdit) bool {
	for _, e := range edits {
		if e.Old == "" || strings.HasPrefix(e.New, "#") || strings.Contains(e.New, "\n#") || strings.Contains(e.New, "\r#") {
			return false
		}
		if strings.Count(body, e.Old) != 1 {
			return false
		}
		start, end, ok := evidenceBounds(body)
		at := strings.Index(body, e.Old)
		if !ok || at < start || at+len(e.Old) > end {
			return false
		}
		body = body[:at] + e.New + body[at+len(e.Old):]
		if ns, _, ok := evidenceBounds(body); !ok || ns != start {
			return false // the section's own heading must survive
		}
	}
	return true
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*$`)

// validID is a root-relative concept id: no leading slash, no dot segments, no extension.
func validID(id string) bool {
	return idPattern.MatchString(id) && !strings.Contains(id, "..") && !strings.HasSuffix(id, ".md")
}
