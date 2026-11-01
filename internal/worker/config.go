package worker

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	// The alpine image has no zoneinfo, so the embedded copy is what makes
	// schedule.window.timezone resolve there as it does on a developer machine.
	// time.LoadLocation tries ZONEINFO and the system first, so no behavioural
	// test can prove this import; TestTzdataIsImported pins it instead.
	_ "time/tzdata"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/worker/connector"
	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
	"github.com/BeppeTemp/cartographer/internal/workerconfig"
	"gopkg.in/yaml.v3"
)

// The small leaf of a worker config that Config keeps for the runner. Everything
// here is validated by LoadConfig: a Config in hand is a config that can work.

// Tiers and providers a profile can name.
const (
	TierSmall = "small"
	TierLarge = "large"

	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)

// Profile is one named model profile and its budget (D21).
type Profile struct {
	Name              string
	Provider          string
	BaseURL           string
	AllowInsecureHTTP bool
	Model             string // the provider's model id
	APIKeyEnv         string
	Tier              string
	DailyTokens       int
	RunTokens         int // at most DailyTokens; the most one run spends

	apiKey string
}

// APIKey is the key read from APIKeyEnv at load, empty when the profile names none.
func (p Profile) APIKey() string { return p.apiKey }

// String redacts the key.
func (p Profile) String() string { return p.GoString() }

// GoString redacts the key.
func (p Profile) GoString() string {
	return fmt.Sprintf("worker.Profile{Name: %q, Provider: %q, BaseURL: %q, Model: %q, Tier: %q, DailyTokens: %d, RunTokens: %d, key: [redacted]}",
		p.Name, p.Provider, p.BaseURL, p.Model, p.Tier, p.DailyTokens, p.RunTokens)
}

// Window is the daily time window the worker acts in, in its own time zone.
type Window struct {
	Start, End int // minutes since midnight; End before Start crosses midnight
	Location   *time.Location
}

// Contains reports whether t falls in the window: the start is included, the
// end excluded, and a window whose end is before its start crosses midnight.
func (w Window) Contains(t time.Time) bool {
	l := t.In(w.Location)
	m := l.Hour()*60 + l.Minute()
	if w.Start < w.End {
		return m >= w.Start && m < w.End
	}
	return m >= w.Start || m < w.End
}

// TaskSpec is one tasks[] entry, in priority order.
type TaskSpec struct {
	Kind    string
	Profile string
	// Options are the decoded options of the kind, defaults applied: string, int,
	// bool, time.Duration or []string, by the option's declared type.
	Options map[string]any
	Line    int
}

// Config is a loaded, validated worker config.
type Config struct {
	Name              string
	ServerURL         string
	AllowInsecureHTTP bool
	TokenEnv          string
	KBs               []string
	Models            []Profile
	Window            *Window // nil when only the idle trigger is set
	IdleMinutes       int     // 0 when unset
	PollSeconds       int
	// FetchAllowPrivate is fetch.allow_private: the ingest task's built-in fetch
	// may reach private, loopback and link-local destinations.
	FetchAllowPrivate bool
	// FetchAllowHosts and FetchAllowInsecureHTTP are fetch.allow_hosts and
	// fetch.allow_insecure_http: they gate the http-pages recipe only (D1262).
	FetchAllowHosts        []string
	FetchAllowInsecureHTTP bool
	Tasks                  []TaskSpec
	// Connectors are the external MCP servers of the perimeter, by name (D1262).
	Connectors []connector.Config
	// RecipeInstructions are the recipes.<name>.instructions_file texts, read at
	// load; GitAllowHosts is git.allow_hosts (empty: no feed uses the git reader).
	RecipeInstructions map[string]string
	GitAllowHosts      []string
	// ExecRecipes are the recipes.<name> of kind exec (protocol v1, D1262), commands
	// already resolved; they come from this file and from nowhere else.
	ExecRecipes []recipe.ExecConfig
	// Warnings are conditions worth one line at start that do not stop it.
	Warnings []string

	token string
	doc   *yaml.Node
}

// Token is the bearer token read from TokenEnv at load.
func (c *Config) Token() string { return c.token }

