package main

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// statusSchema is deliberately versioned: scripts can reject incompatible
// additions while table output remains a human convenience.
const statusSchema = "cartographer.status/v1"

type statusError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cause   string `json:"cause,omitempty"`
}

type providerStatus struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	// DetectedBy names the heuristic behind Installed (#305): a binary on PATH
	// and a leftover config directory both set Installed, and only this field
	// tells them apart. Empty when the provider was not detected.
	DetectedBy   string `json:"detected_by,omitempty"`
	Connected    bool   `json:"connected"`
	State        string `json:"state"`
	Revision     string `json:"revision,omitempty"`
	LockRevision string `json:"lock_revision,omitempty"`
	Kinds        string `json:"kind_status,omitempty"`
	// Problem is why sync would refuse this provider before projecting it: a
	// bound workspace that is gone, or a provider that cannot be scoped to a
	// workspace. Empty otherwise.
	Problem string `json:"problem,omitempty"`
	// ShadowedInstructions names the provider-owned file that takes precedence
	// over the instructions file Cartographer manages, when one exists (D189).
	// A block written correctly into a file the provider does not read is not
	// installed, so it is excluded from the Kinds count and reported here.
	ShadowedInstructions string           `json:"shadowed_instructions,omitempty"`
	Added                []statusArtifact `json:"added,omitempty"`
	Updated              []statusArtifact `json:"updated,omitempty"`
	Removed              []statusArtifact `json:"removed,omitempty"`
	// Diverged lists managed artifacts whose files on disk no longer match
	// what was materialized (D139): edited by hand, deleted, or with their
	// managed key/block removed from a shared file. `cartographer sync`
	// restores them.
	Diverged []statusArtifact `json:"diverged,omitempty"`
	// ServerVersion is the server version this provider's state was
	// materialized against, as recorded in the lockfile (D142). Empty when
	// unknown (a lockfile written before D142). Shown next to the live
	// version so a server change is inspectable without running a sync.
	ServerVersion string `json:"materialized_server_version,omitempty"`
	// HookLimitAcked: connect already showed this provider's SessionHookLimit
	// (D320), so the human status output does not repeat the timer hint for
	// it. Not part of the JSON contract.
	HookLimitAcked bool `json:"-"`
	// BoundKBs and BindingOrigin describe which KBs this provider may receive
	// and whether that was declared or defaulted (D170). Without them the same
	// list of names means two different things and the reader cannot tell.
	BoundKBs      []string `json:"bound_kbs,omitempty"`
	BindingOrigin string   `json:"binding_origin,omitempty"`
	// ArtifactsOff names the bound KBs with `kb_options.<kb>.artifacts: false`
	// (#1408): their missing skills are a setting, not a failure.
	ArtifactsOff []string `json:"artifacts_off,omitempty"`
	// KBCounts breaks the materialized artifacts down by source KB, read from
	// the lockfile's per-file Source (D170). Empty for a lockfile written
	// before that field existed: unknown, not zero.
	KBCounts map[string]int `json:"kb_counts,omitempty"`
	// Workspaces reports this provider's workspace projections (D193), empty
	// for a provider in the default scope. A workspace-scoped provider's own
	// counts above describe its bundle-only global projection, which is
	// deliberately not the interesting part: the KB artifacts live here.
	Workspaces []workspaceStatus `json:"workspaces,omitempty"`
	// InstructionsBlocks measures the managed instructions block of each
	// projection that wrote one (D1182): the block is paid on every model call
	// of every session. Informational, never drift.
	InstructionsBlocks []instructionsBlockStatus `json:"instructions_blocks,omitempty"`
}

// instructionsBlockStatus is the size of one steering file's managed block.
// Tokens are bytes/4, approximate. Largest names up to three KB regions.
type instructionsBlockStatus struct {
	Path         string                     `json:"path"`
	Bytes        int                        `json:"bytes"`
	ApproxTokens int                        `json:"approx_tokens"`
	Threshold    int                        `json:"threshold"`
	Over         bool                       `json:"over"`
	Largest      []provisioning.KBBlockSize `json:"largest,omitempty"`
}

