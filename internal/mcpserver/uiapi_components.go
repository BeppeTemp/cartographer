package mcpserver

import (
	"net/http"
	"sort"
	"time"

	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

// GET /kbs/{kb}/components — what the Atlas reads of the components that
// concern a KB: the registry's expected entries naming it (a silent one is
// present with no replicas) plus the live Surveyors the registry does not
// name. Read-only: it shows heartbeats and the registry, it never acts on them.
// It describes the whole KB's upkeep, like status, so a caller that does not
// see the whole KB gets the 404 of an unknown route. No principal id and no
// token digest leaves the server: a replica is named by the id it declared.

type componentReplicaView struct {
	ID              string           `json:"id"`
	Version         string           `json:"version,omitempty"`
	Protocol        int              `json:"protocol"`
	ProtocolSkew    string           `json:"protocol_skew"`
	KindDeclared    bool             `json:"kind_declared"`
	Tasks           []surveyorTask   `json:"tasks"`
	State           string           `json:"state"`
	LastBeatAt      string           `json:"last_beat_at"`
	IntervalSeconds int              `json:"interval_seconds"`
	LastRun         *surveyorLastRun `json:"last_run,omitempty"`
	Budgets         []surveyorBudget `json:"budgets"`
	NextWindowAt    *time.Time       `json:"next_window_at,omitempty"`
}

type componentView struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// State is one of componentproto.States.
	State string `json:"state"`
	// Declared is whether the registry names it.
	Declared bool `json:"declared"`
	// KindDeclared is true when every live replica's token is declared a
	// surveyor; false with no replica or one from a token with no kind.
	KindDeclared bool                   `json:"kind_declared"`
	Replicas     []componentReplicaView `json:"replicas"`
	MinReplicas  int                    `json:"min_replicas"`
	LastSeenAt   *time.Time             `json:"last_seen_at"`
	KBs          []string               `json:"kbs"`
	// Runs are the recent runs on this KB, newest first (at most 20); Alarms
	// the ones reported for it plus the ones the server computes. Ages come
	// from received_at, the server's stamp.
	Runs   []surveyorRun   `json:"runs"`
	Alarms []surveyorAlarm `json:"alarms"`
}

type componentsServerView struct {
	Version   string `json:"version"`
	StartedAt string `json:"started_at"`
}