// Profile returns the profile with the given name.
func (c *Config) Profile(name string) (Profile, bool) {
	for _, p := range c.Models {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// Document is the validated YAML document, for a settings report that renders
// it with the key table (settingschema.Set builds only one-entry lists).
func (c *Config) Document() *yaml.Node { return c.doc }

// String redacts the token.
func (c Config) String() string { return c.GoString() }

// GoString redacts the token and the keys of the profiles.
func (c Config) GoString() string {
	return fmt.Sprintf("worker.Config{Name: %q, ServerURL: %q, KBs: %v, Models: %v, Tasks: %d, token: [redacted]}",
		c.Name, c.ServerURL, c.KBs, c.Models, len(c.Tasks))
}

// nameSegment stands in a key-table path for any key of a name-keyed mapping.
const nameSegment = "{name}"

var nameRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
var clockRE = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// rawConfig is the typed shape of the file. Task entries stay nodes: their
// options depend on their kind.
type rawConfig struct {
	Name              string      `yaml:"name"`
	ServerURL         string      `yaml:"server_url"`
	AllowInsecureHTTP bool        `yaml:"allow_insecure_http"`
	TokenEnv          string      `yaml:"token_env"`
	KBs               []string    `yaml:"kbs"`
	Models            []rawModel  `yaml:"models"`
	Schedule          rawSchedule `yaml:"schedule"`
	Fetch             struct {
		AllowPrivate      bool     `yaml:"allow_private"`
		AllowHosts        []string `yaml:"allow_hosts"`
		AllowInsecureHTTP bool     `yaml:"allow_insecure_http"`
	} `yaml:"fetch"`
	Tasks      []yaml.Node             `yaml:"tasks"`
	Connectors map[string]rawConnector `yaml:"connectors"`
	Recipes    map[string]rawRecipe    `yaml:"recipes"`
	Git        rawGit                  `yaml:"git"`
}

type rawModel struct {
	Name              string `yaml:"name"`
	Provider          string `yaml:"provider"`
	BaseURL           string `yaml:"base_url"`
	AllowInsecureHTTP bool   `yaml:"allow_insecure_http"`
	Model             string `yaml:"model"`
	APIKeyEnv         string `yaml:"api_key_env"`
	Tier              string `yaml:"tier"`
	Budget            struct {
		DailyTokens int `yaml:"daily_tokens"`
		RunTokens   int `yaml:"run_tokens"`
	} `yaml:"budget"`
}

type rawSchedule struct {
	Window *struct {
		Start    string `yaml:"start"`
		End      string `yaml:"end"`
		Timezone string `yaml:"timezone"`
	} `yaml:"window"`
	IdleMinutes int `yaml:"idle_minutes"`
	PollSeconds int `yaml:"poll_seconds"`
}

// LoadConfig reads and validates a worker config file against the key table and
// the registry of task kinds. The first error in file order names the key path
// and the line. A token or API key variable that is unset or empty is an error
// naming the variable; no value is ever part of an error.
func LoadConfig(path string, kinds *Kinds) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("worker config: read %q: %w", path, err)
	}
	c, err := parseConfig(data, kinds, filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("worker config %s: %w", path, err)
	}
	return c, nil
}

// parseConfig parses a config; dir is the config file's directory, against which
// a relative path in the file is resolved ("" leaves such a path as written).
func parseConfig(data []byte, kinds *Kinds, dir string) (*Config, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, errors.New("the file is empty")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("the top level must be a mapping of keys")
	}
	w := &walker{schema: knownKeys(), kinds: kinds}
	if err := w.walk(root, "", ""); err != nil {
		return nil, err
	}
	var raw rawConfig
	if err := root.Decode(&raw); err != nil {
		return nil, err
	}
	c := &Config{doc: &doc}
	v := &validator{raw: raw, root: root, c: c, kinds: kinds, dir: dir}
	if err := v.run(); err != nil {
		return nil, err
	}
	return c, nil
}

// pathNode is the tree of known keys, built from the key table: the one place
// that lists them.
type pathNode struct {
	children map[string]*pathNode
	list     bool // the key holds a list of mappings
}

