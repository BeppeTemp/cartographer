package kb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The ops of a dashboard patch. The JSON of one op is the wire form of
// dashboard_patch.ops, and what a plugin returns to seed a dashboard.
const (
	DashboardOpAdd            = "add"
	DashboardOpUpdate         = "update"
	DashboardOpRemove         = "remove"
	DashboardOpMove           = "move"
	DashboardOpSetTitle       = "set_title"
	DashboardOpSetDescription = "set_description"
)

// MaxDashboardOps is the number of ops one patch may carry.
const MaxDashboardOps = 200

// DashboardOp is one edit of a dashboard. Index and the strings are pointers
// so that 0 and "" are expressible.
type DashboardOp struct {
	Op          string          `json:"op"`
	ID          string          `json:"id,omitempty"`          // update, remove, move
	Index       *int            `json:"index,omitempty"`       // add (optional, default end), move
	Widget      json.RawMessage `json:"widget,omitempty"`      // add: a whole widget; update: keys to merge, null removes a key
	Title       *string         `json:"title,omitempty"`       // set_title
	Description *string         `json:"description,omitempty"` // set_description
}

// dashboardOpShape is, per op, the keys it requires and the keys it may carry
// besides "op". A key outside both is refused, so a typo never passes silently.
var dashboardOpShape = map[string]struct{ required, optional []string }{
	DashboardOpAdd:            {[]string{"widget"}, []string{"index"}},
	DashboardOpUpdate:         {[]string{"id", "widget"}, nil},
	DashboardOpRemove:         {[]string{"id"}, nil},
	DashboardOpMove:           {[]string{"id", "index"}, nil},
	DashboardOpSetTitle:       {[]string{"title"}, nil},
	DashboardOpSetDescription: {[]string{"description"}, nil},
}

