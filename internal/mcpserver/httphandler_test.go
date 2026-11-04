package mcpserver

import "net/http"

// HTTPHandler serves one Server's /mcp endpoint and nothing else. Tests that
// exercise the MCP transport of a single Server use it; the operational
// endpoints (/health, /ready, /clients) exist only on MultiKBServer.Handler,
// which is what `serve` mounts, and are tested there.
func (s *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	return mux
}
