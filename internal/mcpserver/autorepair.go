package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// The background repair (D323): a KB that is never visited by a kb-doctor
// session still drifts, so the server applies the KB's auto_repair checks
// itself, once per doctor_auto_interval. It is a heartbeat, not a doctor
// session: it never writes the kb-doctor log marker (D290), so it does not
// move last_doctor and does not silence the doctor proposal for the
// judgement work it cannot do.

const (
	// autoRepairLogName is the local record of the runs, under .cartographer/
	// (excluded from git): not a concept and not log.md, so it can neither be
	// linted nor mistaken for a doctor session.
	autoRepairLogName = "auto-repair-log.jsonl"
	// autoRepairReason is the Reason trailer of every commit the heartbeat
	// makes; repair_revert and the Atlas recognise the commits by it.
	autoRepairReason = repairCommitReasonPrefix + " (background)"
	// autoRepairLimit bounds the concepts one check rewrites per run: a KB
	// imported with thousands of findings is worked down over several days
	// instead of in one enormous commit.
	autoRepairLimit = 50
	// autoRepairStartupDelay keeps the first run off the boot path.
	autoRepairStartupDelay = time.Minute
)

// autoRepairCheck is what one check did in one run.
type autoRepairCheck struct {
	Check   string `json:"check"`
	Applied int    `json:"applied"`
	Skipped int    `json:"skipped,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Error   string `json:"error,omitempty"`
}

// autoRepairRun is one line of the JSONL log: one run, every check of it.
type autoRepairRun struct {
	At string `json:"at"`
	// Skipped says why the whole run did nothing (a degraded KB).
	Skipped string            `json:"skipped,omitempty"`
	Checks  []autoRepairCheck `json:"checks,omitempty"`
}

func autoRepairLogPath(k *kb.KB) string {
	return filepath.Join(k.Root, ".cartographer", autoRepairLogName)
}

// appendAutoRepairRun appends one run to the log. A failure to write it is
// reported, never fatal: the repair itself already happened.
func appendAutoRepairRun(k *kb.KB, run autoRepairRun) error {
	line, err := json.Marshal(run)
	if err != nil {
		return err
	}
	p := autoRepairLogPath(k)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(p); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", p) // never write through a symlink (D148)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// lastAutoRepairRun returns the newest run in the log, or false.
func lastAutoRepairRun(k *kb.KB) (autoRepairRun, bool) {
	f, err := os.Open(autoRepairLogPath(k))
	if err != nil {
		return autoRepairRun{}, false
	}
	defer f.Close()
	var last autoRepairRun
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var run autoRepairRun
		if json.Unmarshal(sc.Bytes(), &run) == nil && run.At != "" {
			last, found = run, true
		}
	}
	return last, found
}

// StartAutoRepair starts the heartbeat for the KB this server serves and
// returns at once. It does nothing when the KB has no auto_repair checks or
// doctor_auto_interval is 0. Cancelling ctx stops it: the goroutine ends at the
// next wait, and no run starts after cancellation.
func (s *Server) StartAutoRepair(ctx context.Context) {
	k := s.kbRef
	if k == nil || k.DoctorAutoIntervalDays <= 0 || len(k.AutoRepair) == 0 {
		return
	}
	interval := time.Duration(k.DoctorAutoIntervalDays) * 24 * time.Hour
	delay := autoRepairStartupDelay
	// A restart must not push the next run a whole interval away, nor run
	// again when one just happened: schedule from the last logged run.
	if last, ok := lastAutoRepairRun(k); ok {
		if at, err := time.Parse(time.RFC3339, last.At); err == nil {
			if wait := at.Add(interval).Sub(s.now()); wait > delay {
				delay = wait
			}
		}
	}
	go s.autoRepairLoop(ctx, interval, delay)
}

// autoRepairLoop waits delay, runs, then runs every interval until ctx ends.
func (s *Server) autoRepairLoop(ctx context.Context, interval, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}
		s.runAutoRepairQuota(ctx)
		timer.Reset(interval)
	}
}

// runAutoRepairQuota applies every auto_repair check once, at most
// autoRepairLimit concepts each, through the kb_repair tool: so the KB lock,
// the commit (one per check, Reason "auto-repair (background)"), the git sync
// and the stale-write guard are the ones every write has, not a second copy.
func (s *Server) runAutoRepairQuota(ctx context.Context) autoRepairRun {
	k := s.kbRef
	run := autoRepairRun{At: s.now().UTC().Format(time.RFC3339)}
	if k == nil {
		return run
	}
	// A KB with a conflict to resolve is a KB a person must look at first:
	// writing on top of it would add to the mess.
	if conflicts, err := k.ListConflicts(); err == nil && len(conflicts) > 0 {
		run.Skipped = fmt.Sprintf("%d open git conflict(s)", len(conflicts))
	} else if k.GitSync && k.GitStatusSnapshot().State == "degraded" {
		run.Skipped = "git sync is degraded"
	}
	if run.Skipped != "" {
		fmt.Fprintf(os.Stderr, "cartographer: auto-repair skipped for %s: %s\n", kbName(k), run.Skipped)
		_ = appendAutoRepairRun(k, run)
		return run
	}
	tool, ok := uiTool(s, "kb_repair")
	if !ok {
		return run
	}
	rctx := auth.ContextWithPrincipal(ctx, auth.LocalAdminPrincipal())
	for _, check := range k.AutoRepair {
		if ctx.Err() != nil {
			break // shutting down: what ran is logged, nothing new starts
		}
		args, _ := json.Marshal(map[string]any{"check": check, "dry_run": false, "limit": autoRepairLimit, "reason": autoRepairReason})
		res, err := tool.Handler(rctx, args)
		entry := autoRepairCheck{Check: check, Commit: res.CommitSHA}
		switch {
		case err != nil:
			entry.Error = err.Error()
		case res.IsError:
			entry.Error = firstText(res)
		default:
			var out struct {
				Applied int               `json:"applied"`
				Skipped []json.RawMessage `json:"skipped"`
			}
			if json.Unmarshal([]byte(firstText(res)), &out) == nil {
				entry.Applied, entry.Skipped = out.Applied, len(out.Skipped)
			}
		}
		run.Checks = append(run.Checks, entry)
		if entry.Error != "" {
			fmt.Fprintf(os.Stderr, "cartographer: auto-repair %s on %s: %s\n", check, kbName(k), entry.Error)
		} else if entry.Applied > 0 {
			fmt.Fprintf(os.Stderr, "cartographer: auto-repair %s on %s: %d concept(s) repaired (%s)\n", check, kbName(k), entry.Applied, shortSHA(entry.Commit))
		}
	}
	if err := appendAutoRepairRun(k, run); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: auto-repair log for %s: %v\n", kbName(k), err)
	}
	return run
}

func firstText(res ToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	return strings.TrimSpace(res.Content[0].Text)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
