package mcpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// maxToolName is the longest tool name MCP clients accept.
const maxToolName = 64

// ToolClient is the part of an MCP client a plugin's kb.call uses; it is the
// method set of star.ToolClient, so the bridge need not import the plugin layer.
type ToolClient interface {
	CallContext(ctx context.Context, tool string, args any) (json.RawMessage, error)
	ListToolsContext(ctx context.Context) ([]client.ToolInfo, error)
}

// PluginTool is one tool of an active plugin, as cmd/cartographer hands it to
// the bridge. The bridge lists it as <plugin>_<name> and runs it in this process.
type PluginTool struct {
	Plugin, Name, Description string
	// Params is the tool's JSON object schema.
	Params json.RawMessage
	Writes bool
	// Run executes the tool; an error is the tool's error result.
	Run func(ctx context.Context, call PluginCall) (string, error)
}

// PluginCall is what the bridge gives Run.
type PluginCall struct {
	// Params are the call's arguments, a JSON object.
	Params json.RawMessage
	// ReadOnly is a read-only session.
	ReadOnly bool
	// KB reaches the server through this session's scope, author and token.
	KB ToolClient
	// Log is the bridge log (stderr). Never stdout: that is protocol.
	Log io.Writer
}

// exposedName is <plugin>_<tool> with hyphens turned into underscores.
func (t PluginTool) exposedName() string {
	return strings.ReplaceAll(t.Plugin+"_"+t.Name, "-", "_")
}

// servedSet is the plugin tools that survived the collision rules against one
// server listing.
type servedSet struct {
	byName map[string]PluginTool
	order  []string
}

// pluginState is the bridge's plugin bookkeeping, apart from b.mu.
type pluginState struct {
	mu     sync.Mutex
	pages  map[string]bool // server tool names of the pages of the listing in progress
	served *servedSet
	// servedKey is the scope the served set was settled under: a read-only
	// listing omits the server's write tools, so a name free there may be taken
	// once the scope widens, and the set is settled again.
	servedKey string
	// everServed are the names served as a plugin tool under any scope so far.
	everServed map[string]bool
	logged     map[string]bool
	inflight   map[string]context.CancelFunc
	kb         *sessionClient
	seq        atomic.Int64
	cands      map[string]bool
}

// noteServed remembers the names of set; the caller holds mu.
func (p *pluginState) noteServed(set *servedSet) {
	if p.everServed == nil {
		p.everServed = map[string]bool{}
	}
	for _, n := range set.order {
		p.everServed[n] = true
	}
}

// candidates are the names the plugin tools could take, before any collision.
func (b *Bridge) candidates() map[string]bool {
	b.pl.mu.Lock()
	defer b.pl.mu.Unlock()
	if b.pl.cands == nil {
		b.pl.cands = map[string]bool{}
		for _, t := range b.Plugins {
			if n := t.exposedName(); len(n) <= maxToolName {
				b.pl.cands[n] = true
			}
		}
	}
	return b.pl.cands
}

// isPluginCall is true for a tools/call whose name may be a plugin tool; whether
// it is one is settled against the server's list (settle).
func (b *Bridge) isPluginCall(m message) bool {
	return len(b.Plugins) > 0 && m.isRequest() && m.Method == "tools/call" && b.candidates()[mirroredName(m)]
}

// computeServed applies the naming rules to the server's tool names: a name over
// 64 characters is dropped (never truncated), and so is one equal to a server
// tool or already taken by an earlier plugin in name order (a + b_c against
// a_b + c). Every drop is logged once.
func (b *Bridge) computeServed(server map[string]bool) *servedSet {
	tools := append([]PluginTool(nil), b.Plugins...)
	sort.SliceStable(tools, func(i, j int) bool {
		if tools[i].Plugin != tools[j].Plugin {
			return tools[i].Plugin < tools[j].Plugin
		}
		return tools[i].Name < tools[j].Name
	})
	taken := map[string]bool{}
	for n := range server {
		taken[n] = true
	}
	set := &servedSet{byName: map[string]PluginTool{}}
	for _, t := range tools {
		n := t.exposedName()
		switch {
		case len(n) > maxToolName:
			b.logOnce("plugin tool %s of plugin %s is not served: its name is over %d characters", t.Name, t.Plugin, maxToolName)
		case server[n]:
			b.logOnce("plugin tool %s is not served: the server has a tool of that name", n)
		case taken[n]:
			b.logOnce("plugin tool %s is not served: an earlier plugin took that name", n)
		default:
			taken[n] = true
			set.byName[n] = t
			set.order = append(set.order, n)
		}
	}
	return set
}

