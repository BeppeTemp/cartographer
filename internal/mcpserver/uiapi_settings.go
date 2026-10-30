package mcpserver

import (
	"net/http"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/workerconfig"
)

// SetSettings gives the settings view (D743) the configuration the process
// runs with, read-only, and the update channel the binary was installed by
// ("" when unknown). Without it the view lists the defaults.
func (m *MultiKBServer) SetSettings(cfg *config.Config, updateChannel string) {
	m.settings = cfg
	m.updateChannel = updateChannel
}

type uiSettingsKB struct {
	Name    string         `json:"name"`
	Entries []config.Entry `json:"entries"`
	// YAML is the KB's effective settings as the settings.yaml they amount
	// to, commented by the schema (D818).
	YAML string         `json:"yaml"`
	Git  map[string]any `json:"git,omitempty"`
}

// GET /settings — every setting with its effective value and origin, plus the
// facts that are not settings but belong beside them: version, newer release,
// and per-KB git sync state. Secrets are redacted by config.Settings. A KB the
// caller cannot see is absent.
func (m *MultiKBServer) uiSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg := m.settings
	if cfg == nil {
		cfg = config.Default()
	}
	var names []string
	for name, srv := range m.servers {
		if srv.kbRef != nil && uiKBVisible(ctx, srv.kbRef) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	view := cfg.Settings(names)
	// The server's kbs value lists every configured KB: a caller sees only the
	// names of the KBs it can see, as for the per-KB sections.
	visible := map[string]bool{}
	for _, n := range names {
		visible[n] = true
	}
	for i, e := range view.Server {
		if e.Key != "kbs" {
			continue
		}
		shown := []string{}
		for _, s := range cfg.KBs {
			if visible[s.MountName()] {
				shown = append(shown, s.MountName())
			}
		}
		view.Server[i].Value = shown
	}
	admin := auth.PrincipalFromContext(ctx).Policy.Admin
	whole := map[string]bool{}
	for _, n := range names {
		whole[n] = WholeVisible(ctx, m.servers[n].kbRef, false)
	}
	narrowSettingsView(&view, visible, whole, admin)

	kbs := make([]uiSettingsKB, 0, len(view.KBs))
	for _, section := range view.KBs {
		row := uiSettingsKB{Name: section.Name, Entries: section.Entries}
		if out, ok, err := view.KBYAML(section.Name); ok && err == nil {
			row.YAML = string(out)
		}
		// Replication facts describe the KB as a whole, so they are for a
		// caller that can already see the whole KB (as in the overview).
		if k := m.servers[section.Name].kbRef; WholeVisible(ctx, k, false) {
			remoteURL, hasRemote := k.RemoteInfo()
			st := k.GitStatusSnapshot()
			git := map[string]any{
				"has_remote":       hasRemote,
				"sync":             hasRemote,
				"push_state":       st.State,
				"push_last_error":  st.LastError,
				"unpushed_commits": st.UnpushedCommits,
				"behind_commits":   st.BehindCommits,
				"last_push_at":     st.LastPushAt,
				"last_sync_in_at":  st.LastSyncInAt,
			}
			if hasRemote {
				git["remote_url"] = remoteURL
			}
			row.Git = git
		}
		kbs = append(kbs, row)
	}

	// The caller's own clients, never anybody else's (D818). Each file was
	// rendered by this server's schema and its secrets masked when it arrived
	// (scrubClientReport): the keys it sets documented, the others listed ready
	// to uncomment. Rendering here would cost every read the size of what any
	// token can post.
	clients := m.clientReports.of(auth.PrincipalFromContext(ctx).ID)
	payload := map[string]any{
		"version": m.version,
		"server":  view.Server,
		"kbs":     kbs,
		"clients": clients,
		// Worker perimeters the caller sees whole, and the template (D22).
		"workers": m.workerSettingsViews(r),
	}
	wEntries, wYAML := workerconfig.Template()
	payload["worker_template"] = map[string]any{"entries": wEntries, "yaml": wYAML}
	// What a client can set, for a viewer none of whose clients has reported:
	// every key at its default, and the file they amount to.
	if t := clientconfig.Template(); t != nil {
		tmpl := map[string]any{"entries": t.Entries}
		if out, err := clientconfig.Schema.Format(nil); err == nil {
			tmpl["yaml"] = string(out)
		}
		payload["client_template"] = tmpl
	}
	// The server's configuration as the YAML file it amounts to, commented by
	// the schema, its kbs[] entries limited to the KBs the caller sees (D818).
	// The file also names server paths and roles, so it is for an admin.
	if admin {
		if out, err := view.ServerYAML(); err == nil {
			payload["server_yaml"] = string(out)
		}
	}
	if m.latestVersion != nil {
		if latest := m.latestVersion(); latest != "" {
			payload["latest_version"] = latest
		}
	}
	if m.updateChannel != "" {
		payload["update_channel"] = m.updateChannel
	}
	writeUIJSON(w, http.StatusOK, payload)
}

// settingsCallerKeys are the server-level keys a caller that is not an admin
// still receives: none of them is a path, a role, an identity or a secret.
// "kbs" is already limited to the KBs the caller sees; "git.identities" is
// narrowed to them below.
var settingsCallerKeys = map[string]bool{
	"update_check":   true,
	"web.enabled":    true,
	"kbs":            true,
	"git.identities": true,
}

// narrowSettingsView applies, in place, what the caller may read of the view.
// Every remote URL loses its credentials, for every caller (the same helper as
// kb_status), and is shown only for a KB the caller sees whole. A caller that is not an admin gets only the settingsCallerKeys
// of the server section, and the per-KB map of identities limited to the KBs
// it sees; the YAML views are rendered from the narrowed view afterwards.
func narrowSettingsView(view *config.View, visible, whole map[string]bool, admin bool) {
	for s := range view.KBs {
		section := &view.KBs[s]
		kept := section.Entries[:0:0]
		for _, e := range section.Entries {
			if e.Key == "kbs[].remote" {
				// Where a KB replicates is a replication fact: for a caller
				// that sees the whole KB, as the git block below.
				if !whole[section.Name] {
					continue
				}
				if raw, ok := e.Value.(string); ok {
					e.Value = kb.RedactRemoteURL(raw)
				}
			}
			kept = append(kept, e)
		}
		section.Entries = kept
	}
	if admin {
		return
	}
	kept := view.Server[:0:0]
	for _, e := range view.Server {
		if !settingsCallerKeys[e.Key] {
			continue
		}
		if ids, ok := e.Value.(map[string]string); ok {
			shown := map[string]string{}
			for name, id := range ids {
				if whole[name] {
					shown[name] = id
				}
			}
			e.Value = shown
		}
		kept = append(kept, e)
	}
	view.Server = kept
}
