package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
)

// routedmount.go (D187) — one endpoint for a multi-KB server.
//
// A client that uses N Knowledge Bases pays N copies of the same tool schemas
// in its fixed context, on every model round-trip: measured against a running
// server with three KBs, tools/list returned 82,341 bytes of which two thirds
// were duplicates. D102's tool-name prefix makes multi-KB *work* on a
// flat-namespace client; the D65/D123 agent profile shrinks the set *per
// mount*. Neither removes the duplication, because the duplication is the
// mount topology.
//
// The routed mount registers the union of the mounted KBs' tool descriptors
// exactly once and carries the KB as an explicit `kb` tool argument. It is
// additive over the per-KB plumbing: MountKB and the ?kb=/
// /mcp/<name> endpoints keep answering as before. Since D288 it is the only
// topology written into agent clients, and a connection may narrow it to a
// subset of the KBs with ?kbs= (one KB: no `kb` argument at all).
//
// Everything that is per-KB is resolved *after* `kb`: the read/write
// classification, the auth policy (D118), the git lock and commit wrapper, the
// audit record's KB, and whether a gated tool exists at all. That is achieved
// by dispatching into the target KB's own Server.callTool, which is the whole
// of what a per-KB endpoint does with a call — so the routed path cannot drift
// from the per-KB one.

// RoutedMountPath is the URL path the routed mount is served on. It is
// deliberately not /mcp: a bare /mcp already auto-routes a single-KB server and
// requires ?kb= otherwise, and overloading it would make the same URL mean two
// different things depending on configuration.
const RoutedMountPath = "/mcp/routed"

// routedKBArgument is the tool argument naming the KB a call is for.
const routedKBArgument = "kb"

// EnableRoutedMount builds the routed mount over the KBs already mounted on m.
// Call it after every MountKB. It returns an error when no KB
// is mounted; on success the routed endpoint is served alongside the per-KB
// ones.
//
// D288: the routed mount is the one agent-facing topology. The connection
// chooses its KB set (`?kbs=`, see routedFor): with the default binding the
// schemas carry a required `kb` enum, with a single KB they carry no `kb` at
// all. The caller (serve.go) skips a KB named like the endpoint's own path
// before mounting it, so no name can shadow the route here.
func (m *MultiKBServer) EnableRoutedMount(version string, setup func(s *Server)) error {
	if len(m.servers) == 0 {
		return errors.New("routed mount: no KB is mounted")
	}
	m.routedVersion = version
	m.routedSetup = setup
	names := m.mountedNames()
	m.routedViews = map[string]*Server{}
	m.routed = m.buildRouted(names)
	m.routedViews[strings.Join(names, ",")] = m.routed
	m.routedNames = names
	return nil
}

