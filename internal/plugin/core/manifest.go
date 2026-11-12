package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"github.com/BeppeTemp/cartographer/internal/updatecheck"
	"gopkg.in/yaml.v3"
)

// ManifestFile is the name of the manifest at a plugin's root.
const ManifestFile = "cartographer-plugin.yaml"

const (
	// Schema1 is the client-only manifest 0.28 shipped; it is read unchanged.
	Schema1 = 1
	// SchemaVersion is the highest manifest schema this version reads: schema 2
	// declares component sides and an sdk level per extension-point family.
	SchemaVersion = 2
	// MinSchema2Floor is the lowest binary floor a schema 2 manifest may state.
	MinSchema2Floor = "0.29.0"
	maxSDKLevel     = 99
)

const (
	maxDisplayName = 60
	maxDescription = 300
	maxList        = 16 // net hosts, env names, file globs per list, depends
	maxSettings    = 32
)

var (
	nameRe       = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	envRe        = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	settingKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	hostRe       = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
	placeholder  = regexp.MustCompile(`\{[^{}]*\}`)
	unknownField = regexp.MustCompile(`field (\S+) not found in type \S+`)
)

// filePlaceholders are the anchors a file glob may start with; resolving them
// under the home directory is the caller's, here only the lexical part is checked.
var filePlaceholders = []string{"{home}", "{xdg_config}", "{app_data}", "{local_app_data}", "{documents}"}

// Manifest is cartographer-plugin.yaml, schema 1 or 2. The yaml tags are
// schema 1's; a schema 2 file is decoded into manifestV2 and normalised here.
type Manifest struct {
	Schema       *int               `yaml:"schema"`
	Name         string             `yaml:"name"`
	Version      string             `yaml:"version"`
	DisplayName  string             `yaml:"display_name"`
	Description  string             `yaml:"description"`
	Cartographer string             `yaml:"cartographer"`
	Depends      map[string]string  `yaml:"depends"`
	Permissions  *Permissions       `yaml:"permissions"` // the client side's, in either schema
	Settings     map[string]Setting `yaml:"settings"`

	// Components is the per-side view of both schemas. A schema 1 manifest is a
	// client-only plugin whose sdk levels are the implicit ones.
	Components map[Side]Component `yaml:"-"`
	// UnknownSides are the side names of a schema 2 manifest this build does not
	// know: reported, never an error.
	UnknownSides []string `yaml:"-"`
}

// Component is what a manifest says about one side of the plugin.
type Component struct {
	// SDK maps an extension-point family to the level the plugin is written for.
	// A declared entry is a requirement.
	SDK         map[string]int
	Permissions *Permissions
}

// manifestV2 is the strict shape of a schema 2 file. The sides stay nodes so a
// side this build does not know is kept without being read.
type manifestV2 struct {
	Schema       *int                 `yaml:"schema"`
	Name         string               `yaml:"name"`
	Version      string               `yaml:"version"`
	DisplayName  string               `yaml:"display_name"`
	Description  string               `yaml:"description"`
	Cartographer string               `yaml:"cartographer"`
	Depends      map[string]string    `yaml:"depends"`
	Settings     map[string]Setting   `yaml:"settings"`
	Components   map[string]yaml.Node `yaml:"components"`
}

type sideV2 struct {
	SDK         map[string]int `yaml:"sdk"`
	Permissions *Permissions   `yaml:"permissions"`
}

// Permissions is what a plugin asks for. Here it is validated as data; nothing
// in this package enforces or grants it.
type Permissions struct {
	KB    string   `yaml:"kb"`
	Net   []string `yaml:"net"`
	Files *Files   `yaml:"files"`
	Env   []string `yaml:"env"`
}

// Files holds the path globs a plugin asks to read and to write.
type Files struct {
	Read  []string `yaml:"read"`
	Write []string `yaml:"write"`
}

// Setting declares one user-settable value of the plugin.
type Setting struct {
	Type        string `yaml:"type"`
	Default     any    `yaml:"default"`
	Description string `yaml:"description"`
}

