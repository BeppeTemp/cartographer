package main

import (
	"errors"
	"fmt"
	"net"
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
	Name         string           `json:"name"`
	Installed    bool             `json:"installed"`
	Connected    bool             `json:"connected"`
	State        string           `json:"state"`
	Revision     string           `json:"revision,omitempty"`
	LockRevision string           `json:"lock_revision,omitempty"`
	Kinds        string           `json:"kind_status,omitempty"`
	Added        []statusArtifact `json:"added,omitempty"`
	Updated      []statusArtifact `json:"updated,omitempty"`
	Removed      []statusArtifact `json:"removed,omitempty"`
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
	// BoundKBs and BindingOrigin describe which KBs this provider may receive
	// and whether that was declared or defaulted (D170). Without them the same
	// list of names means two different things and the reader cannot tell.
	BoundKBs      []string `json:"bound_kbs,omitempty"`
	BindingOrigin string   `json:"binding_origin,omitempty"`
	// KBCounts breaks the materialized artifacts down by source KB, read from
	// the lockfile's per-file Source (D170). Empty for a lockfile written
	// before that field existed: unknown, not zero.
	KBCounts map[string]int `json:"kb_counts,omitempty"`
}

type statusArtifact struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
	Path   string `json:"path,omitempty"`
	Signed bool   `json:"signed,omitempty"`
	Trust  string `json:"trust,omitempty"`
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
}

func classifyNetworkError(endpoint string, err error) statusError {
	code := "request_failed"
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
	s := statusSnapshot{Schema: statusSchema, Client: version, State: "in_sync", Providers: providerStatuses(cfg), ServerURL: cfg.ServerURL}
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
		for i := range s.Providers {
			if s.Providers[i].Connected {
				s.Providers[i].State = "unknown"
			}
		}
		return s
	}
	s.Reachable, s.Server, s.Ready = true, health.Version, health.Ready
	if health.KBs != nil {
		for _, kb := range *health.KBs {
			s.KBs = append(s.KBs, kb.Name)
		}
	}
	connectedProviders := make([]string, 0, len(s.Providers))
	for i := range s.Providers {
		if s.Providers[i].Connected {
			connectedProviders = append(connectedProviders, s.Providers[i].Name)
		}
	}
	manifests, err := statusManifestsFn(cfg, connectedProviders)
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
	for _, p := range connectedProviders {
		s.Artifacts += len(manifests[p].Artifacts)
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
		pm := provisioning.FilterForProvider(manifests[s.Providers[i].Name], configurator.Provider(s.Providers[i].Name))
		d := provisioning.ComputeDiff(pm, lockFile.ForProvider(s.Providers[i].Name))
		p := &s.Providers[i]
		p.Revision = pm.Revision
		p.LockRevision = lockFile.ForProvider(p.Name).AppliedRevision
		p.Kinds = formatKindStatus(pm, lockFile.ForProvider(p.Name))
		// On-disk verification (D139): the manifest↔lockfile comparison says
		// nothing about what is actually on disk, so an artifact edited or
		// deleted locally used to report in-sync.
		lock := lockFile.ForProvider(p.Name)
		p.ServerVersion = lock.ServerVersion
		bound, explicit := cfg.BoundKBs(p.Name)
		p.BoundKBs = bound
		p.BindingOrigin = "default"
		if explicit {
			p.BindingOrigin = "explicit"
		}
		p.KBCounts = countBySource(lock)
		p.Diverged = snapshotDiverged(provisioning.VerifyManaged(lock, configurator.Provider(p.Name), provisioning.LockBaseDir(lock, dir)))
		if d.InSync && len(p.Diverged) == 0 {
			p.State = "in_sync"
			continue
		}
		p.State = "drift"
		p.Added, p.Updated, p.Removed = snapshotArtifacts(d.Added, cfg), snapshotArtifacts(d.Updated, cfg), snapshotRemoved(d.Removed)
		s.State = "drift"
	}
	if s.Server != "" && s.Client != "" && s.Client != "dev" && s.Server != "dev" && s.Client != s.Server && s.State == "in_sync" {
		s.State = "version_skew"
	}
	return s
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
		out = append(out, statusArtifact{Kind: f.Kind, Name: f.Name, Path: f.Path, Trust: f.Reason})
	}
	return out
}

func snapshotArtifacts(artifacts []provisioning.Artifact, cfg *clientconfig.Config) []statusArtifact {
	out := make([]statusArtifact, len(artifacts))
	for i, a := range artifacts {
		trust := "needs_approval"
		if a.BuiltIn {
			trust = "built_in"
		} else if a.Signed {
			trust = "verified"
		} else if a.Kind == "mcp" {
			source := strings.TrimPrefix(a.Source, "kb:")
			if approval, ok := cfg.MCPApprovals[source][a.Name]; ok {
				if approval.ContentHash == a.ContentHash {
					trust = "approved"
				} else {
					trust = "approval_stale"
				}
			}
		} else if cfg.Trust && strings.HasPrefix(a.Source, "kb:") {
			trust = "trusted"
		}
		out[i] = statusArtifact{Kind: a.Kind, Name: a.Name, Source: a.Source, Signed: a.Signed, Trust: trust}
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
		out[i] = providerStatus{Name: string(a.Provider), Installed: a.Installed, Connected: connected[string(a.Provider)], State: state}
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
