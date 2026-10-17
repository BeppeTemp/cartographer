package mcpbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// rootsTimeout bounds the wait for a client's answer to roots/list. A client
// that declares roots and never answers must not hang the session.
const rootsTimeout = 5 * time.Second

// maxLine is the largest single JSON-RPC message accepted on stdin.
const maxLine = 16 * 1024 * 1024

// Bridge proxies one stdio MCP session to the routed endpoint, restricted to
// the KBs of the folder it was started in.
type Bridge struct {
	Cfg      *clientconfig.Config
	Provider string
	// Dir is the folder the session runs in: the process working directory.
	Dir string
	// Token is the bearer token (from cfg.TokenEnv), empty when auth is off.
	Token   string
	Version string
	HTTP    *http.Client
	In      io.Reader
	Out     io.Writer
	Log     io.Writer

	outMu sync.Mutex

	mu         sync.Mutex // guards the fields below
	scope      *Scope
	rootsAware bool
	protocol   string
	sessionID  string
}

// line is one message read from stdin.
type line struct {
	raw []byte
	err error
}

// message is the part of a JSON-RPC message the bridge looks at.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

func (m message) isRequest() bool  { return m.Method != "" && len(m.ID) > 0 }
func (m message) isResponse() bool { return m.Method == "" && len(m.ID) > 0 }

// Scope returns the scope in force, resolving it from the working directory on
// first use.
func (b *Bridge) currentScope() Scope {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.scope == nil {
		s := Resolve(b.Cfg, b.Provider, b.Dir)
		b.scope = &s
		b.logf("scope for %s: %s", b.Dir, s.Describe())
	}
	return *b.scope
}

func (b *Bridge) logf(format string, args ...any) {
	if b.Log != nil {
		fmt.Fprintf(b.Log, "cartographer mcp: "+format+"\n", args...)
	}
}

// Run serves the session until stdin closes or ctx is done.
func (b *Bridge) Run(ctx context.Context) error {
	if b.HTTP == nil {
		b.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	lines := make(chan line)
	go func() {
		defer close(lines)
		r := bufio.NewReaderSize(b.In, 64*1024)
		for {
			raw, err := r.ReadBytes('\n')
			if len(raw) > maxLine {
				lines <- line{err: errors.New("message too large")}
				return
			}
			// \r\n is tolerated: Windows clients and shells write it.
			raw = bytes.TrimSpace(raw)
			if len(raw) > 0 {
				cp := append([]byte(nil), raw...)
				select {
				case lines <- line{raw: cp}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					lines <- line{err: err}
				}
				return
			}
		}
	}()

	var wg sync.WaitGroup
	defer wg.Wait()
	var stash [][]byte
	next := func() (line, bool) {
		if len(stash) > 0 {
			l := line{raw: stash[0]}
			stash = stash[1:]
			return l, true
		}
		select {
		case l, ok := <-lines:
			return l, ok
		case <-ctx.Done():
			return line{}, false
		}
	}

	for {
		l, ok := next()
		if !ok {
			return nil
		}
		if l.err != nil {
			return l.err
		}
		var m message
		if err := json.Unmarshal(l.raw, &m); err != nil {
			// A batch (JSON array) or garbage: forward untouched and let the
			// server answer. Batches are not parsed, so they cannot carry the
			// handshake, which is always a single message.
			b.dispatch(ctx, &wg, l.raw, message{})
			continue
		}
		switch {
		case m.Method == "initialize":
			b.noteInitialize(m.Params)
		case m.Method == "notifications/initialized":
			if b.wantsRoots() {
				stash = b.askRoots(ctx, lines, stash)
			}
		}
		b.dispatch(ctx, &wg, l.raw, m)
	}
}

// noteInitialize records what the handshake says about the client.
func (b *Bridge) noteInitialize(params json.RawMessage) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Roots json.RawMessage `json:"roots"`
		} `json:"capabilities"`
	}
	_ = json.Unmarshal(params, &p)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.protocol = p.ProtocolVersion
	b.rootsAware = len(p.Capabilities.Roots) > 0 && string(p.Capabilities.Roots) != "null"
}

