// Package client is a minimal MCP client over HTTP (JSON-RPC 2.0). The
// `cartographer agents/connect/status/sync` commands use it to talk to a remote
// cartographer server.
// The client always uses HTTP (see D37):
// generating stdio MCP configs is out of scope, the CLI itself is the only consumer.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

// ErrUnauthorized indicates the server rejected the request with HTTP 401 —
// distinguished from other failures (network down, timeout, 5xx) so callers
// probing a server before committing to a connect (Ping, D64) can tell "wrong
// token/env" apart from "server unreachable" and word their error accordingly.
var ErrUnauthorized = errors.New("unauthorized (401): check the bearer token/env var")

// unauthorizedCause is the Cause a 401 carries when the client knows which
// environment variable it read the bearer token from. Its message replaces
// ErrUnauthorized's generic "check the bearer token/env var" — the env var is
// precisely what the user does not know — and separates the two cases that need
// different fixes: nothing was sent at all, or what was sent was refused.
// It unwraps to ErrUnauthorized so the errors.Is checks in cmd/cartographer
// (connect, status, sync) keep matching whatever the wording becomes (D222).
type unauthorizedCause struct{ msg string }

func (e *unauthorizedCause) Error() string { return e.msg }
func (e *unauthorizedCause) Unwrap() error { return ErrUnauthorized }

// RemoteState classifies a RemoteError as either the server being completely
// unreachable/unusable (RemoteUnavailable: DNS/connection failure, HTTP
// non-2xx, 401) or reached-but-this-call-failed (RemoteFailed: a malformed
// JSON-RPC response, a JSON-RPC error object, or a tool result with
// isError:true — e.g. an unqualified tool name after a healthy discovery,
// the D120 regression this taxonomy exists to distinguish). Callers
// (cmd/cartographer's status) render on this distinction instead
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
	// CodeStarting is a reachable server whose /health says it is still
	// bootstrapping its KBs: not a network failure, retry shortly.
	CodeStarting = "starting"
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
	// HTTPStatus is the response status of a RemoteError that carries one
	// (CodeHTTPFailed from an answering server), zero otherwise. It lets a
	// caller tell a refused credential (403) from a transport failure (#1299).
	HTTPStatus int
	// RPCCode is the JSON-RPC error code of a CodeMCPFailed that came from an
	// "error" object (for example -32602, invalid params), zero otherwise. It
	// lets a caller tell a refused argument from a failure without parsing
	// Message.
	RPCCode int
	// ServerProtocol and ServerMinProtocol are the component protocol window a
	// server named when it refused the request with 426 (protocol_unsupported);
	// zero otherwise.
	ServerProtocol, ServerMinProtocol int
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