// Options carries what the manifest check needs from its caller, so this
// package imports neither cmd nor the running binary's version.
type Options struct {
	// RunningVersion is the version of this binary, for the `cartographer` range.
	RunningVersion string
	// ReservedName reports a built-in command name a plugin must not take;
	// nil reserves none.
	ReservedName func(string) bool
	// CheckScripts loads the script files of a side's tree (plugin.star and lib/
	// for the client) and
	// returns why they do not, one problem each; nil checks nothing. It is a hook
	// because the Starlark runtime lives in internal/plugin/star and internal/plugin/starcore, which import
	// this package.
	CheckScripts func(side Side, files []TreeFile) []string
	// Side limits what ReadTree loads to the contribution points of one side:
	// a component loads only its own. Empty loads every side (plugin validate,
	// plugin vendor). The digest and the stored files never depend on it.
	Side Side
}

// loads reports whether the points and scripts of a side are loaded.
func (o Options) loads(s Side) bool { return o.Side == "" || o.Side == s }

// ParseManifest reads and checks manifest bytes. The returned problems are every
// problem it can read (a YAML type error stops the semantic checks, which then
// run on the next call), each "path: reason" and sorted, except that an unmet
// `cartographer` range is checked before anything else and is then the only one.
func ParseManifest(data []byte, opts Options) (Manifest, []string) {
	var m Manifest
	if len(data) > contract.ArtifactMaxFileSize {
		return m, []string{fmt.Sprintf("manifest: %d bytes, over the %d cap", len(data), contract.ArtifactMaxFileSize)}
	}
	// Aliases are refused before the strict decode: a custom UnmarshalYAML resets
	// yaml.v3's alias accounting, so one anchored scalar could be repeated at will.
	scan := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := scan.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return m, []string{"manifest: the file is empty"}
		}
		return m, []string{"manifest: " + err.Error()}
	}
	if hasAlias(&root) {
		return m, []string{"manifest: YAML aliases are not allowed"}
	}
	if err := scan.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return m, []string{"manifest: more than one YAML document"}
	}

	// The range first, read without the strict rules: a manifest of a newer
	// schema must say "needs Cartographer >=X", not "unknown key".
	var head struct {
		Cartographer string `yaml:"cartographer"`
	}
	if yaml.Unmarshal(data, &head) == nil && head.Cartographer != "" {
		if met, err := RangeMet(head.Cartographer, opts.RunningVersion); err == nil && !met {
			return m, []string{fmt.Sprintf("cartographer: needs Cartographer %s (this is %s)",
				strings.Join(strings.Fields(head.Cartographer), " "), strings.TrimPrefix(strings.TrimSpace(opts.RunningVersion), "v"))}
		}
	}

	var sh struct {
		Schema *int `yaml:"schema"`
	}
	if yaml.Unmarshal(data, &sh) == nil && sh.Schema != nil && *sh.Schema == SchemaVersion {
		return parseV2(data, opts)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, typeProblems(err)
	}

	var v validator
	v.manifest(&m, opts)
	sort.Strings(v.problems)
	if len(v.problems) == 0 {
		m.Components = map[Side]Component{SideClient: {
			SDK:         map[string]int{"artifacts": 1, "providers": 1, "scripts": 1},
			Permissions: m.Permissions,
		}}
	}
	return m, v.problems
}

// typeProblems turns a strict-decode error into sorted "manifest: ..." problems.
func typeProblems(err error) []string {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return []string{"manifest: " + err.Error()}
	}
	var out []string
	for _, e := range te.Errors {
		if f := unknownField.FindStringSubmatch(e); f != nil {
			e = fmt.Sprintf("unknown key %q", f[1])
		}
		out = append(out, "manifest: "+e)
	}
	sort.Strings(out)
	return out
}

