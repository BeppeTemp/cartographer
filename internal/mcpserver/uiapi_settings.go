package mcpserver

import (
	"net/http"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
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

	// The caller's own clients, never anybody else's (D818). Each file is shown
	// rendered by this server's schema, as the server's and the KBs' are: the
	// keys it sets documented, the others listed ready to uncomment.
	clients := m.clientReports.of(auth.PrincipalFromContext(ctx).ID)
	for i := range clients {
		if out, err := clientconfig.Schema.Format([]byte(clients[i].YAML)); err == nil {
			clients[i].YAML = string(out)
		}
	}
	payload := map[string]any{
		"version": m.version,
		"server":  view.Server,
		"kbs":     kbs,
		"clients": clients,
	}
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
	if out, err := view.ServerYAML(); err == nil {
		payload["server_yaml"] = string(out)
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