// unreachableMessage words a failed request. A timeout while awaiting headers
// means the server accepted the request and is still working on it -- in
// practice a KB whose git remote is slow or down (#348) -- so "could not
// reach" would send the operator after a healthy local server. net/http has
// no typed error for that phase, only this text.
func unreachableMessage(reqURL string, err error) string {
	if strings.Contains(err.Error(), "awaiting headers") {
		return fmt.Sprintf("%s accepted the request but did not answer in time; "+
			"the server is up, so look at its log for a KB whose git remote is slow or unreachable", reqURL)
	}
	return fmt.Sprintf("could not reach %s", reqURL)
}

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
	ServerURL string // e.g. "http://127.0.0.1:39273/mcp"
	// AllowInsecureHTTP lets requests go to a non-loopback http:// URL (see CheckServerURL).
	AllowInsecureHTTP bool
	Token             string // bearer token, empty = no Authorization header
	// TokenEnv names the environment variable Token was read from, so a 401
	// can say which variable to look at (D222). Empty when the caller has no
	// such variable, and the 401 message is then exactly what it was before.
	TokenEnv string
	KB       string // optional KB name; appended as ?kb=<KB> (multi-KB server routing, see httpserver.go)
	HTTP     *http.Client
	// Version identifies this client build in the server's roster. Empty is
	// reported as "unknown" rather than refused: a roster row with no version
	// is still useful, and a missing build stamp must not stop a sync.
	Version string
	// Author is who this client writes as, "Name <email>" (D904), sent on
	// tools/call only. Empty sends nothing and the server authors the write as
	// the token.
	Author string

	// legacyComponents is set (atomically) once a 404 on the component family
	// showed the server predates it (a 0.28 server): the two reports that were
	// released then go straight to their 0.28 routes afterwards.
	legacyComponents int32
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
	// MountMode is "routed" when the server serves every KB through a single
	// endpoint with the KB as a tool argument (D187). Empty — which is what a
	// per-KB server and every pre-D187 server decode to — means the historical
	// one-endpoint-per-KB topology. Additive and byte-compatible.
	MountMode string `json:"mount_mode,omitempty"`
	// RoutedPath is that endpoint's path, named by the server rather than
	// assumed by the client, so the two cannot drift.
	RoutedPath string `json:"routed_path,omitempty"`
	// LatestVersion is a newer release the server knows of (D254). Absent
	// means unknown or up to date; the server never names a command, since
	// how it was deployed is not knowable from inside.
	LatestVersion string `json:"latest_version,omitempty"`
	// ComponentProtocol and ComponentMinProtocol are the component protocol
	// window of the server (internal/componentproto); zero on a server that
	// predates it. Public, so a Surveyor can refuse an unsupported server
	// before its first beat.
	ComponentProtocol    int `json:"component_protocol,omitempty"`
	ComponentMinProtocol int `json:"component_min_protocol,omitempty"`
	// Bootstrapping is true only while the server is still cloning and
	// indexing its KBs (D348). Health turns it into an error: the answer
	// carries no KB list, and reading it as "zero KBs" would let sync prune
	// managed files against an empty list.
	Bootstrapping bool `json:"bootstrapping,omitempty"`
}

// Routed reports whether this server serves a routed mount (D187).
func (h *Health) Routed() bool { return h != nil && h.MountMode == "routed" && h.RoutedPath != "" }

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

// WithHTTPTimeout returns a copy of the client whose HTTP calls time out after d
// (the default is 30 s, and calls take no context). The surveyor writes its closing
// entries with a short one so that a shutdown fits the orchestrator's grace period.
func (c *MCPClient) WithHTTPTimeout(d time.Duration) *MCPClient {
	cp := *c
	hc := *HTTPClient(c.HTTP, c.AllowInsecureHTTP)
	hc.Timeout = d
	cp.HTTP = &hc
	return &cp
}

// WithInsecureHTTP returns a copy of the client that does (or does not) accept
// a non-loopback http:// server URL. The default refuses it (CheckServerURL).
func (c *MCPClient) WithInsecureHTTP(allow bool) *MCPClient {
	cp := *c
	cp.AllowInsecureHTTP = allow
	return &cp
}

// WithTokenEnv returns a copy of the client that knows which environment
// variable its token came from. Only the message of a 401 changes; nothing is
// read from the environment here, and an empty name is the legacy behaviour.
func (c *MCPClient) WithTokenEnv(name string) *MCPClient {
	cp := *c
	cp.TokenEnv = name
	return &cp
}

// WithAuthor returns a copy of the client that names who its tool calls write
// as. The caller resolves it; nothing is read from git or the config here.
func (c *MCPClient) WithAuthor(author string) *MCPClient {
	cp := *c
	cp.Author = author
	return &cp
}