// parseV2 reads a schema 2 manifest: strict, with the sides normalised into
// Components. Its Schema is set even when it has problems, so the tree reader
// knows which schema's rules apply to the rest of the tree.
func parseV2(data []byte, opts Options) (Manifest, []string) {
	two := SchemaVersion
	fail := func(p []string) (Manifest, []string) { return Manifest{Schema: &two}, p }
	var raw manifestV2
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return fail(typeProblems(err))
	}
	m := Manifest{
		Schema: raw.Schema, Name: raw.Name, Version: raw.Version, DisplayName: raw.DisplayName,
		Description: raw.Description, Cartographer: raw.Cartographer, Depends: raw.Depends,
		Settings: raw.Settings, Components: map[Side]Component{},
	}
	var v validator
	var perr []string
	for _, key := range slices.Sorted(maps.Keys(raw.Components)) {
		side := Side(key)
		if !slices.Contains(Sides(), side) {
			m.UnknownSides = append(m.UnknownSides, key)
			continue
		}
		node := raw.Components[key]
		out, err := yaml.Marshal(&node)
		var sv sideV2
		if err == nil {
			d := yaml.NewDecoder(bytes.NewReader(out))
			d.KnownFields(true)
			err = d.Decode(&sv)
		}
		if err != nil {
			for _, e := range typeProblems(err) {
				perr = append(perr, strings.Replace(e, "manifest: ", "components."+key+": ", 1))
			}
			continue
		}
		m.Components[side] = Component{SDK: sv.SDK, Permissions: sv.Permissions}
	}
	if len(perr) > 0 {
		sort.Strings(perr)
		return fail(perr)
	}
	m.Permissions = m.Components[SideClient].Permissions
	v.manifestV2(&m, len(raw.Components), opts)
	sort.Strings(v.problems)
	return m, v.problems
}

func hasAlias(n *yaml.Node) bool {
	return n.Kind == yaml.AliasNode || slices.ContainsFunc(n.Content, hasAlias)
}

type validator struct{ problems []string }

func (v *validator) add(path, format string, a ...any) {
	v.problems = append(v.problems, path+": "+fmt.Sprintf(format, a...))
}

// q quotes a value for a message, cut so a hostile manifest cannot inflate it.
func q(s string) string {
	if len(s) > 48 {
		s = s[:48] + "..."
	}
	return fmt.Sprintf("%q", s)
}

func (v *validator) manifest(m *Manifest, opts Options) {
	switch {
	case m.Schema == nil:
		v.add("schema", "required (%d)", Schema1)
	case *m.Schema != Schema1:
		v.add("schema", "unsupported plugin schema %d", *m.Schema)
	}
	v.common(m, opts)
	if m.Cartographer == "" {
		v.add("cartographer", "required (a range such as >=0.28.0)")
	} else if err := ValidRange(m.Cartographer); err != nil {
		v.add("cartographer", "%v", err)
	}
	if m.Permissions != nil {
		v.permissions("permissions", m.Permissions)
	}
}

// common checks what both schemas hold the same way, plugin-wide.
func (v *validator) common(m *Manifest, opts Options) {
	v.name(m.Name, opts)
	if m.Version == "" {
		v.add("version", "required (X.Y.Z)")
	} else if !versionRe.MatchString(m.Version) {
		v.add("version", "%s is not X.Y.Z (three non-negative integers)", q(m.Version))
	}
	v.text("display_name", m.DisplayName, maxDisplayName, false)
	v.text("description", m.Description, maxDescription, true)
	v.depends(m.Depends)
	v.settings(m.Settings)
}

// manifestV2 checks a schema 2 manifest; sides is the number of keys under
// components, known or not.
func (v *validator) manifestV2(m *Manifest, sides int, opts Options) {
	v.common(m, opts)
	switch {
	case m.Cartographer == "":
		v.add("cartographer", "required (a floor such as >=%s)", MinSchema2Floor)
	case ValidRange(m.Cartographer) != nil:
		v.add("cartographer", "%v", ValidRange(m.Cartographer))
	default:
		// ValidRange passed: one comparator, >=, at least the minimum floor.
		t, _ := parseRange(m.Cartographer)
		if c, ok := updatecheck.Cmp(t[0].ver, MinSchema2Floor); len(t) != 1 || t[0].op != ">=" || !ok || c < 0 {
			v.add("cartographer", "a schema 2 manifest takes a floor, >=%s or later", MinSchema2Floor)
		}
	}
	if sides == 0 {
		v.add("components", "required, with at least one side (%s)", strings.Join(sidesText(), ", "))
	}
	for _, side := range Sides() {
		c, ok := m.Components[side]
		if !ok {
			continue
		}
		base := "components." + string(side)
		if len(c.SDK) > maxList {
			v.add(base+".sdk", "%d entries, at most %d", len(c.SDK), maxList)
		}
		for fam, level := range c.SDK {
			switch {
			case !settingKeyRe.MatchString(fam):
				v.add(base+".sdk", "%s must match %s", q(fam), settingKeyRe)
			case level < 1 || level > maxSDKLevel:
				v.add(base+".sdk."+fam, "%d is not a level from 1 to %d", level, maxSDKLevel)
			}
		}
		if c.Permissions != nil {
			v.sidePermissions(base+".permissions", side, c.Permissions)
		}
	}
}

