package mcpserver

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/atlasext"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// publicRoutes answers atlasext.RouteSet over the route table: a path is a
// public GET when it is under APIPrefix and names a stable or preview route
// that is not an event stream. The UI API (another prefix), /mcp, an internal
// route and the stream all answer false, so a view can only present what the
// public API already shows its caller.
type publicRoutes struct{}

// IsPublicGET implements atlasext.RouteSet.
func (publicRoutes) IsPublicGET(path string) bool {
	rest, ok := strings.CutPrefix(path, APIPrefix+"/")
	if !ok || rest == "" || strings.HasSuffix(rest, "/") || strings.ContainsAny(rest, "\\?#%{}") {
		return false
	}
	segments := strings.Split(rest, "/")
	for _, seg := range segments {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsFunc(seg, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
			return false
		}
	}
	for i := range apiRoutes {
		rt := &apiRoutes[i]
		if rt.Tier&apiPublicTiers != 0 && len(rt.Events) == 0 && rt.match(segments) {
			return true
		}
	}
	return false
}

// ValidateAtlasPlugins refuses a view whose route is not a public GET. The
// server calls it at start, before anything is served, so a bad declaration
// stops the start naming the plugin (fail closed, like a bad pin).
func ValidateAtlasPlugins(plugins []*core.Plugin) error {
	var problems []string
	for _, p := range plugins {
		for _, c := range p.Contributions["views"] {
			if c.View == nil {
				continue
			}
			file := fmt.Sprintf("plugins.%s: views/%s.yaml", p.Manifest.Name, c.Name)
			problems = append(problems, atlasext.Validate(file, *c.View, publicRoutes{})...)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("atlas views: %s", strings.Join(problems, "; "))
	}
	return nil
}

type uiExtSource struct {
	Route string            `json:"route"`
	Query map[string]string `json:"query,omitempty"`
	Items string            `json:"items,omitempty"`
}

type uiExtColumn struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Format string `json:"format"`
}

type uiExtView struct {
	Plugin      string        `json:"plugin"`
	Name        string        `json:"name"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Icon        string        `json:"icon"`
	Scope       string        `json:"scope"`
	Source      uiExtSource   `json:"source"`
	Renderer    string        `json:"renderer"`
	Columns     []uiExtColumn `json:"columns"`
	Limit       int           `json:"limit"`
}

type uiExtApp struct {
	Plugin      string `json:"plugin"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	URL         string `json:"url"`
}

type uiExtComponent struct {
	Component string `json:"component"`
	// Machine names the Compass that reported the plugin; "" for the others.
	Machine string `json:"machine,omitempty"`
	State   string `json:"state"`
	Digest  string `json:"digest"`
	// Version is the version this component holds (it may differ per machine).
	Version string `json:"version,omitempty"`
	// Installed and Approved are the full digests of the installed and the
	// approved tree, for the detail sheet; Digest stays the one the list shows.
	Installed string `json:"installed,omitempty"`
	Approved  string `json:"approved,omitempty"`
	// Source is the redacted source (Compass reports it; Meridian's pin has none).
	Source string `json:"source,omitempty"`
	// Contributions counts what the plugin adds here per extension point;
	// Permissions names the permission kinds its manifest declares (never values).
	Contributions map[string]int `json:"contributions,omitempty"`
	Permissions   []string       `json:"permissions,omitempty"`
}

type uiExtPlugin struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	DisplayName string `json:"display_name"`
	// Description is the manifest's one-line description.
	Description string           `json:"description,omitempty"`
	Components  []uiExtComponent `json:"components"`
}

// uiExtensionsBody is the 200 body of GET /extensions.
type uiExtensionsBody struct {
	Views   []uiExtView   `json:"views"`
	Apps    []uiExtApp    `json:"apps"`
	Plugins []uiExtPlugin `json:"plugins"`
	// Reported names the components whose plugins the list holds: "meridian"
	// always, "compass" when a client report of the caller carried its plugins
	// (an older client omits them: its plugins are unknown, not none).
	Reported    []string `json:"reported"`
	GeneratedAt string   `json:"generated_at"`
}

// extensionSet is what the pinned plugins give the Atlas, built once at start:
// the pin is the approval, so a plugin that does not load never gets here.
type extensionSet struct {
	views   []uiExtView
	apps    []uiExtApp
	plugins []uiExtPlugin
}

