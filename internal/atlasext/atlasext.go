package atlasext

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"gopkg.in/yaml.v3"
)

func init() {
	core.Register(core.Point{Dir: "views", Side: core.SideAtlas, Load: loadViews, Preview: previewView})
	core.Register(core.Point{Dir: "apps", Side: core.SideAtlas, Load: loadApps, Preview: previewApp})
}

// RouteSet says which paths are documented public operations. The server
// implements it over the public route table; atlasext invents no prefix.
type RouteSet interface {
	IsPublicGET(path string) bool
}

const (
	// MaxFileBytes caps one declaration file.
	MaxFileBytes = 8 << 10

	maxTitleView, maxTitleApp = 60, 40
	maxDescription            = 200
	maxQueryValue             = 200
	maxLabel                  = 40
	maxColumns                = 8
	maxQuery                  = 8
	defaultLimit, maxLimit    = 100, 200
	defaultViewIcon           = "list"
	defaultAppIcon            = "paths"
	sampleKB                  = "kb-a"
	dataOnly                  = "data only: nothing runs"
)

// Icons are the Atlas icons a declaration may name; TestIconsExistInTheAtlas
// keeps them equal to web/src/components/Icon.tsx.
var Icons = []string{"list", "sources", "services", "artifacts", "key", "paths", "eye", "lock", "alert", "person"}

var (
	nameRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	dottedRe   = regexp.MustCompile(`^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+){0,4}$`)
	queryKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)
	routeRe    = regexp.MustCompile(`^/[A-Za-z0-9_.{}/-]*$`)
	unknownRe  = regexp.MustCompile(`field (\S+) not found in type \S+`)
	lineRe     = regexp.MustCompile(`^line (\d+): (.*)$`)

	renderers = []string{"table", "list", "stats", "keyvalue"}
	formats   = []string{"text", "number", "date", "relative", "badge", "concept"}
)

type viewFile struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Icon        string `yaml:"icon"`
	Scope       string `yaml:"scope"`
	Source      struct {
		Route string            `yaml:"route"`
		Query map[string]string `yaml:"query"`
		Items string            `yaml:"items"`
	} `yaml:"source"`
	Renderer string `yaml:"renderer"`
	Columns  []struct {
		Key    string `yaml:"key"`
		Label  string `yaml:"label"`
		Format string `yaml:"format"`
	} `yaml:"columns"`
	Limit *int `yaml:"limit"`
}

type appFile struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Icon        string `yaml:"icon"`
	URL         string `yaml:"url"`
}

// decode reads one strict YAML document: an unknown key, an alias, a second
// document or more than MaxFileBytes is a problem.
func decode(data []byte, into any) []string {
	if len(data) > MaxFileBytes {
		return []string{fmt.Sprintf("%d bytes, over the %d cap", len(data), MaxFileBytes)}
	}
	scan := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := scan.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return []string{"the file is empty"}
		}
		return []string{err.Error()}
	}
	if hasAlias(&root) {
		return []string{"YAML aliases are not allowed"}
	}
	if err := scan.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return []string{"more than one YAML document"}
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(into); err != nil {
		var te *yaml.TypeError
		if !errors.As(err, &te) {
			return []string{err.Error()}
		}
		var root yaml.Node
		_ = yaml.Unmarshal(data, &root)
		at := map[int]string{}
		pathsByLine(&root, "", at)
		var out []string
		for _, e := range te.Errors {
			line, reason := 0, e
			if m := lineRe.FindStringSubmatch(e); m != nil {
				line, _ = strconv.Atoi(m[1])
				reason = m[2]
			}
			if f := unknownRe.FindStringSubmatch(reason); f != nil {
				reason = fmt.Sprintf("unknown key %q", f[1])
			} else if k := at[line]; k != "" {
				reason = k + ": " + reason
			}
			out = append(out, reason)
		}
		return out
	}
	return nil
}

// pathsByLine maps the line of each mapping value or sequence item to its key
// path (columns[0].key), so a type error, which yaml.v3 reports by line, can be
// written as "key: reason".
func pathsByLine(n *yaml.Node, prefix string, out map[int]string) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			pathsByLine(c, prefix, out)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			if prefix != "" {
				k = prefix + "." + k
			}
			// Deepest first: a nested value on a line of its own keeps its own path.
			pathsByLine(n.Content[i+1], k, out)
			for _, l := range []int{n.Content[i].Line, n.Content[i+1].Line} {
				if _, ok := out[l]; !ok {
					out[l] = k
				}
			}
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			k := fmt.Sprintf("%s[%d]", prefix, i)
			pathsByLine(c, k, out)
			if _, ok := out[c.Line]; !ok {
				out[c.Line] = k
			}
		}
	}
}

func hasAlias(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode {
		return true
	}
	for _, c := range n.Content {
		if hasAlias(c) {
			return true
		}
	}
	return false
}

// line reports why s is not one printable line of 1..limit characters.
func line(s string, limit int, required bool) string {
	switch {
	case s == "" && required:
		return "required"
	case utf8.RuneCountInString(s) > limit:
		return fmt.Sprintf("at most %d characters", limit)
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return "one line of printable characters"
		}
	}
	return ""
}

