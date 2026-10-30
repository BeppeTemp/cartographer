package synthesis

import (
	"encoding/json"
	"fmt"
	"path"
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
		return nil, false, nil
	}
	if json.Unmarshal([]byte(out.Text), &m) != nil {
		return nil, false, fmt.Errorf("concept_read %q: unreadable answer", id)
	}
	return m, true, nil
}

func (g *Guard) write(raw json.RawMessage) (client.ToolOutput, error) {
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
	type edit struct {
		Old string `json:"old_string"`
		New string `json:"new_string"`
	}
	var p struct {
		ID          string         `json:"id"`
		IfMatch     string         `json:"if_match"`
		Frontmatter map[string]any `json:"frontmatter"`
		Append      map[string]any `json:"frontmatter_append"`
		edit
		Edits []edit `json:"edits"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ID == "" || p.IfMatch == "" {
		return g.refuse("a patch needs id and if_match")
	}
	for k := range p.Frontmatter {
		if k != "confidence" {
			return g.refuse("only confidence may be set")
		}
	}
	if c, ok := p.Frontmatter["confidence"].(string); ok && !contains(confidences, c) {
		return g.refuse("confidence must be one of low, medium, high")
	}
	for k := range p.Append {
		if k != "provenance" && k != "involves" {
			return g.refuse("only provenance and involves may be appended to")
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
	edits := p.Edits
	if p.Old != "" || p.New != "" {
		edits = append(edits, p.edit)
	}
	if len(edits) > 0 {
		body, _ := doc["body"].(string)
		sec := evidenceSection(body)
		for _, e := range edits {
			if e.Old == "" || !strings.Contains(sec, e.Old) || strings.Contains(e.New, "\n#") || strings.HasPrefix(e.New, "#") {
				return g.refuse("only the ## Evidence section may be edited")
			}
		}
	}
	return g.Inner.CallTool("concept_patch", json.RawMessage(raw))
}

// evidenceSection is the text of the body's "## Evidence" section.
func evidenceSection(body string) string {
	_, rest, ok := strings.Cut(body, "## Evidence")
	if !ok {
		return ""
	}
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	return rest
}
