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
)
