package mcpserver

import (
	"net/http"
	"sort"

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
	Git     map[string]any `json:"git,omitempty"`
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

	kbs := make([]uiSettingsKB, 0, len(view.KBs))
	for _, section := range view.KBs {
		row := uiSettingsKB{Name: section.Name, Entries: section.Entries}
		// Replication facts describe the KB as a whole, so they are for a
		// caller that can already see the whole KB (as in the overview).
		if k := m.servers[section.Name].kbRef; WholeVisible(ctx, k, false) {
			remoteURL, hasRemote := k.RemoteInfo()
			st := k.GitStatusSnapshot()
			git := map[string]any{
				"has_remote":       hasRemote,
				"sync":             k.GitSync,
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

	payload := map[string]any{
		"version": m.version,
		"server":  view.Server,
		"kbs":     kbs,
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
