package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The background repair (D323): a KB that is never visited by a cartographer-kb-doctor
// session still drifts, so the server applies every safe fix (lint.AutoRepairChecks)
// itself, once per autoRepairInterval (D907: the interval is fixed; the one
// setting, maintenance.auto_repair, switches the timer off and nothing else:
// repair on write and the write gate are not affected). kb_repair check "auto"
// runs the same staged repair on demand (stagedRepairHandler). A KB a live
// Surveyor repairs is skipped by the timer (SetRepairDelegation). It is a heartbeat, not a doctor
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
	// autoRepairSurveyorReason and autoRepairMCPReason are the same for
	// kb_repair check "auto": MCP for every caller today; the Surveyor's is
	// reserved for the repair task, which must earn it from its authenticated
	// identity (a reason string proves nothing).
	autoRepairSurveyorReason = repairCommitReasonPrefix + " (surveyor)"
	autoRepairMCPReason      = repairCommitReasonPrefix + " (mcp)"
	// autoRepairLimit bounds the concepts each STAGE of one run rewrites (D355,
	// restated per stage by D1554): the concept-local stage, every cross-concept,
	// map and artifact check, and the second pass over the concepts the
	// cross-concept stage touched each take at most this many, so one run can
	// rewrite more than this in total. Within the local stage the fixpoint
	// writes a concept once whatever the number of checks that touched it. A KB
	// imported with thousands of findings is worked down over a few runs
	// instead of in one enormous commit.
	autoRepairLimit = 500
	// autoRepairPullGap is the least time between a run and the next one
	// that a git pull asks for (D355).
	autoRepairPullGap = 10 * time.Minute
	// autoRepairInterval is the heartbeat's period: the default of the removed
	// doctor_auto_interval (D323), now fixed (D907).
	autoRepairInterval = kb.AutoRepairIntervalDays * 24 * time.Hour
	// autoRepairRecheck is how soon the loop asks again while a surveyor owns
	// the repair: the server resumes within one recheck after it stops.
	autoRepairRecheck = 15 * time.Minute
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

// autoRepairLogRetention is how long a run stays in the log: older lines are
// dropped when a run is appended, as lint-history.jsonl does. Empty runs are
// still written (the Atlas "last run" stays truthful); only age prunes.
const autoRepairLogRetention = 90 * 24 * time.Hour

// autoRepairLogMaxLine bounds a log line a reader will hold; a longer one is
// skipped, and reading goes on after it.
const autoRepairLogMaxLine = 1 << 20

// autoRepairFailureMax bounds the failure text of a run: it can be a whole
// write-gate refusal or a git error, and it is repeated on every check line.
const autoRepairFailureMax = 4 << 10

// truncateFailure cuts s to autoRepairFailureMax bytes on a rune boundary.
func truncateFailure(s string) string {
	if len(s) <= autoRepairFailureMax {
		return s
	}
	cut := autoRepairFailureMax
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "... (truncated)"
}

// readAutoRepairLines calls fn with each line of the log, in order. A line
// longer than autoRepairLogMaxLine is skipped whole and reading goes on: one
// oversize line must not freeze "last run" before it.
// It returns how many oversize lines it skipped.
func readAutoRepairLines(r io.Reader, fn func(line []byte)) (skipped int) {
	br := bufio.NewReaderSize(r, 64*1024)
	var cur []byte
	over := false
	for {
		chunk, isPrefix, err := br.ReadLine()
		if len(chunk) > 0 && !over {
			if len(cur)+len(chunk) > autoRepairLogMaxLine {
				over, cur = true, nil
			} else {
				cur = append(cur, chunk...)
			}
		}
		if err != nil {
			if over {
				skipped++
			} else if len(cur) > 0 {
				fn(cur)
			}
			return skipped
		}
		if isPrefix {
			continue
		}
		if over {
			skipped++
		} else {
			fn(cur)
		}
		cur, over = nil, false
	}
}

