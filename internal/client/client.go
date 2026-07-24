// Package client implements a minimal MCP client over HTTP (JSON-RPC 2.0), used by
// `cartographer agents/connect/status/sync` to talk to a remote cartographer server.
// The client always uses the HTTP transport (see decisions.md D-client-http):
// generating stdio MCP configs is out of scope, the CLI itself is the only consumer.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// MCPClient is a minimal JSON-RPC 2.0 client for the MCP `tools/call` method.
type MCPClient struct {
	ServerURL string // e.g. "http://localhost:8080/mcp"
	Token     string // bearer token, empty = no Authorization header
	KB        string // optional KB name; appended as ?kb=<KB> (multi-KB server routing, see httpserver.go)
	HTTP      *http.Client
}

// Health is the additive subset of the server's /health response that clients
// use to distinguish a reachable-but-unusable empty multi-KB server from one
// that can accept MCP requests. Ready and KBs are pointers so callers can
// distinguish an older server that did not send either field from an explicit
// false/empty value.
type Health struct {
	Status string      `json:"status"`
	Ready  *bool       `json:"ready"`
	KBs    *[]HealthKB `json:"kbs"`
}

// HealthKB is the additive per-KB item returned by a MultiKB server's
// /health endpoint. A single-KB (and older) server omits the kbs field
// altogether, which callers distinguish through Health.KBs being nil.
type HealthKB struct {
	Name string `json:"name"`
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

// do sends a single JSON-RPC 2.0 request and returns the raw "result" field.
func (c *MCPClient) do(method string, params any) (json.RawMessage, error) {
	reqURL, err := c.requestURL()
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("client: marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("client: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("client: request to %s: %w", reqURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("client: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("client: %s: %w", reqURL, ErrUnauthorized)
		}
		return nil, fmt.Errorf("client: %s returned HTTP %d: %s", reqURL, resp.StatusCode, respBody)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("client: decode JSON-RPC response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("client: JSON-RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

// Ping performs a minimal round trip against the server to check reachability
// and, when a token is set, that it's accepted — without invoking any tool.
// It uses the JSON-RPC "ping" method (see mcpserver.dispatch), the cheapest
// request the protocol defines: no KB access, no tool lookup. timeout bounds
// this single call independently of the client's normal HTTP timeout (30s),
// so a probe before a full connect (D64) fails fast instead of hanging.
// Returns nil on success, ErrUnauthorized on HTTP 401, or the underlying
// network/timeout error otherwise.
func (c *MCPClient) Ping(timeout time.Duration) error {
	cp := *c
	hc := *c.HTTP
	hc.Timeout = timeout
	cp.HTTP = &hc
	_, err := cp.do("ping", nil)
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
		return nil, fmt.Errorf("client: health request to %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("client: %s: %w", u, ErrUnauthorized)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
		if readErr != nil {
			return nil, fmt.Errorf("client: read health response: %w", readErr)
		}
		return nil, fmt.Errorf("client: %s returned HTTP %d: %s", u, resp.StatusCode, body)
	}

	var health Health
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8*1024*1024)).Decode(&health); err != nil {
		return nil, fmt.Errorf("client: decode health response: %w", err)
	}
	return &health, nil
}

// Call invokes an MCP tool via tools/call and returns the decoded JSON payload from
// the tool's first text content block (the convention used by every cartographer
// tool: textResult/errorResult in internal/mcpserver/protocol.go).
func (c *MCPClient) Call(tool string, args any) (json.RawMessage, error) {
	raw, err := c.do("tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return nil, err
	}

	var tr toolResult
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("client: decode tool result for %q: %w", tool, err)
	}
	if len(tr.Content) == 0 {
		return nil, fmt.Errorf("client: tool %q returned no content", tool)
	}
	if tr.IsError {
		return nil, fmt.Errorf("client: tool %q returned an error: %s", tool, tr.Content[0].Text)
	}
	return json.RawMessage(tr.Content[0].Text), nil
}
