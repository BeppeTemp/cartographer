package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
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
	m.routed = m.buildRouted(names, routedNormal)
	m.routedViews[routedViewKey(names, routedNormal)] = m.routed
	m.routedNames = names
	return nil
}

// AccessReadValue is an accepted value of the routed endpoint's `access`
// query parameter. Absent keeps the connection's normal access.
const AccessReadValue = "read"

// AccessReadMinValue is the second accepted value of `access` (D1184): read-only,
// and tools/list narrowed to the minimal reading profile (readMinTools).
const AccessReadMinValue = "read-min"

// ProvisionedParam and ProvisionedValue are the routed endpoint's `provisioned`
// query parameter (D1097): a client that has the managed instructions block
// sends it, and gets no operating guide in `initialize`. Absent or empty means
// "not provisioned": the connection gets the guide. A server that predates the
// parameter ignores it (unknown query parameters are ignored), so a client
// always sends it.
const (
	ProvisionedParam = "provisioned"
	ProvisionedValue = "1"
)

// AccessHeader is set on every response of a read-only routed session. A bridge
// that asked for ?access=read checks it: a server that predates the parameter
// ignores it and would serve a read-write session without saying so.
const AccessHeader = "Cartographer-Access"

// routedMode is how a routed view treats writes (D1096). It is part of the
// view's cache key, so the cached Server never depends on who asks.
type routedMode int

const (
	// routedNormal lists every tool; the target KB decides each call.
	routedNormal routedMode = iota
	// routedTokenReadOnly is the view of a principal that can write none of
	// the set: no write tool is listed, and a call to one falls through to the
	// target KB, which refuses with read_only_token. No instructions.
	routedTokenReadOnly
	// routedBridgeReadOnly is the bridge's twin (?access=read, D1018): no
	// write tool, the workspace-bind sentence, and a call to a write tool is
	// refused with unbound_folder at this level.
	routedBridgeReadOnly
	// routedReadMin is ?access=read-min (D1184): no write tool, only the
	// readMinTools listed (the other read tools stay callable by name), and a
	// call to a write tool is refused with read_only_profile at this level.
	routedReadMin
)

// denyNoReadableKBHint is the hint of the denied view: it names no KB.
const denyNoReadableKBHint = "the token has no read access to any KB this server serves"

