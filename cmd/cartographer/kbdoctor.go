package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// Exit codes of `kb doctor` (D299). "Debt remains" is not an error: on a real
// KB judgement work almost always remains, so a scheduler must be able to tell
// it from a failure and from a clean KB.
const (
	kbDoctorExitClean     = 0
	kbDoctorExitError     = 2
	kbDoctorExitJudgement = 3
)

// kbDoctorExamples is how many planned fixes per check the report shows.
const kbDoctorExamples = 5

// toolCaller is the slice of client.MCPClient kb doctor uses, so tests can
// drive it without a server.
type toolCaller interface {
	Call(tool string, args any) (json.RawMessage, error)
}

// targetCaller qualifies tool names for one KB target (multikb.go).
type targetCaller struct {
	c      *client.MCPClient
	target kbTarget
}

func (t targetCaller) Call(tool string, args any) (json.RawMessage, error) {
	return callTool(t.c, t.target, tool, args)
}

// kbDoctorCheck is one fixable check's line of the report.
type kbDoctorCheck struct {
	Check    string   `json:"check"`
	Planned  int      `json:"planned"`
	Auto     bool     `json:"auto"`
	Applied  int      `json:"applied"`
	Skipped  int      `json:"skipped"`
	Examples []string `json:"examples,omitempty"`
}

type kbDoctorReport struct {
	KB           string         `json:"kb"`
	Applied      bool           `json:"apply"`
	AutoRepair   []string       `json:"doctor_auto_repair"`
	Checks       []kbDoctorCheck  `json:"checks"`
	ReviewTotal  int            `json:"review_total"`
	ReviewByKind map[string]int `json:"review_by_kind,omitempty"`
	Remaining    int            `json:"remaining"`
}

// cmdKBDoctor implements `cartographer kb doctor <kb> [--apply] [--json]`:
// the unattended half of the doctor loop (D299). It plans every mechanical
// repair, applies only the checks the operator listed in the KB's
// doctor_auto_repair, and reports the judgement work left for a kb-doctor
// session. It never writes the kb-doctor log marker: a mechanical pass is not
// a doctor session (D290), and writing it would silence the doctor proposal
// for a whole interval while the judgement work is still undone.
func cmdKBDoctor(args []string) int {
	name, rest := splitPositional(args, "")
	fs := flag.NewFlagSet("kb doctor", flag.ExitOnError)
	applyFlag := fs.Bool("apply", false, "Apply the checks listed in the KB's doctor_auto_repair (default: plan only)")
	jsonFlag := fs.Bool("json", false, "Print the report as JSON")
	fs.Parse(rest)
	if name == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer kb doctor <kb> [--apply] [--json]")
		return kbDoctorExitError
	}

	cfg := clientconfig.Default()
	if dir, err := clientconfig.TargetDir(); err == nil {
		if loaded, err := clientconfig.Load(dir); err == nil {
			cfg = loaded
		}
	}
	token := resolveToken(cfg)
	health, err := client.New(cfg.ServerURL, token).Health(probeTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb doctor:", err)
		return kbDoctorExitError
	}
	targets, err := resolveKBTargets(health, []string{name})
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb doctor:", err)
		return kbDoctorExitError
	}
	c := targetCaller{c: client.New(cfg.ServerURL, token).WithKB(targets[0].Name), target: targets[0]}
	return runKBDoctor(c, *applyFlag, *jsonFlag, os.Stdout, os.Stderr)
}

// runKBDoctor is cmdKBDoctor once a caller for the KB exists.
func runKBDoctor(c toolCaller, apply, asJSON bool, out, errOut io.Writer) int {
	var status struct {
		KB           string `json:"kb"`
		Capabilities struct {
			DoctorAutoRepair struct {
				Checks []string `json:"checks"`
			} `json:"doctor_auto_repair"`
		} `json:"capabilities"`
		Review struct {
			Total  int            `json:"total"`
			ByKind map[string]int `json:"by_kind"`
		} `json:"review"`
	}
	if err := callInto(c, "kb_status", map[string]any{}, &status); err != nil {
		fmt.Fprintln(errOut, "kb doctor: kb_status:", err)
		return kbDoctorExitError
	}
	auto := map[string]bool{}
	for _, check := range status.Capabilities.DoctorAutoRepair.Checks {
		auto[check] = true
	}

	rep := kbDoctorReport{
		KB:           status.KB,
		Applied:      apply,
		AutoRepair:   append([]string{}, status.Capabilities.DoctorAutoRepair.Checks...),
		ReviewTotal:  status.Review.Total,
		ReviewByKind: status.Review.ByKind,
		Remaining:    status.Review.Total,
	}
	for _, check := range lint.FixableChecks {
		dc, err := kbDoctorRepair(c, check, true)
		if err != nil {
			fmt.Fprintf(errOut, "kb doctor: kb_repair %s: %v\n", check, err)
			return kbDoctorExitError
		}
		dc.Auto = auto[check]
		if apply && dc.Auto && dc.Planned > 0 {
			done, err := kbDoctorRepair(c, check, false)
			if err != nil {
				fmt.Fprintf(errOut, "kb doctor: kb_repair %s: %v\n", check, err)
				return kbDoctorExitError
			}
			dc.Applied, dc.Skipped = done.Applied, done.Skipped
		}
		rep.Remaining += dc.Planned - dc.Applied
		rep.Checks = append(rep.Checks, dc)
	}

	if asJSON {
		data, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Fprintln(out, string(data))
	} else {
		printKBDoctorReport(out, rep)
	}
	if rep.Remaining > 0 {
		return kbDoctorExitJudgement
	}
	return kbDoctorExitClean
}