func (m *MultiKBServer) uiComponents(w http.ResponseWriter, r *http.Request, name string, srv *Server) {
	if !WholeVisible(r.Context(), srv.kbRef, false) {
		writeUINotFound(w)
		return
	}
	live := m.surveyors.live(name)
	now := m.components.clock()
	status := m.components.status(name, live, now)
	byName := map[string][]surveyorDecl{}
	for _, d := range live {
		byName[d.Perimeter] = append(byName[d.Perimeter], d)
	}
	if len(status) == 0 {
		// No registry: nothing is expected, but a live Surveyor is still shown.
		for per, ds := range byName {
			status = append(status, ComponentRow{Kind: componentproto.KindSurveyor, Name: per, State: componentproto.StateUndeclared, Live: len(ds)})
		}
		sort.Slice(status, func(i, j int) bool { return status[i].Name < status[j].Name })
	}
	// Only the KBs the caller sees whole are named: another KB's existence is
	// not this reader's to learn from a perimeter that spans several.
	visible := func(kbs []string) []string {
		out := []string{}
		for _, k := range kbs {
			if s, ok := m.servers[k]; ok && WholeVisible(r.Context(), s.kbRef, false) {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}
	rows := []componentView{}
	for _, c := range status {
		v := componentView{
			Kind: c.Kind, Name: c.Name, State: c.State, Declared: c.Expected,
			MinReplicas: c.MinReplicas, LastSeenAt: c.LastSeenAt, Replicas: []componentReplicaView{},
		}
		kbs := map[string]bool{name: true}
		if c.Expected {
			for _, e := range m.components.specsFor(c.Kind, c.Name) {
				for _, k := range e.KBs {
					kbs[k] = true
				}
			}
		}
		all := true
		for _, d := range byName[c.Name] {
			for _, k := range d.KBs {
				kbs[k] = true
			}
			all = all && d.KindDeclared
			v.Replicas = append(v.Replicas, componentReplicaView{
				ID: d.Surveyor, Version: d.Version, Protocol: d.Protocol, ProtocolSkew: d.ProtocolSkew,
				KindDeclared: d.KindDeclared, Tasks: d.Tasks, State: d.State,
				LastBeatAt: d.ReceivedAt.Format(time.RFC3339), IntervalSeconds: d.IntervalSeconds,
				LastRun: d.LastRun, Budgets: d.Budgets, NextWindowAt: d.NextWindowAt,
			})
		}
		v.KindDeclared = len(v.Replicas) > 0 && all
		v.Runs, v.Alarms = m.surveyors.runsFor(name, c.Name, byName[c.Name])
		v.Alarms = append(v.Alarms, serverAlarms(c, v.Replicas)...)
		for k := range kbs {
			v.KBs = append(v.KBs, k)
		}
		v.KBs = visible(v.KBs)
		rows = append(rows, v)
	}
	writeUIJSON(w, http.StatusOK, map[string]any{
		"protocol":     componentproto.Protocol,
		"min_protocol": componentproto.MinProtocol,
		"server":       componentsServerView{Version: m.version, StartedAt: processStartedAt},
		"components":   rows,
	})
}

// serverAlarms are the alarms computed from health and protocol skew: never
// reported, never stored.
func serverAlarms(c ComponentRow, reps []componentReplicaView) []surveyorAlarm {
	out := []surveyorAlarm{}
	switch c.State {
	case componentproto.StateSilent:
		out = append(out, surveyorAlarm{Code: componentproto.AlarmSilent, Since: c.LastSeenAt, Origin: "server"})
	case componentproto.StateDegraded:
		out = append(out, surveyorAlarm{Code: componentproto.AlarmDegraded, Origin: "server"})
	}
	for _, r := range reps {
		if r.ProtocolSkew != "" {
			out = append(out, surveyorAlarm{Code: componentproto.AlarmProtocolSkew, Source: r.ID, Detail: "replica is " + r.ProtocolSkew, Origin: "server"})
		}
	}
	return out
}

// repairInfo is the maintenance object's repair part: who repairs this KB.
type repairInfo struct {
	// Owner is "server" or "surveyor".
	Owner string `json:"owner"`
	// By is the perimeter that repairs (Owner surveyor) or whose silence made
	// the server take the repair back (State fallback).
	By string `json:"by,omitempty"`
	// State is "server" (no covering Surveyor), "surveyor" (handed over) or
	// "fallback" (an expected Surveyor that repaired is silent, so the server
	// repairs again).
	State string `json:"state"`
	// Since is when the current state began as this process saw it, which is
	// when someone first read it: the transition is recorded on read.
	Since *time.Time `json:"since,omitempty"`
}

// repairInfoOf reads the handover from the single decision that makes it
// (repairOwners, the one the server's timer consults), never re-deriving
// liveness, so the server and the Atlas cannot disagree.
func (m *MultiKBServer) repairInfoOf(kb string, srv *Server) repairInfo {
	now := m.components.clock()
	info := repairInfo{Owner: "server", State: "server"}
	if owners := m.surveyors.repairOwners(kb, srv.surveyorRepairAt); len(owners) > 0 {
		info = repairInfo{Owner: "surveyor", State: "surveyor", By: owners[0].Perimeter}
	} else {
		rows := m.components.status(kb, m.surveyors.live(kb), now)
		if by, ok := m.components.repairFallback(kb, rows); ok {
			info = repairInfo{Owner: "server", State: "fallback", By: by}
		}
	}
	info.Since = m.components.stampRepair(kb, info.State, now)
	return info
}
