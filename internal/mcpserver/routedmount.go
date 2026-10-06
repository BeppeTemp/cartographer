package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// additive over the per-KB plumbing: MountKB/MountKBWithPrefix and the ?kb=/
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
// Call it after every MountKB/MountKBWithPrefix. It returns an error when no KB
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
		var lastErr error = errors.New("forbidden")
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

// routedFor resolves the `kbs` query parameter of a routed request to the
// server that answers it (D288). Absent or empty means every mounted KB. The
// binding only narrows: a name that is not mounted is an error naming it and
// the mounted KBs (a typo must not silently hide a KB), duplicates are
// ignored, and per-KB authorization still runs at the target.
//
// One Server is built per distinct effective set and cached: the set is part
// of the URL, so tools/list differs per connection and cannot come from a
// single shared registry. The number of sets is bounded by the subsets of the
// mounted KBs.
func (m *MultiKBServer) routedFor(rawKBs []string) (*Server, error) {
	seen := map[string]bool{}
	for _, raw := range rawKBs {
		for _, n := range strings.Split(raw, ",") {
			if n = strings.TrimSpace(n); n != "" {
				seen[n] = true
			}
		}
	}
	if len(seen) == 0 {
		return m.routed, nil
	}
	set := make([]string, 0, len(seen))
	for n := range seen {
		if _, ok := m.servers[n]; !ok {
			return nil, fmt.Errorf("unknown kb %q in kbs: this server mounts %s", n, strings.Join(m.routedNames, ", "))
		}
		set = append(set, n)
	}
	sort.Strings(set)
	key := strings.Join(set, ",")
	m.routedMu.Lock()
	defer m.routedMu.Unlock()
	if srv, ok := m.routedViews[key]; ok {
		return srv, nil
	}
	srv := m.buildRouted(set)
	m.routedViews[key] = srv
	return srv, nil
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
			return errorResult(fmt.Sprintf("kb %q is not available on this connection — allowed: %s",
				kbName, strings.Join(m.describeRoutedKBs(set), ", "))), nil
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
		return srv.callTool(ctx, toolName, rest), nil
	}
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

// RoutedMounted reports whether a routed mount is configured on this server.
func (m *MultiKBServer) RoutedMounted() bool { return m.routed != nil }
