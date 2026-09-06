// Package client implements a minimal MCP client over HTTP (JSON-RPC 2.0), used by
// `cartographer agents/connect/status/sync` to talk to a remote cartographer server.
// The client always uses HTTP (see docs/decisions/client-configurator.md D37):
// generating stdio MCP configs is out of scope, the CLI itself is the only consumer.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrUnauthorized indicates the server rejected the request with HTTP 401 —
// distinguished from other failures (network down, timeout, 5xx) so callers
// probing a server before committing to a connect (Ping, D64) can tell "wrong
// token/env" apart from "server unreachable" and word their error accordingly.
var ErrUnauthorized = errors.New("unauthorized (401): check the bearer token/env var")

// RemoteState classifies a RemoteError as either the server being completely
// unreachable/unusable (RemoteUnavailable: DNS/connection failure, HTTP
// non-2xx, 401) or reached-but-this-call-failed (RemoteFailed: a malformed
// JSON-RPC response, a JSON-RPC error object, or a tool result with
// isError:true — e.g. an unqualified tool name after a healthy discovery,
// the D120 regression this taxonomy exists to distinguish). Callers
// (cmd/cartographer's status/TUI panels) render on this distinction instead
// of a single hardcoded "server unreachable".
type RemoteState string

const (
	RemoteUnavailable RemoteState = "unavailable"
	RemoteFailed      RemoteState = "error"
)

// Remote error codes (D120): stable, machine-readable strings surfaced in
// `cartographer status --output json` and matched by cmd/cartographer's
// unified classifier.
const (
	CodeDNSFailed    = "dns_failed"
	CodeUnreachable  = "unreachable"
	CodeUnauthorized = "unauthorized"
	CodeHTTPFailed   = "http_failed"
	CodeMCPFailed    = "mcp_failed"
)

// RemoteError is returned by MCPClient's do/Call/Health/Ping for every
// failure that involves the network or the remote server's response.
// Local-only errors that never reach the network (an invalid server URL, a
// JSON marshal failure) are not wrapped — only genuinely remote-shaped
// failures are, so errors.As(err, &client.RemoteError{}) reliably means "we
// talked to (or tried to talk to) the network for this call".
type RemoteError struct {
	State   RemoteState
	Code    string
	Message string
	Cause   error
}

func (e *RemoteError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

// Unwrap exposes Cause so errors.Is/As (including the pre-D120
// errors.Is(err, ErrUnauthorized) check callers already rely on) keeps
// working through a RemoteError.
func (e *RemoteError) Unwrap() error { return e.Cause }

// classifyDialErr distinguishes a DNS failure from any other dial-time
// network error (connection refused, timeout, ...), mirroring the
// pre-existing cmd/cartographer/status_snapshot.go classification.
func classifyDialErr(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return CodeDNSFailed
	}
	return CodeUnreachable
}

// MCPClient is a minimal JSON-RPC 2.0 client for the MCP `tools/call` method.
type MCPClient struct {
	ServerURL string // e.g. "http://localhost:39273/mcp"
	Token     string // bearer token, empty = no Authorization header
	KB        string // optional KB name; appended as ?kb=<KB> (multi-KB server routing, see httpserver.go)
	HTTP      *http.Client
	// Version identifies this client build in the server's roster. Empty is
	// reported as "unknown" rather than refused: a roster row with no version
	// is still useful, and a missing build stamp must not stop a sync.
	Version string
}

// Health is the additive subset of the server's /health response that clients
// use to distinguish a reachable-but-unusable empty multi-KB server from one
// that can accept MCP requests. Ready and KBs are pointers so callers can
// distinguish an older server that did not send either field from an explicit
// false/empty value.
type Health struct {
	Status  string      `json:"status"`
	Version string      `json:"version"`
	Ready   *bool       `json:"ready"`
	KBs     *[]HealthKB `json:"kbs"`
}

// HealthKB is the additive per-KB item returned by a MultiKB server's
// /health endpoint. A single-KB (and older) server omits the kbs field
// altogether, which callers distinguish through Health.KBs being nil.
// ToolPrefix (D120) is the effective, already-sanitised tool-name prefix
// (mcpserver.KBInfo.ToolPrefix) this KB's tools are registered under; empty
// means unprefixed, which is also what an older server (that never sent the
// field at all) decodes to — additive, byte-compatible with pre-D120
// servers/clients.
type HealthKB struct {
	Name       string `json:"name"`
	ToolPrefix string `json:"tool_prefix,omitempty"`
	// Capabilities is each per-KB gate's state and the configuration key that
	// controls it (D151). Optional and additive, like ToolPrefix: an older
	// server simply omits it and a client must stay silent rather than infer.
	Capabilities map[string]HealthCapability `json:"capabilities,omitempty"`
}

