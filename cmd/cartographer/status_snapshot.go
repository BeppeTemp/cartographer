package main

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
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
}

type statusArtifact struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
	Path   string `json:"path,omitempty"`
	Signed bool   `json:"signed,omitempty"`
}

type serviceSnapshot struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Healthy   bool   `json:"healthy"`
	HTTPAddr  string `json:"http_addr,omitempty"`
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
			s.Service = &serviceSnapshot{st.Installed, st.Running, st.Healthy, st.HTTPAddr}
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
	m, err := statusManifestFn(cfg)
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
	m = upgradeTrustedManifest(m, cfg.Trust)
	s.Artifacts = len(m.Artifacts)
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
		pm := provisioning.FilterForProvider(m, configurator.Provider(s.Providers[i].Name))
		d := provisioning.ComputeDiff(pm, lockFile.ForProvider(s.Providers[i].Name))
		p := &s.Providers[i]
		p.Revision = pm.Revision
		p.LockRevision = lockFile.ForProvider(p.Name).AppliedRevision
		p.Kinds = formatKindStatus(pm, lockFile.ForProvider(p.Name))
		if d.InSync {
			p.State = "in_sync"
			continue
		}
		p.State = "drift"
		p.Added, p.Updated, p.Removed = snapshotArtifacts(d.Added), snapshotArtifacts(d.Updated), snapshotRemoved(d.Removed)
		s.State = "drift"
	}
	if s.Server != "" && s.Client != "" && s.Client != "dev" && s.Server != "dev" && s.Client != s.Server && s.State == "in_sync" {
		s.State = "version_skew"
	}
	return s
}

func snapshotArtifacts(artifacts []provisioning.Artifact) []statusArtifact {
	out := make([]statusArtifact, len(artifacts))
	for i, a := range artifacts {
		out[i] = statusArtifact{Kind: a.Kind, Name: a.Name, Source: a.Source, Signed: a.Signed}
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