// buildRouted assembles the routed server for one effective KB set: the union
// of those KBs' tools, once, with the schemas shaped for that set.
func (m *MultiKBServer) buildRouted(set []string) *Server {
	routed := New(m.routedVersion)
	routed.SetDisplayName("cartographer")
	routed.connKBs = append([]string(nil), set...)
	if m.routedSetup != nil {
		m.routedSetup(routed)
	}
	// The real authorization decision belongs to the target KB and is taken
	// inside its own callTool, after `kb` has been resolved. What this
	// authorizer still has to answer is the metadata gate (initialize, ping,
	// tools/list): a caller with no resolvable principal must be denied rather
	// than implicitly treated as having full access. On a routed mount the
	// honest rule is "can reach at least one of the connection's KBs" — a
	// principal scoped to one KB legitimately lists the union and is refused per
	// call on the others.
	routed.SetAuthorizer(func(ctx context.Context, tool string, args json.RawMessage) error {
		if tool != "" {
			return nil
		}
		var lastErr error = deny(denyNoPrincipal)
		for _, name := range set {
			if err := m.servers[name].authorize(ctx, "", args); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
		return lastErr
	})
	for _, t := range m.unionTools(set) {
		routed.RegisterTool(t)
	}
	return routed
}

// routedUnknownKey carries the request's unknown `kbs` names from the HTTP
// layer to routedHandler. They are request-scoped: the cached Server is shared
// by every URL that resolves to the same effective set, so they never live on it.
type routedUnknownKey struct{}

func withRoutedUnknown(ctx context.Context, unknown []string) context.Context {
	if len(unknown) == 0 {
		return ctx
	}
	return context.WithValue(ctx, routedUnknownKey{}, unknown)
}

func routedUnknown(ctx context.Context) []string {
	u, _ := ctx.Value(routedUnknownKey{}).([]string)
	return u
}

// routedFor resolves the `kbs` query parameter of a routed request to the
// server that answers it (D288). Absent or empty means every mounted KB. The
// binding only narrows: the effective set is the requested names that are
// mounted (D756). Names that are not mounted (a KB renamed or removed since
// the client was synced) are returned sorted, not dropped silently: the caller
// reports them to the agent. Duplicates are ignored and per-KB authorization
// still runs at the target.
//
// An empty effective set stays an error naming every unknown name and the
// mounted KBs. It must not become "every mounted KB": that would widen the
// binding, which only narrows, and an empty set would be a server with no tools.
//
// One Server is built per distinct effective set and cached: the set is part
// of the URL, so tools/list differs per connection and cannot come from a
// single shared registry. The number of sets is bounded by the subsets of the
// mounted KBs; stale URLs with different unknown names share a view.
func (m *MultiKBServer) routedFor(rawKBs []string) (*Server, []string, error) {
	seen := map[string]bool{}
	for _, raw := range rawKBs {
		for _, n := range strings.Split(raw, ",") {
			if n = strings.TrimSpace(n); n != "" {
				seen[n] = true
			}
		}
	}
	if len(seen) == 0 {
		return m.routed, nil, nil
	}
	var set, unknown []string
	for n := range seen {
		if _, ok := m.servers[n]; !ok {
			unknown = append(unknown, n)
			continue
		}
		set = append(set, n)
	}
	sort.Strings(set)
	sort.Strings(unknown)
	if len(set) == 0 {
		return nil, unknown, fmt.Errorf("unknown kb in kbs: %s not mounted on this server, which mounts %s",
			quoteJoin(unknown), strings.Join(m.routedNames, ", "))
	}
	key := strings.Join(set, ",")
	m.routedMu.Lock()
	defer m.routedMu.Unlock()
	if len(unknown) > 0 {
		warn := key + "|" + strings.Join(unknown, ",")
		if !m.routedWarned[warn] {
			if m.routedWarned == nil {
				m.routedWarned = map[string]bool{}
			}
			m.routedWarned[warn] = true
			log.Printf("routed mount: kbs names %s not mounted; serving %s (renamed or removed? run cartographer sync on the client)",
				quoteJoin(unknown), key)
		}
	}
	if srv, ok := m.routedViews[key]; ok {
		return srv, unknown, nil
	}
	srv := m.buildRouted(set)
	m.routedViews[key] = srv
	return srv, unknown, nil
}

func quoteJoin(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(q, ", ")
}

// mountedNames returns the mounted KB names in deterministic order.
func (m *MultiKBServer) mountedNames() []string {
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// unionTools assembles the routed tool list for a KB set: every tool
// registered by any KB of the set, once, in the registration order of the first
// KB that has it, with the `kb` argument shaped into each schema at this single
// point rather than in fifty schema literals.
//
// The exposed set is the **union**, not the intersection. An intersection would
// silently hide artifact_write from a KB that allows it because a sibling KB
// does not; the union registers it once and refuses it per KB at dispatch, with
// an error naming the KB and the setting that would enable it — which is the
// answer a caller can act on.
func (m *MultiKBServer) unionTools(set []string) []Tool {
	seen := map[string]bool{}
	var out []Tool
	for _, name := range set {
		srv := m.servers[name]
		srv.mu.Lock()
		ord := append([]string(nil), srv.toolsOrd...)
		srv.mu.Unlock()
		for _, toolName := range ord {
			if seen[toolName] {
				continue
			}
			seen[toolName] = true
			srv.mu.Lock()
			t := srv.tools[toolName]
			srv.mu.Unlock()
			if t == nil {
				continue
			}
			routedTool := *t
			routedTool.InputSchema = withKBProperty(t.InputSchema, set)
			routedTool.Handler = m.routedHandler(toolName, set)
			out = append(out, routedTool)
		}
	}
	return out
}

// routedHandler resolves `kb` within the connection's set, strips it from the
// arguments and hands the call to that KB's own Server.callTool — the same
// entry point the per-KB endpoint uses, so authorization, audit, the
// read/write classification and the git wrapper are the per-KB ones,
// unchanged, applied to the KB actually named.
func (m *MultiKBServer) routedHandler(toolName string, set []string) func(context.Context, json.RawMessage) (ToolResult, error) {
	return func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
		kbName, rest, err := splitKBArgument(args)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		if kbName == "" {
			if len(set) == 1 {
				// The URL named this KB explicitly (D288), exactly like
				// /mcp/<name>: there is nothing to infer.
				kbName = set[0]
			} else {
				return errorResult(fmt.Sprintf(
					"%q is required on this endpoint: it serves %d Knowledge Bases (%s) and the KB is never inferred",
					routedKBArgument, len(set), strings.Join(set, ", "))), nil
			}
		}
		if !containsString(set, kbName) {
			allowed := strings.Join(m.describeRoutedKBs(set), ", ")
			if _, mounted := m.servers[kbName]; mounted {
				return errorResult(fmt.Sprintf("kb %q is mounted on this server but not bound to this connection (allowed: %s): "+
					"bind it to this client (cartographer client bind), run cartographer sync, then start a new agent session — "+
					"an open session keeps the tool list it started with", kbName, allowed)), nil
			}
			if containsString(routedUnknown(ctx), kbName) {
				return errorResult(fmt.Sprintf("kb %q is in this connection's kbs but is not mounted on this server (renamed or removed?): "+
					"run cartographer sync on this client, then start a new agent session — allowed: %s", kbName, allowed)), nil
			}
			return errorResult(fmt.Sprintf("kb %q is not available on this connection — allowed: %s"+
				"; a KB created after the server started is mounted only by a server restart (cartographer service restart), "+
				"then cartographer sync and a new agent session", kbName, allowed)), nil
		}
		srv := m.servers[kbName]
		srv.mu.Lock()
		_, registered := srv.tools[toolName]
		srv.mu.Unlock()
		if !registered {
			// The union exposed it because a sibling KB has it. Attribute the
			// refusal: the caller must learn which KB refused and why, not that
			// a tool it can see does not exist.
			return errorResult(fmt.Sprintf("kb %q: %s", kbName, unknownToolMessage(toolName))), nil
		}
		res := srv.callTool(withRoutedSet(ctx, set), toolName, rest)
		if toolName == "kb_status" && !res.IsError {
			res = m.withConnectionNotice(res, set, routedUnknown(ctx))
		}
		return res, nil
	}
}

// routedConnectionHint is the fixed text of kb_status.connection.hint.
const routedConnectionHint = "A KB listed in mounted but missing from your tools' kb enum or your instructions was added after this session started: " +
	"run cartographer sync (bind it first with cartographer client bind if it is in unbound), then start a new agent session."

// routedUnknownHint is appended to the hint when the URL named unmounted KBs (D756).
const routedUnknownHint = "Names in unknown are in this connection's kbs but not mounted on this server (renamed or removed?): run cartographer sync on this client."

// withConnectionNotice adds the routed-only `connection` object to a kb_status
// result (D712): the connection's set, every mounted KB, the ones the
// connection does not serve, and the server start time, so an agent whose
// session predates a KB can spot the difference. It decodes and re-encodes the
// JSON instead of appending a content block because CLI callers decode
// Content[0] only and a second block breaks them (doctornudge.go). On any
// decoding trouble the result is returned unchanged: the notice must never
// break kb_status.
func (m *MultiKBServer) withConnectionNotice(res ToolResult, set, unknown []string) ToolResult {
	if len(res.Content) == 0 {
		return res
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.Content[0].Text), &obj); err != nil || obj == nil {
		return res
	}
	mounted := m.mountedNames()
	kbs := append([]string(nil), set...)
	sort.Strings(kbs)
	conn := map[string]any{
		"kbs":               kbs,
		"mounted":           mounted,
		"server_started_at": processStartedAt,
		"hint":              routedConnectionHint,
	}
	if len(unknown) > 0 {
		conn["unknown"] = unknown
		conn["hint"] = routedConnectionHint + " " + routedUnknownHint
	}
	var unbound []string
	for _, n := range mounted {
		if !containsString(set, n) {
			unbound = append(unbound, n)
		}
	}
	if len(unbound) > 0 {
		conn["unbound"] = unbound
	}
	raw, err := json.Marshal(conn)
	if err != nil {
		return res
	}
	obj["connection"] = raw
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return res
	}
	res.Content = append([]ContentBlock(nil), res.Content...)
	res.Content[0].Text = string(out)
	return res
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// describeRoutedKBs names the mounted KBs with their state, so an error about
// an unknown kb carries the same information readiness() already assembles
// rather than a bare list.
func (m *MultiKBServer) describeRoutedKBs(set []string) []string {
	_, kbs, degraded := m.readiness()
	bad := map[string]bool{}
	for _, d := range degraded {
		bad[d] = true
	}
	out := make([]string, 0, len(kbs))
	for _, info := range kbs {
		if !containsString(set, info.Name) {
			continue
		}
		state := info.Status
		if bad[info.Name] {
			state = "degraded"
		}
		out = append(out, fmt.Sprintf("%s (%s)", info.Name, state))
	}
	sort.Strings(out)
	return out
}