func sidesText() []string {
	out := make([]string, len(Sides()))
	for i, s := range Sides() {
		out[i] = string(s)
	}
	return out
}

// sidePermissions applies the per-side permission table: the client may ask for
// everything, the Surveyor for net and env, the server and the Atlas for nothing.
func (v *validator) sidePermissions(base string, side Side, p *Permissions) {
	refuse := func(key string, asked bool) {
		if asked {
			v.add(base+"."+key, "not allowed on the %s side", side)
		}
	}
	switch side {
	case SideSurveyor:
		refuse("kb", p.KB != "")
		refuse("files", p.Files != nil)
	case SideServer, SideAtlas:
		refuse("kb", p.KB != "")
		refuse("net", len(p.Net) > 0)
		refuse("env", len(p.Env) > 0)
		refuse("files", p.Files != nil)
		return
	}
	v.permissions(base, p)
}

func (v *validator) name(name string, opts Options) {
	switch {
	case name == "":
		v.add("name", "required")
	case !nameRe.MatchString(name):
		v.add("name", "%s must match %s", q(name), nameRe)
	case !contract.ValidArtifactName(name):
		v.add("name", "%s is not a valid name", q(name))
	case name == "bundle" || name == "kb":
		v.add("name", "%s is reserved: it names a source of artifacts", q(name))
	case isProviderID(name):
		v.add("name", "%s is a built-in client name", q(name))
	case opts.ReservedName != nil && opts.ReservedName(name):
		v.add("name", "%s is a built-in command name", q(name))
	}
}

func isProviderID(name string) bool {
	for _, p := range configurator.ProviderList() {
		if string(p) == name {
			return true
		}
	}
	return false
}

// text checks a one-paragraph, printable string of at most limit runes.
func (v *validator) text(path, s string, limit int, paragraph bool) {
	if strings.TrimSpace(s) == "" {
		v.add(path, "required")
		return
	}
	if !utf8.ValidString(s) {
		v.add(path, "not valid UTF-8")
		return
	}
	body := strings.ReplaceAll(strings.TrimSpace(s), "\r", "")
	if n := utf8.RuneCountInString(body); n > limit {
		v.add(path, "%d characters, at most %d", n, limit)
	}
	for _, r := range body {
		if r == '\n' {
			continue // one-line / one-paragraph is checked below
		}
		if r == ' ' || (unicode.IsPrint(r) && !unicode.IsControl(r)) {
			continue
		}
		v.add(path, "contains a control or non-printable character (U+%04X)", r)
		return
	}
	if strings.Contains(body, "\n\n") {
		v.add(path, "must be one paragraph")
	}
	if !paragraph && strings.ContainsRune(body, '\n') {
		v.add(path, "must be on one line")
	}
}

func (v *validator) depends(d map[string]string) {
	if len(d) > maxList {
		v.add("depends", "%d entries, at most %d", len(d), maxList)
	}
	for name, rng := range d {
		path := "depends." + name
		if !nameRe.MatchString(name) {
			v.add("depends", "%s is not a plugin name", q(name))
			continue
		}
		if err := ValidRange(rng); err != nil {
			v.add(path, "%v", err)
		}
	}
}

func (v *validator) permissions(base string, p *Permissions) {
	switch p.KB {
	case "", "read", "write":
	default:
		v.add(base+".kb", "%s is not read or write", q(p.KB))
	}
	v.list(base+".net", p.Net, hostProblem)
	v.list(base+".env", p.Env, envProblem)
	if p.Files != nil {
		v.list(base+".files.read", p.Files.Read, GlobProblem)
		v.list(base+".files.write", p.Files.Write, GlobProblem)
	}
}

