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
// additive: MountKB/MountKBWithPrefix and the ?kb=/ /mcp/<name> endpoints keep
// their exact behaviour, tools/list output included, so nobody's working setup
// changes on upgrade.
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
// Call it after every MountKB/MountKBWithPrefix. It returns an error when the
// configuration is inconsistent; on success the routed endpoint is served
// alongside the per-KB ones.
//
// A KB carrying a tool-name prefix (D102) cannot be routed: prefixes exist to
// disambiguate N mounts sharing one flat namespace, and with a single mount
// there is nothing to disambiguate — a prefix would only re-inflate the names
// this mount exists to shrink. Routing and prefixing are alternative answers to
// the same problem, so asking for both is a config error, not a silently
// ignored setting.
func (m *MultiKBServer) EnableRoutedMount(version string, setup func(s *Server)) error {
	if len(m.servers) == 0 {
		return errors.New("routed mount: no KB is mounted")
	}
	names := m.mountedNames()
	for _, name := range names {
		if name == strings.TrimPrefix(RoutedMountPath, "/mcp/") {
			return fmt.Errorf("KB %q collides with the routed mount's own path %s: rename the KB, or serve it per-KB",
				name, RoutedMountPath)
		}
	}
	for _, info := range m.kbs {
		if info.ToolPrefix != "" {
			return fmt.Errorf("KB %q: tool_prefix %q cannot be combined with mcp.mount_mode: routed — "+
				"a routed mount exposes one copy of each tool, so there is no flat namespace to disambiguate; "+
				"drop the prefix for this KB or serve it per-KB", info.Name, info.ToolPrefix)
		}
	}

	routed := New(version)
	routed.SetDisplayName("cartographer")
	if setup != nil {
		setup(routed)
	}
	// The real authorization decision belongs to the target KB and is taken
	// inside its own callTool, after `kb` has been resolved. What this
	// authorizer still has to answer is the metadata gate (initialize, ping,
	// tools/list): a caller with no resolvable principal must be denied rather
	// than implicitly treated as having full access. On a routed mount the
	// honest rule is "can reach at least one of the routed KBs" — a principal
	// scoped to one KB legitimately lists the union and is refused per call on
	// the others.
	routed.SetAuthorizer(func(ctx context.Context, tool string, args json.RawMessage) error {
		if tool != "" {
			return nil
		}
		var lastErr error = errors.New("forbidden")
		for _, name := range names {
			if err := m.servers[name].authorize(ctx, "", args); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
		return lastErr
	})

	for _, t := range m.unionTools() {
		routed.RegisterTool(t)
	}

	m.routed = routed
	m.routedNames = names
	return nil
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

// unionTools assembles the routed mount's tool list: every tool registered by
// any mounted KB, once, in the registration order of the first KB that has it,
// with the `kb` argument injected into each schema at this single point rather
// than in fifty schema literals.
//
// The exposed set is the **union**, not the intersection. An intersection would
// silently hide artifact_write from a KB that allows it because a sibling KB
// does not; the union registers it once and refuses it per KB at dispatch, with
// an error naming the KB and the setting that would enable it — which is the
// answer a caller can act on.
func (m *MultiKBServer) unionTools() []Tool {
	seen := map[string]bool{}
	var out []Tool
	for _, name := range m.mountedNames() {
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
			routedTool.InputSchema = withKBProperty(t.InputSchema, len(m.servers) > 1)
			routedTool.Handler = m.routedHandler(toolName)
			out = append(out, routedTool)
		}
	}
	return out
}

// routedHandler resolves `kb`, strips it from the arguments and hands the call
// to that KB's own Server.callTool — the same entry point the per-KB endpoint
// uses, so authorization, audit, the read/write classification and the git
// wrapper are the per-KB ones, unchanged, applied to the KB actually named.
func (m *MultiKBServer) routedHandler(toolName string) func(context.Context, json.RawMessage) (ToolResult, error) {
	return func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
		kbName, rest, err := splitKBArgument(args)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		if kbName == "" {
			if len(m.routedNames) == 1 {
				// Exactly one KB routed: there is no ambiguity to resolve, so
				// requiring the argument would be ceremony.
				kbName = m.routedNames[0]
			} else {
				return errorResult(fmt.Sprintf(
					"%q is required on this endpoint: it serves %d Knowledge Bases (%s) and the KB is never inferred",
					routedKBArgument, len(m.routedNames), strings.Join(m.routedNames, ", "))), nil
			}
		}
		srv, ok := m.servers[kbName]
		if !ok {
			return errorResult(fmt.Sprintf("unknown kb %q — this endpoint serves: %s",
				kbName, strings.Join(m.describeRoutedKBs(), ", "))), nil
		}
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

// describeRoutedKBs names the mounted KBs with their state, so an error about
// an unknown kb carries the same information readiness() already assembles
// rather than a bare list.
func (m *MultiKBServer) describeRoutedKBs() []string {
	_, kbs, degraded := m.readiness()
	bad := map[string]bool{}
	for _, d := range degraded {
		bad[d] = true
	}
	out := make([]string, 0, len(kbs))
	for _, info := range kbs {
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

// withKBProperty injects the `kb` property into one tool's input schema, and
// marks it required when more than one KB is routed. It edits the decoded
// schema object rather than the JSON text: a tool whose schema is absent or
// not an object is given the minimal one, so no tool can reach the routed
// mount without the argument it is dispatched by.
func withKBProperty(schema json.RawMessage, required bool) json.RawMessage {
	obj := map[string]any{}
	if len(schema) > 0 {
		if err := json.Unmarshal(schema, &obj); err != nil {
			obj = map[string]any{}
		}
	}
	obj["type"] = "object"

	props, _ := obj["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	desc := "Knowledge Base this call is for; optional while this endpoint serves a single KB."
	if required {
		desc = "Knowledge Base this call is for. Required: this endpoint serves several KBs and never infers one."
	}
	props[routedKBArgument] = map[string]any{"type": "string", "description": desc}
	obj["properties"] = props

	if required {
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
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return schema
	}
	return out
}

// RoutedMounted reports whether a routed mount is configured on this server.
func (m *MultiKBServer) RoutedMounted() bool { return m.routed != nil }