// HealthCapability is one gate's state plus the setting controlling it.
type HealthCapability struct {
	State   string `json:"state"`
	Setting string `json:"setting"`
}

// UnmarshalJSON accepts the current {"name":"..."} health shape and the
// short string array used by early multi-KB builds. Keeping both shapes makes
// client enumeration additive in the same way the optional kbs field is.
func (k *HealthKB) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		k.Name = name
		return nil
	}
	type wire HealthKB
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*k = HealthKB(value)
	return nil
}

// New creates an MCPClient for serverURL with an optional bearer token.
func New(serverURL, token string) *MCPClient {
	return &MCPClient{
		ServerURL: serverURL,
		Token:     token,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

// WithKB returns a copy of the client scoped to the given KB name (multi-KB server:
// appends ?kb=<name> to the request URL, see MultiKBServer.Handler in httpserver.go).
// An empty name targets the server's default single-KB endpoint.
func (c *MCPClient) WithKB(name string) *MCPClient {
	cp := *c
	cp.KB = name
	return &cp
}

// requestURL builds the effective request URL, appending ?kb=<KB> when set.
func (c *MCPClient) requestURL() (string, error) {
	if c.KB == "" {
		return c.ServerURL, nil
	}
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return "", fmt.Errorf("client: invalid server URL %q: %w", c.ServerURL, err)
	}
	q := u.Query()
	q.Set("kb", c.KB)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolResult mirrors mcpserver.ToolResult (content[0].text carries the tool's JSON payload).
type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// ProtocolVersion is the MCP revision this client speaks. It is sent on every
// request, in the body's _meta and mirrored in the headers, rather than agreed
// once in a handshake: 2026-07-28 removed initialize, and a stateless server
// decides per request.
const ProtocolVersion = "2026-07-28"

// Reserved _meta keys defined by the revision. They carry a slash, so they can
// never collide with an application key.
const (
	metaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
)

// clientName identifies this client in the server's roster.
const clientName = "cartographer"

// clientVersion is the build stamp reported to the server, or "unknown".
func (c *MCPClient) clientVersion() string {
	if c.Version == "" {
		return "unknown"
	}
	return c.Version
}

// withProtocolMeta returns params with the revision's reserved _meta keys
// merged in. params is left untouched: callers build plain maps and should not
// have to know the protocol decorates them.
func (c *MCPClient) withProtocolMeta(params any) any {
	meta := map[string]any{
		metaProtocolVersion: ProtocolVersion,
		metaClientInfo:      map[string]any{"name": clientName, "version": c.clientVersion()},
		// This client implements no optional capability.
		metaClientCapabilities: map[string]any{},
	}
	merged := map[string]any{"_meta": meta}
	if m, ok := params.(map[string]any); ok {
		for k, v := range m {
			if k != "_meta" {
				merged[k] = v
			}
		}
	}
	return merged
}

// do sends a single JSON-RPC 2.0 request and returns the raw "result" field.
func (c *MCPClient) do(method string, params any) (json.RawMessage, error) {
	reqURL, err := c.requestURL()
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: c.withProtocolMeta(params)})
	if err != nil {
		return nil, fmt.Errorf("client: marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("client: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// Both media types are required by the Streamable HTTP transport, and a
	// request missing either is refused before it reaches a handler. This
	// client never parses an event stream — Cartographer's server always
	// answers with a complete JSON response — but it must still declare that
	// it would accept one.
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	// The revision mirrors these fields in headers so an intermediary can route
	// without parsing the body; the server validates them against it.
	httpReq.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	httpReq.Header.Set("Mcp-Method", method)
	if method == "tools/call" {
		if m, ok := params.(map[string]any); ok {
			if name, ok := m["name"].(string); ok {
				// Tool names are ASCII by construction (registry names and the
				// "<prefix>__<tool>" form), so the plain header form always
				// applies and the RFC 2047 "=?base64?…?=" sentinel is never
				// needed. A future non-ASCII name would have to encode here.
				httpReq.Header.Set("Mcp-Name", name)
			}
		}
	}
	if c.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, &RemoteError{State: RemoteUnavailable, Code: classifyDialErr(err),
			Message: fmt.Sprintf("could not reach %s", reqURL), Cause: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, &RemoteError{State: RemoteUnavailable, Code: CodeHTTPFailed,
			Message: fmt.Sprintf("could not read response from %s", reqURL), Cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, &RemoteError{State: RemoteUnavailable, Code: CodeUnauthorized,
				Message: fmt.Sprintf("%s rejected the request", reqURL), Cause: ErrUnauthorized}
		}
		// 400 and 404 are the statuses the 2026-07-28 revision uses to carry a
		// JSON-RPC error (header mismatch, unsupported protocol version,
		// unknown method). Surfacing the code and message beats "returned HTTP
		// 400", which tells an operator nothing about which of the three it is.
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound {
			var rpcResp rpcResponse
			if err := json.Unmarshal(respBody, &rpcResp); err == nil && rpcResp.Error != nil {
				return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
					Message: fmt.Sprintf("JSON-RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)}
			}
		}
		return nil, &RemoteError{State: RemoteUnavailable, Code: CodeHTTPFailed,
			Message: fmt.Sprintf("%s returned HTTP %d", reqURL, resp.StatusCode),
			Cause:   fmt.Errorf("%s", respBody)}
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
			Message: fmt.Sprintf("invalid JSON-RPC response from %s", reqURL), Cause: err}
	}
	if rpcResp.Error != nil {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
			Message: fmt.Sprintf("JSON-RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)}
	}
	return rpcResp.Result, nil
}

// Ping performs a minimal round trip against the server to check reachability
// and, when a token is set, that it's accepted — without invoking any tool.
// It uses "server/discover": the "ping" method it used before is gone in the
// 2026-07-28 revision (D128), while server/discover is mandatory on any
// conformant server and just as cheap — no KB access, no tool lookup. The Go
// name stays Ping because reachability, not the wire method, is what callers
// ask for. timeout bounds this single call independently of the client's
// normal HTTP timeout (30s), so a probe before a full connect (D64) fails fast
// instead of hanging. Returns nil on success, ErrUnauthorized on HTTP 401, or
// the underlying network/timeout error otherwise.
//
// The request carries the 2026-07-28 metadata like every other: server/discover
// only exists in that revision, so a handshake-era request naming it is an
// unknown method.
func (c *MCPClient) Ping(timeout time.Duration) error {
	cp := *c
	hc := *c.HTTP
	hc.Timeout = timeout
	cp.HTTP = &hc
	_, err := cp.do("server/discover", nil)
	return err
}

// Health fetches GET /health for the configured MCP endpoint. serverURL
// normally ends in /mcp; only that terminal path segment is stripped, leaving
// deployments whose endpoint is rooted elsewhere intact. Like Ping, timeout
// applies only to this call and does not mutate the client shared by later
// sync_pull requests.
func (c *MCPClient) Health(timeout time.Duration) (*Health, error) {
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("client: invalid server URL %q: %w", c.ServerURL, err)
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/mcp")
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/") + "/health"

	hc := *c.HTTP
	hc.Timeout = timeout
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("client: build health request: %w", err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, &RemoteError{State: RemoteUnavailable, Code: classifyDialErr(err),
			Message: fmt.Sprintf("could not reach %s", u), Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, &RemoteError{State: RemoteUnavailable, Code: CodeUnauthorized,
				Message: fmt.Sprintf("%s rejected the request", u), Cause: ErrUnauthorized}
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
		if readErr != nil {
			return nil, &RemoteError{State: RemoteUnavailable, Code: CodeHTTPFailed,
				Message: fmt.Sprintf("could not read response from %s", u), Cause: readErr}
		}
		return nil, &RemoteError{State: RemoteUnavailable, Code: CodeHTTPFailed,
			Message: fmt.Sprintf("%s returned HTTP %d", u, resp.StatusCode), Cause: fmt.Errorf("%s", body)}
	}

	var health Health
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8*1024*1024)).Decode(&health); err != nil {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
			Message: fmt.Sprintf("invalid health response from %s", u), Cause: err}
	}
	return &health, nil
}

// Call invokes an MCP tool via tools/call and preserves all text content blocks.
func (c *MCPClient) Call(tool string, args any) (json.RawMessage, error) {
	raw, err := c.do("tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return nil, err
	}

	var tr toolResult
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
			Message: fmt.Sprintf("invalid tool result for %q", tool), Cause: err}
	}
	if len(tr.Content) == 0 {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
			Message: fmt.Sprintf("tool %q returned no content", tool)}
	}
	if tr.IsError {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
			Message: fmt.Sprintf("tool %q returned an error: %s", tool, tr.Content[0].Text)}
	}
	texts := make([]string, 0, len(tr.Content))
	for _, block := range tr.Content {
		if block.Type == "text" {
			texts = append(texts, block.Text)
		}
	}
	if len(texts) == 1 {
		return json.RawMessage(texts[0]), nil
	}
	combined, _ := json.Marshal(texts)
	return combined, nil
}
