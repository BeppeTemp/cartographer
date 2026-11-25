package kb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// DashboardsDir is the KB-root folder of the dashboards: one file
// dashboards/<name>.yaml per dashboard. It sits beside data/ and templates/,
// outside the concepts: no OKF frontmatter, not walked as a Map, not indexed.
const DashboardsDir = "dashboards"

const (
	// MaxDashboards is the number of dashboards a KB may hold.
	MaxDashboards = 32
	// MaxDashboardWidgets is the number of widgets of one dashboard.
	MaxDashboardWidgets = 40
	// MaxDashboardBytes is the size of one dashboard file.
	MaxDashboardBytes = 64 << 10

	dashboardTextMaxBytes   = 8000
	dashboardTitleMaxBytes  = 120
	dashboardDescMaxBytes   = 500
	dashboardMaxLimit       = 200
	dashboardMaxFilterList  = 20
	dashboardMaxFields      = 10
	dashboardMaxHorizon     = 365
	dashboardMaxColumns     = 8
	dashboardColumnMaxBytes = 200
)

var (
	dashboardNameRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	dashboardIDRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	dashboardFieldRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)
)

// dashboardWidgetTypes, dashboardIcons and dashboardAccents are the closed
// lists a widget draws from. The Atlas mirrors them and a test keeps both
// sides equal. Icons are names of the Atlas icon set; accents are names of
// its colour tokens, never a colour (D227: appearance is not free).
var (
	dashboardWidgetTypes = []string{"kanban", "list", "stat", "agenda", "note"}
	dashboardIcons       = []string{"list", "work", "health", "maps", "sources", "services", "artifacts", "components", "key", "paths", "eye", "lock", "alert", "person", "check", "info", "activity", "search", "grow", "settings"}
	dashboardAccents     = []string{"neutral", "primary", "accent", "clay", "ok", "info", "warning", "error"}
)

// DashboardWidgetTypes returns a copy of the widget types a dashboard accepts.
func DashboardWidgetTypes() []string { return append([]string(nil), dashboardWidgetTypes...) }

// DashboardIcons returns a copy of the icon names a widget may carry.
func DashboardIcons() []string { return append([]string(nil), dashboardIcons...) }

// DashboardAccents returns a copy of the accent names a widget may carry.
func DashboardAccents() []string { return append([]string(nil), dashboardAccents...) }

// Dashboard is the content of dashboards/<name>.yaml: named widgets that hold
// queries over the KB, never copies of its content (only a note holds text).
type Dashboard struct {
	Title       string            `yaml:"title,omitempty"       json:"title,omitempty"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Widgets     []DashboardWidget `yaml:"widgets"               json:"widgets"`
}

// DashboardWidget is one widget. The YAML of the file and the JSON of a patch
// op are this same struct, so a file converts to ops without a second schema.
type DashboardWidget struct {
	ID     string `yaml:"id"               json:"id"`   // slug, unique in the dashboard
	Type   string `yaml:"type"             json:"type"` // DashboardWidgetTypes()
	Title  string `yaml:"title,omitempty"  json:"title,omitempty"`
	Icon   string `yaml:"icon,omitempty"   json:"icon,omitempty"`   // DashboardIcons()
	Accent string `yaml:"accent,omitempty" json:"accent,omitempty"` // DashboardAccents()
	Width  int    `yaml:"width,omitempty"  json:"width,omitempty"`  // 1..4, 0 = default
	Source string `yaml:"source,omitempty" json:"source,omitempty"` // "work" (default) | "concepts"

	Filter  *DashboardFilter `yaml:"filter,omitempty"   json:"filter,omitempty"`
	GroupBy string           `yaml:"group_by,omitempty" json:"group_by,omitempty"` // status | map | type | field:<key>
	Columns []string         `yaml:"columns,omitempty"  json:"columns,omitempty"`
	Sort    string           `yaml:"sort,omitempty"     json:"sort,omitempty"` // age | title | status | field:<key>
	Limit   int              `yaml:"limit,omitempty"    json:"limit,omitempty"`

	Text        string `yaml:"text,omitempty"         json:"text,omitempty"`         // note only, 8000 bytes
	DateField   string `yaml:"date_field,omitempty"   json:"date_field,omitempty"`   // agenda; default review_after
	HorizonDays int    `yaml:"horizon_days,omitempty" json:"horizon_days,omitempty"` // agenda, default 14
	Show        string `yaml:"show,omitempty"         json:"show,omitempty"`         // stat: count | groups

	// Problem is set by the tolerant read only: why this widget cannot draw.
	// It is never written, and a file carrying an `error` key is refused.
	Problem string `yaml:"-" json:"error,omitempty"`
}

// DashboardFilter selects the entries a widget shows.
type DashboardFilter struct {
	Status   []string          `yaml:"status,omitempty"    json:"status,omitempty"`
	Map      []string          `yaml:"map,omitempty"       json:"map,omitempty"`
	Type     []string          `yaml:"type,omitempty"      json:"type,omitempty"`
	IDPrefix string            `yaml:"id_prefix,omitempty" json:"id_prefix,omitempty"`
	Stale    *bool             `yaml:"stale,omitempty"     json:"stale,omitempty"`
	HasItems *bool             `yaml:"has_items,omitempty" json:"has_items,omitempty"`
	Fields   map[string]string `yaml:"fields,omitempty"    json:"fields,omitempty"`
	Text     string            `yaml:"text,omitempty"      json:"text,omitempty"`
}

// windowsDeviceRE bars the names Windows treats as devices (con.yaml is one).
var windowsDeviceRE = regexp.MustCompile(`^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)

