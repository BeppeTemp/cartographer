package mcpserver

import (
	"github.com/BeppeTemp/cartographer/internal/feedback"
)

// internalErrorHint is the one sentence appended to an internal-error result
// (a recovered panic, a handler's Go error) when the operator has not switched
// feedback off. It is a fixed constant and carries nothing from the tool, its
// arguments or the error.
//
// Trap: it goes in the SAME text block as the error. A second content block
// breaks client.Call, which reads exactly one (doctornudge.go).
const internalErrorHint = " This looks like a defect in Cartographer; if the user agrees, offer to report it with the cartographer-feedback skill."

// SetFeedback installs the operator's feedback.destination (D1925).
// The zero value is github, the default.
func (s *Server) SetFeedback(d feedback.Destination) { s.feedback = d }

// feedbackDestination is the destination kb_status and sync_pull report.
func (s *Server) feedbackDestination() feedback.Destination {
	if s == nil {
		return feedback.Destination{}
	}
	return s.feedback
}

// internalError builds the result of a recovered panic or a handler's Go
// error. Both sites go through it so the hint cannot drift between them; no
// other errorResult carries the hint (a user error is not a defect).
func (s *Server) internalError(msg string) ToolResult {
	if s.feedbackDestination().Mode() != feedback.ModeOff {
		msg += internalErrorHint
	}
	return errorResult(msg)
}

// SetFeedback installs the destination on every KB server and routed view the
// multi-KB server builds from now on, so call it before MountKB.
func (m *MultiKBServer) SetFeedback(d feedback.Destination) { m.feedback = d }