// snapshotInstructionsBlocks measures, for provider, the block of every
// projection the lockfile records as having written one (the global one and
// each workspace's). A projection that wrote none (a derived binding, D1019, or
// a refused write, D148) has no instructions entry and measures nothing.
func snapshotInstructionsBlocks(lockFile provisioning.LockFile, provider, dir string) []instructionsBlockStatus {
	var out []instructionsBlockStatus
	seen := map[string]bool{}
	for _, proj := range lockFile.Projections() {
		if proj.Provider != provider {
			continue
		}
		lock := lockFile.ForProjection(proj)
		base := provisioning.LockBaseDir(lock, dir)
		for _, mf := range lock.Managed {
			if mf.Kind != "instructions" {
				continue
			}
			path := filepath.Join(base, mf.Path)
			if seen[path] {
				continue
			}
			seen[path] = true
			size := provisioning.MeasureInstructionsBlock(path)
			if size.Bytes == 0 {
				continue
			}
			largest := size.KBs
			if len(largest) > 3 {
				largest = largest[:3]
			}
			out = append(out, instructionsBlockStatus{
				Path: path, Bytes: size.Bytes, ApproxTokens: size.ApproxTokens(),
				Threshold: provisioning.InstructionsBlockWarnBytes, Over: size.Over(), Largest: largest,
			})
		}
	}
	return out
}

// workspaceStatus is one workspace projection's state, as `status` reports it.
type workspaceStatus struct {
	Path string   `json:"path"`
	KBs  []string `json:"kbs,omitempty"`
	// State is "ok", "gone" (the bound directory is not there any more),
	// "moved" (its git remote is not the one it was bound to) or "inactive"
	// (the files are correct but the client will not read them — a Codex
	// project that is not trusted). Each is reported, none is healed: a
	// projection that silently fell back to the global catalogue is the
	// exposure this whole scope exists to prevent.
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	// Derived marks a binding that comes from a KB's paths.yaml (D1019);
	// Source says which KBs. "unresolved" is a declared repository with no
	// local clone: its Path is "repo:<key>".
	Derived bool   `json:"derived,omitempty"`
	Source  string `json:"source,omitempty"`
}

type statusArtifact struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
	Path   string `json:"path,omitempty"`
	Signed bool   `json:"signed,omitempty"`
	Trust  string `json:"trust,omitempty"`
	// Detail says why a diverged artifact is diverged when the reason alone
	// does not: an "unrunnable" hook names the command and what is wrong with
	// it (D267).
	Detail string `json:"detail,omitempty"`
	// HookSettings is the untracked settings file a pending hook registers in
	// when a derived binding receives it (#1300), resolved by sync's own
	// function; empty otherwise. Only the human output reads it.
	HookSettings string `json:"-"`
}

// serviceSnapshot is part of the `service status --output json` contract:
// fields may be added, existing ones may not change meaning (D174).
//
// Healthy is a verdict only when HealthChecked is true; otherwise
// HealthSkipReason says why nothing was measured. Running means the init
// system knows the job, which on darwin is not proof of a live process —
// Lifecycle is the field to read for the observable state.
type serviceSnapshot struct {
	Installed        bool   `json:"installed"`
	Running          bool   `json:"running"`
	Healthy          bool   `json:"healthy"`
	HTTPAddr         string `json:"http_addr,omitempty"`
	Lifecycle        string `json:"lifecycle,omitempty"`
	HealthChecked    bool   `json:"health_checked"`
	HealthSkipReason string `json:"health_skip_reason,omitempty"`
	// UIURL is additive (D227): present only when the service is installed
	// in HTTP mode with the embedded UI enabled.
	UIURL string `json:"ui_url,omitempty"`
}

// newServiceSnapshot projects a service.Status onto the JSON contract. One
// constructor, so the two call sites cannot drift on a newly added field.
func newServiceSnapshot(st service.Status) *serviceSnapshot {
	return &serviceSnapshot{
		Installed:        st.Installed,
		Running:          st.Running,
		Healthy:          st.Healthy,
		HTTPAddr:         st.HTTPAddr,
		Lifecycle:        string(st.Lifecycle),
		HealthChecked:    st.HealthChecked,
		HealthSkipReason: st.HealthSkipReason,
		UIURL:            st.UIURL,
	}
}