// unauthorizedCauseFor builds the Cause of a 401 for this client's credential.
func (c *MCPClient) unauthorizedCauseFor() error {
	switch {
	case c.TokenEnv == "":
		return ErrUnauthorized
	case c.Token == "":
		return &unauthorizedCause{fmt.Sprintf("unauthorized (401): $%s is unset or empty, so no bearer token was sent", c.TokenEnv)}
	default:
		return &unauthorizedCause{fmt.Sprintf("unauthorized (401): the bearer token from $%s was rejected", c.TokenEnv)}
	}
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

// ClientName identifies this client in the server's roster, and tells the
// server a caller is the CLI, which decodes one JSON answer per tool call and
// so never receives the doctor nudge (D299).
const ClientName = "cartographer"

// AuthorHeader carries the author of a tool call's writes, as "Name <email>"
// (D904) in its EncodeAuthor form. The server reads it, and the clients send it
// on tools/call only: it names a person, so it goes where a commit can come of
// it.
const AuthorHeader = "Cartographer-Author"

// withProtocolMeta returns params with the revision's reserved _meta keys
// merged in. params is left untouched: callers build plain maps and should not
// have to know the protocol decorates them.
func (c *MCPClient) withProtocolMeta(params any) any {
	merged := map[string]any{"_meta": RequestMeta(c.Version)}
	if m, ok := params.(map[string]any); ok {
		for k, v := range m {
			if k != "_meta" {
				merged[k] = v
			}
		}
	}
	return merged
}

// RequestMeta is the _meta object of one request: the revision, this client's
// identity and no optional capability. version is the build stamp, "unknown"
// when empty. `cartographer call` builds its requests with it too.
func RequestMeta(version string) map[string]any {
	if version == "" {
		version = "unknown"
	}
	return map[string]any{
		metaProtocolVersion: ProtocolVersion,
		metaClientInfo:      map[string]any{"name": ClientName, "version": version},
		// This client implements no optional capability.
		metaClientCapabilities: map[string]any{},
	}
}

// do sends a single JSON-RPC 2.0 request and returns the raw "result" field.
func (c *MCPClient) do(method string, params any) (json.RawMessage, error) {
	return c.doCtx(context.Background(), method, params)
}

// doCtx is do under a context: when it ends the request is abandoned.
func (c *MCPClient) doCtx(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := CheckServerURL(c.ServerURL, c.AllowInsecureHTTP); err != nil {
		return nil, err
	}
	reqURL, err := c.requestURL()
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: c.withProtocolMeta(params)})
	if err != nil {
		return nil, fmt.Errorf("client: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
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
		if c.Author != "" {
			httpReq.Header.Set(AuthorHeader, EncodeAuthor(c.Author))
		}
	}
	if c.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := HTTPClient(c.HTTP, c.AllowInsecureHTTP).Do(httpReq)
	if err != nil {
		return nil, &RemoteError{State: RemoteUnavailable, Code: classifyDialErr(err),
			Message: unreachableMessage(reqURL, err), Cause: err}
	}
	defer resp.Body.Close()

	// Read one byte past the cap so an oversized response is reported as such
	// instead of surfacing as "unexpected end of JSON input" (sync_pull carries
	// every artifact's content in one response).
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, &RemoteError{State: RemoteUnavailable, Code: CodeHTTPFailed,
			Message: fmt.Sprintf("could not read response from %s", reqURL), Cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, &RemoteError{State: RemoteUnavailable, Code: CodeUnauthorized,
				Message: fmt.Sprintf("%s rejected the request", reqURL), Cause: c.unauthorizedCauseFor()}
		}
		// 400 and 404 are the statuses the 2026-07-28 revision uses to carry a
		// JSON-RPC error (header mismatch, unsupported protocol version,
		// unknown method). Surfacing the code and message beats "returned HTTP
		// 400", which tells an operator nothing about which of the three it is.
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound {
			var rpcResp rpcResponse
			if err := json.Unmarshal(respBody, &rpcResp); err == nil && rpcResp.Error != nil {
				return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed, RPCCode: rpcResp.Error.Code,
					Message: fmt.Sprintf("JSON-RPC error %d: %s", rpcResp.Error.Code, Escape(rpcResp.Error.Message))}
			}
		}
		return nil, &RemoteError{State: RemoteUnavailable, Code: CodeHTTPFailed,
			Message: fmt.Sprintf("%s returned HTTP %d", reqURL, resp.StatusCode),
			Cause:   fmt.Errorf("%s", Escape(bodyPrefix(respBody))), HTTPStatus: resp.StatusCode}
	}

	// Checked after the status switch: a 401 or a 5xx with a big body keeps
	// its own classification instead of reading as "over 8 MiB".
	if len(respBody) > maxResponseBytes {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeHTTPFailed,
			Message: fmt.Sprintf("response over 8 MiB from %s", reqURL)}
	}
	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
			Message: fmt.Sprintf("invalid JSON-RPC response from %s", reqURL), Cause: err}
	}
	if rpcResp.Error != nil {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed, RPCCode: rpcResp.Error.Code,
			Message: fmt.Sprintf("JSON-RPC error %d: %s", rpcResp.Error.Code, Escape(rpcResp.Error.Message))}
	}
	return rpcResp.Result, nil
}