// ValidDashboardName reports whether name can name a dashboard file.
func ValidDashboardName(name string) bool {
	return dashboardNameRE.MatchString(name) && !windowsDeviceRE.MatchString(name)
}

// DashboardHash is the sha256 hex of a dashboard's bytes, the if_match token
// of its writes. A missing dashboard (nil bytes) has hash "".
func DashboardHash(data []byte) string {
	if data == nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// yamlKeys is the set of YAML keys a struct declares, from its tags.
func yamlKeys(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		if tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]; tag != "" && tag != "-" {
			keys[tag] = true
		}
	}
	return keys
}

var (
	dashboardKeys = yamlKeys(reflect.TypeOf(Dashboard{}))
	widgetKeys    = yamlKeys(reflect.TypeOf(DashboardWidget{}))
	filterKeys    = yamlKeys(reflect.TypeOf(DashboardFilter{}))
)

// dashboardReport is the outcome of reading a file once: the dashboard as far
// as it decodes, the problems of the file as a whole and those of each widget.
type dashboardReport struct {
	dash    Dashboard
	file    []string
	widgets map[int][]string // by widget index
}

func (r *dashboardReport) widgetProblem(i int, format string, a ...any) {
	r.widgets[i] = append(r.widgets[i], fmt.Sprintf(format, a...))
}

// unknownKeys names the keys of a mapping node that the struct does not have.
func unknownKeys(n *yaml.Node, known map[string]bool) []string {
	var out []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		if !known[n.Content[i].Value] {
			out = append(out, n.Content[i].Value)
		}
	}
	return out
}

// readDashboard decodes data and judges its shape. err is non-nil only when
// the bytes are not a YAML mapping (or are too large to be one).
func readDashboard(data []byte) (*dashboardReport, error) {
	if len(data) > MaxDashboardBytes {
		return nil, fmt.Errorf("dashboard: %d bytes, the cap is %d", len(data), MaxDashboardBytes)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("dashboard: not valid YAML: %v", err)
	}
	r := &dashboardReport{widgets: map[int][]string{}, dash: Dashboard{Widgets: []DashboardWidget{}}}
	if root.Kind == 0 || len(root.Content) == 0 || (root.Content[0].Kind == yaml.ScalarNode && root.Content[0].Tag == "!!null") {
		return r, nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, errors.New("dashboard: not a mapping")
	}
	for _, k := range unknownKeys(doc, dashboardKeys) {
		r.file = append(r.file, fmt.Sprintf("unknown field %q", k))
	}
	var wnode *yaml.Node
	topSeen := map[string]bool{}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		k, v := doc.Content[i].Value, doc.Content[i+1]
		if topSeen[k] {
			r.file = append(r.file, fmt.Sprintf("field %q declared twice", k))
			continue
		}
		topSeen[k] = true
		switch k {
		case "title", "description":
			if v.Kind != yaml.ScalarNode {
				r.file = append(r.file, k+" must be a string")
				continue
			}
			if k == "title" {
				r.dash.Title = v.Value
			} else {
				r.dash.Description = v.Value
			}
		case "widgets":
			wnode = v
		}
	}
	if len(r.dash.Title) > dashboardTitleMaxBytes || len(r.dash.Description) > dashboardDescMaxBytes {
		r.file = append(r.file, "title or description is too long")
	}
	if wnode == nil || (wnode.Kind == yaml.ScalarNode && wnode.Tag == "!!null") {
		return r, nil
	}
	if wnode.Kind != yaml.SequenceNode {
		r.file = append(r.file, "widgets must be a list")
		return r, nil
	}
	if len(wnode.Content) > MaxDashboardWidgets {
		r.file = append(r.file, fmt.Sprintf("%d widgets, the cap is %d", len(wnode.Content), MaxDashboardWidgets))
	}
	seen := map[string]bool{}
	for i, n := range wnode.Content {
		var w DashboardWidget
		if n.Kind != yaml.MappingNode {
			r.dash.Widgets = append(r.dash.Widgets, w)
			r.widgetProblem(i, "must be a mapping")
			continue
		}
		for _, k := range unknownKeys(n, widgetKeys) {
			r.widgetProblem(i, "unknown field %q", k)
		}
		if err := n.Decode(&w); err != nil {
			r.widgetProblem(i, "%s", strings.ReplaceAll(strings.TrimPrefix(err.Error(), "yaml: unmarshal errors:\n  "), "\n  ", "; "))
		}
		for j := 0; j+1 < len(n.Content); j += 2 {
			if fn := n.Content[j+1]; n.Content[j].Value == "filter" && fn.Kind == yaml.MappingNode {
				for _, k := range unknownKeys(fn, filterKeys) {
					r.widgetProblem(i, "filter: unknown field %q", k)
				}
			}
		}
		w.Problem = ""
		for _, p := range checkWidget(w, seen) {
			r.widgetProblem(i, "%s", p)
		}
		r.dash.Widgets = append(r.dash.Widgets, w)
	}
	for i, ps := range r.widgets {
		r.dash.Widgets[i].Problem = strings.Join(ps, "; ")
	}
	return r, nil
}

