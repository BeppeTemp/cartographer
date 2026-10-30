package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

// The doctor task kind (D1261): a delegated cartographer-kb-doctor session on a
// KB whose kb_status says maintenance is due. The only text the model receives
// as instructions is doctorPreamble plus the SKILL.md embedded in the binary
// (D15); the KB's own pages and instructions.md are data the session reads.

const (
	doctorSkillPath = "bundled/cartographer-kb-doctor/SKILL.md"
	// doctorMinGrant is the least a session may start with: below it the model
	// could not reach its closing step, so no session starts (D12).
	doctorMinGrant = 20_000
	// doctorMaxSteps replaces the loop's default of 30: a session works through
	// up to ten review items plus every mechanical repair.
	doctorMaxSteps = 120
	// doctorReviewItems is the skill's own review-item budget, passed in the prompt.
	doctorReviewItems = 10
)

// doctorMarker is the substring kb_status reads as "a session closed"
// (mcpserver doctorMarker). Only the model's own closing log_append may carry
// it: every text this file writes or returns is passed through noMarker.
const doctorMarker = "kb-doctor"

// doctorTools is the default allowlist plus the two tools only the doctor needs.
// kb_repair is writable by design (denyTools says so); nothing from the deny
// set (concept_delete, repair_revert, artifact_*) is added, and the deny set is
// subtracted by NewAllowlist whatever is listed here.
func doctorTools() []string { return append(DefaultTools(), "kb_review", "kb_repair") }

// DoctorKind is the doctor task kind: no options, one session per KB per run.
func DoctorKind() Kind {
	return Kind{
		Name:  "doctor",
		Build: func(map[string]any) (Instance, error) { return &doctorInstance{now: time.Now}, nil },
	}
}

type doctorInstance struct {
	now func() time.Time
}

// doctorStatus is the part of kb_status the doctor reads.
type doctorStatus struct {
	PushState   string `json:"push_state"`
	Conformance struct {
		DoctorSuggested bool   `json:"doctor_suggested"`
		LastDoctor      string `json:"last_doctor"`
	} `json:"conformance"`
}

func readDoctorStatus(tools ToolCaller) (doctorStatus, error) {
	var st doctorStatus
	out, err := tools.CallTool("kb_status", map[string]any{})
	if err != nil {
		return st, err
	}
	if out.IsError {
		return st, fmt.Errorf("kb_status refused: %s", oneLine(out.Text))
	}
	if err := json.Unmarshal([]byte(out.Text), &st); err != nil {
		return st, fmt.Errorf("kb_status: %w", err)
	}
	return st, nil
}

// skipReason says why a KB is not eligible ("" when it is). A degraded KB
// refuses writes anyway; a session that ran today is a replica that got there
// first.
func (d *doctorInstance) skipReason(st doctorStatus) string {
	switch {
	case st.PushState == "degraded":
		return "KB degraded"
	case !st.Conformance.DoctorSuggested:
		return "no doctor session due"
	case st.Conformance.LastDoctor == d.today():
		return "a session already closed today"
	}
	return ""
}

func (d *doctorInstance) today() string { return d.now().UTC().Format("2006-01-02") }

// Next offers the one doctor unit of a KB when its signal is true. A KB whose
// kb_status fails is not eligible; the error is the runner's to count.
func (d *doctorInstance) Next(_ context.Context, _ string, tools ToolCaller) (*WorkItem, error) {
	st, err := readDoctorStatus(tools)
	if err != nil {
		return nil, err
	}
	if d.skipReason(st) != "" {
		return nil, nil
	}
	return &WorkItem{Key: "doctor"}, nil
}

// doctorSkillBody is the embedded skill without its frontmatter.
func doctorSkillBody() (string, error) {
	data, err := fs.ReadFile(skillbundle.FS, doctorSkillPath)
	if err != nil {
		return "", err
	}
	_, body, _ := okf.SplitFrontmatter(string(data))
	return body, nil
}

// doctorSystem assembles the instructions: the worker's preamble, then the
// skill. Nothing read from a KB is ever concatenated here.
func doctorSystem(kb, perimeter, day string, tokens int) (string, error) {
	body, err := doctorSkillBody()
	if err != nil {
		return "", err
	}
	pre := fmt.Sprintf("This session is run by a worker (perimeter %q) on the KB %q, delegated mode: nobody is in the session to ask, "+
		"and the operator deploying this worker is the one who asked for it. Review-item budget: %d. Token budget for the whole session: %d. "+
		"Today is %s (UTC). Artifacts cannot be written from a worker session: record a gap instead. "+
		"Close with the log_append of step 8 before the tokens run out.\n\n",
		perimeter, kb, doctorReviewItems, tokens, day)
	return pre + body, nil
}

// noMarker removes the closing marker from a text the worker writes: only the
// model's own closing step may carry it, or a half session would silence the
// proposal with the debt still there.
func noMarker(s string) string { return strings.ReplaceAll(s, doctorMarker, "doctor") }

// Run drives one session and then checks it closed (WP3).
func (d *doctorInstance) Run(ctx context.Context, _ *WorkItem, rt Runtime) (Result, error) {
	// Re-read right before starting: a replica that closed meanwhile makes this
	// a skip, and a lost race costs tokens, never a double write.
	st, err := readDoctorStatus(rt.Tools)
	if err != nil {
		return Result{}, err
	}
	if why := d.skipReason(st); why != "" {
		return Result{Summary: "doctor skipped: " + why}, nil
	}
	if rt.Grant < doctorMinGrant {
		return Result{Summary: "doctor skipped: budget below the reserve"}, nil
	}
	system, err := doctorSystem(rt.KB, rt.Perimeter, d.today(), rt.Grant)
	if err != nil {
		return Result{}, err
	}
	agent := NewAgent(rt.Model, rt.Tools, NewAllowlist(doctorTools()...),
		Limits{MaxSteps: doctorMaxSteps, MaxTotalTokens: rt.Grant})
	out, runErr := agent.Run(ctx, Task{System: system, User: "Run the session on this KB now."})
	res := Result{Tokens: out.Usage.Total(), Units: 1}
	for _, c := range out.Calls {
		if !c.IsError && !isRead(c.Tool) && c.Tool != "log_append" {
			res.Commits++
		}
	}
	if ctx.Err() != nil {
		return res, runErr // shutdown: the runner's own closing entry says so
	}
	after, err := readDoctorStatus(rt.Tools)
	if err == nil && after.Conformance.LastDoctor == d.today() {
		res.Summary = "doctor closed"
		return res, runErr
	}
	why := string(out.Reason)
	if runErr != nil {
		why = "error"
	}
	entry := noMarker(fmt.Sprintf("doctor session interrupted (%s) after %d tokens; the next window continues", why, res.Tokens))
	if out, aerr := rt.Tools.CallTool("log_append", map[string]any{"entry": entry}); aerr != nil || out.IsError {
		res.Summary = "doctor interrupted, its log entry not written"
	} else {
		res.Summary = "doctor interrupted"
	}
	return res, runErr
}
