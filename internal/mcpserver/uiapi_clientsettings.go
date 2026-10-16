package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
)

// ClientSettingsPath is where a client reports its own settings after every
// sync (D818), so the Atlas Settings page can show the viewer their client
// beside the server and the KBs. Client-to-server metadata like UsagePath:
// an HTTP route inside the auth chain of /mcp, not an MCP tool.
const ClientSettingsPath = "/api/client-settings"

// maxClientSettingsBody bounds a report: a few dozen keys and the file.
const maxClientSettingsBody = 256 << 10

// maxClientReportsPerPrincipal bounds the machines kept for one principal;
// the oldest report goes first.
const maxClientReportsPerPrincipal = 8

// clientReport is one machine's client settings, as the client built them
// (clientconfig.SettingsReport) plus when the server received them.
type clientReport struct {
	Machine    string         `json:"machine"`
	Version    string         `json:"version"`
	ServerURL  string         `json:"server_url"`
	Entries    []config.Entry `json:"entries"`
	YAML       string         `json:"yaml"`
	ReceivedAt time.Time      `json:"received_at"`
}

// clientReports holds the reports in memory, per principal and machine. A
// restart forgets them until each client's next sync (its timer runs every
// half hour): they describe the workstations, which the server does not own.
type clientReports struct {
	mu sync.Mutex
	by map[string]map[string]clientReport
}

func (c *clientReports) put(principal string, r clientReport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.by == nil {
		c.by = map[string]map[string]clientReport{}
	}
	mine := c.by[principal]
	if mine == nil {
		mine = map[string]clientReport{}
		c.by[principal] = mine
	}
	mine[r.Machine] = r
	for len(mine) > maxClientReportsPerPrincipal {
		oldest := ""
		for name, x := range mine {
			if oldest == "" || x.ReceivedAt.Before(mine[oldest].ReceivedAt) {
				oldest = name
			}
		}
		delete(mine, oldest)
	}
}

// of returns the reports a principal sent, by machine name. Only ever the
// caller's own: on a shared server another person's workstation is not theirs
// to see, and no list of clients is exposed.
func (c *clientReports) of(principal string) []clientReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]clientReport, 0, len(c.by[principal]))
	for _, r := range c.by[principal] {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Machine < out[j].Machine })
	return out
}

// handleClientSettings stores the caller's report under its principal.
func (m *MultiKBServer) handleClientSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "use POST", "")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxClientSettingsBody+1))
	if err != nil || len(data) > maxClientSettingsBody {
		writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "client settings report too large", "")
		return
	}
	var rep clientReport
	if err := json.Unmarshal(data, &rep); err != nil || rep.Machine == "" {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "body must be a client settings report with a machine name", "")
		return
	}
	rep.ReceivedAt = time.Now().UTC()
	m.clientReports.put(auth.PrincipalFromContext(r.Context()).ID, rep)
	writeUIJSON(w, http.StatusOK, map[string]bool{"received": true})
}
