package mcpserver

import "time"

// touchActivity records that a principal used this KB, for the idle signal the
// worker heartbeat answers with (uiapi_workers.go).
//
// Two calls are deliberately not activity:
//   - sync_pull: `cartographer sync` runs it from a 30-minute timer on every
//     KB a client serves, so counting it would keep every connected KB
//     "active" and an idle trigger of 30 minutes or more could never fire;
//   - a call by a principal with a LIVE worker declaration: a worker's own
//     work must not keep the KB busy. Only a live declaration excludes, so a
//     worker that stopped beating for three intervals counts as a person.
//
// It runs in callToolChecked after authorization, so a denied call, an unknown
// tool and a call refused by the argument check never reach it.
func (s *Server) touchActivity(principal, tool string) {
	if tool == "sync_pull" {
		return
	}
	if s.liveWorker != nil && s.liveWorker(principal) {
		return
	}
	s.activity.Store(s.now().UnixNano())
}

// lastActivity is the time of the last counted call; false when there was none
// since the process started.
func (s *Server) lastActivity() (time.Time, bool) {
	n := s.activity.Load()
	if n == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, n).UTC(), true
}
