package mcpserver

import "time"

// touchActivity records that a principal used this KB, for the idle signal the
// surveyor heartbeat answers with (uiapi_surveyors.go).
//
// Two calls are deliberately not activity:
//   - sync_pull: `cartographer sync` runs it from a 30-minute timer on every
//     KB a client serves, so counting it would keep every connected KB
//     "active" and an idle trigger of 30 minutes or more could never fire;
//   - a call by a Surveyor's own work, which must not keep the KB busy: a token
//     DECLARED a surveyor excludes by kind, even while its Surveyor is silent
//     (so one that missed three beats is not a person); a token with no kind
//     is excluded only while it has a LIVE declaration, as before kinds.
//
// It runs in callToolChecked after authorization and the audit attempt, so a
// denied call, an unknown tool, a call refused by the argument check and one
// refused because its audit attempt could not be written never reach it.
func (s *Server) touchActivity(principal string, surveyorKind bool, tool string) {
	if tool == "sync_pull" {
		return
	}
	if surveyorKind || (s.liveSurveyor != nil && s.liveSurveyor(principal)) {
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
