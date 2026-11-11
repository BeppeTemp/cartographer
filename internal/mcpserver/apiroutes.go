package mcpserver

import (
	"net/http"
	"strings"
)

// apiTier says how stable a route is promised to be.
type apiTier int

const (
	// tierStable: a breaking change is a new API version.
	tierStable apiTier = 1 << iota
	// tierPreview: may change in a minor release.
	tierPreview
	// tierInternal: the Atlas's own, never offered to another client.
	tierInternal
)

// tierAll is what the Atlas mount serves.
const tierAll = tierStable | tierPreview | tierInternal

func (t apiTier) String() string {
	switch t {
	case tierStable:
		return "stable"
	case tierPreview:
		return "preview"
	case tierInternal:
		return "internal"
	}
	return "unknown"
}

// apiHandler serves one route. kbName and srv are empty/nil for a route that is
// not KB-scoped; for a KB-scoped one the KB is resolved, exists and is visible
// to the caller by the time the handler runs.
type apiHandler func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, kbName string, srv *Server)

// apiRoute is one read route of the UI API: one definition, with its tier.
// A new read route is added here, never by a `case` in the dispatcher.
type apiRoute struct {
	// Pattern is relative to the mount prefix; `{kb}` is the one variable and
	// the second segment of a KB-scoped route.
	Pattern string
	Tier    apiTier
	Summary string
	Handler apiHandler

	segments []string
}

func (rt apiRoute) kbScoped() bool {
	return len(rt.segments) >= 2 && rt.segments[1] == "{kb}"
}

// match reports whether the path segments are the route's. Literal segments
// are compared exactly; `{kb}` matches any non-empty segment.
func (rt apiRoute) match(segments []string) bool {
	if len(segments) != len(rt.segments) {
		return false
	}
	for i, want := range rt.segments {
		if want == "{kb}" {
			if segments[i] == "" {
				return false
			}
			continue
		}
		if segments[i] != want {
			return false
		}
	}
	return true
}

// kbRoute adapts a handler that needs only the resolved server.
func kbRoute(fn func(w http.ResponseWriter, r *http.Request, srv *Server)) apiHandler {
	return func(_ *MultiKBServer, w http.ResponseWriter, r *http.Request, _ string, srv *Server) {
		fn(w, r, srv)
	}
}

