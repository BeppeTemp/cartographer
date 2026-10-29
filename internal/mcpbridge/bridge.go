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

// accessHeader and accessRead mirror mcpserver.AccessHeader and
// mcpserver.AccessReadValue: the bridge does not import the server package.
const (
	accessHeader = "Cartographer-Access"
	accessRead   = "read"
	// provisionedParam and provisionedValue mirror mcpserver.ProvisionedParam
	// and mcpserver.ProvisionedValue (D1097): a bridge runs under a client that
	// has the managed instructions block, so it never needs the server's guide.
	provisionedParam = "provisioned"
	provisionedValue = "1"
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
	// accessConfirmed is set once a server response of a read-only session
	// carried Cartographer-Access: read; serverTooOld once one did not.
	accessConfirmed bool
	serverTooOld    bool
	protocol        string
	sessionID       string

	authorMu       sync.Mutex // guards the fields below
	gitAuthors     map[string]gitAuthor
	warnedIdentity map[string]bool
	// workTreeIdent replaces gitx.WorkTreeIdent in tests.
	workTreeIdent func(dir string) string
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
	defer b.closeSession()
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
			if m.isRequest() && b.wantsRoots() {
				// The folder is under no binding and the client has roots: the
				// final set is not known until `initialized`, and a server
				// cannot ask for roots before it has answered. Forwarding now
				// would put the instructions of another set (naming its KBs)
				// into a session that ends up with a different one, so the
				// handshake is answered here without naming any KB.
				instr := rootsPendingInstructions
				if sc := b.currentScope(); sc.Unbound && !sc.ReadOnly {
					instr += rootsUnboundWriteNote
				}
				b.answerInitialize(m, instr)
				continue
			}
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
	if b.isServerTooOld() {
		scope = serverTooOldScope(scope)
	}
	if scope.None() {
		b.answerLocally(scope, m)
		return
	}
	if b.awaitingAccess(scope) {
		// TRAP: until the server has confirmed read-only mode, forward one
		// message at a time and wait for its confirmation. Concurrent forwards
		// would put the next requests on an old server that ignores
		// ?access=read before the first answer says so. The wait ends when
		// forward has checked the response headers, not when the body is done:
		// a long-lived event stream (subscriptions/listen) must not hold the
		// read loop, and a stream's headers already carry the verdict.
		released := make(chan struct{})
		var once sync.Once
		release := func() { once.Do(func() { close(released) }) }
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer release()
			b.forward(ctx, scope, raw, m, release)
		}()
		<-released
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.forward(ctx, scope, raw, m, func() {})
	}()
}

// awaitingAccess is true for a read-only scope whose mode the server has not
// confirmed yet.
func (b *Bridge) awaitingAccess(scope Scope) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return scope.ReadOnly && !b.accessConfirmed
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
		b.answerInitialize(m, noKBInstructions(scope))
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

// rootsPendingInstructions is the handshake text while the client's roots decide
// the set. It names no KB: the tool list that follows is the real answer.
const rootsPendingInstructions = "Cartographer's KBs for this session are chosen from the workspace roots " +
	"the client reports; list the tools to see them."

// rootsUnboundWriteNote is appended to rootsPendingInstructions when the
// unbound folder is read-write (#1356).
const rootsUnboundWriteNote = " If none of the roots is bound to a KB, the session is unbound: " + unboundWriteNote

// unboundWriteNote is the sentence the bridge adds to the initialize result of
// an unbound read-write session (#1356). The server's own text for a bridge
// session is empty (provisioned=1), so this is the only wording.
const unboundWriteNote = "this session was started in a folder bound to no KB and has write access to the provider's KBs. " +
	"No KB skills, agents or instructions are installed here: pass `kb` explicitly to every tool and read the KB's guide before writing. " +
	"`cartographer workspace bind` gives the folder a KB's skills."

// withUnboundNote returns body with unboundWriteNote appended to the
// instructions of an initialize result; any other body comes back untouched.
func withUnboundNote(body []byte) []byte {
	var env map[string]json.RawMessage
	if json.Unmarshal(body, &env) != nil || env["result"] == nil {
		return body
	}
	var res map[string]json.RawMessage
	if json.Unmarshal(env["result"], &res) != nil || res == nil {
		return body
	}
	var cur string
	if raw, ok := res["instructions"]; ok {
		_ = json.Unmarshal(raw, &cur)
	}
	if cur != "" {
		cur += "\n\n"
	}
	res["instructions"], _ = json.Marshal(cur + "This is an unbound session: " + unboundWriteNote)
	env["result"], _ = json.Marshal(res)
	out, err := json.Marshal(env)
	if err != nil {
		return body
	}
	return out
}

// answerInitialize answers the handshake locally.
func (b *Bridge) answerInitialize(m message, instructions string) {
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
		"instructions":    instructions,
	})
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

// errServerTooOld is the scope error of a read-only session whose server did
// not confirm the mode (Cartographer-Access: read). An older server ignores
// ?access=read and would serve write tools silently, so the bridge serves no
// KB instead (fail closed across versions).
var errServerTooOld = errors.New("the Cartographer server must be upgraded to serve read-only sessions " +
	"(this folder is bound to no KB, so the session must be read-only, and this server did not confirm it)")

func serverTooOldScope(s Scope) Scope {
	return Scope{Source: s.Source, ReadOnly: true, Err: errServerTooOld}
}