// statusSnapshot has no renderer-specific fields and is the single remote
// read used by status and the dashboard. Provider states are explicit so one
// server failure cannot turn into several conflicting error messages.
type statusSnapshot struct {
	Schema    string           `json:"schema_version"`
	ServerURL string           `json:"server_url,omitempty"`
	Client    string           `json:"client_version"`
	Server    string           `json:"server_version,omitempty"`
	Reachable bool             `json:"reachable"`
	Ready     *bool            `json:"ready,omitempty"`
	KBs       []string         `json:"kbs,omitempty"`
	Providers []providerStatus `json:"providers"`
	Artifacts int              `json:"artifact_count"`
	State     string           `json:"state"`
	Error     *statusError     `json:"error,omitempty"`
	Service   *serviceSnapshot `json:"service,omitempty"`
	// MountMode is the topology the server is serving right now, and
	// ConfiguredMountMode the one this client's MCP entries were written
	// against (D187). When they differ the entries are for the wrong endpoint
	// shape: status reports it, and does not heal it — the fix is an explicit
	// `cartographer reconnect`, the same answer D142 gives to a server-version
	// change.
	MountMode           string `json:"mount_mode,omitempty"`
	ConfiguredMountMode string `json:"configured_mount_mode,omitempty"`
	// Update is set only when a newer release than this client is known
	// (D254), read from the update cache alone: status adds no network call.
	Update *updateSnapshot `json:"update,omitempty"`
	// ServerLatest is the newer release a remote server reports about itself
	// in /health (D254). Loopback servers are this machine's own binary,
	// already covered by Update.
	ServerLatest string `json:"server_latest_version,omitempty"`
	// UnresolvedPlaceholders are the placeholder keys the last sync could not
	// resolve on this machine, read from the lockfile (D262). Additive and
	// omitted when empty; informational like Update — it never changes State,
	// because an unresolved key is left verbatim, not a failed sync.
	UnresolvedPlaceholders []placeholderRow `json:"unresolved_placeholders,omitempty"`
}

// snapshotUnresolvedPlaceholders reads the lockfile's unresolved keys. A
// lockfile that cannot be read reports none here: the snapshot's own lockfile
// read is where that error surfaces.
func snapshotUnresolvedPlaceholders(dir string) []placeholderRow {
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		return nil
	}
	var ignored []string
	if cfg, err := clientconfig.Load(dir); err == nil {
		ignored = cfg.IgnoredPaths
	}
	return unresolvedRows(lf, ignored)
}

// updateSnapshot is the `update` object of the status contract. Additive.
type updateSnapshot struct {
	Latest  string `json:"latest"`
	Kind    string `json:"kind"`
	Channel string `json:"channel"`
	Command string `json:"command,omitempty"`
}

// snapshotUpdate projects the cached update answer onto the snapshot.
func snapshotUpdate() *updateSnapshot {
	res, ch := cachedUpdate()
	if !res.Available {
		return nil
	}
	return &updateSnapshot{Latest: res.Latest, Kind: res.Kind, Channel: string(ch), Command: ch.UpgradeCommand(res.Latest)}
}

// startingStatusCode is the status.error.code of a server that answers but is
// still loading its KBs: reachable, not ready, never "could not reach".
const startingStatusCode = "starting"

func classifyNetworkError(endpoint string, err error) statusError {
	code := "request_failed"
	var remote *client.RemoteError
	if errors.As(err, &remote) && remote.Code == client.CodeStarting {
		return statusError{Code: startingStatusCode, Message: "the server is starting and loading its KBs; retry shortly", Cause: err.Error()}
	}
	if errors.Is(err, client.ErrUnauthorized) {
		code = "unauthorized"
	} else {
		var dnsErr *net.DNSError
		var opErr *net.OpError
		switch {
		case errors.As(err, &dnsErr):
			code = "dns_failed"
		case errors.As(err, &opErr):
			code = "unreachable"
		}
	}
	action := "check the configured URL"
	if isLoopbackURL(endpoint) {
		action = "check `cartographer service status`"
	}
	return statusError{Code: code, Message: fmt.Sprintf("could not reach %s; %s", endpoint, action), Cause: err.Error()}
}

func outputFlag(args []string) (string, []string, error) {
	output := "table"
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--output" && i+1 < len(args) {
			output, i = args[i+1], i+1
			continue
		}
		if strings.HasPrefix(args[i], "--output=") {
			output = strings.TrimPrefix(args[i], "--output=")
			continue
		}
		remaining = append(remaining, args[i])
	}
	if output != "table" && output != "json" {
		return "", nil, fmt.Errorf("invalid --output %q (want table|json)", output)
	}
	return output, remaining, nil
}

