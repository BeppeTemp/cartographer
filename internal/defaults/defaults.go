// Package defaults contains the local endpoint values shared by the native
// service and client first-run paths. It deliberately has no dependencies so
// these values cannot drift between those entry points.
package defaults

import "strings"

const (
	// DefaultPort is the reserved local port used for newly generated service
	// configuration and client configuration.
	DefaultPort = 39273

	// DefaultListenAddress is the loopback HTTP address used when generating a
	// native service configuration for the first time.
	DefaultListenAddress = "127.0.0.1:39273"

	// DefaultMCPURL is the HTTP MCP endpoint used for a first-run client when
	// no persisted configuration or CARTOGRAPHER_SERVER_URL is available. It is
	// built from DefaultListenAddress so the address clients are given is the
	// one the service listens on: it used to say localhost, which Windows
	// resolves to ::1 first, where nothing listens — a client without a fast
	// IPv4 fallback then could not connect at all (#329).
	DefaultMCPURL = "http://" + DefaultListenAddress + "/mcp"

	// legacyMCPOrigin is the origin DefaultMCPURL carried before #329.
	legacyMCPOrigin = "http://localhost:39273"

	// WindowsShutdownEventName is the Windows named event `serve` waits on and
	// `internal/service` sets to ask for a graceful shutdown (D217). It lives
	// here for the same reason every other value in this package does: the two
	// sides must not be able to drift, and neither of them can import the other.
	//
	// The Local\ prefix scopes it to the user's own terminal-services session,
	// which is what makes it the equivalent of a SIGTERM on unix: reachable by
	// another process of the same user in the same session, and by nobody else.
	// Two servers running as one user in one session would share it — an
	// arrangement that already shares the listen port, so it is not a case this
	// name has to distinguish.
	WindowsShutdownEventName = `Local\cartographer-serve-shutdown`
)

// MigrateLegacyMCPURL rewrites a URL on the pre-#329 default origin,
// http://localhost:39273, to the loopback literal the service listens on,
// keeping the path (a per-KB mount such as /mcp/<kb> stays one). Anything else —
// another port, another host, https — is the user's choice and is returned
// unchanged.
func MigrateLegacyMCPURL(raw string) string {
	if len(raw) < len(legacyMCPOrigin) || !strings.EqualFold(raw[:len(legacyMCPOrigin)], legacyMCPOrigin) {
		return raw
	}
	rest := raw[len(legacyMCPOrigin):]
	if rest != "" && rest[0] != '/' && rest[0] != '?' {
		// localhost:392730 or similar: not the default origin.
		return raw
	}
	return "http://" + DefaultListenAddress + rest
}
