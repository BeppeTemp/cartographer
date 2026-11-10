package surveyor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	repairKindName = "repair"
	// repairMinInterval is the floor of repair_interval: one staged repair
	// reads the whole KB, so a shorter period only costs the server.
	repairMinInterval = time.Hour
	// repairRefusedRest is how long a KB the server refuses to repair (an open
	// git conflict, a degraded sync) is left alone.
	repairRefusedRest = time.Hour
	// repairRefusedMarker is what kb_repair check "auto" says when it refuses
	// for the state of the KB (autoRepairBlocked) rather than for a bad call.
	repairRefusedMarker = "auto-repair refused"
)

// RepairKind is the repair task kind: it asks the server for its own staged
// repair (kb_repair check "auto") once per repair_interval and KB. It calls no
// model, so it carries NoModel: no profile, no grant, no token is spent.
func RepairKind() Kind {
	return Kind{
		Name:    repairKindName,
		NoModel: true,
		Options: []KindOption{schemaOption("repair_interval")},
		Validate: func(o map[string]any) error {
			if d, _ := o["repair_interval"].(time.Duration); d < repairMinInterval {
				return fmt.Errorf("repair_interval must be at least %s", repairMinInterval)
			}
			return nil
		},
		Build: func(o map[string]any) (Instance, error) {
			d, _ := o["repair_interval"].(time.Duration)
			if d < repairMinInterval {
				d = 24 * time.Hour
			}
			return &repairInstance{now: time.Now, interval: d}, nil
		},
	}
}

type repairInstance struct {
	now       func() time.Time
	interval  time.Duration
	restUntil map[string]time.Time // per KB, in memory: a restart repairs once
}

func (r *repairInstance) rest(kb string, d time.Duration) {
	if r.restUntil == nil {
		r.restUntil = map[string]time.Time{}
	}
	r.restUntil[kb] = r.now().Add(d)
}

// Next offers the KB unless it rested for repair_interval.
func (r *repairInstance) Next(_ context.Context, kb string, _ ToolCaller) (*WorkItem, error) {
	if until, ok := r.restUntil[kb]; ok && r.now().Before(until) {
		return nil, nil
	}
	return &WorkItem{Key: "repair"}, nil
}

// Run calls kb_repair check "auto" with no scope and no limit (the server
// refuses both). The commit's attribution comes from the Surveyor's token, not
// from anything sent here.
func (r *repairInstance) Run(_ context.Context, _ *WorkItem, rt Runtime) (Result, error) {
	out, err := rt.Tools.CallTool("kb_repair", map[string]any{"check": "auto", "dry_run": false})
	if err != nil {
		return Result{}, err
	}
	if out.IsError {
		if strings.Contains(out.Text, repairRefusedMarker) {
			r.rest(rt.KB, repairRefusedRest)
			return Result{Summary: "repair refused: " + oneLine(out.Text)}, nil
		}
		return Result{}, fmt.Errorf("kb_repair auto: %s", oneLine(out.Text))
	}
	var answer struct {
		AppliedTotal int `json:"applied_total"`
	}
	if err := json.Unmarshal([]byte(out.Text), &answer); err != nil {
		return Result{}, fmt.Errorf("kb_repair auto: %w", err)
	}
	r.rest(rt.KB, r.interval)
	res := Result{Units: answer.AppliedTotal}
	if answer.AppliedTotal > 0 {
		res.Commits = 1
		res.Summary = fmt.Sprintf("auto-repair: %d fixes in one commit", answer.AppliedTotal)
	}
	return res, nil
}