func knownKeys() *pathNode {
	root := &pathNode{children: map[string]*pathNode{}}
	for _, k := range workerconfig.Schema.Keys {
		n := root
		for _, seg := range strings.Split(k.Path, ".") {
			name, list := strings.CutSuffix(seg, "[]")
			child, ok := n.children[name]
			if !ok {
				child = &pathNode{children: map[string]*pathNode{}}
				n.children[name] = child
			}
			child.list = child.list || list
			n = child
		}
	}
	return root
}

// walker checks a parsed document against the key table, in file order.
type walker struct {
	schema *pathNode
	kinds  *Kinds
}

// walk checks the mapping n, whose known keys are those of the table node at
// path ("" is the top level). at is the concrete path to n for messages, with
// list indexes ("models[0]"); "" is the top level.
func (w *walker) walk(n *yaml.Node, path, at string) error {
	where := at
	if at == "" {
		where = "the top level"
	}
	known := w.schema
	if path != "" {
		known = w.lookup(path)
	}
	seen := map[string]int{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if prev, dup := seen[k.Value]; dup {
			return fmt.Errorf("duplicate key %q under %s (line %d, column %d; first at line %d)", k.Value, where, k.Line, k.Column, prev)
		}
		seen[k.Value] = k.Line
		child, ok := known.children[k.Value]
		seg := k.Value
		if !ok {
			// A name-keyed mapping (connectors): the table spells its keys {name}.
			child, ok = known.children[nameSegment]
			seg = nameSegment
		}
		if !ok {
			return fmt.Errorf("unknown key %q under %s (line %d, column %d)", k.Value, where, k.Line, k.Column)
		}
		childPath := join(path, seg)
		if child.list {
			childPath += "[]"
		}
		// A key that holds other keys must hold the right shape, so a wrong one
		// is reported with its key path and line rather than as a decoder error.
		// A null value is left to the "required" checks.
		if len(child.children) == 0 || v.Tag == "!!null" {
			continue
		}
		here := join(at, k.Value)
		switch {
		case child.list:
			if v.Kind != yaml.SequenceNode {
				return fmt.Errorf("%s (line %d): must be a list of mappings", here, v.Line)
			}
			for j, e := range v.Content {
				entry := fmt.Sprintf("%s[%d]", here, j)
				if k.Value == "tasks" {
					// Checked against their kind's options by the validator, which
					// also words the error for a non-mapping entry.
					if e.Kind == yaml.MappingNode {
						if err := dupKeys(e, entry); err != nil {
							return err
						}
					}
					continue
				}
				if e.Kind != yaml.MappingNode {
					return fmt.Errorf("%s (line %d): must be a mapping of keys", entry, e.Line)
				}
				if err := w.walk(e, childPath, entry); err != nil {
					return err
				}
			}
		default:
			if v.Kind != yaml.MappingNode {
				return fmt.Errorf("%s (line %d): must be a mapping of keys", here, v.Line)
			}
			if err := w.walk(v, childPath, here); err != nil {
				return err
			}
		}
	}
	return nil
}

// lookup finds the table node of a path such as "models[]" or "schedule.window".
func (w *walker) lookup(path string) *pathNode {
	n := w.schema
	for _, seg := range strings.Split(path, ".") {
		n = n.children[strings.TrimSuffix(seg, "[]")]
	}
	return n
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func dupKeys(n *yaml.Node, at string) error {
	seen := map[string]int{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if prev, dup := seen[k.Value]; dup {
			return fmt.Errorf("duplicate key %q under %s (line %d, column %d; first at line %d)", k.Value, at, k.Line, k.Column, prev)
		}
		seen[k.Value] = k.Line
	}
	return nil
}

// validator fills a Config from the typed file, checking each value.
type validator struct {
	raw   rawConfig
	root  *yaml.Node
	c     *Config
	kinds *Kinds
	dir   string
}

// line is the line of key (a path of keys and list indexes) in the document, or 0.
func (v *validator) line(path ...any) int {
	n := v.root
	var keyLine int
	for _, p := range path {
		switch p := p.(type) {
		case string:
			if n == nil || n.Kind != yaml.MappingNode {
				return keyLine
			}
			var next *yaml.Node
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == p {
					keyLine, next = n.Content[i].Line, n.Content[i+1]
				}
			}
			n = next
		case int:
			if n == nil || n.Kind != yaml.SequenceNode || p >= len(n.Content) {
				return keyLine
			}
			n = n.Content[p]
			keyLine = n.Line
		}
	}
	return keyLine
}