// SetAtlasPlugins hands the server the plugins whose atlas side the operator
// pinned. Call it before serving; it refuses a view that is not over a public
// GET route (ValidateAtlasPlugins).
func (m *MultiKBServer) SetAtlasPlugins(plugins []*core.Plugin) error {
	if err := ValidateAtlasPlugins(plugins); err != nil {
		return err
	}
	sorted := append([]*core.Plugin(nil), plugins...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Manifest.Name < sorted[j].Manifest.Name })
	set := extensionSet{views: []uiExtView{}, apps: []uiExtApp{}, plugins: []uiExtPlugin{}}
	for _, p := range sorted {
		name := p.Manifest.Name
		// Only the Meridian's own pins are known here; Compass's plugins are
		// merged per request from the caller's client reports.
		set.plugins = append(set.plugins, uiExtPlugin{Name: name, Version: p.Manifest.Version, DisplayName: p.Manifest.DisplayName,
			Description: p.Manifest.Description,
			Components: []uiExtComponent{{Component: "meridian", State: "approved", Digest: p.Digest, Version: p.Manifest.Version,
				Installed: p.Digest, Approved: p.Digest, Contributions: PluginContributionCounts(*p),
				Permissions: PluginPermissionNames(p.Manifest)}}})
		for _, c := range p.Contributions["views"] {
			if c.View == nil {
				continue
			}
			v := c.View
			cols := make([]uiExtColumn, 0, len(v.Columns))
			for _, col := range v.Columns {
				cols = append(cols, uiExtColumn{Key: col.Key, Label: col.Label, Format: col.Format})
			}
			set.views = append(set.views, uiExtView{Plugin: name, Name: c.Name, Title: v.Title, Description: v.Description,
				Icon: v.Icon, Scope: v.Scope, Source: uiExtSource{Route: v.Source.Route, Query: v.Source.Query, Items: v.Source.Items},
				Renderer: v.Renderer, Columns: cols, Limit: v.Limit})
		}
		for _, c := range p.Contributions["apps"] {
			if c.App == nil {
				continue
			}
			set.apps = append(set.apps, uiExtApp{Plugin: name, Name: c.Name, Title: c.App.Title, Description: c.App.Description,
				Icon: c.App.Icon, URL: c.App.URL})
		}
	}
	less := func(a1, a2, b1, b2 string) bool { return a1 < b1 || (a1 == b1 && a2 < b2) }
	sort.SliceStable(set.views, func(i, j int) bool {
		return less(set.views[i].Plugin, set.views[i].Name, set.views[j].Plugin, set.views[j].Name)
	})
	sort.SliceStable(set.apps, func(i, j int) bool {
		return less(set.apps[i].Plugin, set.apps[i].Name, set.apps[j].Plugin, set.apps[j].Name)
	})
	m.extensions = set
	return nil
}

// PluginContributionCounts is what a plugin adds, per extension point: the
// numbers `plugin list` prints, nothing of the contributions themselves.
func PluginContributionCounts(p core.Plugin) map[string]int {
	out := map[string]int{}
	for pt, cs := range p.Contributions {
		if len(cs) > 0 {
			out[pt] = len(cs)
		}
	}
	return out
}

// PluginPermissionNames lists the permission kinds a manifest declares (kb,
// net, files, env, net_private), in that order, over its top level and its
// components: names only, never a host, a path or a variable.
func PluginPermissionNames(m core.Manifest) []string {
	perms := []*core.Permissions{m.Permissions}
	for _, c := range m.Components {
		perms = append(perms, c.Permissions)
	}
	has := map[string]bool{}
	for _, p := range perms {
		if p == nil {
			continue
		}
		has["kb"] = has["kb"] || p.KB != ""
		has["net"] = has["net"] || len(p.Net) > 0
		has["net_private"] = has["net_private"] || len(p.NetPrivate) > 0
		has["files"] = has["files"] || (p.Files != nil && (len(p.Files.Read) > 0 || len(p.Files.Write) > 0))
		has["env"] = has["env"] || len(p.Env) > 0
	}
	out := []string{}
	for _, k := range []string{"kb", "net", "net_private", "files", "env"} {
		if has[k] {
			out = append(out, k)
		}
	}
	return out
}

// mergeCompassPlugins adds, to a copy of Meridian's plugin rows, one `compass`
// component per plugin each report carries (D2079): on
// the row of the same name, or on a row of its own when Meridian does not pin
// it. The second result says whether any report carried the field.
func mergeCompassPlugins(base []uiExtPlugin, reports []clientReport) ([]uiExtPlugin, bool) {
	out := make([]uiExtPlugin, len(base))
	for i, p := range base {
		p.Components = append([]uiExtComponent(nil), p.Components...)
		out[i] = p
	}
	reported := false
	for _, rep := range reports {
		if rep.Plugins == nil {
			continue
		}
		reported = true
		for _, cp := range rep.Plugins {
			digest := cp.Installed
			if digest == "" {
				digest = cp.Approved
			}
			comp := uiExtComponent{Component: "compass", Machine: rep.Machine, State: cp.State, Digest: digest, Version: cp.Version,
				Installed: cp.Installed, Approved: cp.Approved, Source: cp.Source, Contributions: cp.Contributions, Permissions: cp.Permissions}
			idx := -1
			for i := range out {
				if out[i].Name == cp.Name {
					idx = i
					break
				}
			}
			if idx < 0 {
				out = append(out, uiExtPlugin{Name: cp.Name, Version: cp.Version, DisplayName: cp.DisplayName, Description: cp.Description})
				idx = len(out) - 1
			} else if out[idx].Description == "" {
				out[idx].Description = cp.Description
			}
			out[idx].Components = append(out[idx].Components, comp)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	for i := range out {
		if out[i].Components == nil {
			out[i].Components = []uiExtComponent{}
		}
	}
	return out, reported
}

// GET /extensions: the approved views and apps the Atlas draws, and the
// installed plugins. Views and apps are not KB data (the public API filters a
// view's data with the caller's principal); the plugin list is the operator's.
// The answer carries no path, environment or permission value.
func (m *MultiKBServer) uiExtensions(w http.ResponseWriter, r *http.Request) {
	set := m.extensions
	plugins := []uiExtPlugin{}
	reported := []string{"meridian"}
	if p := auth.PrincipalFromContext(r.Context()); p.Policy.Admin {
		var compass bool
		plugins, compass = mergeCompassPlugins(set.plugins, m.clientReports.of(p.ID))
		if compass {
			reported = append(reported, "compass")
		}
	}
	views, apps := set.views, set.apps
	if views == nil {
		views = []uiExtView{}
	}
	if apps == nil {
		apps = []uiExtApp{}
	}
	writeUIJSON(w, http.StatusOK, uiExtensionsBody{Views: views, Apps: apps, Plugins: plugins,
		Reported: reported, GeneratedAt: time.Now().UTC().Format(time.RFC3339)})
}