func (b *Bridge) logOnce(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	b.pl.mu.Lock()
	seen := b.pl.logged[msg]
	if b.pl.logged == nil {
		b.pl.logged = map[string]bool{}
	}
	b.pl.logged[msg] = true
	b.pl.mu.Unlock()
	if !seen {
		b.logf("%s", msg)
	}
}

// mergePluginTools adds the plugin tools to the last page of a tools/list
// response (the one with no nextCursor). Any other body, an error included,
// comes back untouched. The names of earlier pages are remembered, so a
// collision is settled against the whole listing.
func (b *Bridge) mergePluginTools(scope Scope, m message, body []byte) []byte {
	var env map[string]json.RawMessage
	if json.Unmarshal(body, &env) != nil || env["result"] == nil || string(env["id"]) != string(m.ID) {
		return body
	}
	var res map[string]json.RawMessage
	if json.Unmarshal(env["result"], &res) != nil || res == nil {
		return body
	}
	var tools []json.RawMessage
	if raw := res["tools"]; len(raw) > 0 {
		if json.Unmarshal(raw, &tools) != nil {
			return body
		}
	}
	var page struct {
		Name string `json:"name"`
	}
	b.pl.mu.Lock()
	var p struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if p.Cursor == "" || b.pl.pages == nil {
		b.pl.pages = map[string]bool{}
	}
	for _, t := range tools {
		if json.Unmarshal(t, &page) == nil {
			b.pl.pages[page.Name] = true
		}
	}
	var next string
	_ = json.Unmarshal(res["nextCursor"], &next)
	if next != "" {
		b.pl.mu.Unlock()
		return body
	}
	names := b.pl.pages
	b.pl.pages = nil
	b.pl.mu.Unlock()

	set := b.computeServed(names)
	b.pl.mu.Lock()
	b.pl.served, b.pl.servedKey = set, scope.Query()
	// The listing the client receives: what it knows as a plugin tool is exactly
	// this set, so a re-list clears a refusal (settle, which the client does not
	// see, only adds).
	b.pl.everServed = nil
	b.pl.noteServed(set)
	b.pl.mu.Unlock()
	for _, n := range set.order {
		t := set.byName[n]
		if t.Writes && scope.ReadOnly {
			continue // a read-only session lists no write tool, as ?access=read does
		}
		schema := t.Params
		if len(bytes.TrimSpace(schema)) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		entry, err := json.Marshal(map[string]any{
			"name":        n,
			"description": "[plugin " + t.Plugin + "] " + t.Description,
			"inputSchema": schema,
			"annotations": map[string]any{"readOnlyHint": !t.Writes},
		})
		if err == nil {
			tools = append(tools, entry)
		}
	}
	if tools == nil {
		tools = []json.RawMessage{}
	}
	res["tools"], _ = json.Marshal(tools)
	env["result"], _ = json.Marshal(res)
	out, err := json.Marshal(env)
	if err != nil {
		return body
	}
	return out
}

// settle returns the served set, listing the server's tools once when no
// tools/list of this session has settled it yet.
func (b *Bridge) settle(ctx context.Context, scope Scope) (*servedSet, error) {
	b.pl.mu.Lock()
	set, key := b.pl.served, b.pl.servedKey
	b.pl.mu.Unlock()
	if set != nil && key == scope.Query() {
		return set, nil
	}
	list, err := b.sessionKB().listTools(ctx, scope)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, t := range list {
		names[t.Name] = true
	}
	set = b.computeServed(names)
	b.pl.mu.Lock()
	b.pl.served, b.pl.servedKey = set, scope.Query()
	b.pl.noteServed(set)
	b.pl.mu.Unlock()
	return set, nil
}

// dispatchPluginCall runs a tools/call that may name a plugin tool on its own
// goroutine. Like the forward of a read-only session that is not confirmed yet,
// it holds the read loop until the first exchange with the server has been
// checked (the listing that settles the names does that), so no second message
// reaches an old server before it has confirmed the mode.
func (b *Bridge) dispatchPluginCall(ctx context.Context, wg *sync.WaitGroup, scope Scope, raw []byte, m message) {
	released := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	awaiting := b.awaitingAccess(scope)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer release()
		b.runPluginCall(ctx, scope, raw, m, release)
	}()
	if awaiting {
		<-released
	}
}