func (v *validator) errf(path string, line int, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if line > 0 {
		return fmt.Errorf("%s (line %d): %s", path, line, msg)
	}
	return fmt.Errorf("%s: %s", path, msg)
}

// envValue reads the variable a *_env key names; unset or empty is an error
// that names the variable and never a value.
func (v *validator) envValue(path string, line int, name string) (string, error) {
	val := os.Getenv(name)
	if val == "" {
		return "", v.errf(path, line, "environment variable %s is unset or empty", name)
	}
	return val, nil
}

func (v *validator) run() error {
	r, c := v.raw, v.c
	c.Name, c.ServerURL, c.AllowInsecureHTTP, c.TokenEnv = r.Name, r.ServerURL, r.AllowInsecureHTTP, r.TokenEnv
	c.FetchAllowPrivate = r.Fetch.AllowPrivate
	c.FetchAllowInsecureHTTP = r.Fetch.AllowInsecureHTTP

	if !nameRE.MatchString(r.Name) {
		return v.errf("name", v.line("name"), "required, 1 to 40 characters of a-z, 0-9 and -")
	}
	if strings.Contains(r.Name, doctorMarker) {
		return v.errf("name", v.line("name"), "must not contain %q: the server reads it in a log entry as a closed doctor session", doctorMarker)
	}
	if r.ServerURL == "" {
		return v.errf("server_url", 0, "required: the MCP server URL")
	}
	if err := checkURL(r.ServerURL, r.AllowInsecureHTTP); err != nil {
		return v.errf("server_url", v.line("server_url"), "%v", err)
	}
	if r.TokenEnv == "" {
		return v.errf("token_env", 0, "required: the name of the environment variable holding the bearer token")
	}
	tok, err := v.envValue("token_env", v.line("token_env"), r.TokenEnv)
	if err != nil {
		return err
	}
	c.token = tok

	if len(r.KBs) == 0 {
		return v.errf("kbs", v.line("kbs"), "required: at least one KB name")
	}
	if len(r.KBs) > MaxKBs {
		return v.errf("kbs", v.line("kbs"), "at most %d KBs per perimeter (the server refuses a heartbeat naming more)", MaxKBs)
	}
	seenKB := map[string]bool{}
	for i, kb := range r.KBs {
		switch {
		case strings.TrimSpace(kb) == "":
			return v.errf(fmt.Sprintf("kbs[%d]", i), v.line("kbs"), "a KB name cannot be empty")
		case seenKB[kb]:
			return v.errf(fmt.Sprintf("kbs[%d]", i), v.line("kbs"), "KB %q is listed twice", kb)
		}
		seenKB[kb] = true
	}
	c.KBs = slices.Clone(r.KBs)

	if err := v.models(); err != nil {
		return err
	}
	if err := v.schedule(); err != nil {
		return err
	}
	if err := v.connectors(); err != nil {
		return err
	}
	if err := v.recipes(); err != nil {
		return err
	}
	return v.tasks()
}

// Limits of the heartbeat and settings routes (D1259): a config past them would
// have every beat refused with 400, so it is refused at load instead.
const (
	MaxKBs      = 32
	MaxProfiles = 8
)

