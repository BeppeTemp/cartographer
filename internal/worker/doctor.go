package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
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
	// defaultMaxWrites is the cap of applied writes of one session (tasks[].max_writes).
	defaultMaxWrites = 20
)

// doctorMarker is the substring kb_status reads as "a session closed"
// (mcpserver doctorMarker). Only the model's own closing log_append may carry
// it: every text this file writes or returns is passed through noMarker.
const doctorMarker = "kb-doctor"

// doctorOperatorOnly are tools of the default allowlist an unattended session
// does not get: a fold or an archive is a judgement the operator makes.
var doctorOperatorOnly = []string{"concept_merge", "concept_archive"}

// doctorTools is the default allowlist minus doctorOperatorOnly, plus the two
// tools only the doctor needs. kb_repair is writable by design (denyTools says
// so); nothing from the deny set (concept_delete, repair_revert, artifact_*) is
// added, and the deny set is subtracted by NewAllowlist whatever is listed here.
func doctorTools() []string {
	var out []string
	for _, t := range DefaultTools() {
		if !slices.Contains(doctorOperatorOnly, t) {
			out = append(out, t)
		}
	}
	return append(out, "kb_review", "kb_repair")
}

// doctorGuard wraps the session's tool caller with what the allowlist cannot
// express. Applied writes are counted here (a kb_review or a dry-run kb_repair
// is not one). The marker is refused until the session has applied a write, so
// injected page text cannot silence doctor_suggested with an empty session; a
// write beyond the cap is refused; and consecutive refused writes (a read-only
// grant) abort the session with an error instead of spending the whole grant.
type doctorGuard struct {
	inner   ToolCaller
	maxW    int
	applied int
	refused int // consecutive refused writes
}

// maxRefusedWrites aborts a session whose writes keep being refused.
const maxRefusedWrites = 3

func (g *doctorGuard) ListTools() ([]client.ToolInfo, error) { return g.inner.ListTools() }

func (g *doctorGuard) CallTool(name string, args any) (client.ToolOutput, error) {
	refuse := func(text string) (client.ToolOutput, error) {
		return client.ToolOutput{IsError: true, Text: text}, nil
	}
	write := isDoctorWrite(name, args)
	// The server decodes the arguments, so the marker is looked for in the
	// decoded strings (an escaped "kb\u002ddoctor" is the same text), in every
	// write: an id, a map name or a path containing it would also be a closing
	// marker in the log. Only the closing log_append, after a write, may carry it.
	if write && hasMarker(args) {
		if name != "log_append" {
			return refuse("refused: the text \"" + doctorMarker + "\" is reserved for the closing log entry")
		}
		if g.applied == 0 {
			return refuse("refused: a closing entry needs the session to have applied at least one write; record a gap or a repair first")
		}
	}
	weight := writeWeight(name, args)
	if write && g.applied+weight > g.maxW {
		return refuse(fmt.Sprintf("refused: this session reached its cap of %d writes; close it now", g.maxW))
	}
	out, err := g.inner.CallTool(name, args)
	if !write {
		return out, err
	}
	if err != nil || out.IsError {
		if g.refused++; g.refused >= maxRefusedWrites {
			return out, fmt.Errorf("doctor: %d consecutive refused writes (is the token read-only?)", g.refused)
		}
		return out, err
	}
	g.refused = 0
	if name != "log_append" {
		g.applied += weight
	}
	return out, nil
}

// isDoctorWrite says whether a call changes the KB: not a read, not kb_review,
// not a kb_repair that is a plan (it applies only with dry_run: false).
func isDoctorWrite(name string, args any) bool {
	switch name {
	case "kb_review":
		return false
	case "kb_repair":
		var a struct {
			DryRun *bool `json:"dry_run"`
		}
		_ = json.Unmarshal(rawJSON(args), &a)
		return a.DryRun != nil && !*a.DryRun
	}
	return !isRead(name)
}

// writeWeight is how many writes a call counts for: a concept_batch is one per
// operation, anything else one.
func writeWeight(name string, args any) int {
	if name != "concept_batch" {
		return 1
	}
	var a struct {
		Operations []json.RawMessage `json:"operations"`
	}
	if json.Unmarshal(rawJSON(args), &a) != nil || len(a.Operations) == 0 {
		return 1
	}
	return len(a.Operations)
}

// hasMarker reports whether any decoded string (key or value, at any depth) of
// the arguments contains the closing marker. Arguments that do not decode are
// checked as raw text.
func hasMarker(args any) bool {
	raw := rawJSON(args)
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return strings.Contains(string(raw), doctorMarker)
	}
	var walk func(any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case string:
			return strings.Contains(x, doctorMarker)
		case []any:
			for _, e := range x {
				if walk(e) {
					return true
				}
			}
		case map[string]any:
			for k, e := range x {
				if strings.Contains(k, doctorMarker) || walk(e) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}

func rawJSON(args any) []byte {
	if r, ok := args.(json.RawMessage); ok {
		return r
	}
	b, _ := json.Marshal(args)
	return b
}

// DoctorKind is the doctor task kind: no options, one session per KB per run.
func DoctorKind() Kind {
	return Kind{
		Name:    "doctor",
		Options: []KindOption{{Key: MaxWritesKey}},
		Validate: func(o map[string]any) error {
			if n, _ := o["max_writes"].(int); n < 1 {
				return fmt.Errorf("max_writes must be at least 1")
			}
			return nil
		},
		Build: func(o map[string]any) (Instance, error) {
			n, _ := o["max_writes"].(int)
			if n <= 0 {
				n = defaultMaxWrites
			}
			return &doctorInstance{now: time.Now, maxWrites: n}, nil
		},
	}
}

type doctorInstance struct {
	now       func() time.Time
	maxWrites int
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
		"Today is %s (UTC). Artifacts cannot be written, and concept_merge and concept_archive are operator-only: record a gap instead. "+
		"Close with the log_append of step 8 before the tokens run out.\n\n",
		perimeter, kb, doctorReviewItems, tokens, day)
	return pre + body, nil
}

// noMarker removes the closing marker from a text the worker writes: only the
// model's own closing step may carry it, or a half session would silence the
// proposal with the debt still there.
// The replacement has a space where the marker has a hyphen, so no text around
// a replaced marker can rebuild it ("kb-kb-doctor"); the loop is belt and braces.
func noMarker(s string) string {
	for strings.Contains(s, doctorMarker) {
		s = strings.ReplaceAll(s, doctorMarker, "kb doctor")
	}
	return s
}

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
	if d.maxWrites <= 0 {
		d.maxWrites = defaultMaxWrites
	}
	guard := &doctorGuard{inner: rt.Tools, maxW: d.maxWrites}
	agent := NewAgent(rt.Model, guard, NewAllowlist(doctorTools()...),
		Limits{MaxSteps: doctorMaxSteps, MaxTotalTokens: rt.Grant})
	out, runErr := agent.Run(ctx, Task{System: system, User: "Run the session on this KB now."})
	res := Result{Tokens: out.Usage.Total(), CacheReadTokens: out.Usage.CacheReadTokens, Units: 1, Commits: guard.applied}
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