func (b *Bridge) runPluginCall(ctx context.Context, scope Scope, raw []byte, m message, release func()) {
	set, err := b.settle(ctx, scope)
	if err != nil {
		release()
		if b.isServerTooOld() {
			b.answerLocally(serverTooOldScope(scope), m)
			return
		}
		b.writeError(m.ID, -32000, err.Error())
		return
	}
	name := mirroredName(m)
	t, ok := set.byName[name]
	if !ok {
		b.pl.mu.Lock()
		was := b.pl.everServed[name]
		b.pl.mu.Unlock()
		if was {
			// The client saw this name as a plugin tool (possibly read-only, so
			// auto-approved) under an earlier scope; the server owns it now.
			// Forwarding would run the server's tool under that approval.
			release()
			b.writeResult(m.ID, toolResult("the tool list changed: "+name+" is now a server tool; list the tools again", true))
			return
		}
		// The name belongs to the server (or a collision dropped the plugin's):
		// the message goes through as it always did.
		b.forward(ctx, scope, raw, m, release)
		return
	}
	release()
	if t.Writes && scope.ReadOnly {
		b.writeResult(m.ID, toolResult("plugin tool "+name+" writes, and this session is read-only", true))
		return
	}
	var p struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	_ = json.Unmarshal(m.Params, &p)

	cctx, cancel := context.WithCancel(ctx)
	key := string(m.ID)
	b.pl.mu.Lock()
	if b.pl.inflight == nil {
		b.pl.inflight = map[string]context.CancelFunc{}
	}
	b.pl.inflight[key] = cancel
	b.pl.mu.Unlock()
	defer func() {
		cancel()
		b.pl.mu.Lock()
		delete(b.pl.inflight, key)
		b.pl.mu.Unlock()
	}()

	text, err := t.Run(cctx, PluginCall{Params: p.Arguments, ReadOnly: scope.ReadOnly, KB: b.sessionKB(), Log: b.Log})
	if err != nil {
		b.writeResult(m.ID, toolResult(err.Error(), true))
		return
	}
	b.writeResult(m.ID, toolResult(text, false))
}

func toolResult(text string, isError bool) map[string]any {
	r := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
	if isError {
		r["isError"] = true
	}
	return r
}

// cancelPluginCall stops the local call a notifications/cancelled names.
func (b *Bridge) cancelPluginCall(params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if len(b.Plugins) == 0 || json.Unmarshal(params, &p) != nil || len(p.RequestID) == 0 {
		return
	}
	b.pl.mu.Lock()
	cancel := b.pl.inflight[string(p.RequestID)]
	b.pl.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// sessionClient is the bridge's adapter for a plugin's kb.call: the tools of
// the routed endpoint, under the scope, token, session and author of this
// session. The plugin layer leaves the `kb` argument unrestricted (the server's
// scopes hold), so the session's KB set is applied here, as the bridge applies
// it to every other message with ?kbs=.
type sessionClient struct{ b *Bridge }

func (b *Bridge) sessionKB() *sessionClient {
	b.pl.mu.Lock()
	defer b.pl.mu.Unlock()
	if b.pl.kb == nil {
		b.pl.kb = &sessionClient{b: b}
	}
	return b.pl.kb
}

// scope is the scope in force, as dispatch sees it.
func (c *sessionClient) scope() (Scope, error) {
	s := c.b.currentScope()
	if c.b.isServerTooOld() {
		s = serverTooOldScope(s)
	}
	if s.None() {
		return s, errors.New("this session serves no KB")
	}
	return s, nil
}

func (c *sessionClient) CallContext(ctx context.Context, tool string, args any) (json.RawMessage, error) {
	scope, err := c.scope()
	if err != nil {
		return nil, err
	}
	// Fail closed: arguments that are not an object cannot name a KB of the set.
	m, ok := args.(map[string]any)
	if !ok {
		return nil, errors.New("the arguments must be an object carrying kb=<name>")
	}
	name, _ := m["kb"].(string)
	allowed := scope.KBs
	if scope.All {
		allowed = c.b.Cfg.KnownKBs
	}
	if name == "" {
		return nil, errors.New("pass kb=<name>: a plugin reaches one KB of this session at a time")
	}
	found := false
	for _, k := range allowed {
		found = found || k == name
	}
	if !found {
		return nil, fmt.Errorf("KB %q is not one of the KBs this session serves", name)
	}
	params, _ := json.Marshal(map[string]any{"name": tool, "arguments": args})
	res, err := c.b.exchange(ctx, scope, "tools/call", params)
	if err != nil {
		return nil, err
	}
	var tr struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &tr); err != nil {
		return nil, fmt.Errorf("invalid tool result for %q", tool)
	}
	if len(tr.Content) == 0 {
		return nil, fmt.Errorf("tool %q returned no content", tool)
	}
	if tr.IsError {
		return nil, fmt.Errorf("tool %q returned an error: %s", tool, client.Escape(tr.Content[0].Text))
	}
	var texts []string
	for _, blk := range tr.Content {
		if blk.Type == "text" {
			texts = append(texts, blk.Text)
		}
	}
	if len(texts) == 1 {
		return json.RawMessage(texts[0]), nil
	}
	combined, _ := json.Marshal(texts)
	return combined, nil
}