// buildRouted assembles the routed server for one effective KB set: the union
// of those KBs' tools, once, with the schemas shaped for that set. The mode
// shapes the read-only views (see routedMode). An empty set is the denied
// view: no tool, and every metadata call is refused.
func (m *MultiKBServer) buildRouted(set []string, mode routedMode) *Server {
	routed := New(m.routedVersion)
	routed.readOnlySession = mode == routedBridgeReadOnly
	routed.tokenReadOnly = mode == routedTokenReadOnly
	routed.readMinSession = mode == routedReadMin
	routed.SetDisplayName("cartographer")
	routed.connKBs = append([]string(nil), set...)
	routed.connKB = func(name string) *kb.KB {
		if srv := m.servers[name]; srv != nil {
			return srv.kbRef
		}
		return nil
	}
	if m.routedSetup != nil {
		m.routedSetup(routed)
	}
	// The real authorization decision belongs to the target KB and is taken
	// inside its own callTool, after `kb` has been resolved. What this
	// authorizer still has to answer is the metadata gate (initialize, ping,
	// tools/list): a caller with no resolvable principal must be denied rather
	// than implicitly treated as having full access. The set is already the
	// KBs the principal reads (routedFor), so this is defence in depth (D1096).
	routed.SetAuthorizer(func(ctx context.Context, tool string, args json.RawMessage) error {
		if tool != "" {
			// Fail-closed: an unknown name requires write (ToolRequiresWrite).
			if mode == routedBridgeReadOnly && ToolRequiresWrite(tool) {
				return deny(denyUnboundFolder)
			}
			// A write is refused here, not left to the target KB: a read-write
			// token would be accepted there (D1184).
			if mode == routedReadMin && ToolRequiresWrite(tool) {
				return deny(denyReadOnlyProfile)
			}
			return nil
		}
		if len(set) == 0 {
			return deny(denyNoKBAccess, denyNoReadableKBHint)
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

// provisionedKey marks a request whose client declared itself provisioned
// (?provisioned=1, D1097). TRAP: like unknown and renamed it rides the request
// context and never the cached routed view: the view is shared by every URL that
// resolves to the same set, and the target KB's server (where the doctor nudge
// runs) is not the view.
type provisionedKey struct{}

func withProvisioned(ctx context.Context) context.Context {
	return context.WithValue(ctx, provisionedKey{}, true)
}

// isProvisioned reports whether the request carried ?provisioned=1 on the
// routed endpoint.
func isProvisioned(ctx context.Context) bool {
	v, _ := ctx.Value(provisionedKey{}).(bool)
	return v
}

// operatorKey marks a request that is the operator's own surface (the Atlas UI
// API), which keeps the full, command-bearing texts like a provisioned client.
type operatorKey struct{}

func withOperator(ctx context.Context) context.Context {
	return context.WithValue(ctx, operatorKey{}, true)
}

// addressesOperator reports whether the texts of this request may name a
// `cartographer` command or the operator's tools (D1101): true for a
// connection that declared ?provisioned=1 and for the Atlas. Everything else
// (per-KB endpoints, stdio, an unmarked routed URL, tests) is generic and gets
// neutral wording. The marker selects wording, never access. TRAP: like
// provisionedKey it rides the request context and never the cached routed
// view, and the texts are read in the target KB's Server, which the context
// reaches through routedHandler's callTool.
func addressesOperator(ctx context.Context) bool {
	if isProvisioned(ctx) {
		return true
	}
	v, _ := ctx.Value(operatorKey{}).(bool)
	return v
}

// routedRenamedKey carries the request's names resolved through kb_aliases
// (old → new), request-scoped for the same reason as routedUnknownKey.
type routedRenamedKey struct{}

func withRoutedRenamed(ctx context.Context, renamed map[string]string) context.Context {
	if len(renamed) == 0 {
		return ctx
	}
	return context.WithValue(ctx, routedRenamedKey{}, renamed)
}

func routedRenamed(ctx context.Context) map[string]string {
	r, _ := ctx.Value(routedRenamedKey{}).(map[string]string)
	return r
}

// SetKBAliases records the renamed KBs' old names (D772). Call it after every
// MountKB: a mounted name always beats an alias, so an item whose from is
// mounted is logged and dropped here, once, instead of being consulted on every
// request. An item whose target is not mounted is kept and stays unknown at
// resolution, because only a restart changes the mounted set.
func (m *MultiKBServer) SetKBAliases(aliases []config.KBAlias) {
	m.routedAliases = map[string]string{}
	for _, a := range aliases {
		if _, mounted := m.servers[a.From]; mounted {
			log.Printf("kb_aliases: %q is a mounted KB, so the alias to %q is ignored (a mounted name wins)", a.From, a.To)
			continue
		}
		m.routedAliases[a.From] = a.To
	}
}

// routedScopeHint is appended to the refusal of a KB outside the connection's
// set. A client with a workspace binding starts a local bridge that sets the
// set from the folder the session runs in (D737), so "not allowed" can mean
// "this folder is bound elsewhere". No local path is sent to the server: the
// agent is told which command shows the answer on the machine that has it.
const routedScopeHint = "If the client has a workspace binding, this connection's KB set comes from the folder " +
	"the session was started in: run `cartographer workspace resolve` there to see which binding applies."

// routedFor resolves the `kbs` query parameter of a routed request to the
// server that answers it (D288). The effective set is what the principal can
// read, narrowed by `kbs` (D1096): absent or empty means every mounted KB it
// reads. The binding only narrows: the effective set is the requested names
// that are mounted and readable (D756). A name that is not mounted but is the
// old name of a renamed KB (kb_aliases, D772) selects that KB, if the target is
// readable too; the pair is returned so the caller reports it, and a mounted
// name always wins over an alias. Names that are neither (a KB removed since
// the client was synced, or one the principal cannot read: the two are
// indistinguishable by design, D1096) are returned sorted, not dropped
// silently: the caller reports them to the agent. Duplicates are ignored and
// per-KB authorization still runs at the target, by the name the KB has now:
// an alias selects a KB, it never grants access.
//
// An empty effective set stays an error naming every unknown name and the
// readable KBs. It must not become "every mounted KB": that would widen the
// binding, which only narrows, and an empty set would be a server with no
// tools. A principal that reads no mounted KB at all gets the denied view (a
// Server with an empty set: D118 has no HTTP 403 from the gate).
//
// One Server is built per distinct (effective set, mode) and cached: the set
// is part of the URL, so tools/list differs per connection and cannot come
// from a single shared registry. The number of views is bounded by the subsets
// of the mounted KBs times the modes; stale URLs with different unknown or
// renamed names share a view.
//
// TRAP: the cache key is the set and the mode and nothing else. A cached Server
// never carries the principal or the request's names (unknown, renamed): those
// come from the request context, as routedHandler reads them.
//
// access (?access=, "", AccessReadValue or AccessReadMinValue) wins over the
// token's own read-only view: the client asked for it and checks the response
// header.
func (m *MultiKBServer) routedFor(ctx context.Context, rawKBs []string, access string) (*Server, []string, map[string]string, error) {
	seen := map[string]bool{}
	for _, raw := range rawKBs {
		for _, n := range strings.Split(raw, ",") {
			if n = strings.TrimSpace(n); n != "" {
				seen[n] = true
			}
		}
	}
	readable := readableKBs(ctx, m.routedNames)
	if len(readable) == 0 {
		return m.routedView(nil, routedNormal), nil, nil, nil
	}
	if len(seen) == 0 {
		return m.routedView(readable, m.routedModeFor(ctx, readable, access)), nil, nil, nil
	}
	isReadable := make(map[string]bool, len(readable))
	for _, n := range readable {
		isReadable[n] = true
	}
	var unknown []string
	renamed := map[string]string{}
	inSet := map[string]bool{}
	for n := range seen {
		if isReadable[n] {
			inSet[n] = true
			continue
		}
		// TRAP: the mounted check comes first. A name that is mounted is never
		// looked up as an alias, so a KB created later under an old name is
		// reached by its own name, not redirected to the renamed one. (A
		// mounted name the principal cannot read falls to unknown below, with
		// no alias lookup: SetKBAliases drops an alias whose from is mounted.)
		if to, ok := m.routedAliases[n]; ok && isReadable[to] {
			renamed[n] = to
			inSet[to] = true
			continue
		}
		unknown = append(unknown, n)
	}
	set := make([]string, 0, len(inSet))
	for n := range inSet {
		set = append(set, n)
	}
	sort.Strings(set)
	sort.Strings(unknown)
	if len(set) == 0 {
		return nil, unknown, nil, fmt.Errorf("unknown kb in kbs: %s not mounted on this server, which mounts %s",
			quoteJoin(unknown), strings.Join(readable, ", "))
	}
	key := strings.Join(set, ",")
	m.routedMu.Lock()
	if len(renamed) > 0 {
		warn := key + "|renamed|" + renamedKey(renamed)
		if !m.routedWarned[warn] {
			if m.routedWarned == nil {
				m.routedWarned = map[string]bool{}
			}
			m.routedWarned[warn] = true
			log.Printf("routed mount: kbs names %s resolved through kb_aliases; serving %s (the client URL is stale: run cartographer sync on the client)",
				renamedKey(renamed), key)
		}
	}
	if len(unknown) > 0 {
		warn := key + "|" + strings.Join(unknown, ",")
		if !m.routedWarned[warn] {
			if m.routedWarned == nil {
				m.routedWarned = map[string]bool{}
			}
			m.routedWarned[warn] = true
			log.Printf("routed mount: kbs names %s not mounted or not readable by the token; serving %s (renamed or removed? run cartographer sync on the client)",
				quoteJoin(unknown), key)
		}
	}
	m.routedMu.Unlock()
	if len(renamed) == 0 {
		renamed = nil
	}
	return m.routedView(set, m.routedModeFor(ctx, set, access)), unknown, renamed, nil
}

// routedModeFor picks the view mode of a connection over set (see routedFor).
func (m *MultiKBServer) routedModeFor(ctx context.Context, set []string, access string) routedMode {
	switch {
	case access == AccessReadMinValue:
		return routedReadMin
	case access == AccessReadValue:
		return routedBridgeReadOnly
	case !writesAnyKB(ctx, set):
		return routedTokenReadOnly
	}
	return routedNormal
}

// routedView returns the cached view for (set, mode), building it on first use.
// The default view (every mounted KB, normal) is the pre-built m.routed.
func (m *MultiKBServer) routedView(set []string, mode routedMode) *Server {
	key := routedViewKey(set, mode)
	m.routedMu.Lock()
	defer m.routedMu.Unlock()
	if srv, ok := m.routedViews[key]; ok {
		return srv
	}
	srv := m.buildRouted(set, mode)
	m.routedViews[key] = srv
	return srv
}

// routedViewKey is the one function that names a view in routedViews: the
// sorted set and the mode. A KB name cannot contain "|", so a suffix never
// collides with a set key. An empty set is the denied view.
func routedViewKey(set []string, mode routedMode) string {
	if len(set) == 0 {
		return "|denied"
	}
	key := strings.Join(set, ",")
	switch mode {
	case routedTokenReadOnly:
		key += "|token=read"
	case routedBridgeReadOnly:
		key += "|access=read"
	case routedReadMin:
		key += "|access=read-min"
	}
	return key
}

// renamedKey renders old → new pairs in a stable order, for logs.
func renamedKey(renamed map[string]string) string {
	pairs := make([]string, 0, len(renamed))
	for from, to := range renamed {
		pairs = append(pairs, fmt.Sprintf("%q → %q", from, to))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ", ")
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
// silently hide a tool from a KB that registers it because a sibling KB does
// not; the union registers it once and refuses it per KB at dispatch, with an
// error naming the KB — which is the answer a caller can act on.
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
			routedTool.InputSchema = withKBProperty(t.InputSchema, len(set) >= 2)
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
		// A session that outlived the rename still names the old KB in its
		// tool schemas and instructions: dispatch to the KB it became. The
		// call runs through that KB's own callTool, so policy, audit and the
		// git wrapper are the target's (D772).
		if to, ok := routedRenamed(ctx)[kbName]; ok {
			kbName = to
		}
		if !containsString(set, kbName) {
			allowed := strings.Join(m.describeRoutedKBs(set), ", ")
			// D1096: only a KB the principal can read is confirmed as mounted;
			// an unreadable one is "unknown" (named in the URL) or "not
			// available", with the same texts as a name that does not exist.
			operator := addressesOperator(ctx)
			if _, mounted := m.servers[kbName]; mounted && kbRosterVisible(ctx, kbName) {
				if !operator {
					return errorResult(fmt.Sprintf("kb %q is mounted on this server but is not part of this connection (allowed: %s). "+
						"The connection's KB set is fixed when the integration connects: whoever runs the integration adds the KB to its URL (?kbs=), "+
						"then the integration reconnects or re-synchronizes its tool list — an open session keeps the tool list it started with.", kbName, allowed)), nil
				}
				return errorResult(fmt.Sprintf("kb %q is mounted on this server but not bound to this connection (allowed: %s): "+
					"bind it to this client (cartographer client bind), run cartographer sync, then start a new agent session — "+
					"an open session keeps the tool list it started with. "+routedScopeHint, kbName, allowed)), nil
			}
			if containsString(routedUnknown(ctx), kbName) {
				if !operator {
					return errorResult(fmt.Sprintf("kb %q is named by this connection's URL but is not mounted on this server (renamed or removed?): "+
						"whoever runs the integration needs to update the URL — allowed: %s", kbName, allowed)), nil
				}
				return errorResult(fmt.Sprintf("kb %q is in this connection's kbs but is not mounted on this server (renamed or removed?): "+
					"run cartographer sync on this client, then start a new agent session — allowed: %s", kbName, allowed)), nil
			}
			if !operator {
				return errorResult(fmt.Sprintf("kb %q is not available on this connection — allowed: %s. "+
					"A KB created after the server started is served only once the server's operator restarts the server; "+
					"the integration then reconnects or re-synchronizes its tool list.", kbName, allowed)), nil
			}
			return errorResult(fmt.Sprintf("kb %q is not available on this connection — allowed: %s"+
				"; a KB created after the server started is mounted only by a server restart (cartographer service restart), "+
				"then cartographer sync and a new agent session. "+routedScopeHint, kbName, allowed)), nil
		}
		srv := m.servers[kbName]
		srv.mu.Lock()
		_, registered := srv.tools[toolName]
		srv.mu.Unlock()
		if !registered {
			// The union exposed it because a sibling KB has it. Attribute the
			// refusal: the caller must learn which KB refused and why, not that
			// a tool it can see does not exist.
			return errorResult(fmt.Sprintf("kb %q: tool not found: %s", kbName, toolName)), nil
		}
		res := srv.callTool(withRoutedSet(ctx, set), toolName, rest)
		if toolName == "kb_status" && !res.IsError {
			res = m.withConnectionNotice(ctx, res, set, routedUnknown(ctx), routedRenamed(ctx))
		}
		return res, nil
	}
}

// routedConnectionHint is the fixed text of kb_status.connection.hint.
const routedConnectionHint = "A KB listed in mounted but missing from your instructions' KB list was added after this session started: " +
	"run cartographer sync (bind it first with cartographer client bind if it is in unbound), then start a new agent session."

// The generic wordings of the three hints (D1101): the same facts, no command.
const (
	routedConnectionHintGeneric = "A KB listed in mounted but missing from your instructions' KB list was added after this session started: " +
		"the integration picks it up when it reconnects or re-synchronizes its tool list."
	routedUnknownHintGeneric = "Names in unknown are in this connection's kbs but not mounted on this server (renamed or removed?): the integration's URL needs updating."
	routedRenamedHintGeneric = "Names in renamed are the old names of KBs served under their new name: the URL still works, but it is stale: update the integration's URL."
)

// routedUnknownHint is appended to the hint when the URL named unmounted KBs (D756).
const routedUnknownHint = "Names in unknown are in this connection's kbs but not mounted on this server (renamed or removed?): run cartographer sync on this client."

// routedRenamedHint is appended to the hint when the URL named a renamed KB by
// its old name (D772).
const routedRenamedHint = "Names in renamed are the old names of KBs served under their new name: the URL still works, but it is stale: run cartographer sync on this client."

// withConnectionNotice adds the routed-only `connection` object to a kb_status
// result (D712): the connection's set, every mounted KB the principal reads
// (D1096: no other name is spoken), the ones the
// connection does not serve, and the server start time, so an agent whose
// session predates a KB can spot the difference. It decodes and re-encodes the
// JSON instead of appending a content block because CLI callers decode
// Content[0] only and a second block breaks them (doctornudge.go). On any
// decoding trouble the result is returned unchanged: the notice must never
// break kb_status.
func (m *MultiKBServer) withConnectionNotice(ctx context.Context, res ToolResult, set, unknown []string, renamed map[string]string) ToolResult {
	if len(res.Content) == 0 {
		return res
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.Content[0].Text), &obj); err != nil || obj == nil {
		return res
	}
	mounted := readableKBs(ctx, m.mountedNames())
	kbs := append([]string(nil), set...)
	sort.Strings(kbs)
	base, unknownHint, renamedHint := routedConnectionHint, routedUnknownHint, routedRenamedHint
	if !addressesOperator(ctx) {
		base, unknownHint, renamedHint = routedConnectionHintGeneric, routedUnknownHintGeneric, routedRenamedHintGeneric
	}
	conn := map[string]any{
		"kbs":               kbs,
		"mounted":           mounted,
		"server_started_at": processStartedAt,
		"hint":              base,
	}
	if len(unknown) > 0 {
		conn["unknown"] = unknown
		conn["hint"] = base + " " + unknownHint
	}
	if len(renamed) > 0 {
		conn["renamed"] = renamed
		conn["hint"] = conn["hint"].(string) + " " + renamedHint
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
	out, err := json.Marshal(obj)
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

// routedKBDescription is the description of the `kb` property: fixed, free of
// any KB name, pointing at the KB list the connection already has (D1183).
const routedKBDescription = "Target KB: a name from the KB list in your instructions."

// withKBProperty shapes the `kb` property of one tool's input schema for a
// connection's KB set (D288, D1183): with two or more KBs it is a required
// string with no enum and a short description that is the same for every set,
// so the list does not grow with the number of KBs (the valid names are in the
// instructions' KB list, and a wrong one is refused server-side naming them);
// with exactly one the property is absent, because the URL already named the KB. It edits the decoded schema object rather than the JSON text: a tool
// whose schema is absent or not an object is given the minimal one.
//
// Outside Go, deploy/demo/smoke.sh and test/e2e scenario 17 assert this shape.
// The demo smoke runs in CI's docker job, which a PR touching only internal/
// does not trigger (scripts/ci-filters.sh): change the shape, update it too.
func withKBProperty(schema json.RawMessage, several bool) json.RawMessage {
	obj := map[string]any{}
	if len(schema) > 0 {
		if err := json.Unmarshal(schema, &obj); err != nil {
			obj = map[string]any{}
		}
	}
	obj["type"] = "object"
	if !several {
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
	props[routedKBArgument] = map[string]any{
		"type":        "string",
		"description": routedKBDescription,
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
