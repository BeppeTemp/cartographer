package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Tool describes an MCP tool registered in the server.
type Tool struct {
	Name        string
	Description string
	// ReadOnly marks tools that never mutate KB content (safe under a read-only
	// scope token). See ToolRequiresWrite and the readOnlyTools golden test.
	ReadOnly bool
	// InputSchema is the JSON Schema for the "arguments" parameter.
	InputSchema json.RawMessage
	// Handler receives the raw parameters (JSON object) and returns a result and application error.
	// Application errors go in the ToolResult (isError:true), not as Go errors.
	Handler func(args json.RawMessage) (ToolResult, error)
}

// toolDescriptor is the representation exported to the client for tools/list.
type toolDescriptor struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	InputSchema json.RawMessage  `json:"inputSchema"`
	Annotations *toolAnnotations `json:"annotations,omitempty"`
}

// toolAnnotations carries the MCP tool annotations exposed in tools/list
// (spec: https://modelcontextprotocol.io/ — "annotations"). Only ReadOnlyHint
// is populated today, derived from Tool.ReadOnly (kept in sync with
// readOnlyToolNames by TestReadOnlyToolsGolden).
type toolAnnotations struct {
	ReadOnlyHint bool `json:"readOnlyHint"`
}

// Server is the MCP stdio server.
type Server struct {
	version  string
	tools    map[string]*Tool
	toolsOrd []string // maintains registration order for tools/list
	// agentProfile hides advanced tools (advancedToolNames, D65) from
	// tools/list; they remain callable via tools/call. Zero value = full list,
	// so New() keeps its historical behavior; `serve` sets it from
	// config.ToolsProfile (default "agent").
	agentProfile bool
	mu           sync.Mutex

	writeMu sync.Mutex    // serializes writes to the shared encoder
	enc     *json.Encoder // active stdio encoder; nil when not in Run (e.g. HTTP)
}

// New creates a new Server with the given version.
func New(version string) *Server {
	return &Server{
		version: version,
		tools:   make(map[string]*Tool),
	}
}

// SetToolsProfile selects which tools tools/list advertises: "agent" hides
// the advancedToolNames set, anything else ("full", "") advertises everything.
// tools/call is unaffected — hidden tools stay callable by name.
func (s *Server) SetToolsProfile(profile string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentProfile = profile == "agent"
}

// Tools returns a snapshot of all registered tools, keyed by name (for
// introspection/tests, e.g. the ReadOnly golden test).
func (s *Server) Tools() map[string]Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Tool, len(s.tools))
	for name, t := range s.tools {
		out[name] = *t
	}
	return out
}

// RegisterTool registers an MCP tool. Overwrites if the same name is already registered.
func (s *Server) RegisterTool(t Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tools[t.Name]; !exists {
		s.toolsOrd = append(s.toolsOrd, t.Name)
	}
	s.tools[t.Name] = &t
}

// Run starts the read/write loop on reader/writer.
// Blocks until EOF on reader or a fatal I/O error.
// Diagnostic logs (if needed) must go to stderr, not to the writer.
func (s *Server) Run(reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
	// Increase buffer for large messages.
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	enc := json.NewEncoder(writer)
	enc.SetEscapeHTML(false)

	// Store the encoder so Notify can push notifications on the same stream.
	s.writeMu.Lock()
	s.enc = enc
	s.writeMu.Unlock()
	defer func() {
		s.writeMu.Lock()
		s.enc = nil
		s.writeMu.Unlock()
	}()

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			resp := errorResponse(nil, ErrCodeParseError, "parse error: "+err.Error())
			s.writeMu.Lock()
			enc.Encode(resp)
			s.writeMu.Unlock()
			continue
		}

		if req.JSONRPC != "2.0" {
			if !req.isNotification() {
				resp := errorResponse(req.ID, ErrCodeInvalidRequest, "jsonrpc must be '2.0'")
				s.writeMu.Lock()
				enc.Encode(resp)
				s.writeMu.Unlock()
			}
			continue
		}

		// Notifications do not receive a response.
		if req.isNotification() {
			s.handleNotification(&req)
			continue
		}

		// IMPORTANT: do not hold writeMu across dispatch — Notify is called
		// within the dispatch goroutine and acquires writeMu itself.
		resp := s.dispatch(&req)
		s.writeMu.Lock()
		enc.Encode(resp)
		s.writeMu.Unlock()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner: %w", err)
	}
	return nil
}

// handleNotification handles messages without an id (no response expected).
func (s *Server) handleNotification(req *Request) {
	// notifications/initialized: no-op
	// other notification methods: silently ignored
}

// Notify writes a JSON-RPC notification to the active stdio encoder. It is a no-op
// when no stdio encoder is set (e.g. the HTTP transport), which gracefully degrades
// push (Layer 3) to the pull-based Layers 1-2.
func (s *Server) Notify(method string, params any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.enc == nil {
		return nil
	}
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	return s.enc.Encode(msg)
}

// dispatch routes the request to the appropriate method.
func (s *Server) dispatch(req *Request) Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		return successResponse(req.ID, map[string]interface{}{})
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	default:
		return errorResponse(req.ID, ErrCodeMethodNotFound, "method not found: "+req.Method)
	}
}

// handleInitialize handles the initial MCP negotiation.
func (s *Server) handleInitialize(req *Request) Response {
	// Extract the protocol version requested by the client.
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(req.Params) > 0 {
		json.Unmarshal(req.Params, &params)
	}

	// Negotiation: use the requested version if provided, otherwise ours.
	negotiated := SupportedProtocolVersion
	if params.ProtocolVersion != "" {
		// In M1 we accept any version provided by the client (echo).
		// Future: validate the version and downgrade if necessary.
		negotiated = params.ProtocolVersion
	}

	result := map[string]interface{}{
		"protocolVersion": negotiated,
		"capabilities": map[string]interface{}{
			"tools":     map[string]interface{}{},
			"resources": map[string]interface{}{},
			"skills":    map[string]interface{}{"listChanged": true},
		},
		"serverInfo": map[string]interface{}{
			"name":    "cartographer",
			"version": s.version,
		},
	}
	return successResponse(req.ID, result)
}

// handleToolsList responds with the list of registered tools.
func (s *Server) handleToolsList(req *Request) Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	descriptors := make([]toolDescriptor, 0, len(s.toolsOrd))
	for _, name := range s.toolsOrd {
		if s.agentProfile && ToolAdvanced(name) {
			continue
		}
		t := s.tools[name]
		schema := t.InputSchema
		if schema == nil {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		var annotations *toolAnnotations
		if t.ReadOnly {
			annotations = &toolAnnotations{ReadOnlyHint: true}
		}
		descriptors = append(descriptors, toolDescriptor{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
			Annotations: annotations,
		})
	}
	return successResponse(req.ID, map[string]interface{}{"tools": descriptors})
}

// handleToolsCall routes the call to the correct tool.
func (s *Server) handleToolsCall(req *Request) Response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, ErrCodeInvalidParams, "invalid params: "+err.Error())
	}

	s.mu.Lock()
	tool, ok := s.tools[params.Name]
	s.mu.Unlock()

	if !ok {
		return successResponse(req.ID, errorResult("tool not found: "+params.Name))
	}

	args := params.Arguments
	if args == nil {
		args = json.RawMessage(`{}`)
	}

	result, err := tool.Handler(args)
	if err != nil {
		// Internal server error (not an application error): return isError in the result.
		return successResponse(req.ID, errorResult("internal error: "+err.Error()))
	}
	return successResponse(req.ID, result)
}