func (v *validator) models() error {
	if len(v.raw.Models) == 0 {
		return v.errf("models", v.line("models"), "required: at least one model profile")
	}
	if len(v.raw.Models) > MaxProfiles {
		return v.errf("models", v.line("models"), "at most %d model profiles (the server refuses a heartbeat with more budgets)", MaxProfiles)
	}
	for i, m := range v.raw.Models {
		at := fmt.Sprintf("models[%d]", i)
		ln := func(key ...string) int {
			p := []any{"models", i}
			for _, k := range key {
				p = append(p, k)
			}
			return v.line(p...)
		}
		if !nameRE.MatchString(m.Name) {
			return v.errf(at+".name", ln(), "required, 1 to 40 characters of a-z, 0-9 and -")
		}
		if _, dup := v.c.Profile(m.Name); dup {
			return v.errf(at+".name", ln("name"), "profile %q is defined twice", m.Name)
		}
		p := Profile{Name: m.Name, Provider: m.Provider, BaseURL: m.BaseURL, AllowInsecureHTTP: m.AllowInsecureHTTP,
			Model: m.Model, APIKeyEnv: m.APIKeyEnv, Tier: m.Tier, DailyTokens: m.Budget.DailyTokens, RunTokens: m.Budget.RunTokens}
		if m.Provider != ProviderAnthropic && m.Provider != ProviderOpenAI {
			return v.errf(at+".provider", ln("provider"), "required: %s or %s", ProviderAnthropic, ProviderOpenAI)
		}
		if m.Model == "" {
			return v.errf(at+".model", ln(), "required: the provider's model id")
		}
		switch {
		case m.BaseURL == "" && m.Provider == ProviderOpenAI:
			return v.errf(at+".base_url", ln(), "required for provider openai: the endpoint, for example a local llama.cpp or LiteLLM")
		case m.BaseURL != "":
			if err := checkURL(m.BaseURL, m.AllowInsecureHTTP); err != nil {
				return v.errf(at+".base_url", ln("base_url"), "%v", err)
			}
		}
		if m.APIKeyEnv == "" && m.Provider == ProviderAnthropic {
			return v.errf(at+".api_key_env", ln(), "required for provider anthropic: the name of the environment variable holding the key")
		}
		if m.APIKeyEnv != "" {
			key, err := v.envValue(at+".api_key_env", ln("api_key_env"), m.APIKeyEnv)
			if err != nil {
				return err
			}
			p.apiKey = key
		}
		switch m.Tier {
		case "":
			p.Tier = TierSmall
		case TierSmall, TierLarge:
		default:
			return v.errf(at+".tier", ln("tier"), "%q is not %s or %s", m.Tier, TierSmall, TierLarge)
		}
		if m.Budget.DailyTokens <= 0 {
			return v.errf(at+".budget.daily_tokens", ln("budget"), "required: tokens per day, above 0")
		}
		switch {
		case m.Budget.RunTokens < 0:
			return v.errf(at+".budget.run_tokens", ln("budget"), "must be above 0")
		case m.Budget.RunTokens == 0:
			p.RunTokens = m.Budget.DailyTokens
		case m.Budget.RunTokens > m.Budget.DailyTokens:
			return v.errf(at+".budget.run_tokens", ln("budget"), "%d is above daily_tokens (%d)", m.Budget.RunTokens, m.Budget.DailyTokens)
		}
		v.c.Models = append(v.c.Models, p)
	}
	return nil
}

func (v *validator) schedule() error {
	s, c := v.raw.Schedule, v.c
	line := v.line("schedule")
	if w := s.Window; w != nil {
		wl := v.line("schedule", "window")
		if !clockRE.MatchString(w.Start) {
			return v.errf("schedule.window.start", wl, "required, as HH:MM")
		}
		if !clockRE.MatchString(w.End) {
			return v.errf("schedule.window.end", wl, "required, as HH:MM")
		}
		if w.Start == w.End {
			return v.errf("schedule.window", wl, "start and end are equal: the window would be empty or the whole day")
		}
		if w.Timezone == "" || w.Timezone == "Local" {
			return v.errf("schedule.window.timezone", wl, "required, an IANA name such as Europe/Rome or UTC: a container's local time is UTC")
		}
		loc, err := time.LoadLocation(w.Timezone)
		if err != nil {
			return v.errf("schedule.window.timezone", wl, "unknown time zone %q", w.Timezone)
		}
		c.Window = &Window{Start: clockMinutes(w.Start), End: clockMinutes(w.End), Location: loc}
	}
	if s.IdleMinutes < 0 {
		return v.errf("schedule.idle_minutes", line, "must be at least 1")
	}
	c.IdleMinutes = s.IdleMinutes
	switch {
	case s.PollSeconds == 0:
		c.PollSeconds = 60
	case s.PollSeconds < 10:
		return v.errf("schedule.poll_seconds", line, "%d is below the minimum of 10", s.PollSeconds)
	default:
		c.PollSeconds = s.PollSeconds
	}
	switch {
	case c.Window == nil && c.IdleMinutes == 0:
		return v.errf("schedule", line, "set schedule.window or schedule.idle_minutes: without a trigger the worker never runs")
	case c.Window == nil:
		return v.errf("schedule.idle_minutes", line, "the idle trigger needs a server that reports KB activity, and none does yet: set schedule.window")
	case c.IdleMinutes > 0:
		c.Warnings = append(c.Warnings, "schedule.idle_minutes is set but the server does not report KB activity yet: the worker runs on the window alone")
	}
	return nil
}