// wantsRoots is true when the client declares roots and the working directory
// matched no binding: only then can a root change the answer.
func (b *Bridge) wantsRoots() bool {
	b.mu.Lock()
	aware := b.rootsAware
	b.mu.Unlock()
	if !aware {
		return false
	}
	s := b.currentScope()
	return s.Binding == nil && s.Err == nil
}

// askRoots asks the client for its roots and, when one is bound, narrows the
// scope to them. It runs once, between `initialized` and the first request, so
// the scope is fixed before any tool is listed. Messages that arrive while it
// waits are stashed and processed afterwards, in order.
func (b *Bridge) askRoots(ctx context.Context, lines <-chan line, stash [][]byte) [][]byte {
	const id = "cartographer-roots-1"
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "roots/list"})
	b.write(req)
	timeout := time.After(rootsTimeout)
	for {
		select {
		case l, ok := <-lines:
			if !ok || l.err != nil {
				return stash
			}
			var m message
			if json.Unmarshal(l.raw, &m) == nil && m.isResponse() && strings.Trim(string(m.ID), `"`) == id {
				var res struct {
					Roots []struct {
						URI string `json:"uri"`
					} `json:"roots"`
				}
				_ = json.Unmarshal(m.Result, &res)
				uris := make([]string, 0, len(res.Roots))
				for _, r := range res.Roots {
					uris = append(uris, r.URI)
				}
				if s, ok := ResolveRoots(b.Cfg, b.Provider, uris); ok {
					b.mu.Lock()
					b.scope = &s
					b.mu.Unlock()
					b.logf("scope from roots: %s", s.Describe())
				}
				return stash
			}
			stash = append(stash, l.raw)
		case <-timeout:
			b.logf("client did not answer roots/list; keeping the working directory scope")
			return stash
		case <-ctx.Done():
			return stash
		}
	}
}

// write emits one message on stdout, atomically with respect to other writers.
func (b *Bridge) write(raw []byte) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return
	}
	compact.WriteByte('\n')
	b.outMu.Lock()
	defer b.outMu.Unlock()
	_, _ = b.Out.Write(compact.Bytes())
}

func (b *Bridge) writeError(id json.RawMessage, code int, msg string) {
	if len(id) == 0 {
		b.logf("%s", msg)
		return
	}
	out, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": msg},
	})
	b.write(out)
}

func (b *Bridge) writeResult(id json.RawMessage, result any) {
	out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	b.write(out)
}

// dispatch answers a message locally (a session with no KB) or forwards it on
// its own goroutine, so a slow tool call never blocks a cancellation.
func (b *Bridge) dispatch(ctx context.Context, wg *sync.WaitGroup, raw []byte, m message) {
	scope := b.currentScope()
	if scope.None() {
		b.answerLocally(scope, m)
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.forward(ctx, scope, raw, m)
	}()
}

// answerLocally serves a session whose folder is bound to no KB: the handshake
// and an empty tool list, never a request to the server (an empty kbs= would
// mean every KB there).
func (b *Bridge) answerLocally(scope Scope, m message) {
	if !m.isRequest() {
		return
	}
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		b.writeResult(m.ID, map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "cartographer", "version": orUnknown(b.Version)},
			"instructions":    noKBInstructions(scope),
		})
	case "tools/list":
		b.writeResult(m.ID, map[string]any{"tools": []any{}})
	case "ping":
		b.writeResult(m.ID, map[string]any{})
	case "tools/call":
		b.writeError(m.ID, -32602, "no Cartographer tool is available in this folder: "+noKBInstructions(scope))
	default:
		b.writeError(m.ID, -32601, "method not found")
	}
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