func (b *Bridge) isServerTooOld() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.serverTooOld
}

// checkAccess verifies the server's confirmation of a read-only session on a
// successful response. It returns false when the response must not be relayed.
func (b *Bridge) checkAccess(scope Scope, resp *http.Response) bool {
	b.mu.Lock()
	confirmed := b.accessConfirmed
	b.mu.Unlock()
	if !scope.ReadOnly || confirmed || resp.StatusCode >= 300 {
		return true
	}
	if resp.Header.Get(accessHeader) == accessRead {
		b.mu.Lock()
		b.accessConfirmed = true
		b.mu.Unlock()
		return true
	}
	b.mu.Lock()
	b.serverTooOld = true
	b.mu.Unlock()
	b.logf("%s", errServerTooOld)
	return false
}

func noKBInstructions(scope Scope) string {
	if errors.Is(scope.Err, errServerTooOld) {
		return "Cartographer serves no KB in this session: " + scope.Err.Error() + "."
	}
	if scope.Err != nil {
		return "This folder's Cartographer binding cannot be honoured, so no KB is served here: " + scope.Err.Error()
	}
	return "This folder is bound to no KB, so Cartographer serves none in this session (" + scope.Source + "). " +
		"See `cartographer workspace resolve` to change that."
}

// endpoint is the routed URL for a scope.
func (b *Bridge) endpoint(scope Scope) (string, error) {
	// The bridge builds its own requests with the bearer header, so the gate is
	// here: both the POST forward and the shutdown DELETE go through endpoint (D1023).
	if err := client.CheckServerURL(b.Cfg.ServerURL, b.Cfg.InsecureHTTPAllowed()); err != nil {
		return "", err
	}
	u, err := url.Parse(b.Cfg.ServerURL)
	if err != nil {
		return "", fmt.Errorf("parse server URL %q: %w", b.Cfg.ServerURL, err)
	}
	if b.Cfg.ServerRoutedPath == "" {
		return "", errors.New("the server has no routed endpoint recorded; run `cartographer sync`")
	}
	u.Path = b.Cfg.ServerRoutedPath
	// scope.Query() stays scope-only (an empty set never produces a kbs= query);
	// the marker is added here so forward and the closing DELETE both carry it.
	q := scope.Query()
	if q != "" {
		q += "&"
	}
	u.RawQuery = q + provisionedParam + "=" + provisionedValue
	return u.String(), nil
}

// forward POSTs one message to the routed endpoint and writes what comes back.
// release is called once the access check of a response that opens an event
// stream has passed, before the stream is relayed.
func (b *Bridge) forward(ctx context.Context, scope Scope, raw []byte, m message, release func()) {
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
	if m.Method == "tools/call" {
		if a := b.author(scope, m.Params); a != "" {
			req.Header.Set(client.AuthorHeader, client.EncodeAuthor(a))
		}
	}

	// The handshake of an unbound read-write session says it is unbound.
	rewrite := m.Method == "initialize" && scope.Unbound && !scope.ReadOnly

	resp, err := client.HTTPClient(b.HTTP, b.Cfg.InsecureHTTPAllowed()).Do(req)
	if err != nil {
		b.writeError(m.ID, -32000, client.UnreachableMessage(reqURL, err)+": "+err.Error())
		return
	}
	defer resp.Body.Close()
	if !b.checkAccess(scope, resp) {
		b.answerLocally(serverTooOldScope(scope), m)
		return
	}
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		b.mu.Lock()
		b.sessionID = id
		b.mu.Unlock()
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		c := client.New(reqURL, b.Token).WithInsecureHTTP(b.Cfg.InsecureHTTPAllowed()).WithTokenEnv(b.Cfg.TokenEnv())
		b.writeError(m.ID, -32000, reqURL+" rejected the request: "+c.UnauthorizedError().Error())
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		// A notification or a client response: nothing to relay.
	case strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"):
		// A stream that carries no verdict (an error status) keeps the
		// serialization until forward returns.
		b.mu.Lock()
		confirmed := b.accessConfirmed
		b.mu.Unlock()
		if confirmed {
			release()
		}
		b.relayEvents(resp.Body, rewrite)
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
			if rewrite {
				body = withUnboundNote(body)
			}
			b.write(body)
		}
	}
}

// relayEvents copies each `data:` event of an SSE response to stdout.
func (b *Bridge) relayEvents(r io.Reader, rewriteInit bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	var data []string
	flush := func() {
		if len(data) > 0 {
			ev := []byte(strings.Join(data, "\n"))
			if rewriteInit {
				ev = withUnboundNote(ev)
			}
			b.write(ev)
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

// closeSession ends the server-side session, if the server opened one. The
// routed server is stateless and opens none, so this is normally a no-op; it
// keeps the bridge correct against a server that does. Best effort.
//
// Not done: the optional GET stream. Server notifications and requests that
// are not part of a POST response never reach the client; the routed server
// sends none (D737).
func (b *Bridge) closeSession() {
	b.mu.Lock()
	sid, scope := b.sessionID, b.scope
	b.mu.Unlock()
	if sid == "" || scope == nil || scope.None() {
		return
	}
	reqURL, err := b.endpoint(*scope)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", sid)
	if b.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.Token)
	}
	if resp, err := client.HTTPClient(b.HTTP, b.Cfg.InsecureHTTPAllowed()).Do(req); err == nil {
		resp.Body.Close()
	}
}
