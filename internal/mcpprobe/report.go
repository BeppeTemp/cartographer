package mcpprobe

// Result is what one probe found. It holds names and classes, never a value.
type Result struct {
	Target    string `json:"target"`    // the approved endpoint or command
	Transport string `json:"transport"` // "http" or "stdio"
	// Unresolved are the ${VAR} names unset or empty in the probing shell.
	Unresolved []string `json:"unresolved,omitempty"`
	Net        Kind     `json:"net,omitempty"`
	Detail     string   `json:"detail,omitempty"`
	Hint       string   `json:"hint,omitempty"`
	// Error is a failure of the MCP session itself (initialize or tools/list):
	// the server answered but is not usable.
	Error string `json:"error,omitempty"`
	Tools int    `json:"tools"`
}

// Exit codes of `cartographer mcp probe`, documented in its help and in
// docs/mcp-clients.md. 2 (usage, not found, not approved) is decided before a
// Result exists.
const (
	ExitOK         = 0
	ExitNotUsable  = 1
	ExitUsage      = 2
	ExitUnresolved = 3
	ExitNetwork    = 4
	ExitTLS        = 5
)

// ExitCode maps r to an exit code, the first fault in the order the probe finds
// them: variables (nothing was launched), then network, then TLS, then session.
func ExitCode(r Result) int {
	switch {
	case len(r.Unresolved) > 0:
		return ExitUnresolved
	case r.Net == KindTLSUnknownAuthority || r.Net == KindTLSOther:
		return ExitTLS
	case r.Net != "":
		return ExitNetwork
	case r.Error != "":
		return ExitNotUsable
	}
	return ExitOK
}
