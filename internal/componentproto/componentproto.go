// Package componentproto is the server-component wire contract.
// It holds the route family, the token kinds, the health states of a component and the protocol compatibility
// rule. It imports only the standard library, so the client packages and the
// server share it without crossing the client import firewall.
//
// The path prefix (/api/component/v1) is the envelope (auth, content type,
// error shape, body caps) and changes essentially never. Protocol is the
// field-level contract and is what is negotiated: additive optional fields
// never bump it, removing or re-typing a field or changing its meaning does.
package componentproto

// Protocol is the component protocol this build speaks (N). MinProtocol is the
// oldest it still serves (N-1, but never below 1: 0 means "not negotiated" and
// belongs to the legacy aliases).
const (
	Protocol    = 1
	MinProtocol = 1
)

// The routes of the family, relative to the origin.
const (
	Prefix        = "/api/component/v1/"
	HeartbeatPath = Prefix + "heartbeat"
	SettingsPath  = Prefix + "settings"
	UsagePath     = Prefix + "usage"
)

// Paths lists every route of the family.
func Paths() []string { return []string{HeartbeatPath, SettingsPath, UsagePath} }

// The 0.28 routes. They were released, so they stay served as aliases of the
// family for the whole 0.29 series, frozen and treated as protocol 0.
const (
	LegacyUsagePath          = "/api/usage"
	LegacyClientSettingsPath = "/api/client-settings"
)

// The kinds of a component, mirroring the token kinds of internal/auth as
// strings (a test in internal/mcpserver pins the two sets equal).
const (
	KindSurveyor = "surveyor"
	KindClient   = "client"
	KindHuman    = "human"
)

// Kinds lists the kinds.
func Kinds() []string { return []string{KindSurveyor, KindClient, KindHuman} }

// The health states of a component (a closed set).
const (
	StateUp         = "up"         // live replicas >= the expected minimum
	StateDegraded   = "degraded"   // some live, fewer than expected
	StateSilent     = "silent"     // expected, none live
	StateStarting   = "starting"   // expected, none seen yet, inside the grace window
	StateUndeclared = "undeclared" // live but not expected: informational
)

// States lists the health states.
func States() []string {
	return []string{StateUp, StateDegraded, StateSilent, StateStarting, StateUndeclared}
}

// The values of a replica's protocol skew as the server reports it.
const (
	SkewBehind = "behind"
	SkewNone   = ""
	SkewAhead  = "ahead"
)

// Error codes of the family.
const (
	CodeProtocolUnsupported = "protocol_unsupported" // HTTP 426
	CodeWrongTokenKind      = "wrong_token_kind"     // HTTP 403
)

// TaskKinds is the closed set of task names a heartbeat may declare. The
// Surveyor's kinds register under the same names (a test there pins it).
func TaskKinds() []string {
	return []string{"doctor", "ingest", "poll", "prompt", "repair", "synthesis"}
}

// Skew applies the compatibility rule of a server speaking Protocol to a peer
// speaking peer: N-1, N and N+1 are served (ok), anything else is refused. A
// peer ahead is served as N; the obligation that makes it safe is the newer
// peer's, which must itself speak the server's N.
func Skew(peer int) (skew string, ok bool) { return skewAgainst(Protocol, peer) }

func skewAgainst(n, peer int) (string, bool) {
	switch {
	case peer == n:
		return SkewNone, true
	case peer == n-1:
		return SkewBehind, true
	case peer == n+1:
		return SkewAhead, true
	}
	return "", false
}

// Supports reports whether a peer that speaks protocol own can use a server
// whose /health names serverProtocol: the rule is symmetric, so the server's N
// must lie within own-1..own+1.
func Supports(own, serverProtocol int) bool {
	_, ok := skewAgainst(own, serverProtocol)
	return ok
}