// appendAutoRepairRun appends one run to the log and drops the lines older
// than autoRepairLogRetention (the file is then rewritten through a temporary
// file, so a crash never leaves half a log). A failure to write it is
// reported, never fatal: the repair itself already happened.
func appendAutoRepairRun(k *kb.KB, run autoRepairRun) error {
	line, err := json.Marshal(run)
	if err != nil {
		return err
	}
	p := autoRepairLogPath(k)
	// Prune-and-rename is read-modify-write: two writers of one log (the run's
	// own entry and the skipped-run entry) must not interleave.
	mu, _ := autoRepairLogLocks.LoadOrStore(p, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if err := ensureLocalStateDir(p); err != nil { // never write through a symlink (D148)
		return err
	}
	now, err := time.Parse(time.RFC3339, run.At)
	if err != nil {
		now = time.Now()
	}
	if kept, pruned := pruneAutoRepairLog(p, now.Add(-autoRepairLogRetention)); pruned {
		return rewriteAutoRepairLog(p, append(kept, line))
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	// On a network filesystem the write error can only show at close.
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// autoRepairLogLocks serialises the writers of each log file, by path.
var autoRepairLogLocks sync.Map

// pruneAutoRepairLog returns the lines of the log at or after cutoff, and
// whether any line had to go (older, unparseable or oversize).
func pruneAutoRepairLog(p string, cutoff time.Time) (kept [][]byte, pruned bool) {
	f, err := os.Open(p)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	total := 0
	// A line without its newline at the end (a crash, or an oversize last
	// line) would glue the next append onto it: rewrite to repair it.
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err == nil && last[0] != '\n' {
			pruned = true
		}
	}
	skipped := readAutoRepairLines(f, func(line []byte) {
		total++
		var run autoRepairRun
		if json.Unmarshal(line, &run) != nil {
			return
		}
		if at, err := time.Parse(time.RFC3339, run.At); err != nil || at.Before(cutoff) {
			return
		}
		kept = append(kept, append([]byte(nil), line...))
	})
	return kept, pruned || skipped > 0 || len(kept) != total
}

func rewriteAutoRepairLog(p string, lines [][]byte) error {
	// A temporary file left by a crash is never read as the log: drop it (the
	// caller holds the log's lock, so none is live).
	if stale, _ := filepath.Glob(p + ".*"); len(stale) > 0 {
		for _, f := range stale {
			os.Remove(f)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), autoRepairLogName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriter(tmp)
	for _, l := range lines {
		w.Write(l)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp makes 0600; the append path creates the log 0644.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// autoRepairRunsSince returns the runs logged at or after since, newest first,
// at most limit of them. A line that does not parse is skipped.
func autoRepairRunsSince(k *kb.KB, since time.Time, limit int) []autoRepairRun {
	out := []autoRepairRun{}
	if checkLocalStatePath(autoRepairLogPath(k)) != nil {
		return out
	}
	f, err := os.Open(autoRepairLogPath(k))
	if err != nil {
		return out
	}
	defer f.Close()
	readAutoRepairLines(f, func(line []byte) {
		var run autoRepairRun
		if json.Unmarshal(line, &run) != nil || run.At == "" {
			return
		}
		if at, err := time.Parse(time.RFC3339, run.At); err != nil || at.Before(since) {
			return
		}
		out = append(out, run)
	})
	// The log is appended in time order: newest first is the reverse.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// lastAutoRepairRun returns the newest run in the log, or false.
func lastAutoRepairRun(k *kb.KB) (autoRepairRun, bool) {
	if checkLocalStatePath(autoRepairLogPath(k)) != nil {
		return autoRepairRun{}, false
	}
	f, err := os.Open(autoRepairLogPath(k))
	if err != nil {
		return autoRepairRun{}, false
	}
	defer f.Close()
	var last autoRepairRun
	found := false
	readAutoRepairLines(f, func(line []byte) {
		var run autoRepairRun
		if json.Unmarshal(line, &run) == nil && run.At != "" {
			last, found = run, true
		}
	})
	return last, found
}

// SetAutoRepairEnabled records whether the background repair timer runs on this
// server (maintenance.auto_repair, default true). The caller that turns it off
// does not call StartAutoRepair; the setter only lets the Atlas say so. Call it
// before serving.
func (s *Server) SetAutoRepairEnabled(on bool) { s.autoRepairOff = !on }

// AutoRepairEnabled reports the setting recorded by SetAutoRepairEnabled.
func (s *Server) AutoRepairEnabled() bool { return !s.autoRepairOff }

// SetRepairDelegation installs the answer to "which live surveyors own this
// KB's repair". Before every run the loop asks it; a non-empty answer skips the
// run (nothing is logged to the run log) and the loop asks again after
// autoRepairRecheck. Call it before StartAutoRepair.
func (s *Server) SetRepairDelegation(f func() []string) { s.repairDelegation = f }

// noteSurveyorRepair records that principal's kb_repair check "auto" succeeded
// now. Bounded: one entry per principal, expired and oldest entries dropped
// past maxSurveyorsTotal.
func (s *Server) noteSurveyorRepair(principal string) {
	if principal == "" {
		return
	}
	s.surveyorRepairMu.Lock()
	defer s.surveyorRepairMu.Unlock()
	now := s.now()
	if s.surveyorRepairs == nil {
		s.surveyorRepairs = map[string]time.Time{}
	}
	s.surveyorRepairs[principal] = now
	if len(s.surveyorRepairs) > maxSurveyorsTotal {
		oldest, oldestAt := "", now
		for p, at := range s.surveyorRepairs {
			if now.Sub(at) > 16*24*time.Hour {
				delete(s.surveyorRepairs, p)
			} else if at.Before(oldestAt) {
				oldest, oldestAt = p, at
			}
		}
		if len(s.surveyorRepairs) > maxSurveyorsTotal && oldest != "" {
			delete(s.surveyorRepairs, oldest)
		}
	}
}

// surveyorRepairAt is the observation repairOwners consults.
func (s *Server) surveyorRepairAt(principal string) (time.Time, bool) {
	s.surveyorRepairMu.Lock()
	defer s.surveyorRepairMu.Unlock()
	at, ok := s.surveyorRepairs[principal]
	return at, ok
}

// delegatedTo returns the surveyors that own the repair now, logging once per
// change.
func (s *Server) delegatedTo() []string {
	var ids []string
	if s.repairDelegation != nil {
		ids = s.repairDelegation()
	}
	key := strings.Join(ids, ",")
	if key != s.delegationLogged {
		s.delegationLogged = key
		if len(ids) > 0 {
			fmt.Fprintf(os.Stderr, "cartographer: auto-repair on %s paused: surveyor %s owns it\n", kbName(s.kbRef), key)
		} else {
			fmt.Fprintf(os.Stderr, "cartographer: auto-repair on %s resumes: no surveyor owns it\n", kbName(s.kbRef))
		}
	}
	return ids
}

// StartAutoRepair starts the heartbeat for the KB this server serves and
// returns at once. Cancelling ctx stops it: the goroutine ends at the next
// wait, and no run starts after cancellation.
func (s *Server) StartAutoRepair(ctx context.Context) {
	k := s.kbRef
	if k == nil {
		return
	}
	interval := autoRepairInterval
	// Every start runs once, shortly after boot (D704): a start is an
	// upgrade, a config change or a fresh clone, each of which can make pages
	// nobody wrote fixable, and a fix waiting a day is a queue the Atlas would
	// have to show. A run with nothing to fix commits nothing.
	delay := autoRepairStartupDelay
	// Edits that bypass MCP (an editor, a push) arrive through a pull: when it
	// moves HEAD, the repair runs soon instead of at the next tick (D355).
	// SyncIn calls OnSyncIn only after a pull that changed HEAD.
	s.autoRepairTrigger = make(chan struct{}, 1)
	prev := k.OnSyncIn
	k.OnSyncIn = func() {
		if prev != nil {
			prev()
		}
		s.triggerAutoRepair()
	}
	go s.autoRepairLoop(ctx, interval, delay)
}

// triggerAutoRepair signals the heartbeat after a pull that moved HEAD. It
// never blocks: a signal already pending covers this one.
func (s *Server) triggerAutoRepair() {
	if s.autoRepairTrigger == nil {
		return
	}
	select {
	case s.autoRepairTrigger <- struct{}{}:
	default:
	}
}

// autoRepairLoop waits delay, runs, then runs every interval until ctx ends.
// A pull-triggered run goes through the same goroutine, so it is never
// concurrent with a scheduled one, and at most one is pending: it waits until
// autoRepairPullGap after the previous run (any kind).
func (s *Server) autoRepairLoop(ctx context.Context, interval, delay time.Duration) {
	gap := s.autoRepairPullGap
	if gap <= 0 {
		gap = autoRepairPullGap
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	var lastRun time.Time
	var pending <-chan time.Time
	for {
		scheduled := false
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			scheduled = true
		case <-s.autoRepairTrigger:
			if pending == nil {
				wait := gap - time.Since(lastRun)
				if wait < 0 || lastRun.IsZero() {
					wait = 0
				}
				pending = time.After(wait)
			}
			continue
		case <-pending:
			pending = nil
		}
		if ctx.Err() != nil {
			return
		}
		if len(s.delegatedTo()) > 0 {
			// A pull-triggered request is dropped; either way the timer
			// re-arms at the recheck so the server resumes soon after the
			// surveyor stops covering the KB.
			recheck := s.autoRepairRecheckFor
			if recheck <= 0 {
				recheck = autoRepairRecheck
			}
			timer.Reset(recheck)
			continue
		}
		s.runAutoRepairQuota(ctx)
		lastRun = time.Now()
		if scheduled {
			timer.Reset(interval)
		}
	}
}

// autoRepairQuotaOr is the per-stage quota: autoRepairLimit unless a test set
// a smaller one.
func (s *Server) autoRepairQuotaOr() int {
	if s.autoRepairQuota > 0 {
		return s.autoRepairQuota
	}
	return autoRepairLimit
}

// rotateIDs picks at most quota of the sorted ids, starting after *cursor and
// wrapping around. When everything fits it returns the ids unchanged and
// resets the cursor: there is nothing to rotate.
func rotateIDs(ids []string, quota int, cursor *string) []string {
	if len(ids) <= quota {
		if cursor != nil {
			*cursor = ""
		}
		return ids
	}
	start := 0
	if cursor != nil && *cursor != "" {
		start = sort.SearchStrings(ids, *cursor)
		if start < len(ids) && ids[start] == *cursor {
			start++
		}
		start %= len(ids)
	}
	out := make([]string, 0, quota)
	for i := 0; i < quota; i++ {
		out = append(out, ids[(start+i)%len(ids)])
	}
	return out
}

// stagedRepair is what one run did, per check.
type stagedRepair struct {
	applied map[string]int // concepts (or files) changed, per check
	skipped map[string]int
	errs    []string
	renames []repairTarget // concepts whose renames the map contracts must follow
}

func newStagedRepair() *stagedRepair {
	return &stagedRepair{applied: map[string]int{}, skipped: map[string]int{}}
}

func (r *stagedRepair) total() int {
	n := 0
	for _, v := range r.applied {
		n += v
	}
	return n
}

// fixpoint runs repairConceptFixpoint on one concept and records the outcome.
// seeded names the checks the concept was selected for, charged with the skip
// when the concept is left alone.
func (r *stagedRepair) fixpoint(k *kb.KB, id okf.ConceptID, local []string, seed []lint.Finding, seeded []string) bool {
	out, skip := repairConceptFixpointOnly(k, id, local, seed, unattendedFix)
	if skip != nil {
		for _, c := range seeded {
			r.skipped[c]++
		}
		return false
	}
	for c, n := range out.Changed {
		if n > 0 {
			r.applied[c]++
		}
	}
	for _, c := range out.Stuck {
		r.skipped[c]++
	}
	if out.Hash == "" && len(out.Stuck) == 0 {
		// Nothing written and nothing reported stuck: the concept did not
		// move, and the log must say so rather than show a quiet run.
		for _, c := range seeded {
			r.skipped[c]++
		}
	}
	if len(out.Renames) > 0 {
		r.renames = append(r.renames, repairTarget{ID: id, Path: okf.IDToPath(id), Fixes: out.Renames})
	}
	return out.Hash != ""
}

// stagedAutoRepair is the heartbeat's work (D355), in the order a KB needs; the
// quota applies to each stage separately (D1554):
//
//  1. every concept with a concept-local finding of an allowed check is
//     repaired to a fixpoint, at most quota concepts (rotating, so concepts
//     that stay stuck do not take the same slots every run), one write each;
//  2. the cross-concept checks run one by one through the per-check applier,
//     which keeps the mutual-pair guard and sees the state stage 1 left;
//  3. if stage 2 changed anything, stage 1 runs once more on the concepts it
//     touched: a rebased link can expose a duplicate.
//
// Artifact checks run last. The caller holds the KB lock.
func stagedAutoRepair(ctx context.Context, k *kb.KB, checks []string, quota int) *stagedRepair {
	return stagedAutoRepairFrom(ctx, k, checks, quota, nil)
}

// stagedAutoRepairFrom is stagedAutoRepair with the rotation cursor of stage 1:
// the concepts it picks when more than quota qualify start after *cursor (the
// last concept the previous run processed), wrapping around, so concepts that
// stay stuck do not take the same slots every run. A nil cursor starts at the
// first. The cursor is in memory only: a restart starts over.
func stagedAutoRepairFrom(ctx context.Context, k *kb.KB, checks []string, quota int, cursor *string) *stagedRepair {
	r := newStagedRepair()
	var local, cross, artifact, folders []string
	for _, c := range checks {
		s, _ := lint.Spec(c)
		switch {
		case lint.ArtifactRepairCheck(c):
			artifact = append(artifact, c)
		case lint.MapRepairCheck(c):
			folders = append(folders, c)
		case s.CrossConcept:
			cross = append(cross, c)
		default:
			local = append(local, c)
		}
	}
	isLocal := map[string]bool{}
	for _, c := range local {
		isLocal[c] = true
	}

	// seedsFor groups the fixable findings of the local checks by concept.
	seedsFor := func() (map[string][]lint.Finding, []string, error) {
		findings, err := lint.Run(k, "", false)
		if err != nil {
			return nil, nil, err
		}
		by := map[string][]lint.Finding{}
		for _, f := range findings {
			// unattendedFix is the filter repairWritten and the fixpoint apply:
			// a finding whose only fix is a removal never moves in the
			// background, so it must not take a slot.
			if f.Fix == nil || f.Artifact || !isLocal[f.Check] || !unattendedFix(f) {
				continue
			}
			if id := uiFindingConcept(f.Path); id != "" {
				by[id] = append(by[id], f)
			}
		}
		ids := make([]string, 0, len(by))
		for id := range by {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return by, ids, nil
	}
	checksOf := func(fs []lint.Finding) []string {
		seen := map[string]bool{}
		var out []string
		for _, f := range fs {
			if !seen[f.Check] {
				seen[f.Check] = true
				out = append(out, f.Check)
			}
		}
		return out
	}

	// A folder with no descriptor is repaired first: it needs nothing from the
	// concept stages, and its own pages are then in a map when they are fixed.
	for _, check := range folders {
		if ctx.Err() != nil {
			return r
		}
		targets, _, err := planMapRepair(k, check, "", nil)
		if err != nil {
			r.errs = append(r.errs, check+": "+err.Error())
			continue
		}
		if len(targets) > quota {
			targets = targets[:quota]
		}
		if len(targets) == 0 {
			continue
		}
		applied, skipped := applyMapRepair(k, targets)
		r.applied[check] += len(applied)
		r.skipped[check] += len(skipped)
	}

	if len(local) > 0 {
		by, ids, err := seedsFor()
		if err != nil {
			r.errs = append(r.errs, "lint: "+err.Error())
			return r
		}
		ids = rotateIDs(ids, quota, cursor)
		for _, id := range ids {
			if ctx.Err() != nil {
				return r
			}
			r.fixpoint(k, okf.ConceptID(id), local, by[id], checksOf(by[id]))
			if cursor != nil {
				*cursor = id
			}
		}
	}

	touched := map[string]bool{}
	for _, check := range cross {
		if ctx.Err() != nil {
			return r
		}
		targets, _, err := planRepair(k, check, "", nil)
		if err != nil {
			r.errs = append(r.errs, check+": "+err.Error())
			continue
		}
		if len(targets) > quota {
			targets = targets[:quota]
		}
		if len(targets) == 0 {
			continue
		}
		applied, skipped := applyRepair(k, targets, check == "reciprocal_link_item")
		r.applied[check] += len(applied)
		r.skipped[check] += len(skipped)
		for _, t := range applied {
			touched[string(t.ID)] = true
		}
	}
	if len(touched) > 0 && len(local) > 0 && ctx.Err() == nil {
		by, _, err := seedsFor()
		if err != nil {
			r.errs = append(r.errs, "lint: "+err.Error())
		} else {
			ids := make([]string, 0, len(touched))
			for id := range touched {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				if len(by[id]) == 0 {
					continue
				}
				r.fixpoint(k, okf.ConceptID(id), local, by[id], checksOf(by[id]))
			}
		}
	}

	for _, check := range artifact {
		if ctx.Err() != nil {
			return r
		}
		targets, _, err := planArtifactRepair(k, check, "", nil)
		if err != nil {
			r.errs = append(r.errs, check+": "+err.Error())
			continue
		}
		if len(targets) > quota {
			targets = targets[:quota]
		}
		if len(targets) == 0 {
			continue
		}
		applied, skipped := applyArtifactRepair(k, targets)
		r.applied[check] += len(applied)
		r.skipped[check] += len(skipped)
	}
	return r
}

// runAutoRepairQuota runs one background repair (D355) and logs it. It goes
// through gitWrap, so the KB lock, the commit (one per run, Reason
// "auto-repair (background)", one body line per check), the git sync and the
// stale-write guard are the ones every write has, not a second copy.
func (s *Server) runAutoRepairQuota(ctx context.Context) autoRepairRun {
	k := s.kbRef
	run := autoRepairRun{At: s.now().UTC().Format(time.RFC3339)}
	if k == nil {
		return run
	}
	run.Skipped = autoRepairBlocked(k)
	if run.Skipped != "" {
		fmt.Fprintf(os.Stderr, "cartographer: auto-repair skipped for %s: %s\n", kbName(k), run.Skipped)
		_ = appendAutoRepairRun(k, run)
		return run
	}
	checks := lint.AutoRepairChecks()
	var stats *stagedRepair
	// The cursor moves with the run, and is kept only if the run's commit
	// held: a rolled-back run must not make the next one skip its slots.
	nextCursor := s.autoRepairCursor
	// Named kb_repair so the commit, audit and authorization treat it as the
	// repair it is; it is not registered, so tools/list does not change. The
	// body is the one kb_repair check "auto" runs (stagedRepairHandler).
	tool := gitWrap(k, Tool{
		Name:    "kb_repair",
		Handler: stagedRepairHandler(k, checks, s.autoRepairQuotaOr(), &nextCursor, autoRepairReason, &stats),
	})
	rctx := withUnattended(auth.ContextWithPrincipal(ctx, auth.LocalAdminPrincipal()))
	args, _ := json.Marshal(map[string]any{"reason": autoRepairReason})
	res, err := tool.Handler(rctx, args)
	failure := ""
	switch {
	case err != nil:
		failure = err.Error()
	case res.IsError:
		failure = firstText(res)
	}
	failure = truncateFailure(failure)
	for _, check := range checks {
		entry := autoRepairCheck{Check: check, Error: failure}
		if stats != nil {
			entry.Applied, entry.Skipped = stats.applied[check], stats.skipped[check]
		}
		if entry.Applied > 0 {
			entry.Commit = res.CommitSHA
		}
		// A check that found nothing is not reported.
		if entry.Applied == 0 && entry.Skipped == 0 && entry.Error == "" {
			continue
		}
		run.Checks = append(run.Checks, entry)
		if entry.Applied > 0 {
			fmt.Fprintf(os.Stderr, "cartographer: auto-repair %s on %s: %d concept(s) repaired (%s)\n", check, kbName(k), entry.Applied, shortSHA(entry.Commit))
		}
	}
	// A run that rewrote concepts but produced no commit (the commit failed,
	// which gitWrap logs without returning) did not hold either.
	if failure == "" && (stats == nil || stats.total() == 0 || res.CommitSHA != "") {
		s.autoRepairCursor = nextCursor
	}
	if failure != "" {
		fmt.Fprintf(os.Stderr, "cartographer: auto-repair on %s: %s\n", kbName(k), failure)
	}
	if stats != nil {
		for _, e := range stats.errs {
			fmt.Fprintf(os.Stderr, "cartographer: auto-repair on %s: %s\n", kbName(k), e)
		}
	}
	if err := appendAutoRepairRun(k, run); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: auto-repair log for %s: %v\n", kbName(k), err)
	}
	return run
}

// autoRepairBlocked says why the staged repair must not write now, empty when
// it may. A KB with a conflict to resolve is a KB a person must look at first:
// writing on top of it would add to the mess. The timer logs the text; kb_repair
// check "auto" refuses with it, and a Surveyor treats the refusal as "next run".
func autoRepairBlocked(k *kb.KB) string {
	if conflicts, err := k.ListConflicts(); err == nil && len(conflicts) > 0 {
		return fmt.Sprintf("%d open git conflict(s)", len(conflicts))
	}
	if k.GitStatusSnapshot().State == "degraded" {
		return "git sync is degraded"
	}
	return ""
}

// stagedRepairHandler is the body of one staged repair, the same for the timer
// and for kb_repair check "auto" (parity is a test): the stages of
// stagedAutoRepairFrom, the map contracts that follow a rename, the log.md
// entry and the commit subject, both led by label. It stores what it did in
// *out and rotates *cursor; whether the cursor is kept is the caller's, who
// knows whether the commit held. It runs under the KB lock, inside gitWrap.
func stagedRepairHandler(k *kb.KB, checks []string, quota int, cursor *string, label string, out **stagedRepair) func(requestContext, json.RawMessage) (ToolResult, error) {
	return func(ctx requestContext, _ json.RawMessage) (ToolResult, error) {
		stats := stagedAutoRepairFrom(ctx, k, checks, quota, cursor)
		*out = stats
		if len(stats.renames) > 0 {
			if _, err := renameInContracts(k, stats.renames); err != nil {
				stats.errs = append(stats.errs, "map contracts: "+err.Error())
			}
		}
		res := ToolResult{Content: textResult(stagedRepairSummary(checks, stats)).Content}
		if stats.total() > 0 {
			var lines []string
			for _, c := range checks {
				if n := stats.applied[c]; n > 0 {
					lines = append(lines, fmt.Sprintf("%s: %d", c, n))
				}
			}
			body := strings.Join(lines, "\n")
			_ = k.AppendLog(label+"\n\n"+body, time.Now())
			res.CommitSubject = label + "\n\n" + body
		}
		return res, nil
	}
}

// logAutoRepairCall appends the JSONL line of one kb_repair check "auto" call,
// as the timer appends its runs, so the Atlas runs[] and last_auto_repair show
// on-demand runs too. commit is empty when nothing changed.
func logAutoRepairCall(k *kb.KB, checks []string, stats *stagedRepair, commit string) {
	run := autoRepairRun{At: time.Now().UTC().Format(time.RFC3339)}
	for _, c := range checks {
		e := autoRepairCheck{Check: c, Applied: stats.applied[c], Skipped: stats.skipped[c]}
		if e.Applied > 0 {
			e.Commit = commit
		}
		if e.Applied == 0 && e.Skipped == 0 {
			continue
		}
		run.Checks = append(run.Checks, e)
	}
	if err := appendAutoRepairRun(k, run); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: auto-repair log for %s: %v\n", kbName(k), err)
	}
}

// stagedRepairSummary is the JSON answer of kb_repair check "auto".
func stagedRepairSummary(checks []string, stats *stagedRepair) string {
	entries := []map[string]any{}
	skipped := 0
	for _, c := range checks {
		a, sk := stats.applied[c], stats.skipped[c]
		skipped += sk
		if a > 0 || sk > 0 {
			entries = append(entries, map[string]any{"check": c, "applied": a, "skipped": sk})
		}
	}
	out := map[string]any{
		"dry_run":       false,
		"auto":          true,
		"checks":        entries,
		"applied_total": stats.total(),
		"skipped_total": skipped,
		"checks_run":    len(checks),
	}
	if len(stats.errs) > 0 {
		out["errors"] = stats.errs
	}
	b, _ := json.Marshal(out)
	return string(b)
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