// maxResponseBytes caps one MCP response body.
const maxResponseBytes = 8 * 1024 * 1024

// maxCauseBytes bounds the body text carried in an HTTP error's Cause.
const maxCauseBytes = 512

// bodyPrefix returns at most maxCauseBytes of an error body, marked when cut.
func bodyPrefix(b []byte) string {
	if len(b) <= maxCauseBytes {
		return string(b)
	}
	return string(b[:maxCauseBytes]) + "…"
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
	hc := *HTTPClient(c.HTTP, c.AllowInsecureHTTP)
	hc.Timeout = timeout
	cp.HTTP = &hc
	_, err := cp.do("server/discover", nil)
	return err
}

// mcpSubpathHint names the server URL when serverURL has a path segment after
// /mcp (a routed KB path such as /mcp/routed): the health route is derived from
// the URL by stripping a terminal /mcp, so a longer path asks for a route that
// does not exist and the 404 says nothing of why (#1282). Empty otherwise.
func mcpSubpathHint(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}
	p := strings.TrimRight(u.Path, "/")
	i := strings.LastIndex(p, "/mcp/")
	if i < 0 {
		return ""
	}
	u.Path, u.RawQuery, u.Fragment = p[:i]+"/mcp", "", ""
	return fmt.Sprintf(" (the server URL is %s, without the trailing %q)", u, p[i+len("/mcp"):])
}

// ServerBase is the server's base URL for an MCP endpoint URL: the terminal
// /mcp segment, a trailing slash, the query and the fragment are dropped, so
// "https://host/prefix/mcp" gives a URL whose path is "/prefix". Health and
// AtlasURL both build on it, so the routes they derive cannot drift.
func ServerBase(serverURL string) (*url.URL, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("client: invalid server URL %q: %w", serverURL, err)
	}
	u.Path = strings.TrimRight(strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/mcp"), "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

// AtlasURL is where the server at serverURL serves the Atlas UI: its base
// (ServerBase) plus /ui/, without any userinfo. Whether the server has the
// Atlas enabled is not known here.
func AtlasURL(serverURL string) (string, error) {
	u, err := ServerBase(serverURL)
	if err != nil {
		return "", err
	}
	u.User = nil
	u.Path += "/ui/"
	return u.String(), nil
}

// Health fetches GET /health for the configured MCP endpoint. serverURL
// normally ends in /mcp; only that terminal path segment is stripped, leaving
// deployments whose endpoint is rooted elsewhere intact. Like Ping, timeout
// applies only to this call and does not mutate the client shared by later
// sync_pull requests.
func (c *MCPClient) Health(timeout time.Duration) (*Health, error) {
	if err := CheckServerURL(c.ServerURL, c.AllowInsecureHTTP); err != nil {
		return nil, err
	}
	u, err := ServerBase(c.ServerURL)
	if err != nil {
		return nil, err
	}
	u.Path += "/health"

	hc := *HTTPClient(c.HTTP, c.AllowInsecureHTTP)
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
				Message: fmt.Sprintf("%s rejected the request", u), Cause: c.unauthorizedCauseFor()}
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
		if readErr != nil {
			return nil, &RemoteError{State: RemoteUnavailable, Code: CodeHTTPFailed,
				Message: fmt.Sprintf("could not read response from %s", u), Cause: readErr}
		}
		msg := fmt.Sprintf("%s returned HTTP %d", u, resp.StatusCode)
		if resp.StatusCode == http.StatusNotFound {
			msg += mcpSubpathHint(c.ServerURL)
		}
		return nil, &RemoteError{State: RemoteUnavailable, Code: CodeHTTPFailed,
			Message: msg, Cause: fmt.Errorf("%s", Escape(string(body))), HTTPStatus: resp.StatusCode}
	}

	var health Health
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8*1024*1024)).Decode(&health); err != nil {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
			Message: fmt.Sprintf("invalid health response from %s", u), Cause: err}
	}
	if health.Bootstrapping {
		return nil, &RemoteError{State: RemoteUnavailable, Code: CodeStarting,
			Message: "server is bootstrapping its KBs, retry shortly"}
	}
	return &health, nil
}