func oneOfString(s string, list []string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// checkField judges "<keyword>" or "field:<key>" against the keywords given.
func checkField(name, v string, keywords []string, allowField bool) string {
	if v == "" || oneOfString(v, keywords) {
		return ""
	}
	if allowField && strings.HasPrefix(v, "field:") && dashboardFieldRE.MatchString(strings.TrimPrefix(v, "field:")) {
		return ""
	}
	want := strings.Join(keywords, ", ")
	if allowField {
		want += ", field:<key>"
	}
	return fmt.Sprintf("%s %q is not one of %s", name, v, want)
}

// checkWidget judges the shape of one decoded widget, never the KB: a
// preset is written before the KB it lands in. seen carries the ids of the
// widgets before it.
func checkWidget(w DashboardWidget, seen map[string]bool) []string {
	var p []string
	add := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	switch {
	case w.ID == "":
		add("id is required")
	case !dashboardIDRE.MatchString(w.ID):
		add("id %q must be a lowercase slug of at most 32 characters", w.ID)
	case seen[w.ID]:
		add("duplicate widget id %q", w.ID)
	}
	seen[w.ID] = true
	if !oneOfString(w.Type, dashboardWidgetTypes) {
		add("type %q is not one of %s", w.Type, strings.Join(dashboardWidgetTypes, ", "))
	}
	if len(w.Title) > dashboardTitleMaxBytes {
		add("title is longer than %d bytes", dashboardTitleMaxBytes)
	}
	if w.Icon != "" && !oneOfString(w.Icon, dashboardIcons) { // empty = none
		add("icon %q is not one of %s", w.Icon, strings.Join(dashboardIcons, ", "))
	}
	if w.Accent != "" && !oneOfString(w.Accent, dashboardAccents) {
		add("accent %q is not one of %s", w.Accent, strings.Join(dashboardAccents, ", "))
	}
	if w.Width < 0 || w.Width > 4 {
		add("width %d is not between 1 and 4", w.Width)
	}
	if !oneOfString(w.Source, []string{"", "work", "concepts"}) {
		add("source %q is not one of work, concepts", w.Source)
	}
	if m := checkField("group_by", w.GroupBy, []string{"status", "map", "type"}, true); m != "" {
		add("%s", m)
	}
	if m := checkField("sort", w.Sort, []string{"age", "title", "status"}, true); m != "" {
		add("%s", m)
	}
	if w.Limit < 0 || w.Limit > dashboardMaxLimit {
		add("limit %d is not between 0 and %d", w.Limit, dashboardMaxLimit)
	}
	if len(w.Columns) > dashboardMaxColumns {
		add("%d columns, the cap is %d", len(w.Columns), dashboardMaxColumns)
	}
	for _, c := range w.Columns {
		// A column is a value of the group-by field ("in progress", "2026"),
		// not a field name.
		if c == "" || len(c) > dashboardColumnMaxBytes || strings.ContainsFunc(c, unicode.IsControl) {
			add("column %q must be a non-empty value of at most %d bytes with no control character", c, dashboardColumnMaxBytes)
		}
	}
	if w.Type != "note" && w.Text != "" {
		add("text belongs to a note")
	}
	if w.Type == "note" && strings.TrimSpace(w.Text) == "" {
		add("a note needs text")
	}
	if len(w.Text) > dashboardTextMaxBytes {
		add("text is longer than %d bytes", dashboardTextMaxBytes)
	}
	if (w.DateField != "" || w.HorizonDays != 0) && w.Type != "agenda" {
		add("date_field and horizon_days are only for an agenda")
	}
	if w.DateField != "" && !dashboardFieldRE.MatchString(w.DateField) {
		add("date_field %q is not a field name", w.DateField)
	}
	if w.HorizonDays < 0 || w.HorizonDays > dashboardMaxHorizon {
		add("horizon_days %d is not between 1 and %d", w.HorizonDays, dashboardMaxHorizon)
	}
	if w.Show != "" && (w.Type != "stat" || (w.Show != "count" && w.Show != "groups")) {
		add("show %q is only for a stat, one of count, groups", w.Show)
	}
	if f := w.Filter; f != nil {
		if len(f.Status) > dashboardMaxFilterList || len(f.Map) > dashboardMaxFilterList || len(f.Type) > dashboardMaxFilterList {
			add("a filter list has more than %d values", dashboardMaxFilterList)
		}
		if len(f.Fields) > dashboardMaxFields || len(f.IDPrefix) > 200 || len(f.Text) > 200 {
			add("filter: at most %d fields, id_prefix and text at most 200 bytes", dashboardMaxFields)
		}
		for k := range f.Fields {
			if !dashboardFieldRE.MatchString(k) {
				add("filter.fields key %q is not a field name", k)
			}
		}
	}
	return p
}

// ParseDashboard reads a dashboard tolerantly: err is non-nil only when the
// bytes are not a YAML mapping at all (or exceed MaxDashboardBytes). A bad
// widget stays in Widgets with Problem set, so the rest of the dashboard draws.
// Pure function of its bytes: no KB, no filesystem.
func ParseDashboard(data []byte) (Dashboard, error) {
	r, err := readDashboard(data)
	if err != nil {
		return Dashboard{}, err
	}
	return r.dash, nil
}

// ValidateDashboard judges data strictly and reports every problem in one
// error, each with its widget index and id. It checks the shape only, never
// that a field or an id exists in a KB. Pure function of its bytes.
func ValidateDashboard(data []byte) error {
	r, err := readDashboard(data)
	if err != nil {
		return err
	}
	msgs := append([]string(nil), r.file...)
	idx := make([]int, 0, len(r.widgets))
	for i := range r.widgets {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		id := r.dash.Widgets[i].ID
		for _, m := range r.widgets[i] {
			msgs = append(msgs, fmt.Sprintf("widgets[%d] (id %q): %s", i, id, m))
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf("dashboard: %s", strings.Join(msgs, "; "))
}

// EncodeDashboard writes d as canonical YAML: the key order of the structs,
// two-space indent, Problem dropped.
func EncodeDashboard(d Dashboard) ([]byte, error) {
	if d.Widgets == nil {
		d.Widgets = []DashboardWidget{}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// dashboardsDir resolves the dashboards folder and refuses a symlinked one, as
// every KB-root artifact is (D148). exists is false when it is absent.
func (kb *KB) dashboardsDir() (dir string, exists bool, err error) {
	dir, err = kb.ResolveRootPath(DashboardsDir)
	if err != nil {
		return "", false, err
	}
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return dir, false, nil
	}
	if err != nil {
		return "", false, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("%s: symlink not allowed", DashboardsDir)
	}
	return dir, true, nil
}

// ReadDashboard returns the bytes of dashboards/<name>.yaml. A missing
// dashboard is an error wrapping os.ErrNotExist; its hash is "". A symlink or
// a non-regular file is refused.
func (kb *KB) ReadDashboard(name string) ([]byte, error) {
	if !ValidDashboardName(name) {
		return nil, fmt.Errorf("dashboard name %q: want a lowercase slug of at most 48 characters", name)
	}
	dir, exists, err := kb.dashboardsDir()
	if err != nil {
		return nil, err
	}
	rel := DashboardsDir + "/" + name + ".yaml"
	path := filepath.Join(dir, name+".yaml")
	fi, lerr := os.Lstat(path)
	if !exists || errors.Is(lerr, os.ErrNotExist) {
		return nil, fmt.Errorf("dashboard %q: %w", name, os.ErrNotExist)
	}
	switch {
	case lerr != nil:
		return nil, lerr
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("%s: symlink not allowed", rel)
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("%s: not a regular file", rel)
	case fi.Size() > MaxDashboardBytes:
		return nil, fmt.Errorf("%s: %d bytes, the cap is %d", rel, fi.Size(), MaxDashboardBytes)
	}
	return os.ReadFile(path)
}

// ListDashboards names the dashboards of the KB, sorted. A symlink, a
// non-regular file or a file whose name is not a dashboard name is not one.
func (kb *KB) ListDashboards() ([]string, error) {
	dir, exists, err := kb.dashboardsDir()
	if err != nil || !exists {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n, ok := strings.CutSuffix(e.Name(), ".yaml")
		if ok && ValidDashboardName(n) && e.Type().IsRegular() {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}