func (c *sessionClient) ListToolsContext(ctx context.Context) ([]client.ToolInfo, error) {
	scope, err := c.scope()
	if err != nil {
		return nil, err
	}
	return c.listTools(ctx, scope)
}

// listTools lists under the scope the caller names, never one it reads itself:
// settle keys the result to that scope.
func (c *sessionClient) listTools(ctx context.Context, scope Scope) ([]client.ToolInfo, error) {
	var out []client.ToolInfo
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, _ := json.Marshal(params)
		res, err := c.b.exchange(ctx, scope, "tools/list", raw)
		if err != nil {
			return nil, err
		}
		var l struct {
			Tools []struct {
				Name        string `json:"name"`
				Annotations *struct {
					ReadOnlyHint bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(res, &l); err != nil {
			return nil, errors.New("invalid tool list")
		}
		for _, t := range l.Tools {
			out = append(out, client.ToolInfo{Name: t.Name, ReadOnly: t.Annotations != nil && t.Annotations.ReadOnlyHint})
		}
		if l.NextCursor == "" || l.NextCursor == cursor {
			return out, nil
		}
		cursor = l.NextCursor
	}
	return nil, errors.New("the tool list never ends")
}

// exchange sends one request of the bridge's own (not the client's) to the
// routed endpoint under scope and returns its result. It goes through the same
// newRequest, endpoint and access check as forward; a JSON-RPC error comes back
// as an error.
func (b *Bridge) exchange(ctx context.Context, scope Scope, method string, params json.RawMessage) (json.RawMessage, error) {
	id := json.RawMessage(fmt.Sprintf(`"cartographer-plugin-%d"`, b.pl.seq.Add(1)))
	m := message{ID: id, Method: method, Params: params}
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	reqURL, err := b.endpoint(scope)
	if err != nil {
		return nil, err
	}
	req, err := b.newRequest(ctx, scope, reqURL, raw, m)
	if err != nil {
		return nil, err
	}
	resp, err := client.HTTPClient(b.HTTP, b.Cfg.InsecureHTTPAllowed()).Do(req)
	if err != nil {
		return nil, errors.New(client.UnreachableMessage(reqURL, err) + ": " + err.Error())
	}
	defer resp.Body.Close()
	if !b.checkAccess(scope, resp) {
		return nil, errServerTooOld
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		b.mu.Lock()
		b.sessionID = sid
		b.mu.Unlock()
	}
	if resp.StatusCode == http.StatusUnauthorized {
		c := client.New(reqURL, b.Token).WithInsecureHTTP(b.Cfg.InsecureHTTPAllowed()).WithTokenEnv(b.Cfg.TokenEnv())
		return nil, errors.New(reqURL + " rejected the request: " + c.UnauthorizedError().Error())
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("could not read response from %s: %v", reqURL, err)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		body = eventFor(body, id)
	}
	var out message
	if json.Unmarshal(bytes.TrimSpace(body), &out) != nil || !out.isResponse() {
		return nil, fmt.Errorf("%s returned HTTP %d without a JSON-RPC response", reqURL, resp.StatusCode)
	}
	if len(out.Error) > 0 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(out.Error, &e)
		return nil, errors.New(client.Escape(e.Message))
	}
	return out.Result, nil
}

// eventFor returns the data of the SSE event that answers id.
func eventFor(stream []byte, id json.RawMessage) []byte {
	var data []string
	var found []byte
	flush := func() {
		if len(data) > 0 {
			ev := []byte(strings.Join(data, "\n"))
			var em message
			if json.Unmarshal(ev, &em) == nil && em.isResponse() && string(em.ID) == string(id) {
				found = ev
			}
			data = nil
		}
	}
	for _, l := range strings.Split(string(stream), "\n") {
		t := strings.TrimRight(l, "\r")
		switch {
		case t == "":
			flush()
		case strings.HasPrefix(t, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(t, "data:"), " "))
		}
	}
	flush()
	return found
}