func clockMinutes(s string) int {
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[3:])
	return h*60 + m
}

func (v *validator) tasks() error {
	if len(v.raw.Tasks) == 0 {
		return v.errf("tasks", v.line("tasks"), "required: at least one task, as a list of mappings with kind and model")
	}
	used := map[string]bool{}
	firstOf := map[string]int{} // line of the first entry of each kind
	for i := range v.raw.Tasks {
		n := &v.raw.Tasks[i]
		at := fmt.Sprintf("tasks[%d]", i)
		if n.Kind != yaml.MappingNode {
			return v.errf(at, n.Line, "each task is a mapping with kind and model, not %q", n.Value)
		}
		t := TaskSpec{Line: n.Line, Options: map[string]any{}}
		var kindName string
		var kindNode *yaml.Node
		for j := 0; j+1 < len(n.Content); j += 2 {
			switch n.Content[j].Value {
			case "kind":
				kindName, kindNode = n.Content[j+1].Value, n.Content[j+1]
			case "model":
				t.Profile = n.Content[j+1].Value
			}
		}
		if kindNode == nil || kindName == "" {
			return v.errf(at+".kind", n.Line, "required: one of %s", v.kindList())
		}
		kind, ok := v.kinds.Lookup(kindName)
		if !ok {
			return v.errf(at+".kind", kindNode.Line, "unknown kind %q: %s", kindName, v.kindList())
		}
		t.Kind = kindName
		if t.Profile == "" {
			return v.errf(at+".model", n.Line, "required: one of the profiles %s", v.profileList())
		}
		if _, ok := v.c.Profile(t.Profile); !ok {
			return v.errf(at+".model", n.Line, "unknown profile %q: defined %s", t.Profile, v.profileList())
		}
		used[t.Profile] = true
		if err := v.options(at, n, kind, &t); err != nil {
			return err
		}
		if prev := firstOf[kind.Name]; prev > 0 {
			if !kind.repeatable() {
				return v.errf(at+".kind", n.Line, "kind %q is listed twice (lines %d and %d): only a kind with a name option may repeat", kind.Name, prev, n.Line)
			}
			// Entries of a repeatable kind are told apart by name (D23).
			name, _ := t.Options["name"].(string)
			if name == "" {
				return v.errf(at+".name", n.Line, "kind %q appears more than once: every entry sets name", kind.Name)
			}
			for _, p := range v.c.Tasks {
				if p.Kind != kind.Name {
					continue
				}
				switch pn, _ := p.Options["name"].(string); {
				case pn == "":
					return v.errf("tasks", p.Line, "kind %q appears more than once: every entry sets name", kind.Name)
				case pn == name:
					return v.errf(at+".name", n.Line, "name %q is used twice by kind %q (also at line %d)", name, kind.Name, p.Line)
				}
			}
		}
		v.c.Tasks = append(v.c.Tasks, t)
		if firstOf[kind.Name] == 0 {
			firstOf[kind.Name] = n.Line
		}
	}
	for i, t := range v.c.Tasks {
		if k, _ := v.kinds.Lookup(t.Kind); k.Last && i < len(v.c.Tasks)-1 {
			return v.errf(fmt.Sprintf("tasks[%d].kind", i), t.Line, "kind %q must be the last entry of tasks: it runs on what the entries before it leave, and listed first it would shadow them", t.Kind)
		}
	}
	for _, p := range v.c.Models {
		if !used[p.Name] {
			v.c.Warnings = append(v.c.Warnings, fmt.Sprintf("model profile %q is used by no task", p.Name))
		}
	}
	return nil
}

func (v *validator) kindList() string {
	names := v.kinds.Names()
	if len(names) == 0 {
		return "no task kind is registered in this binary"
	}
	return "the registered kinds are " + strings.Join(names, ", ")
}

