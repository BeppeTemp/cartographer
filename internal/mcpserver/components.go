package mcpserver

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"github.com/BeppeTemp/cartographer/internal/config"
)

// componentGrace is how long after the process start an expected component
// that has not been seen yet is "starting" rather than "silent": a pod that
// boots with the server is not an alarm (three heartbeat intervals).
const componentGrace = 3 * time.Duration(defaultSurveyorInterval) * time.Second

// componentRegistry holds the components the server expects (the components:
// key) and, for the health state of each, when it was last heard. It is in
// memory like the heartbeats (D1259): a restarted server is "starting" again
// for the grace window. It never gates /health or /ready, which stay liveness:
// a silent Surveyor must not restart the server (D84, D119).
type componentRegistry struct {
	mu        sync.Mutex
	now       func() time.Time // nil = time.Now; tests inject
	expected  []config.ComponentSpec
	startedAt time.Time
	// lastSeen is the last accepted beat per expected component. It is written
	// only for names in the registry, so its size is bounded by the registry.
	lastSeen map[componentKey]time.Time
	// logged is the last state announced per (component, KB), so a transition
	// is logged once and a recovery once.
	logged map[componentKey]string
}

type componentKey struct{ kind, name, kb string }

func (r *componentRegistry) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// set installs the registry and starts the grace window.
func (r *componentRegistry) set(specs []config.ComponentSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expected = append([]config.ComponentSpec(nil), specs...)
	r.startedAt = r.clock()
	r.lastSeen = map[componentKey]time.Time{}
	r.logged = map[componentKey]string{}
}

// noteBeat records an accepted beat of a Surveyor with this perimeter, for the
// KBs the server accepted for it (the ones its principal may write) that the
// entry names: a beat for kb-b says nothing about the same name on kb-a, and a
// principal cannot vouch for a KB it does not hold.
func (r *componentRegistry) noteBeat(kind, name string, accepted []string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.expected {
		if c.Kind != kind || c.Name != name {
			continue
		}
		for _, kb := range accepted {
			if containsString(c.KBs, kb) {
				r.lastSeen[componentKey{kind: kind, name: name, kb: kb}] = at
			}
		}
		return
	}
}

// ComponentRow is the health of one component on one KB.
type ComponentRow struct {
	Kind        string     `json:"kind"`
	Name        string     `json:"name"`
	State       string     `json:"state"`
	Expected    bool       `json:"expected"`
	MinReplicas int        `json:"min_replicas,omitempty"`
	Live        int        `json:"live"`
	LastSeenAt  *time.Time `json:"last_seen_at"`
}

// status returns the rows of the components that concern kb, sorted by name.
// live holds the live Surveyor declarations naming kb; a declaration only names
// the KBs the server accepted for its principal, so a replica counts on a KB
// only if its principal may write it. With an empty registry it
// returns nothing: no expectation, no row, no log. The caller decides whether
// kb is visible to the reader: this method knows nothing about principals.
func (r *componentRegistry) status(kb string, live []surveyorDecl, now time.Time) []ComponentRow {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.expected) == 0 {
		return nil
	}
	count := map[string]int{}
	for _, d := range live {
		count[d.Perimeter]++
	}
	var rows []ComponentRow
	declared := map[string]bool{}
	for _, c := range r.expected {
		declared[c.Name] = true
		if !containsString(c.KBs, kb) {
			continue
		}
		row := ComponentRow{Kind: c.Kind, Name: c.Name, Expected: true, MinReplicas: c.MinReplicas, Live: count[c.Name]}
		seen, ok := r.lastSeen[componentKey{kind: c.Kind, name: c.Name, kb: kb}]
		if ok {
			t := seen.UTC()
			row.LastSeenAt = &t
		}
		switch {
		case row.Live >= c.MinReplicas:
			row.State = componentproto.StateUp
		case row.Live > 0:
			row.State = componentproto.StateDegraded
		case !ok && now.Sub(r.startedAt) < componentGrace:
			row.State = componentproto.StateStarting
		default:
			row.State = componentproto.StateSilent
		}
		r.announceLocked(componentKey{kind: c.Kind, name: c.Name, kb: kb}, row.State)
		rows = append(rows, row)
	}
	for name, n := range count {
		if !declared[name] {
			rows = append(rows, ComponentRow{Kind: componentproto.KindSurveyor, Name: name, State: componentproto.StateUndeclared, Live: n})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

// announceLocked logs once on each transition into silent or degraded and once
// on the recovery, to stderr like the other operational messages.
func (r *componentRegistry) announceLocked(k componentKey, state string) {
	prev := r.logged[k]
	r.logged[k] = state
	bad := func(s string) bool { return s == componentproto.StateSilent || s == componentproto.StateDegraded }
	switch {
	case bad(state) && prev != state:
		log.Printf("component %s %q on KB %q is %s", k.kind, k.name, k.kb, state)
	case state == componentproto.StateUp && bad(prev):
		log.Printf("component %s %q on KB %q recovered", k.kind, k.name, k.kb)
	}
}

// SetComponents installs the registry of expected components and starts the
// grace window. It is called once, at start: it resets every state. Call it after every MountKB, once the specs are validated
// against the mounted KBs (config.UnmountedComponentKBs, a warning).
func (m *MultiKBServer) SetComponents(specs []config.ComponentSpec) { m.components.set(specs) }

// ComponentStatus is the health of the components that concern kb, for the
// reader in ctx. Like the surveyors view it describes the whole KB, so a KB the
// caller does not see whole (or does not exist) answers ok=false, the same as
// an unmounted one: no existence oracle. The route that serves it answers 404.
func (m *MultiKBServer) ComponentStatus(ctx context.Context, kb string) (rows []ComponentRow, ok bool) {
	srv, mounted := m.servers[kb]
	if !mounted || !WholeVisible(ctx, srv.kbRef, false) {
		return nil, false
	}
	rows = m.components.status(kb, m.surveyors.live(kb), m.components.clock())
	if rows == nil {
		rows = []ComponentRow{}
	}
	return rows, true
}