// DecodeDashboardOps decodes a JSON array of ops strictly: an unknown key, a
// key the op does not use and a missing required key are each refused, naming
// the op by its 1-based position.
func DecodeDashboardOps(data []byte) ([]DashboardOp, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("ops: not a JSON array: %v", err)
	}
	if len(raw) > MaxDashboardOps {
		return nil, fmt.Errorf("ops: %d ops, the cap is %d", len(raw), MaxDashboardOps)
	}
	ops := make([]DashboardOp, 0, len(raw))
	for i, r := range raw {
		keys, err := objectKeys(r)
		if err != nil {
			return nil, fmt.Errorf("op %d: %v", i+1, err)
		}
		var op DashboardOp
		dec := json.NewDecoder(bytes.NewReader(r))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&op); err != nil {
			return nil, fmt.Errorf("op %d: %v", i+1, err)
		}
		if err := checkOpKeys(op.Op, keys); err != nil {
			return nil, fmt.Errorf("op %d: %v", i+1, err)
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// objectKeys reads a JSON object into its raw values, refusing a repeated key
// (encoding/json would keep the last silently) and anything that is not an
// object.
func objectKeys(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not an object")
	}
	keys := map[string]json.RawMessage{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := t.(string)
		if _, dup := keys[k]; dup {
			return nil, fmt.Errorf("duplicate key %q", k)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		keys[k] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return keys, nil
}

func checkOpKeys(op string, keys map[string]json.RawMessage) error {
	shape, ok := dashboardOpShape[op]
	if !ok {
		return fmt.Errorf("unknown op %q", op)
	}
	allowed := map[string]bool{"op": true}
	for _, k := range shape.required {
		allowed[k] = true
		if _, has := keys[k]; !has {
			return fmt.Errorf("%s needs %q", op, k)
		}
	}
	for _, k := range shape.optional {
		allowed[k] = true
	}
	for k := range keys {
		if !allowed[k] {
			return fmt.Errorf("%s does not use %q", op, k)
		}
	}
	return nil
}

// NewAddOp builds the add op of a widget, marshalling it with its json tags:
// the one way a widget read from a file becomes an op. A widget that carries a
// Problem is refused: it cannot be written.
func NewAddOp(w DashboardWidget) (DashboardOp, error) {
	if w.Problem != "" {
		return DashboardOp{}, fmt.Errorf("widget %q has a problem: %s", w.ID, w.Problem)
	}
	raw, err := json.Marshal(w)
	if err != nil {
		return DashboardOp{}, err
	}
	return DashboardOp{Op: DashboardOpAdd, Widget: raw}, nil
}

// decodeWidget decodes a whole widget strictly: unknown keys refused, and the
// read-only "error" key too.
func decodeWidget(raw json.RawMessage) (DashboardWidget, error) {
	var w DashboardWidget
	keys, err := objectKeys(raw)
	if err != nil {
		return w, fmt.Errorf("widget: %v", err)
	}
	if _, has := keys["error"]; has {
		return w, errors.New(`"error" is not a widget field`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return w, err
	}
	return w, nil
}

func findWidget(ws []DashboardWidget, id string) int {
	for i := range ws {
		if ws[i].ID == id {
			return i
		}
	}
	return -1
}

func checkIndex(i *int, max int) (int, error) {
	if i == nil {
		return max, nil
	}
	if *i < 0 || *i > max {
		return 0, fmt.Errorf("index %d out of range 0..%d", *i, max)
	}
	return *i, nil
}

// mergeWidget applies the keys of patch to w: a given key replaces the value
// (a nested filter is replaced whole), null removes the key.
func mergeWidget(w DashboardWidget, patch json.RawMessage) (DashboardWidget, error) {
	keys, err := objectKeys(patch)
	if err != nil {
		return w, fmt.Errorf("widget: %v", err)
	}
	if _, has := keys["error"]; has {
		return w, errors.New(`"error" is not a widget field`)
	}
	if v, has := keys["id"]; has {
		var id string
		if json.Unmarshal(v, &id) != nil || id != w.ID {
			return w, errors.New("an update cannot change the widget id; remove and add instead")
		}
	}
	cur, err := json.Marshal(w)
	if err != nil {
		return w, err
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(cur, &merged); err != nil {
		return w, err
	}
	// A widget read with a Problem carries it as "error"; an update repairs it.
	delete(merged, "error")
	for k, v := range keys {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			delete(merged, k)
		} else {
			merged[k] = v
		}
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return w, err
	}
	return decodeWidget(out)
}

// ApplyDashboardOps applies ops in order to the dashboard file data (empty
// data is an empty dashboard) and returns the canonical bytes of the result.
// It is all-or-nothing: the result is validated as a whole, and on any error
// nothing is returned and the message names the failing op by its 1-based
// position. Pure: the caller checks the hash and writes the file.
func ApplyDashboardOps(data []byte, ops []DashboardOp) ([]byte, error) {
	if len(ops) > MaxDashboardOps {
		return nil, fmt.Errorf("ops: %d ops, the cap is %d", len(ops), MaxDashboardOps)
	}
	d, err := ParseDashboard(data)
	if err != nil {
		return nil, err
	}
	if err := refuseUntargetedProblems(d, ops); err != nil {
		return nil, err
	}
	d.Widgets = append([]DashboardWidget(nil), d.Widgets...)
	// touched[i] is the 1-based number of the last op that added or updated the
	// widget at index i (a move does not count), 0 when none did: it lets a
	// problem of the result name the op that made it.
	touched := make([]int, len(d.Widgets))
	for n, op := range ops {
		if err := applyOp(&d, &touched, n+1, op); err != nil {
			return nil, fmt.Errorf("op %d (%s): %v", n+1, opSubject(op), err)
		}
	}
	out, err := EncodeDashboard(d)
	if err != nil {
		return nil, err
	}
	issues, err := validateDashboardIssues(out)
	if err != nil {
		return nil, err
	}
	if len(issues) == 0 {
		return out, nil
	}
	msgs := make([]string, 0, len(issues))
	for _, is := range issues {
		switch {
		case is.idx < 0:
			msgs = append(msgs, is.msg)
		case is.idx < len(touched) && touched[is.idx] > 0:
			n := touched[is.idx]
			msgs = append(msgs, fmt.Sprintf("op %d (%s, widget %q): %s", n, ops[n-1].Op, is.id, is.msg))
		default:
			msgs = append(msgs, fmt.Sprintf("widget %d (id %q): %s", is.idx+1, is.id, is.msg))
		}
	}
	return nil, fmt.Errorf("dashboard: %s", strings.Join(msgs, "; "))
}

// opSubject is what an op's error names after its number: the op, and the
// widget it targets when it targets one.
func opSubject(op DashboardOp) string {
	id := op.ID
	if id == "" && op.Op == DashboardOpAdd {
		// An add names its widget inside the widget.
		var w struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(op.Widget, &w) == nil {
			id = w.ID
		}
	}
	if id == "" {
		return op.Op
	}
	return fmt.Sprintf("%s, widget %q", op.Op, id)
}

// refuseUntargetedProblems refuses a patch that would silently rewrite a bad
// widget: the tolerant read keeps it, but encoding drops its Problem and its
// unknown keys. A widget with a problem must be updated or removed by an op.
func refuseUntargetedProblems(d Dashboard, ops []DashboardOp) error {
	targeted := map[string]bool{}
	for _, op := range ops {
		if op.Op == DashboardOpUpdate || op.Op == DashboardOpRemove {
			targeted[op.ID] = true
		}
	}
	for i, w := range d.Widgets {
		if w.Problem != "" && (w.ID == "" || !targeted[w.ID]) {
			return fmt.Errorf("widget %d (id %q) has a problem that no op updates or removes: %s", i+1, w.ID, w.Problem)
		}
	}
	return nil
}

// applyOp applies op, the n-th of the patch, to d and keeps touched, the op
// number of each widget, in step with d.Widgets.
func applyOp(d *Dashboard, touched *[]int, n int, op DashboardOp) error {
	// An op built in Go (not decoded) is held to the same shape.
	have := map[string]json.RawMessage{}
	if op.ID != "" {
		have["id"] = nil
	}
	if op.Index != nil {
		have["index"] = nil
	}
	if op.Widget != nil {
		have["widget"] = nil
	}
	if op.Title != nil {
		have["title"] = nil
	}
	if op.Description != nil {
		have["description"] = nil
	}
	have["op"] = nil
	if err := checkOpKeys(op.Op, have); err != nil {
		return err
	}
	switch op.Op {
	case DashboardOpAdd:
		w, err := decodeWidget(op.Widget)
		if err != nil {
			return err
		}
		at, err := checkIndex(op.Index, len(d.Widgets))
		if err != nil {
			return err
		}
		d.Widgets = append(d.Widgets, DashboardWidget{})
		copy(d.Widgets[at+1:], d.Widgets[at:])
		d.Widgets[at] = w
		*touched = append(*touched, 0)
		copy((*touched)[at+1:], (*touched)[at:])
		(*touched)[at] = n
	case DashboardOpUpdate:
		i := findWidget(d.Widgets, op.ID)
		if i < 0 {
			return errors.New("no widget")
		}
		w, err := mergeWidget(d.Widgets[i], op.Widget)
		if err != nil {
			return err
		}
		d.Widgets[i] = w
		(*touched)[i] = n
	case DashboardOpRemove:
		i := findWidget(d.Widgets, op.ID)
		if i < 0 {
			return errors.New("no widget")
		}
		d.Widgets = append(d.Widgets[:i], d.Widgets[i+1:]...)
		*touched = append((*touched)[:i], (*touched)[i+1:]...)
	case DashboardOpMove:
		i := findWidget(d.Widgets, op.ID)
		if i < 0 {
			return errors.New("no widget")
		}
		to, err := checkIndex(op.Index, len(d.Widgets)-1)
		if err != nil {
			return err
		}
		w := d.Widgets[i]
		d.Widgets = append(d.Widgets[:i], d.Widgets[i+1:]...)
		d.Widgets = append(d.Widgets, DashboardWidget{})
		copy(d.Widgets[to+1:], d.Widgets[to:])
		d.Widgets[to] = w
		t := (*touched)[i]
		*touched = append((*touched)[:i], (*touched)[i+1:]...)
		*touched = append(*touched, 0)
		copy((*touched)[to+1:], (*touched)[to:])
		(*touched)[to] = t
	case DashboardOpSetTitle:
		d.Title = *op.Title
	case DashboardOpSetDescription:
		d.Description = *op.Description
	}
	return nil
}