func snapshotForConfig(dir string, cfg *clientconfig.Config, includeService bool) statusSnapshot {
	s := statusSnapshot{Schema: statusSchema, Client: version, State: "in_sync", Providers: providerStatuses(cfg), ServerURL: cfg.ServerURL, Update: snapshotUpdate()}
	if includeService && isLoopbackURL(cfg.ServerURL) {
		if st, err := statusServiceFn(); err == nil {
			s.Service = newServiceSnapshot(st)
		}
	}
	health, err := statusHealthFn(cfg)
	if err != nil {
		s.State = "unavailable"
		e := classifyNetworkError(cfg.ServerURL, err)
		s.Error = &e
		if e.Code == startingStatusCode {
			notReady := false
			s.Reachable, s.Ready = true, &notReady
		}
		for i := range s.Providers {
			if s.Providers[i].Connected {
				s.Providers[i].State = "unknown"
			}
		}
		return s
	}
	s.Reachable, s.Server, s.Ready = true, health.Version, health.Ready
	if health.LatestVersion != "" && !isLoopbackURL(cfg.ServerURL) {
		s.ServerLatest = health.LatestVersion
	}
	if health.Routed() {
		s.MountMode = "routed"
	}
	s.ConfiguredMountMode = cfg.ServerMountMode
	if health.KBs != nil {
		for _, kb := range *health.KBs {
			s.KBs = append(s.KBs, kb.Name)
		}
	}
	routedPath := ""
	if health.Routed() {
		routedPath = health.RoutedPath
	}
	connectedProviders := make([]string, 0, len(s.Providers))
	for i := range s.Providers {
		if s.Providers[i].Connected {
			connectedProviders = append(connectedProviders, s.Providers[i].Name)
		}
	}
	// D193: every provider is judged projection by projection, built in ONE
	// call exactly as sync builds them (a bundle-only projection gets the bundle
	// depending on what the whole run pulls, so splitting the call would make
	// status disagree with sync). A workspace-scoped provider has the
	// bundle-only global projection plus one per bound workspace; a default one
	// has the single global projection.
	provProjections := map[string][]syncProjection{}
	projectionErr := map[string]error{}
	var allProjs []syncProjection
	for _, name := range connectedProviders {
		ps, perr := projectionsFor(cfg, name, dir)
		if perr != nil {
			projectionErr[name] = perr
			continue
		}
		provProjections[name] = ps
		allProjs = append(allProjs, ps...)
	}
	var manifests map[string]provisioning.Manifest
	if len(allProjs) > 0 || len(connectedProviders) == 0 {
		manifests, err = statusManifestsFn(cfg, allProjs)
	}
	if err != nil {
		s.State = "unavailable"
		e := classifyNetworkError(cfg.ServerURL, err)
		s.Error = &e
		for i := range s.Providers {
			if s.Providers[i].Connected {
				s.Providers[i].State = "unknown"
			}
		}
		return s
	}
	// The count keeps its meaning: distinct artifacts per provider, summed. An
	// artifact in two workspaces of one provider is one artifact.
	for _, name := range connectedProviders {
		distinct := map[string]bool{}
		for _, sp := range provProjections[name] {
			for _, a := range manifests[projectionKey(sp)].Artifacts {
				distinct[a.Kind+"\x00"+a.Name] = true
			}
		}
		s.Artifacts += len(distinct)
	}
	lockFile, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		s.State = "error"
		s.Error = &statusError{Code: "lockfile_failed", Message: "could not read local sync state", Cause: err.Error()}
		return s
	}
	for i := range s.Providers {
		if !s.Providers[i].Connected {
			continue
		}
		p := &s.Providers[i]
		var pm provisioning.Manifest
		var d provisioning.Diff
		var wsDiverged []provisioning.DriftFinding
		if perr := projectionErr[p.Name]; perr != nil {
			// Sync would refuse this provider (a bound workspace is gone, or
			// the provider cannot be scoped to a workspace): not in sync, and
			// the cause belongs on the card.
			p.State = "drift"
			p.Problem = perr.Error()
			s.State = "drift"
			continue
		}
		if cfg.WorkspaceScope(p.Name) == clientconfig.ScopeWorkspace {
			pm, d, wsDiverged = workspaceProviderDiff(cfg, provProjections[p.Name], manifests, lockFile, dir)
		} else {
			pm = provisioning.FilterForProvider(manifests[p.Name], configurator.Provider(p.Name))
			d = provisioning.ComputeDiffResolved(pm, lockFile.ForProvider(p.Name), statusResolveOptions(cfg, p.Name, manifests[p.Name]))
		}
		p.Revision = pm.Revision
		p.LockRevision = lockFile.ForProvider(p.Name).AppliedRevision
		if shadowing, _, ok := configurator.ShadowedInstructions(
			configurator.Provider(p.Name),
			provisioning.LockBaseDir(lockFile.ForProvider(p.Name), dir),
		); ok {
			p.ShadowedInstructions = shadowing
		}
		p.Kinds = formatKindStatus(pm, lockFile.ForProvider(p.Name), p.ShadowedInstructions != "",
			provisioning.SessionHookLimit(configurator.Provider(p.Name)))
		if p.ShadowedInstructions == "" {
			p.InstructionsBlocks = snapshotInstructionsBlocks(lockFile, p.Name, dir)
		}

		// On-disk verification (D139): the manifest↔lockfile comparison says
		// nothing about what is actually on disk, so an artifact edited or
		// deleted locally used to report in-sync.
		lock := lockFile.ForProvider(p.Name)
		p.ServerVersion = lock.ServerVersion
		p.HookLimitAcked = lock.SessionHookLimitAcked
		bound, explicit := cfg.BoundKBs(p.Name)
		p.BoundKBs = bound
		for _, kb := range bound {
			if !cfg.KBArtifacts(kb) {
				p.ArtifactsOff = append(p.ArtifactsOff, kb)
			}
		}
		p.BindingOrigin = "default"
		if explicit {
			p.BindingOrigin = "explicit"
		}
		p.KBCounts = countBySource(lock)
		p.Diverged = snapshotDiverged(append(provisioning.VerifyManaged(lock, configurator.Provider(p.Name), provisioning.LockBaseDir(lock, dir)), wsDiverged...))
		p.Diverged = append(p.Diverged, snapshotStaleBridge(cfg, p.Name, dir, routedPath)...)
		held := heldArtifacts(pm, d, cfg, configurator.Provider(p.Name))
		if d.InSync && len(p.Diverged) == 0 && len(held) == 0 {
			p.State = "in_sync"
			continue
		}
		p.State = "drift"
		p.Added, p.Updated, p.Removed = snapshotArtifacts(d.Added, cfg), snapshotArtifacts(d.Updated, cfg), snapshotRemoved(d.Removed)
		p.Updated = append(p.Updated, snapshotArtifacts(held, cfg)...)
		tagHookSettings(p.Added, provProjections[p.Name], manifests)
		tagHookSettings(p.Updated, provProjections[p.Name], manifests)
		s.State = "drift"
	}
	if s.Server != "" && s.Client != "" && s.Client != "dev" && s.Server != "dev" && s.Client != s.Server && s.State == "in_sync" {
		s.State = "version_skew"
	}
	return s
}