// list checks a bounded list whose entries each pass check (which returns the
// reason or "").
func (v *validator) list(path string, items []string, check func(string) string) {
	if len(items) > maxList {
		v.add(path, "%d entries, at most %d", len(items), maxList)
	}
	for i, it := range items {
		if why := check(it); why != "" {
			v.add(fmt.Sprintf("%s[%d]", path, i), "%s %s", q(it), why)
		}
	}
}

// GlobProblem is the lexical part of a file permission: it starts with one
// known placeholder, stays in slash form and never climbs with "..". Resolving
// the placeholder under the home directory is the permission enforcer's.
func GlobProblem(g string) string {
	if g == "" || len(g) > 200 {
		return "must be 1 to 200 characters"
	}
	for _, r := range g {
		if unicode.IsControl(r) || r == '\\' {
			return "has a control character or a backslash: use / separators"
		}
	}
	if strings.Contains(g, "..") {
		return "must not contain .."
	}
	anchor := ""
	for _, p := range filePlaceholders {
		if g == p || strings.HasPrefix(g, p+"/") {
			anchor = p
			break
		}
	}
	if anchor == "" {
		return "must start with one of " + strings.Join(filePlaceholders, ", ") + " (no absolute or relative path)"
	}
	rest := g[len(anchor):]
	if placeholder.MatchString(rest) || strings.ContainsAny(rest, "{}") {
		return "has a placeholder or brace after the first segment"
	}
	if rest == "" && anchor != "{documents}" {
		return "names a whole base directory, which holds Cartographer's own state: name a path below it"
	}
	if rest != "" {
		for _, seg := range strings.Split(rest[1:], "/") {
			if strings.TrimRight(seg, " .") == "" {
				return "has an empty or . segment (a trailing / or //, or a segment of dots or spaces): name a path below the base directory"
			}
			if strings.ContainsAny(seg, "~:") {
				return "has a ~ or : in a segment: Windows short names and streams can alias Cartographer's own state"
			}
		}
	}
	if reachesOwnState(anchor, rest) {
		return "reaches Cartographer's own state (its config file, config, state and cache directories, the plugin store)"
	}
	return ""
}

func (v *validator) settings(s map[string]Setting) {
	if len(s) > maxSettings {
		v.add("settings", "%d keys, at most %d", len(s), maxSettings)
	}
	for key, st := range s {
		path := "settings." + key
		if !settingKeyRe.MatchString(key) {
			v.add("settings", "%s must match %s", q(key), settingKeyRe)
			continue
		}
		if strings.TrimSpace(st.Description) == "" {
			v.add(path+".description", "required")
		} else if n := utf8.RuneCountInString(st.Description); n > maxDescription {
			v.add(path+".description", "%d characters, at most %d", n, maxDescription)
		}
		switch st.Type {
		case "string", "bool", "int":
			if st.Default != nil && !defaultMatches(st.Type, st.Default) {
				v.add(path+".default", "is not a %s", st.Type)
			}
		case "":
			v.add(path+".type", "required (string, bool or int)")
		default:
			v.add(path+".type", "%s is not string, bool or int", q(st.Type))
		}
	}
}

func defaultMatches(typ string, d any) bool {
	switch typ {
	case "string":
		_, ok := d.(string)
		return ok
	case "bool":
		_, ok := d.(bool)
		return ok
	default:
		switch d.(type) {
		case int, int64, uint64:
			return true
		}
		return false
	}
}

const hostHint = "is not a lowercase host name (an exact host of at least two labels or *.suffix of at least two labels, no scheme, port or path)"

// hostProblem checks a net permission. A wildcard needs two labels after "*."
// ("*.com" would name a whole TLD); a single-label host ("localhost") and an
// address in any spelling are refused, because the approval shows a name and a
// name that is an address says nothing about who is reached. A public suffix
// such as *.github.io is accepted: the user approves each permission.
func hostProblem(h string) string {
	if len(h) > 253 {
		return "longer than 253 characters"
	}
	name := strings.TrimPrefix(h, "*.")
	if !hostRe.MatchString(name) || !strings.Contains(name, ".") {
		return hostHint
	}
	// No top-level domain starts with a digit, so a last label that does is an
	// IPv4 address in some spelling: dotted decimal, a decimal such as
	// 2130706433, or hex or octal parts such as 0x7f.0x1 or 127.0x1 (a resolver
	// reads all of them as an address). hostRe is lowercase, so "0x..." is
	// covered by the digit test too.
	last := name[strings.LastIndexByte(name, '.')+1:]
	if last[0] >= '0' && last[0] <= '9' {
		return "is an IP address, not a host name"
	}
	return ""
}