// apiRoutes is the table. Tiers: stable = what an external dashboard is
// promised; preview = readable by one but may change in a minor release;
// internal = the Atlas's own (settings, secrets, artifacts, operator panels).
var apiRoutes = buildAPIRoutes([]apiRoute{
	{Pattern: "/kbs", Tier: tierStable, Summary: "List the KBs the caller can see",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, _ string, _ *Server) { m.uiListKBs(w, r) }},
	{Pattern: "/whoami", Tier: tierStable, Summary: "Describe the caller's identity and permissions",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, _ string, _ *Server) { m.uiWhoami(w, r) }},
	{Pattern: "/settings", Tier: tierInternal, Summary: "Read the server settings the Atlas edits",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, _ string, _ *Server) {
			m.uiSettings(w, r)
		}},
	{Pattern: "/releases", Tier: tierInternal, Summary: "Read the release notes the Atlas shows",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, _ string, _ *Server) {
			m.uiReleases(w, r)
		}},

	{Pattern: "/kbs/{kb}/overview", Tier: tierStable, Summary: "Counts and collections of a KB",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, _ string, srv *Server) {
			m.uiOverview(w, r, srv)
		}},
	{Pattern: "/kbs/{kb}/concept", Tier: tierStable, Summary: "Read one concept",
		Handler: kbRoute(func(w http.ResponseWriter, r *http.Request, srv *Server) { uiConcept(w, r, srv.kbRef, srv) })},
	{Pattern: "/kbs/{kb}/search", Tier: tierStable, Summary: "Keyword search over a KB",
		Handler: kbRoute(uiSearch)},
	{Pattern: "/kbs/{kb}/changes", Tier: tierStable, Summary: "Changes since a point in time",
		Handler: kbRoute(uiChanges)},

	{Pattern: "/kbs/{kb}/graph", Tier: tierPreview, Summary: "The link graph",
		Handler: kbRoute(func(w http.ResponseWriter, r *http.Request, srv *Server) { uiGraph(w, r, srv.kbRef) })},
	{Pattern: "/kbs/{kb}/lint", Tier: tierPreview, Summary: "Lint findings",
		Handler: kbRoute(uiLint)},
	{Pattern: "/kbs/{kb}/checks", Tier: tierPreview, Summary: "The lint checks and their state",
		Handler: kbRoute(func(w http.ResponseWriter, _ *http.Request, srv *Server) { uiChecks(w, srv.kbRef) })},
	{Pattern: "/kbs/{kb}/maps", Tier: tierPreview, Summary: "Maps and journals with their ontology",
		Handler: kbRoute(uiMaps)},
	{Pattern: "/kbs/{kb}/history", Tier: tierPreview, Summary: "The commit history of a concept",
		Handler: kbRoute(uiHistory)},
	{Pattern: "/kbs/{kb}/work", Tier: tierPreview, Summary: "Open work items and unchecked items",
		Handler: kbRoute(uiWork)},
	{Pattern: "/kbs/{kb}/sources", Tier: tierPreview, Summary: "Registered sources",
		Handler: kbRoute(uiSources)},
	{Pattern: "/kbs/{kb}/retired", Tier: tierPreview, Summary: "Retired pages",
		Handler: kbRoute(uiRetired)},
	{Pattern: "/kbs/{kb}/services", Tier: tierPreview, Summary: "The Service concepts",
		Handler: kbRoute(func(w http.ResponseWriter, r *http.Request, srv *Server) { uiServices(w, r, srv.kbRef) })},
	{Pattern: "/kbs/{kb}/conflicts", Tier: tierPreview, Summary: "Open git conflicts",
		Handler: kbRoute(uiConflicts)},
	{Pattern: "/kbs/{kb}/assets", Tier: tierPreview, Summary: "The assets of a concept",
		Handler: kbRoute(uiAssets)},
	{Pattern: "/kbs/{kb}/feeds", Tier: tierPreview, Summary: "Feeds of a KB",
		Handler: kbRoute(uiFeeds)},
	{Pattern: "/kbs/{kb}/births", Tier: tierPreview, Summary: "The commit that added each concept",
		Handler: kbRoute(func(w http.ResponseWriter, r *http.Request, srv *Server) { uiBirths(w, r, srv.kbRef) })},
	{Pattern: "/kbs/{kb}/journal", Tier: tierPreview, Summary: "The last entries of the KB log",
		Handler: kbRoute(func(w http.ResponseWriter, r *http.Request, srv *Server) { uiJournal(w, r, srv.kbRef) })},
	{Pattern: "/kbs/{kb}/status", Tier: tierPreview, Summary: "Open gaps and searches that found nothing",
		Handler: kbRoute(uiStatus)},
	{Pattern: "/kbs/{kb}/revision", Tier: tierPreview, Summary: "The KB revision, for change polling",
		Handler: kbRoute(func(w http.ResponseWriter, r *http.Request, srv *Server) { uiRevision(w, r, srv.kbRef) })},
	{Pattern: "/kbs/{kb}/maintenance/review", Tier: tierPreview, Summary: "The doctor's review agenda",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, name string, srv *Server) {
			uiMaintenance(w, r, srv, "review", func() repairInfo { return m.repairInfoOf(name, srv) })
		}},

	{Pattern: "/kbs/{kb}/maintenance/summary", Tier: tierInternal, Summary: "Auto-repair and doctor state",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, name string, srv *Server) {
			uiMaintenance(w, r, srv, "summary", func() repairInfo { return m.repairInfoOf(name, srv) })
		}},
	{Pattern: "/kbs/{kb}/components", Tier: tierInternal, Summary: "Deployed components, their runs and alarms",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, name string, srv *Server) {
			m.uiComponents(w, r, name, srv)
		}},
	{Pattern: "/kbs/{kb}/artifacts", Tier: tierInternal, Summary: "Artifacts of a KB",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, _ string, srv *Server) {
			m.uiArtifacts(w, r, srv)
		}},
	{Pattern: "/kbs/{kb}/artifact", Tier: tierInternal, Summary: "One artifact",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, _ string, srv *Server) {
			m.uiArtifact(w, r, srv)
		}},
	{Pattern: "/kbs/{kb}/secrets", Tier: tierInternal, Summary: "Secrets panel",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, _ string, srv *Server) {
			m.uiSecrets(w, r, srv)
		}},
	{Pattern: "/kbs/{kb}/placeholders", Tier: tierInternal, Summary: "Placeholders and their resolution",
		Handler: func(m *MultiKBServer, w http.ResponseWriter, r *http.Request, _ string, srv *Server) {
			m.uiPlaceholders(w, r, srv)
		}},
	{Pattern: "/kbs/{kb}/audit", Tier: tierInternal, Summary: "The audit chain (admin only)",
		Handler: func(_ *MultiKBServer, w http.ResponseWriter, r *http.Request, name string, srv *Server) {
			uiAudit(w, r, name, srv)
		}},
})

func buildAPIRoutes(routes []apiRoute) []apiRoute {
	for i := range routes {
		routes[i].segments = strings.Split(strings.Trim(routes[i].Pattern, "/"), "/")
	}
	return routes
}

// serveAPI routes a request under prefix to the table, serving only the routes
// whose tier is in tiers. Read-only by construction: the table holds no route
// that mutates anything, so a bearer token that could write still cannot here.
// A route outside tiers, an unknown path and a KB the caller cannot see all
// answer the same 404.
func (m *MultiKBServer) serveAPI(w http.ResponseWriter, r *http.Request, prefix string, tiers apiTier) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow,
			"the UI API is read-only: use GET or HEAD", "")
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
	segments := []string{}
	if rest != "" {
		segments = strings.Split(rest, "/")
	}

	var route *apiRoute
	for i := range apiRoutes {
		if apiRoutes[i].Tier&tiers != 0 && apiRoutes[i].match(segments) {
			route = &apiRoutes[i]
			break
		}
	}
	if route == nil {
		writeUINotFound(w)
		return
	}
	if !route.kbScoped() {
		route.Handler(m, w, r, "", nil)
		return
	}

	name := segments[1]
	srv, ok := m.servers[name]
	if !ok || srv.kbRef == nil || !uiKBVisible(r.Context(), srv.kbRef) {
		writeUINotFound(w)
		return
	}
	route.Handler(m, w, r, name, srv)
}