// statusResolveOptions is what sync resolves placeholders with for one
// provider (materializeForProviders), so status reports the same resolution
// drift sync would rewrite (#967). The manifest is the unfiltered one: the
// filtered copy does not carry the placeholder lists and registries.
func statusResolveOptions(cfg *clientconfig.Config, provider string, m provisioning.Manifest) provisioning.ApplyOptions {
	return provisioning.ApplyOptions{
		Provider:           configurator.Provider(provider),
		ExpandPlaceholders: true,
		SearchRoots:        cfg.ScanRoots(),
		Paths:              cfg.Paths,
		Placeholders:       m.Placeholders,
		PathRegistries:     m.PathRegistries,
		KBOrder:            kbOrderForProviders(cfg, []string{provider})[provider],
	}
}

// workspaceProviderDiff judges a workspace-scoped provider the way sync applies
// it (D193): the global projection against the bundle only, each workspace
// against its own manifest and lock. It returns the global manifest (for the
// revision and the kind counts), one merged Diff, and the on-disk findings of
// the workspace locks.
func workspaceProviderDiff(cfg *clientconfig.Config, ps []syncProjection, manifests map[string]provisioning.Manifest, lockFile provisioning.LockFile, dir string) (provisioning.Manifest, provisioning.Diff, []provisioning.DriftFinding) {
	var global provisioning.Manifest
	merged := provisioning.Diff{InSync: true}
	var diverged []provisioning.DriftFinding
	for _, sp := range ps {
		provider := configurator.Provider(sp.Provider)
		src := manifests[projectionKey(sp)]
		lock := lockFile.ForProjection(sp.Key())
		diffLock := lock
		if sp.Derived {
			// Judge a derived projection the way sync applies it (D1019): what
			// it deliberately leaves alone is neither pending nor drift, and
			// the lock entries held out of its diff are not "removed".
			if sp.InstructionsSkipped != "" {
				src = withoutInstructions(src)
			}
			src = withoutKinds(src, sp.SkipKinds)
			if len(sp.SkipKinds) > 0 {
				diffLock.Managed = nil
				for _, mf := range lock.Managed {
					if !slices.Contains(sp.SkipKinds, mf.Kind) {
						diffLock.Managed = append(diffLock.Managed, mf)
					}
				}
			}
		}
		pm := provisioning.FilterForProviderScoped(src, provider, sp.Scope)
		d := provisioning.ComputeDiffResolved(pm, diffLock, statusResolveOptions(cfg, sp.Provider, manifests[projectionKey(sp)]))
		merged.Added = append(merged.Added, d.Added...)
		merged.Updated = append(merged.Updated, d.Updated...)
		merged.Removed = append(merged.Removed, d.Removed...)
		merged.InSync = merged.InSync && d.InSync
		if sp.Workspace == "" {
			global = pm
			continue
		}
		diverged = append(diverged, provisioning.VerifyManaged(lock, provider, provisioning.LockBaseDir(lock, dir))...)
	}
	return global, merged, diverged
}