// splitKBArgument extracts the `kb` argument and returns the remaining
// arguments with it removed, so the per-KB handler receives exactly the
// arguments it would have received on its own endpoint.
func splitKBArgument(args json.RawMessage) (string, json.RawMessage, error) {
	if len(args) == 0 {
		return "", json.RawMessage(`{}`), nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return "", nil, fmt.Errorf("invalid params: %v", err)
	}
	v, ok := raw[routedKBArgument]
	if !ok {
		return "", args, nil
	}
	var name string
	if err := json.Unmarshal(v, &name); err != nil {
		return "", nil, fmt.Errorf("invalid params: %q must be a string", routedKBArgument)
	}
	delete(raw, routedKBArgument)
	rest, err := json.Marshal(raw)
	if err != nil {
		return "", nil, fmt.Errorf("invalid params: %v", err)
	}
	return strings.TrimSpace(name), rest, nil
}

// withKBProperty shapes the `kb` property of one tool's input schema for a
// connection's KB set (D288): with two or more KBs it is a required enum of the
// set; with exactly one the property is absent, because the URL already named
// the KB. It edits the decoded schema object rather than the JSON text: a tool
// whose schema is absent or not an object is given the minimal one.
func withKBProperty(schema json.RawMessage, set []string) json.RawMessage {
	obj := map[string]any{}
	if len(schema) > 0 {
		if err := json.Unmarshal(schema, &obj); err != nil {
			obj = map[string]any{}
		}
	}
	obj["type"] = "object"
	if len(set) < 2 {
		out, err := json.Marshal(obj)
		if err != nil {
			return schema
		}
		return out
	}

	props, _ := obj["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	enum := make([]any, len(set))
	for i, n := range set {
		enum[i] = n
	}
	props[routedKBArgument] = map[string]any{
		"type":        "string",
		"enum":        enum,
		"description": "Knowledge Base this call is for. Required: this endpoint serves several KBs and never infers one.",
	}
	obj["properties"] = props

	var req []any
	if existing, ok := obj["required"].([]any); ok {
		req = existing
	}
	found := false
	for _, r := range req {
		if s, ok := r.(string); ok && s == routedKBArgument {
			found = true
			break
		}
	}
	if !found {
		req = append([]any{routedKBArgument}, req...)
	}
	obj["required"] = req

	out, err := json.Marshal(obj)
	if err != nil {
		return schema
	}
	return out
}
