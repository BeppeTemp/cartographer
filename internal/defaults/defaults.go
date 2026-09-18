// Package defaults contains the local endpoint values shared by the native
// service and client first-run paths. It deliberately has no dependencies so
// these values cannot drift between those entry points.
package defaults

const (
	// DefaultPort is the reserved local port used for newly generated service
	// configuration and client configuration.
	DefaultPort = 39273

	// DefaultListenAddress is the loopback HTTP address used when generating a
	// native service configuration for the first time.
	DefaultListenAddress = "127.0.0.1:39273"

	// DefaultMCPURL is the HTTP MCP endpoint used for a first-run client when
	// no persisted configuration or CARTOGRAPHER_SERVER_URL is available.
	DefaultMCPURL = "http://localhost:39273/mcp"

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