// snapshotStaleBridge reports the provider's bridge entry when its command is
// not the one sync would write now (a moved or removed binary): the MCP entry
// is not in the lockfile, so VerifyManaged cannot see it. Sync rewrites it.
func snapshotStaleBridge(cfg *clientconfig.Config, provider, dir, routedPath string) []statusArtifact {
	if !cfg.UsesBridge(provider, routedPath) {
		return nil
	}
	claudeDirs, _ := claudeConfigDirs(dir, cfg)
	var out []statusArtifact
	for _, st := range staleBridgeEntries(provider, []mcpEntry{{Name: cfg.ServerName, Bridge: true}}, dir, claudeDirs) {
		detail := fmt.Sprintf("the bridge command is %s, sync would write %s", st.Have, st.Want)
		if st.Missing {
			detail = fmt.Sprintf("the bridge command %s does not exist, sync would write %s", st.Have, st.Want)
		}
		out = append(out, statusArtifact{Kind: "mcp", Name: st.Name, Path: st.Path, Trust: provisioning.DriftModified, Detail: detail})
	}
	return out
}

// snapshotDiverged renders the healable on-disk findings (D139). "unknown"
// findings — a lockfile written before materialized hashes existed — are not
// drift and are left out.
func snapshotDiverged(findings []provisioning.DriftFinding) []statusArtifact {
	var out []statusArtifact
	for _, f := range findings {
		if !f.Healable() {
			continue
		}
		out = append(out, statusArtifact{Kind: f.Kind, Name: f.Name, Path: f.Path, Trust: f.Reason, Detail: f.Detail})
	}
	return out
}

// tagHookSettings records on each pending hook the settings file the sync
// registers it in: the HookSettingsRel of the derived projection that receives
// it, the very field sync applies with (#1300). A hook no derived projection
// receives keeps the provider's own registration file.
func tagHookSettings(arts []statusArtifact, ps []syncProjection, manifests map[string]provisioning.Manifest) {
	for i, a := range arts {
		if a.Kind != "hook" {
			continue
		}
		for _, sp := range ps {
			if sp.HookSettingsRel == "" {
				continue
			}
			for _, m := range manifests[projectionKey(sp)].Artifacts {
				if m.Kind == "hook" && m.Name == a.Name {
					arts[i].HookSettings = sp.HookSettingsRel
				}
			}
		}
	}
}