func noKBInstructions(scope Scope) string {
	if scope.Err != nil {
		return "This folder's Cartographer binding cannot be honoured, so no KB is served here: " + scope.Err.Error()
	}
	return "This folder is bound to no KB, so Cartographer serves none in this session (" + scope.Source + "). " +
		"See `cartographer workspace resolve` to change that."
}

// endpoint is the routed URL for a scope.
func (b *Bridge) endpoint(scope Scope) (string, error) {
	u, err := url.Parse(b.Cfg.ServerURL)
	if err != nil {
		return "", fmt.Errorf("parse server URL %q: %w", b.Cfg.ServerURL, err)
	}
	if b.Cfg.ServerRoutedPath == "" {
		return "", errors.New("the server has no routed endpoint recorded; run `cartographer sync`")
	}
	u.Path = b.Cfg.ServerRoutedPath
	u.RawQuery = scope.Query()
	return u.String(), nil
}

// forward POSTs one message to the routed endpoint and writes what comes back.
func (b *Bridge) forward(ctx context.Context, scope Scope, raw []byte, m message) {
	reqURL, err := b.endpoint(scope)
	if err != nil {
		b.writeError(m.ID, -32000, err.Error())
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(raw))
	if err != nil {
		b.writeError(m.ID, -32000, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if b.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.Token)
	}
	b.mu.Lock()
	sid, proto := b.sessionID, b.protocol
	b.mu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	if v := metaProtocol(m.Params); v != "" {
		proto = v
	}
	if proto != "" && m.Method != "initialize" {
		req.Header.Set("MCP-Protocol-Version", proto)
	}
	if m.Method != "" {
		req.Header.Set("Mcp-Method", m.Method)
		if name := mirroredName(m); name != "" {
			req.Header.Set("Mcp-Name", name)
		}
	}

	resp, err := b.HTTP.Do(req)
	if err != nil {
		b.writeError(m.ID, -32000, client.UnreachableMessage(reqURL, err)+": "+err.Error())
		return
	}
	defer resp.Body.Close()
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		b.mu.Lock()
		b.sessionID = id
		b.mu.Unlock()
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		c := client.New(reqURL, b.Token).WithTokenEnv(b.Cfg.TokenEnv)
		b.writeError(m.ID, -32000, reqURL+" rejected the request: "+c.UnauthorizedError().Error())
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		// A notification or a client response: nothing to relay.
	case strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"):
		b.relayEvents(resp.Body)
	default:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024*1024))
		if err != nil {
			b.writeError(m.ID, -32000, fmt.Sprintf("could not read response from %s: %v", reqURL, err))
			return
		}
		body = bytes.TrimSpace(body)
		if resp.StatusCode != http.StatusOK && !json.Valid(body) {
			b.writeError(m.ID, -32000, fmt.Sprintf("%s returned HTTP %d: %s", reqURL, resp.StatusCode, strings.TrimSpace(string(body))))
			return
		}
		if len(body) > 0 {
			b.write(body)
		}
	}
}

// relayEvents copies each `data:` event of an SSE response to stdout.
func (b *Bridge) relayEvents(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	var data []string
	flush := func() {
		if len(data) > 0 {
			b.write([]byte(strings.Join(data, "\n")))
			data = nil
		}
	}
	for sc.Scan() {
		t := strings.TrimRight(sc.Text(), "\r")
		switch {
		case t == "":
			flush()
		case strings.HasPrefix(t, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(t, "data:"), " "))
		}
	}
	flush()
}

// metaProtocol reads the 2026-07-28 revision's per-request protocol version.
func metaProtocol(params json.RawMessage) string {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return ""
	}
	var v string
	_ = json.Unmarshal(p.Meta["io.modelcontextprotocol/protocolVersion"], &v)
	return v
}

// mirroredName is the value of the Mcp-Name header the 2026-07-28 revision
// mirrors from the body, for the three methods that carry one.
func mirroredName(m message) string {
	var p struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return ""
	}
	switch m.Method {
	case "tools/call", "prompts/get":
		return p.Name
	case "resources/read":
		return p.URI
	}
	return ""
}
