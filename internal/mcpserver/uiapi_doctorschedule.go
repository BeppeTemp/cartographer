package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// DoctorSchedulePath is where a client declares (POST) or withdraws (DELETE)
// the scheduled headless doctor session it installed (D369). Like UsagePath it
// is client-to-server metadata, not an agent operation: an HTTP route behind the
// same auth chain as /mcp, always routed, with or without the web UI.
const DoctorSchedulePath = "/api/doctor-schedule"

const maxDoctorScheduleBody = 4 << 10

// doctorScheduleMaxAhead bounds a declared next run: a daily job is never due
// further out than this, and a far-future date would keep Health promising a
// session forever.
const doctorScheduleMaxAhead = 8 * 24 * time.Hour

var doctorClientRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

type doctorScheduleBody struct {
	Client  string `json:"client"`
	NextRun string `json:"next_run"`
}

// handleDoctorSchedule stores or clears the KB's declaration. Writing needs the
// whole KB in write scope, like usage; a caller without it gets the 404 of an
// unknown KB so the route is no existence oracle.
func (m *MultiKBServer) handleDoctorSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "POST, DELETE")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "use POST or DELETE", "")
		return
	}
	name := r.URL.Query().Get("kb")
	var k *kb.KB
	if name == "" && len(m.servers) == 1 {
		for _, srv := range m.servers {
			k = srv.kbRef
		}
	} else if srv, ok := m.servers[name]; ok {
		k = srv.kbRef
	}
	if k == nil || !WholeVisible(r.Context(), k, true) {
		writeUINotFound(w)
		return
	}
	if r.Method == http.MethodDelete {
		if err := k.ClearDoctorSchedule(); err != nil {
			writeUIInternal(w, "doctor schedule", err)
			return
		}
		writeUIJSON(w, http.StatusOK, map[string]bool{"declared": false})
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxDoctorScheduleBody+1))
	if err != nil || len(data) > maxDoctorScheduleBody {
		writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "doctor schedule too large", "")
		return
	}
	var body doctorScheduleBody
	if err := json.Unmarshal(data, &body); err != nil || !doctorClientRe.MatchString(body.Client) {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "body must be {\"client\": <name>, \"next_run\": <RFC 3339>}", "")
		return
	}
	next, err := time.Parse(time.RFC3339, body.NextRun)
	now := time.Now()
	if err != nil || next.After(now.Add(doctorScheduleMaxAhead)) {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "next_run must be an RFC 3339 time within the next 8 days", "")
		return
	}
	if err := k.DeclareDoctorSchedule(kb.DoctorSchedule{Client: body.Client, NextRun: next.UTC(), DeclaredAt: now.UTC()}); err != nil {
		writeUIInternal(w, "doctor schedule", err)
		return
	}
	writeUIJSON(w, http.StatusOK, map[string]bool{"declared": true})
}