func snapshotArtifacts(artifacts []provisioning.Artifact, cfg *clientconfig.Config) []statusArtifact {
	out := make([]statusArtifact, len(artifacts))
	for i, a := range artifacts {
		trust := "needs_approval"
		if a.BuiltIn {
			trust = "built_in"
		} else if provisioning.ArtifactRequiresApproval(a, provisioning.ApplyOptions{}) {
			// A signature never installs an MCP descriptor, a hook or an
			// executable file (D906, D1141): only the hash-bound approval does,
			// so it is checked before Signed.
			source := strings.TrimPrefix(a.Source, "kb:")
			if approval, ok := cfg.MCPApprovals[source][provisioning.ApprovalName(a)]; ok {
				if approval.ContentHash == a.ContentHash {
					trust = "approved"
				} else {
					trust = "approval_stale"
				}
			}
		} else if a.Signed {
			trust = "verified"
		} else if strings.HasPrefix(a.Source, "kb:") {
			// Unsigned but installed (D906): there is no trust switch.
			trust = "trusted"
		}
		out[i] = statusArtifact{Kind: a.Kind, Name: a.Name, Source: a.Source, Signed: a.Signed, Trust: trust}
	}
	return out
}

// heldArtifacts are the artifacts of m that wait for an approval without being
// in the diff: installed earlier (before D1141) and unchanged on the server, so
// ComputeDiff calls them in sync while Apply holds them and reports them pending.
func heldArtifacts(m provisioning.Manifest, d provisioning.Diff, cfg *clientconfig.Config, provider configurator.Provider) []provisioning.Artifact {
	inDiff := map[string]bool{}
	for _, a := range append(append([]provisioning.Artifact(nil), d.Added...), d.Updated...) {
		inDiff[a.Kind+"\x00"+a.Name] = true
	}
	opts := provisioning.ApplyOptions{Provider: provider, ApprovedMCP: cfg.ApprovedMCPHashes()}
	var out []provisioning.Artifact
	for _, a := range m.Artifacts {
		if a.Kind == "mcp" || a.Kind == "instructions" || inDiff[a.Kind+"\x00"+a.Name] || provisioning.ArtifactAuthorized(a, opts) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func snapshotRemoved(files []provisioning.ManagedFile) []statusArtifact {
	out := make([]statusArtifact, len(files))
	for i, f := range files {
		out[i] = statusArtifact{Kind: f.Kind, Name: f.Name, Path: f.Path}
	}
	return out
}

func providerStatuses(cfg *clientconfig.Config) []providerStatus {
	connected := map[string]bool{}
	if cfg != nil {
		for _, p := range cfg.Agents {
			connected[p] = true
		}
	}
	detected := agents.Detect()
	out := make([]providerStatus, len(detected))
	for i, a := range detected {
		state := "not_connected"
		if connected[string(a.Provider)] {
			state = "unknown"
		}
		out[i] = providerStatus{Name: string(a.Provider), Installed: a.Installed, DetectedBy: string(a.DetectedBy), Connected: connected[string(a.Provider)], State: state}
		if cfg != nil && connected[string(a.Provider)] {
			base, err := clientconfig.TargetDir()
			if err == nil {
				out[i].Workspaces = workspaceStatuses(cfg, string(a.Provider), base)
			}
		}
	}
	return out
}

func emptySnapshot() statusSnapshot {
	return statusSnapshot{Schema: statusSchema, Client: version, State: "not_configured", Providers: providerStatuses(nil)}
}

// snapshotMaterializedVersions returns the distinct recorded server versions
// of the connected providers that differ from the live one (D142), sorted;
// empty when there is nothing to report. Unknown and "dev" versions are
// ignored on either side, the same rule the client/server skew line uses.
func snapshotMaterializedVersions(s statusSnapshot) []string {
	if !s.Reachable || !versionIsComparable(s.Server) {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range s.Providers {
		if !p.Connected || !versionIsComparable(p.ServerVersion) || p.ServerVersion == s.Server || seen[p.ServerVersion] {
			continue
		}
		seen[p.ServerVersion] = true
		out = append(out, p.ServerVersion)
	}
	sort.Strings(out)
	return out
}

// countBySource groups a provider's managed files by the KB they came from
// (D170). Entries with no Source — every lockfile written before it existed —
// are omitted rather than bucketed under a made-up name: unknown provenance is
// not the same as none, and inventing a bucket would misreport a whole machine
// until its next sync.
func countBySource(lock provisioning.Lock) map[string]int {
	counted := make(map[string]bool)
	out := make(map[string]int)
	for _, mf := range lock.Managed {
		if mf.Source == "" {
			continue
		}
		key := mf.Kind + "\x00" + mf.Name
		if counted[key] {
			continue // one artifact, however many files it materialized
		}
		counted[key] = true
		out[mf.Source]++
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