// kbDoctorRepair runs kb_repair for one check and summarises its answer.
func kbDoctorRepair(c toolCaller, check string, dryRun bool) (kbDoctorCheck, error) {
	var res struct {
		Planned []struct {
			Path string    `json:"path"`
			Fix  *lint.Fix `json:"fix"`
		} `json:"planned"`
		PlannedTotal int             `json:"planned_total"`
		Applied      int             `json:"applied"`
		Skipped      json.RawMessage `json:"skipped"`
	}
	if err := callInto(c, "kb_repair", map[string]any{"check": check, "dry_run": dryRun}, &res); err != nil {
		return kbDoctorCheck{}, err
	}
	dc := kbDoctorCheck{Check: check, Planned: res.PlannedTotal, Applied: res.Applied}
	var skipped []json.RawMessage
	if json.Unmarshal(res.Skipped, &skipped) == nil {
		dc.Skipped = len(skipped)
	}
	for _, p := range res.Planned {
		if len(dc.Examples) == kbDoctorExamples {
			break
		}
		dc.Examples = append(dc.Examples, describeFix(p.Path, p.Fix))
	}
	return dc, nil
}

func describeFix(path string, f *lint.Fix) string {
	if f == nil {
		return path
	}
	s := path + ": " + f.Kind
	if f.Field != "" {
		s += " " + f.Field
	}
	if f.To != "" {
		s += " → " + f.To
	}
	return s
}

// callInto calls a tool and decodes its JSON answer. A server that appends
// a notice block (D299 nudge) makes client.Call return a JSON array of the
// blocks' texts: the answer is the first one.
func callInto(c toolCaller, tool string, args any, v any) error {
	raw, err := c.Call(tool, args)
	if err != nil {
		return err
	}
	var blocks []string
	if json.Unmarshal(raw, &blocks) == nil && len(blocks) > 0 {
		raw = json.RawMessage(blocks[0])
	}
	return json.Unmarshal(raw, v)
}

func printKBDoctorReport(w io.Writer, rep kbDoctorReport) {
	mode := "plan only"
	if rep.Applied {
		mode = "apply"
	}
	fmt.Fprintf(w, "KB %s — doctor (%s)\n", displayKBName(rep.KB), mode)
	if len(rep.AutoRepair) == 0 {
		fmt.Fprintln(w, "doctor_auto_repair is empty for this KB: nothing is applied unattended (set kbs[].doctor_auto_repair in the server config).")
	}
	fmt.Fprintln(w, "\nMechanical repairs:")
	for _, dc := range rep.Checks {
		tag := ""
		if dc.Auto {
			tag = " [auto]"
		}
		line := fmt.Sprintf("  %-20s %d planned%s", dc.Check, dc.Planned, tag)
		if dc.Applied > 0 || dc.Skipped > 0 {
			line += fmt.Sprintf(", %d applied, %d skipped", dc.Applied, dc.Skipped)
		}
		fmt.Fprintln(w, line)
		if dc.Applied == 0 {
			for _, ex := range dc.Examples {
				fmt.Fprintln(w, "      "+ex)
			}
		}
	}
	fmt.Fprintf(w, "\nJudgement work: %d review item(s)", rep.ReviewTotal)
	if len(rep.ReviewByKind) > 0 {
		kinds := make([]string, 0, len(rep.ReviewByKind))
		for k, n := range rep.ReviewByKind {
			kinds = append(kinds, fmt.Sprintf("%s %d", k, n))
		}
		sort.Strings(kinds)
		fmt.Fprintf(w, " (%s)", strings.Join(kinds, ", "))
	}
	fmt.Fprintln(w)
	if rep.Remaining > 0 {
		fmt.Fprintln(w, "Run a kb-doctor session in an agent for what is left.")
	}
}
