package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

// The component route family (/api/component/v1/..., internal/componentproto):
// what the Surveyor and Compass report to the server, in one versioned place.
// Always routed, with or without the Atlas, inside the auth chain of /mcp, and
// never an MCP tool (D285). A new route goes through componentproto and the
// route-table test (componentroutes_test.go), which fails on a path routed
// here and missing from the package, or the other way round.

// maxComponentSettingsBody is the larger of the two settings bodies; each
// handler then applies its own cap.
const maxComponentSettingsBody = maxClientSettingsBody

// handleComponent dispatches the family. The legacy 0.28 aliases are routed
// by Handler straight to the handlers, as protocol 0.
func (m *MultiKBServer) handleComponent(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case componentproto.HeartbeatPath:
		if !requireKind(w, r, componentproto.KindSurveyor) {
			return
		}
		m.handleSurveyors(w, r)
	case componentproto.RunsPath:
		if !requireKind(w, r, componentproto.KindSurveyor) {
			return
		}
		m.handleSurveyorRuns(w, r)
	case componentproto.SettingsPath:
		m.handleComponentSettings(w, r)
	case componentproto.UsagePath:
		if !requireKind(w, r, componentproto.KindClient) {
			return
		}
		m.handleUsage(w, r, true)
	default:
		writeUINotFound(w)
	}
}

// componentPathRouted reports whether p belongs to the family.
func componentPathRouted(p string) bool {
	return len(p) > len(componentproto.Prefix) && p[:len(componentproto.Prefix)] == componentproto.Prefix
}

// writeProtocolUnsupported answers 426 with both numbers, so the component can
// say why it was refused. They sit in the error object beside code and
// message: the envelope of uiapi.go is unchanged.
func writeProtocolUnsupported(w http.ResponseWriter, peer int) {
	writeUIJSON(w, http.StatusUpgradeRequired, map[string]any{
		"error": struct {
			uiError
			Protocol    int `json:"protocol"`
			MinProtocol int `json:"min_protocol"`
			Peer        int `json:"peer_protocol"`
		}{
			uiError: uiError{Code: componentproto.CodeProtocolUnsupported,
				Message: "this server speaks component protocol " + strconv.Itoa(componentproto.Protocol) +
					" and serves " + strconv.Itoa(componentproto.MinProtocol) + " to " + strconv.Itoa(componentproto.Protocol+1) +
					"; the component sent " + strconv.Itoa(peer),
				Field: "protocol"},
			Protocol: componentproto.Protocol, MinProtocol: componentproto.MinProtocol, Peer: peer,
		},
	})
}

// checkProtocol applies the compatibility rule to the protocol a component
// sent: a non-positive value is a 400 (the field is required on this family),
// one outside N-1..N+1 a 426. It returns the skew to keep and whether the
// request may go on; otherwise the answer is written.
func checkProtocol(w http.ResponseWriter, peer int) (string, bool) {
	if peer <= 0 {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "protocol must be a positive integer", "protocol")
		return "", false
	}
	skew, ok := componentproto.Skew(peer)
	if !ok {
		writeProtocolUnsupported(w, peer)
		return "", false
	}
	return skew, true
}

// handleComponentSettings serves the settings route: the body's `component`
// ("surveyor" or "client") selects which report it is, and `protocol` is
// negotiated as on the heartbeat.
func (m *MultiKBServer) handleComponentSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "use POST", "")
		return
	}
	if !requireJSON(w, r) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxComponentSettingsBody))
	if err != nil {
		writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "settings report too large", "")
		return
	}
	var probe struct {
		Component string `json:"component"`
		Protocol  int    `json:"protocol"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		// A body cut by a cap further down would not parse either: past the
		// smaller cap the honest answer is the size, not the syntax.
		if len(data) > maxSurveyorSettingsBody {
			writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "settings report too large", "")
			return
		}
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "body must be a JSON object", "")
		return
	}
	if _, ok := checkProtocol(w, probe.Protocol); !ok {
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	switch probe.Component {
	case componentproto.KindSurveyor:
		if !requireKind(w, r, componentproto.KindSurveyor) {
			return
		}
		m.handleSurveyorSettings(w, r)
	case componentproto.KindClient:
		if !requireKind(w, r, componentproto.KindClient) {
			return
		}
		m.handleClientSettings(w, r)
	default:
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "component must be surveyor or client", "component")
	}
}

// requireKind refuses a token of the wrong kind on the versioned routes with
// 403 wrong_token_kind. A route is a Surveyor's (want = surveyor) or a client's
// (want = client: Compass, driven by a person's token or a client token).
//
//   - The auth-off local principal is let through everywhere.
//   - A token has no "unset" kind once loaded: auth maps an absent kind to
//     human (auth.ParseKind), and a declared human is the same value. So a
//     human token must be let through on a Surveyor route too, or every
//     deployment whose operator never declared kinds would break; what the
//     check refuses is the declared machine kind on the other's route (a
//     client token on the heartbeat, a surveyor token on usage).
//   - The 0.28 aliases are protocol 0 and never come here.
func requireKind(w http.ResponseWriter, r *http.Request, want string) bool {
	p := auth.PrincipalFromContext(r.Context())
	if p.Local {
		return true
	}
	other := auth.KindClient
	if want == componentproto.KindClient {
		other = auth.KindSurveyor
	}
	if p.Kind != other {
		return true
	}
	writeUIError(w, http.StatusForbidden, componentproto.CodeWrongTokenKind,
		"this route takes a "+want+" token; this token is a "+string(p.Kind), "")
	return false
}
