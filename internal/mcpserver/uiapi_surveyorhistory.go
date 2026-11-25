package mcpserver

import (
	"net/http"
	"strconv"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// SetRunHistory loads the persistent Surveyor run counters of the server's data
// directory (surveyorhistory.go). "" is no data directory (a folder session):
// the counters stay in memory and the route says persisted:false. Call it
// before serving (it is not synchronised with the routes): it replaces the store, so counts made earlier are forgotten.
func (m *MultiKBServer) SetRunHistory(dataDir string) {
	h := newRunHistory()
	h.load(config.SurveyorHistoryPath(dataDir), m.surveyors.clock())
	m.runHistory = h
}

// defaultHistoryDays is the window of GET /kbs/{kb}/surveyor-history without `days`.
const defaultHistoryDays = 30

type surveyorHistoryBody struct {
	RetentionDays int          `json:"retention_days"`
	Persisted     bool         `json:"persisted"`
	FirstDay      *string      `json:"first_day"`
	Now           time.Time    `json:"now"`
	Days          []historyRow `json:"days"`
}

// uiSurveyorHistory answers the daily counters of one KB. It copies the gate of
// uiComponents: a caller that does not see the KB whole gets the 404 of an
// unknown route. Only rows of {kb} are served, and no principal, replica or run
// id leaves the store.
func (m *MultiKBServer) uiSurveyorHistory(w http.ResponseWriter, r *http.Request, name string, srv *Server) {
	if !WholeVisible(r.Context(), srv.kbRef, false) {
		writeUINotFound(w)
		return
	}
	days := defaultHistoryDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > runHistoryDays {
			writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "days must be a whole number from 1 to "+strconv.Itoa(runHistoryDays), "days")
			return
		}
		days = n
	}
	now := m.surveyors.clock().UTC()
	body := surveyorHistoryBody{RetentionDays: runHistoryDays, Now: now, Days: []historyRow{}}
	if h := m.runHistory; h != nil {
		v := h.query(name, days, now)
		body.Persisted = v.Persisted
		if v.FirstDay != "" {
			body.FirstDay = &v.FirstDay
		}
		if v.Days != nil {
			body.Days = v.Days
		}
	}
	writeUIJSON(w, http.StatusOK, body)
}