// envReservedPrefixes and envReservedNames cannot be passed through to a
// plugin: Cartographer's own settings, and the variables a loader or a shell
// resolves programs and files by.
var (
	envReservedPrefixes = []string{"CARTOGRAPHER_", "LD_", "DYLD_", "XDG_"}
	envReservedNames    = []string{
		"PATH", "HOME", "TMPDIR", "TEMP", "TMP",
		"PATHEXT", "COMSPEC", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "SYSTEMROOT",
	}
)

func envProblem(e string) string {
	if !envRe.MatchString(e) {
		return "must match " + envRe.String()
	}
	for _, pre := range envReservedPrefixes {
		if strings.HasPrefix(e, pre) {
			return "cannot be passed through: the " + pre + "* variables are reserved"
		}
	}
	if slices.Contains(envReservedNames, e) {
		return "cannot be passed through: it is reserved"
	}
	return ""
}

// ownStateNames are the path segments that are Cartographer's own state: the
// client config file and the config, state and cache directories (the plugin
// store lives in the cache one). A literal one is refused under any anchor and
// at any depth, on any platform spelling.
var ownStateNames = []string{".cartographer.yaml", "cartographer"}

// ownStateLocations are the places of Cartographer's own state, per anchor,
// lowercased and split in segments. A wildcard segment is refused only where
// it can still lead into one of them; below a client's own folder it is not.
// A custom XDG_CONFIG_HOME under an arbitrary folder is not visible to this
// lexical rule: the runtime enforcer's state directories cover it.
var ownStateLocations = map[string][][]string{
	"home": {
		{"cartographer-data"}, {".config", "cartographer"}, {".cache", "cartographer"},
		{".local", "share", "cartographer"}, {"library", "application support", "cartographer"},
		{"library", "caches", "cartographer"}, {"appdata", "roaming", "cartographer"},
		{"appdata", "local", "cartographer"}, {".cartographer"}, {".cartographer.yaml"},
	},
	"xdg_config":     {{"cartographer"}},
	"app_data":       {{"cartographer"}},
	"local_app_data": {{"cartographer"}},
}

// reachesOwnState reports whether a glob below the anchor ("home", "xdg_config",
// ... , with or without braces) can name Cartographer's own state: literally,
// as a ".cartographer*" dotfile or a "cartographer" segment at any depth, or
// through a wildcard segment that, with the segments before it, matches the
// start of a known location of it (ownStateLocations). The runtime enforcer is
// the real control (star/files.go); this is the early answer of the preview.
func reachesOwnState(anchor, rest string) bool {
	segs := strings.Split(strings.ToLower(strings.Trim(rest, "/")), "/")
	locs := ownStateLocations[strings.Trim(anchor, "{}")]
	for i := range segs {
		// Windows drops trailing dots and spaces from a name: "cartographer. "
		// is the "cartographer" directory.
		segs[i] = strings.TrimRight(segs[i], " .")
	}
	for i, seg := range segs {
		if strings.HasPrefix(seg, ".cartographer") {
			return true
		}
		if !strings.ContainsAny(seg, "*?[") {
			if slices.Contains(ownStateNames, seg) {
				return true
			}
			continue
		}
	loc:
		for _, l := range locs {
			// A wildcard below a location its preceding segments fully match
			// (i >= len(l)) is inside that location: it reaches state too.
			for j := 0; j < min(i, len(l)); j++ {
				if ok, _ := path.Match(segs[j], l[j]); !ok {
					continue loc
				}
			}
			if i >= len(l) {
				return true
			}
			if ok, _ := path.Match(seg, l[i]); ok {
				return true
			}
		}
	}
	return false
}