// ReportUsage posts a usage report (a JSON-encodable list of entries) for one
// KB to the server's component usage route (D326). Like Health, it derives the
// origin from the MCP endpoint URL and applies timeout to this call only. It is
// best-effort by design — the caller logs and goes on — so any non-2xx is an
// error the caller is free to ignore. Against a 0.28 server (404 on the
// component route) it falls back to /api/usage, see reportReleased.
func (c *MCPClient) ReportUsage(kbName string, entries any, timeout time.Duration) error {
	return c.reportReleased(componentproto.UsagePath, componentproto.LegacyUsagePath, kbName, entries, entries, timeout)
}

// reportReleased posts one of the two reports 0.28 already had a route for.
// A 404 on the component route is a server that predates the family (or an
// unknown KB, which the legacy route answers the same way): the report is
// retried once on the 0.28 route with the 0.28 body, and the client remembers
// so later calls go straight there. The Surveyor's routes were never released
// and have no fallback.
func (c *MCPClient) reportReleased(route, legacy, kbName string, body, legacyBody any, timeout time.Duration) error {
	if atomic.LoadInt32(&c.legacyComponents) == 0 {
		err := c.apiCall(http.MethodPost, route, kbName, body, timeout)
		var re *RemoteError
		if !errors.As(err, &re) || re.HTTPStatus != http.StatusNotFound {
			return err
		}
		atomic.StoreInt32(&c.legacyComponents, 1)
	}
	return c.apiCall(http.MethodPost, legacy, kbName, legacyBody, timeout)
}

// ReportClientSettings posts this machine's client settings to the server, so
// the Atlas can show the viewer their own client (D818). The report is a JSON
// object; the component and the protocol are added here. Best-effort like
// ReportUsage.
func (c *MCPClient) ReportClientSettings(report any, timeout time.Duration) error {
	body, err := settingsBody(componentproto.KindClient, report)
	if err != nil {
		return err
	}
	return c.reportReleased(componentproto.SettingsPath, componentproto.LegacyClientSettingsPath, "", body, report, timeout)
}

// ReportSurveyorSettings posts a surveyor's settings report. Best-effort like
// ReportClientSettings.
func (c *MCPClient) ReportSurveyorSettings(report any, timeout time.Duration) error {
	return c.reportSettings(componentproto.KindSurveyor, report, timeout)
}

// reportSettings posts one settings report with the `component` and `protocol`
// the settings route reads, set over whatever the report holds.
func (c *MCPClient) reportSettings(component string, report any, timeout time.Duration) error {
	obj, err := settingsBody(component, report)
	if err != nil {
		return err
	}
	return c.apiCall(http.MethodPost, componentproto.SettingsPath, "", obj, timeout)
}

// settingsBody is the report with the `component` and `protocol` the settings
// route reads.
func settingsBody(component string, report any) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("client: encode settings report: %w", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("client: a settings report must be a JSON object")
	}
	obj["component"], _ = json.Marshal(component)
	obj["protocol"], _ = json.Marshal(componentproto.Protocol)
	return obj, nil
}

// HeartbeatAnswer is the answer of the component heartbeat: the idle signal of
// each accepted KB, whether the server wants the settings report, and the
// protocol window the server speaks (zero on a server that predates the field).
type HeartbeatAnswer struct {
	Accepted []string `json:"accepted"`
	KBs      map[string]struct {
		LastActivityAt *time.Time `json:"last_activity_at"`
	} `json:"kbs"`
	ActivityKnown   bool      `json:"activity_known"`
	ServerStartedAt time.Time `json:"server_started_at"`
	Now             time.Time `json:"now"`
	SettingsWanted  bool      `json:"settings_wanted"`
	RunsWanted      bool      `json:"runs_wanted"`
	Protocol        int       `json:"protocol"`
	MinProtocol     int       `json:"min_protocol"`
}

// Heartbeat posts a surveyor's declaration (a JSON-encodable object that
// carries its own `protocol`) and returns the server's answer. A server too old
// for the route answers 404, an error the surveyor ignores; one that refuses
// the surveyor's protocol answers 426, a *RemoteError with HTTPStatus 426 and
// the server's numbers in ServerProtocol and ServerMinProtocol.
func (c *MCPClient) Heartbeat(report any, timeout time.Duration) (*HeartbeatAnswer, error) {
	var a HeartbeatAnswer
	if err := c.apiCallInto(http.MethodPost, componentproto.HeartbeatPath, "", report, &a, timeout); err != nil {
		return nil, err
	}
	return &a, nil
}