func (v *validator) profileList() string {
	var names []string
	for _, p := range v.c.Models {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

// options decodes the options of one entry against its kind's declarations.
func (v *validator) options(at string, n *yaml.Node, kind Kind, t *TaskSpec) error {
	given := map[string]*yaml.Node{}
	for j := 0; j+1 < len(n.Content); j += 2 {
		key, val := n.Content[j], n.Content[j+1]
		if key.Value == "kind" || key.Value == "model" {
			continue
		}
		if _, ok := kind.option(key.Value); !ok {
			var have []string
			for _, o := range kind.Options {
				have = append(have, o.name())
			}
			return fmt.Errorf("unknown key %q under %s (kind %q, line %d, column %d): its options are %s",
				key.Value, at, kind.Name, key.Line, key.Column, orNone(have))
		}
		given[key.Value] = val
	}
	for _, o := range kind.Options {
		name := o.name()
		val, ok := given[name]
		var decoded any
		var err error
		switch {
		case ok:
			decoded, err = decodeNode(o.Key.Type, val)
			if err != nil {
				return v.errf(at+"."+name, val.Line, "%v", err)
			}
		case o.Required:
			return v.errf(at+"."+name, n.Line, "required by kind %q", kind.Name)
		default:
			if decoded, err = defaultOf(o); err != nil {
				return v.errf(at+"."+name, n.Line, "%v", err)
			}
		}
		t.Options[name] = decoded
	}
	if kind.Validate != nil {
		if err := kind.Validate(t.Options); err != nil {
			return v.errf(at, n.Line, "%v", err)
		}
	}
	if kind.Load != nil {
		set := map[string]bool{}
		for k := range given {
			set[k] = true
		}
		env := LoadEnv{Dir: v.dir, Perimeter: v.c.Name, KBs: v.c.KBs, Given: set,
			Warn: func(f string, a ...any) { v.c.Warnings = append(v.c.Warnings, fmt.Sprintf(f, a...)) }}
		env.Profile, _ = v.c.Profile(t.Profile)
		if err := kind.Load(env, t.Options); err != nil {
			return v.errf(at, n.Line, "%v", err)
		}
	}
	return nil
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// decodeLiteral decodes a YAML literal such as a key's Default.
func decodeLiteral(typ, lit string) (any, error) {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(lit), &n); err != nil || len(n.Content) == 0 {
		return nil, fmt.Errorf("not a YAML value")
	}
	return decodeNode(typ, n.Content[0])
}

// decodeNode decodes one option value by its declared type.
func decodeNode(typ string, n *yaml.Node) (any, error) {
	switch typ {
	case "string":
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
			return nil, errors.New("must be a string")
		}
		return n.Value, nil
	case "int":
		var i int
		if n.Kind != yaml.ScalarNode || n.Tag != "!!int" || n.Decode(&i) != nil {
			return nil, errors.New("must be an integer")
		}
		return i, nil
	case "bool":
		var b bool
		if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" || n.Decode(&b) != nil {
			return nil, errors.New("must be true or false")
		}
		return b, nil
	case "duration":
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
			return nil, errors.New("must be a duration such as 30m or 24h")
		}
		return durationOf(n.Value)
	case "list":
		if n.Kind != yaml.SequenceNode {
			return nil, errors.New("must be a list of strings")
		}
		out := []string{}
		for _, e := range n.Content {
			if e.Kind != yaml.ScalarNode || e.Tag != "!!str" {
				return nil, errors.New("must be a list of strings")
			}
			out = append(out, e.Value)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown option type %q", typ)
}

// checkURL gates a URL key: it names the key's problem without ever echoing the
// value, which may carry credentials. Userinfo is refused before
// client.CheckServerURL sees the value, so no error can contain it.
func checkURL(s string, allowInsecure bool) error {
	u, err := url.Parse(s)
	switch {
	case err != nil:
		return errors.New("not a valid URL")
	case u.Host == "":
		return errors.New("has no host")
	case u.User != nil:
		return errors.New("credentials in the URL are refused: put the secret in an environment variable named by a *_env key")
	}
	return client.CheckServerURL(s, allowInsecure)
}