func oneOf(s string, set []string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

// entries splits a point's files into name -> file, reporting what is not a
// declaration file. The result is sorted by name.
func entries(files []core.TreeFile, dir string) ([]string, map[string]core.TreeFile, []string) {
	var problems []string
	by := map[string]core.TreeFile{}
	for _, f := range files {
		stem, ok := strings.CutSuffix(strings.TrimPrefix(f.Path, dir+"/"), ".yaml")
		switch {
		case !ok || strings.Contains(stem, "/") || !nameRe.MatchString(stem):
			problems = append(problems, fmt.Sprintf("%s: expected a file %s/<name>.yaml, <name> matching %s", f.Path, dir, nameRe))
		case f.Executable:
			problems = append(problems, f.Path+": the atlas side holds data, not an executable file")
		default:
			by[stem] = f
		}
	}
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, by, problems
}

// loadViews judges the shape of each view. Whether source.route is a
// documented public GET operation is Validate's, which the server calls when
// the UI API serves views (the change after this one); load does not know the
// public route table.
func loadViews(files []core.TreeFile) ([]core.Contribution, []string) {
	names, by, problems := entries(files, "views")
	var out []core.Contribution
	titles := map[string]string{}
	for _, name := range names {
		f := by[name]
		v, probs := ParseView(f.Content)
		if t := v.Title; len(probs) == 0 {
			if prev, dup := titles[t]; dup {
				probs = append(probs, fmt.Sprintf("title: %q is already the title of views/%s.yaml", t, prev))
			}
			titles[t] = name
		}
		for _, p := range probs {
			problems = append(problems, f.Path+": "+p)
		}
		if len(probs) == 0 {
			out = append(out, core.Contribution{Point: "views", Kind: "view", Name: name, Description: v.Description, View: &v,
				Files: artifactFiles(f)})
		}
	}
	sort.Strings(problems)
	return out, problems
}

func loadApps(files []core.TreeFile) ([]core.Contribution, []string) {
	names, by, problems := entries(files, "apps")
	var out []core.Contribution
	titles := map[string]string{}
	for _, name := range names {
		f := by[name]
		a, probs := ParseApp(f.Content)
		if t := a.Title; len(probs) == 0 {
			if prev, dup := titles[t]; dup {
				probs = append(probs, fmt.Sprintf("title: %q is already the title of apps/%s.yaml", t, prev))
			}
			titles[t] = name
		}
		for _, p := range probs {
			problems = append(problems, f.Path+": "+p)
		}
		if len(probs) == 0 {
			out = append(out, core.Contribution{Point: "apps", Kind: "app", Name: name, Description: a.Description, App: &a,
				Files: artifactFiles(f)})
		}
	}
	sort.Strings(problems)
	return out, problems
}

// ParseView reads and judges one view file. Every problem is "key: reason" and
// the list is sorted; whether the route is a public operation is Validate's.
func ParseView(data []byte) (core.View, []string) {
	var raw viewFile
	if probs := decode(data, &raw); probs != nil {
		sort.Strings(probs)
		return core.View{}, probs
	}
	var probs []string
	bad := func(key, format string, a ...any) { probs = append(probs, key+": "+fmt.Sprintf(format, a...)) }
	if m := line(raw.Title, maxTitleView, true); m != "" {
		bad("title", "%s", m)
	}
	if m := line(raw.Description, maxDescription, false); m != "" {
		bad("description", "%s", m)
	}
	v := core.View{Title: raw.Title, Description: raw.Description, Icon: raw.Icon, Scope: raw.Scope, Renderer: raw.Renderer, Limit: defaultLimit}
	if v.Icon == "" {
		v.Icon = defaultViewIcon
	} else if !oneOf(v.Icon, Icons) {
		bad("icon", "%q is not one of %s", v.Icon, strings.Join(Icons, ", "))
	}
	switch v.Scope {
	case "":
		v.Scope = "kb"
	case "kb", "global":
	default:
		bad("scope", "%q is not kb or global", v.Scope)
	}
	if raw.Limit != nil {
		if *raw.Limit < 1 || *raw.Limit > maxLimit {
			bad("limit", "1 to %d", maxLimit)
		} else {
			v.Limit = *raw.Limit
		}
	}
	if m := routeProblem(raw.Source.Route); m != "" {
		bad("source.route", "%s", m)
	}
	v.Source = core.ViewSource{Route: raw.Source.Route, Query: raw.Source.Query, Items: raw.Source.Items}
	if len(raw.Source.Query) > maxQuery {
		bad("source.query", "at most %d keys", maxQuery)
	}
	for k, val := range raw.Source.Query {
		if !queryKeyRe.MatchString(k) {
			bad("source.query", "key %q must match %s", k, queryKeyRe)
		}
		if m := line(val, maxQueryValue, false); m != "" || strings.Contains(strings.ReplaceAll(val, "{kb}", ""), "{") {
			bad("source.query."+k, "a value of one printable line; {kb} is the only placeholder")
		}
	}
	if raw.Source.Items != "" && !dottedRe.MatchString(raw.Source.Items) {
		bad("source.items", "a dotted path of at most 5 segments of [A-Za-z0-9_]")
	}
	if !oneOf(v.Renderer, renderers) {
		bad("renderer", "%q is not one of %s", v.Renderer, strings.Join(renderers, ", "))
	}
	most := maxColumns
	if v.Renderer == "list" {
		most = 1
	}
	if len(raw.Columns) < 1 || len(raw.Columns) > most {
		bad("columns", "%d to %d entries for a %s", 1, most, v.Renderer)
	}
	for i, c := range raw.Columns {
		at := fmt.Sprintf("columns[%d]", i)
		if !dottedRe.MatchString(c.Key) {
			bad(at+".key", "%q is not a dotted path of at most 5 segments of [A-Za-z0-9_]", c.Key)
		}
		if m := line(c.Label, maxLabel, true); m != "" {
			bad(at+".label", "%s", m)
		}
		f := c.Format
		if f == "" {
			f = "text"
		} else if !oneOf(f, formats) {
			bad(at+".format", "%q is not one of %s", f, strings.Join(formats, ", "))
		}
		v.Columns = append(v.Columns, core.ViewColumn{Key: c.Key, Label: c.Label, Format: f})
	}
	sort.Strings(probs)
	if len(probs) > 0 {
		return core.View{Title: raw.Title}, probs
	}
	return v, nil
}

// routeProblem judges the shape of a route template: a path, with {kb} as the
// only placeholder. It does not know the public API: Validate asks that.
func routeProblem(r string) string {
	switch {
	case r == "":
		return "required"
	case strings.Contains(r, "?") || strings.Contains(r, "#"):
		return "a path only: the query goes under source.query"
	case !routeRe.MatchString(r):
		return "a path starting with / and no scheme or host"
	case strings.Contains(r, "//"):
		return "no empty segment"
	}
	for _, seg := range strings.Split(r, "/") {
		if seg == ".." || seg == "." {
			return "no . or .. segment"
		}
	}
	if strings.Contains(strings.ReplaceAll(r, "{kb}", ""), "{") || strings.Contains(strings.ReplaceAll(r, "{kb}", ""), "}") {
		return "{kb} is the only placeholder"
	}
	return ""
}

// ParseApp reads and judges one app file.
func ParseApp(data []byte) (core.App, []string) {
	var raw appFile
	if probs := decode(data, &raw); probs != nil {
		sort.Strings(probs)
		return core.App{}, probs
	}
	var probs []string
	bad := func(key, format string, a ...any) { probs = append(probs, key+": "+fmt.Sprintf(format, a...)) }
	if m := line(raw.Title, maxTitleApp, true); m != "" {
		bad("title", "%s", m)
	}
	if m := line(raw.Description, maxDescription, false); m != "" {
		bad("description", "%s", m)
	}
	a := core.App{Title: raw.Title, Description: raw.Description, Icon: raw.Icon, URL: raw.URL}
	if a.Icon == "" {
		a.Icon = defaultAppIcon
	} else if !oneOf(a.Icon, Icons) {
		bad("icon", "%q is not one of %s", a.Icon, strings.Join(Icons, ", "))
	}
	if m := appURLProblem(raw.URL); m != "" {
		bad("url", "%s", m)
	}
	sort.Strings(probs)
	if len(probs) > 0 {
		return core.App{Title: raw.Title}, probs
	}
	return a, nil
}

func appURLProblem(s string) string {
	if s == "" {
		return "required"
	}
	if len(s) > 2048 || strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) {
		return "one printable URL of at most 2048 bytes"
	}
	u, err := url.Parse(s)
	switch {
	case err != nil:
		return "not a URL"
	case strings.Contains(s, "#") || u.Fragment != "":
		return "no fragment"
	case u.User != nil:
		return "no user information"
	case u.Scheme != "https" && u.Scheme != "http":
		return "the scheme must be https"
	case u.Hostname() == "":
		return "an absolute URL with a host"
	case u.Scheme == "https":
		return ""
	case u.Scheme == "http":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return ""
		}
	}
	return "http only for a loopback host; use https"
}

// Validate checks a parsed view against the public route table. file is the
// declaration's path, put in front of each problem. {kb} is replaced by a
// sample before the check, so a template and its expansion are one route.
func Validate(file string, v core.View, routes RouteSet) []string {
	if routes == nil || !routes.IsPublicGET(strings.ReplaceAll(v.Source.Route, "{kb}", sampleKB)) {
		return []string{file + ": source.route: " + fmt.Sprintf("%q is not a documented public read-only operation", v.Source.Route)}
	}
	return nil
}

func artifactFiles(f core.TreeFile) []contract.ArtifactFile {
	return []contract.ArtifactFile{{Path: path.Base(f.Path), Content: f.Content}}
}

func previewView(w io.Writer, c core.Contribution) {
	fmt.Fprintf(w, "view %q (%s) reads GET %s\n%s\n", c.Name, c.View.Renderer, c.View.Source.Route, dataOnly)
}

func previewApp(w io.Writer, c core.Contribution) {
	fmt.Fprintf(w, "app %q opens %s\n%s\n", c.Name, c.App.URL, dataOnly)
}