// RunsAnswer is the answer of the runs report.
type RunsAnswer struct {
	Received  int    `json:"received"`
	LastRunID string `json:"last_run_id"`
}

// ReportRuns posts a surveyor's finished runs and current alarms (a
// JSON-encodable object that carries its own `protocol`). Errors are mapped as
// on the heartbeat: 404 and 426 are *RemoteError.
func (c *MCPClient) ReportRuns(report any, timeout time.Duration) (*RunsAnswer, error) {
	var a RunsAnswer
	if err := c.apiCallInto(http.MethodPost, componentproto.RunsPath, "", report, &a, timeout); err != nil {
		return nil, err
	}
	return &a, nil
}

// apiCall sends one request to a server /api route (client-to-server metadata,
// not an MCP call) with the same auth and error mapping for every caller.
func (c *MCPClient) apiCall(method, route, kbName string, body any, timeout time.Duration) error {
	return c.apiCallInto(method, route, kbName, body, nil, timeout)
}

// apiCallInto is apiCall that also decodes a 2xx JSON answer into out (nil
// keeps the old behaviour: the body is read, capped at 1 MiB, and dropped).
func (c *MCPClient) apiCallInto(method, route, kbName string, body, out any, timeout time.Duration) error {
	if err := CheckServerURL(c.ServerURL, c.AllowInsecureHTTP); err != nil {
		return err
	}
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return fmt.Errorf("client: invalid server URL %q: %w", c.ServerURL, err)
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/mcp")
	u.Path = strings.TrimRight(u.Path, "/") + route
	u.Fragment = ""
	q := url.Values{}
	if kbName != "" {
		q.Set("kb", kbName)
	}
	// A body that is not an object (usage is an array) cannot carry the
	// protocol, so on the component family it travels in the query for every
	// call; the routes that read it from the body ignore this one.
	if strings.HasPrefix(route, componentproto.Prefix) {
		q.Set("protocol", strconv.Itoa(componentproto.Protocol))
	}
	u.RawQuery = q.Encode()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("client: encode %s body: %w", route, err)
		}
		reader = bytes.NewReader(raw)
	}
	hc := *HTTPClient(c.HTTP, c.AllowInsecureHTTP)
	hc.Timeout = timeout
	req, err := http.NewRequest(method, u.String(), reader)
	if err != nil {
		return fmt.Errorf("client: build %s request: %w", route, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &RemoteError{State: RemoteUnavailable, Code: classifyDialErr(err),
			Message: fmt.Sprintf("could not reach %s", u.Redacted()), Cause: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return &RemoteError{State: RemoteUnavailable, Code: CodeUnauthorized,
			Message: fmt.Sprintf("%s rejected the request", u.Redacted()), Cause: c.unauthorizedCauseFor()}
	case resp.StatusCode == http.StatusUpgradeRequired:
		re := &RemoteError{State: RemoteUnavailable, Code: CodeHTTPFailed,
			Message: fmt.Sprintf("%s returned HTTP %d", u.Redacted(), resp.StatusCode), HTTPStatus: resp.StatusCode}
		var body struct {
			Error struct {
				Protocol    int `json:"protocol"`
				MinProtocol int `json:"min_protocol"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &body) == nil {
			re.ServerProtocol, re.ServerMinProtocol = body.Error.Protocol, body.Error.MinProtocol
		}
		return re
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &RemoteError{State: RemoteUnavailable, Code: CodeHTTPFailed,
			Message: fmt.Sprintf("%s returned HTTP %d", u.Redacted(), resp.StatusCode), HTTPStatus: resp.StatusCode}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
				Message: fmt.Sprintf("invalid answer from %s", u.Redacted()), Cause: err}
		}
	}
	return nil
}

// decodeToolResult parses a tools/call result; Call and CallTool share it so
// the "invalid tool result" wording lives in one place.
func decodeToolResult(tool string, raw json.RawMessage) (toolResult, error) {
	var tr toolResult
	if err := json.Unmarshal(raw, &tr); err != nil {
		return tr, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
			Message: fmt.Sprintf("invalid tool result for %q", tool), Cause: err}
	}
	return tr, nil
}

// ToolOutput is a tool result that keeps the tool-level isError flag instead
// of turning it into a Go error (see CallTool).
type ToolOutput struct {
	Text    string
	IsError bool
}

// CallTool invokes a tool like Call, but returns the result text and the
// tool's own isError flag so a caller can hand a tool-level refusal back to a
// model. Transport and JSON-RPC failures remain *RemoteError.
func (c *MCPClient) CallTool(tool string, args any) (ToolOutput, error) {
	raw, err := c.do("tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return ToolOutput{}, err
	}
	tr, err := decodeToolResult(tool, raw)
	if err != nil {
		return ToolOutput{}, err
	}
	if len(tr.Content) == 0 {
		return ToolOutput{IsError: true, Text: "tool returned no content"}, nil
	}
	texts := make([]string, 0, len(tr.Content))
	for _, block := range tr.Content {
		if block.Type == "text" {
			texts = append(texts, block.Text)
		}
	}
	return ToolOutput{Text: strings.Join(texts, "\n"), IsError: tr.IsError}, nil
}

// Call invokes an MCP tool via tools/call and preserves all text content blocks.
func (c *MCPClient) Call(tool string, args any) (json.RawMessage, error) {
	return c.CallContext(context.Background(), tool, args)
}

// CallContext is Call under a context: when it ends the request is abandoned,
// so a caller that reports a deadline does not leave a write running.
func (c *MCPClient) CallContext(ctx context.Context, tool string, args any) (json.RawMessage, error) {
	raw, err := c.doCtx(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return nil, err
	}

	tr, err := decodeToolResult(tool, raw)
	if err != nil {
		return nil, err
	}
	if len(tr.Content) == 0 {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
			Message: fmt.Sprintf("tool %q returned no content", tool)}
	}
	if tr.IsError {
		return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed,
			Message: fmt.Sprintf("tool %q returned an error: %s", tool, Escape(tr.Content[0].Text))}
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

// ToolInfo is one tool of tools/list: its name, whether the server annotates
// it read-only (what a plugin's kb.call needs), and the description and input
// schema a surveyor hands to a model. InputSchema is passed through untouched.
type ToolInfo struct {
	Name        string
	ReadOnly    bool
	Description string
	InputSchema json.RawMessage
}

// ListTools returns every tool of tools/list (the server filters it per token),
// following nextCursor. ReadOnly is the server's own readOnlyHint, so the
// classification is never copied here; an absent annotation is false. A tool
// without a name is skipped: nothing can call it.
func (c *MCPClient) ListTools() ([]ToolInfo, error) {
	return c.ListToolsContext(context.Background())
}

// ListToolsContext is ListTools under a context.
func (c *MCPClient) ListToolsContext(ctx context.Context) ([]ToolInfo, error) {
	var out []ToolInfo
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.doCtx(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var res struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
				Annotations *struct {
					ReadOnlyHint bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed, Message: "invalid tool list", Cause: err}
		}
		for _, t := range res.Tools {
			if t.Name == "" {
				continue
			}
			out = append(out, ToolInfo{
				Name:        t.Name,
				ReadOnly:    t.Annotations != nil && t.Annotations.ReadOnlyHint,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
		if res.NextCursor == "" || res.NextCursor == cursor {
			return out, nil
		}
		cursor = res.NextCursor
	}
	// A server that never stops paging is not trusted with a partial list: a
	// missing tool would read as a write, but a caller must not run on half a list.
	return nil, &RemoteError{State: RemoteFailed, Code: CodeMCPFailed, Message: "the tool list never ends"}
}

// UnreachableMessage is the text of a request that never got an answer, for
// callers that speak HTTP themselves but must word the failure the same way
// (the stdio bridge, D737).
func UnreachableMessage(reqURL string, err error) string { return unreachableMessage(reqURL, err) }

// UnauthorizedError is the cause of a 401 for this client's credential, naming
// the environment variable when there is one (D222).
func (c *MCPClient) UnauthorizedError() error { return c.unauthorizedCauseFor() }
