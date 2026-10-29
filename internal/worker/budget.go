package worker

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The budget is rebuilt from the KBs' logs, per profile, never from local disk
// (D21): the worker keeps no state, so a replica that restarts, or a second
// replica, reads the same figures every other one wrote. Each run closes with
// one log_append per KB it touched, and the entries are the ledger.

// Why a run ended, the stopped= field of a closing entry.
const (
	StoppedDone     = "done"
	StoppedWindow   = "window"
	StoppedBudget   = "budget"
	StoppedIdle     = "idle"
	StoppedShutdown = "shutdown"
	StoppedError    = "error"
)

const (
	// ledgerStart and ledgerCap bound the log_tail read: log_tail has no upper
	// bound on n, so the reader doubles it from ledgerStart to ledgerCap and
	// fails closed there (the run is skipped) rather than undercount a day.
	ledgerStart = 100
	ledgerCap   = 6400

	// summarySep separates the machine-read fields of a closing entry from the
	// one-line summary of the kinds; the reader cuts at its first occurrence.
	summarySep = " — "
)

// maxFigure is the largest tokens figure the reader believes, per entry and per
// sum. A forged or corrupted figure above it counts as exactly this much, so a
// poisoned ledger can only read as spent, never wrap negative and read as free;
// it is far below MaxInt64 so that the sums of a few thousand entries cannot
// overflow either.
const maxFigure = 1 << 50

func satAdd(a, b int) int { return min(a+b, maxFigure) }

// ErrLedgerTooLong means a day's entries outgrew the capped read: the run is
// skipped, because spending on an undercounted ledger is the one thing the
// budget must not do.
//
// Trade-off: about ledgerCap unreadable entries under the worker's path (written
// by anyone with a write grant) make every run hit this error. Failing closed is
// the accepted price; Begin's error and a warning name the cause.
var ErrLedgerTooLong = errors.New("worker: the capped ledger read is full of entries from today or entries it cannot read (a poisoned ledger fails closed)")

// KBCaller returns the MCP caller scoped to one KB; *client.MCPClient.WithKB
// satisfies it through a one-line adapter.
type KBCaller func(kb string) ToolCaller

// Budget is the per-profile token accounting of one run.
type Budget struct {
	name     string
	profiles map[string]Profile
	loc      *time.Location
	callers  KBCaller

	// Logf receives the warnings and the failures that never fail a run; the
	// standard logger when nil.
	Logf func(format string, args ...any)

	mu      sync.Mutex
	runID   string
	day     string
	today   map[string]int            // spent today by profile, before this run
	run     map[string]int            // spent by this run by profile
	perKB   map[string]map[string]int // this run, by KB then profile
	units   map[string]int
	commits map[string]int
	touched map[string]bool
	order   []string // touched KBs in first-touch order
}

// NewBudget builds the accounting for the perimeter name. loc is the time zone
// of schedule.window (nil: UTC); its calendar day is the budget's day.
func NewBudget(name string, profiles []Profile, loc *time.Location, callers KBCaller) *Budget {
	if loc == nil {
		loc = time.UTC
	}
	b := &Budget{name: name, profiles: map[string]Profile{}, loc: loc, callers: callers}
	for _, p := range profiles {
		b.profiles[p.Name] = p
	}
	return b
}

func (b *Budget) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// RunID is the random string that names the run in its closing entries.
func (b *Budget) RunID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runID
}

// Day is the budget's calendar day for t, YYYY-MM-DD in the window's zone.
func (b *Budget) Day(t time.Time) string { return t.In(b.loc).Format("2006-01-02") }

func (b *Budget) path() string { return "worker/" + b.name }

// Begin starts a run: it rebuilds what was spent today from the log of every KB
// and clears the run's own counters. Any error means the run must be skipped
// and retried at the next poll: no read, no spending.
func (b *Budget) Begin(kbs []string, now time.Time) error {
	day := b.Day(now)
	today := map[string]int{}
	bad := 0
	for _, kb := range kbs {
		got, n, err := b.readKB(kb, day)
		if err != nil {
			return fmt.Errorf("budget: reading the ledger of %s: %w", kb, err)
		}
		bad += n
		for p, t := range got {
			today[p] = satAdd(today[p], t)
		}
	}
	if bad > 0 {
		b.logf("worker: %d malformed run entr%s in the ledger ignored", bad, map[bool]string{true: "y", false: "ies"}[bad == 1])
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return fmt.Errorf("budget: run id: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.runID, b.day, b.today = hex.EncodeToString(id), day, today
	b.run = map[string]int{}
	b.perKB = map[string]map[string]int{}
	b.units, b.commits = map[string]int{}, map[string]int{}
	b.touched, b.order = map[string]bool{}, nil
	return nil
}

// Remaining is what the profile may still spend today, before this run's own
// spending: daily_tokens minus what the ledger says was spent today.
func (b *Budget) Remaining(profile string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.profiles[profile]
	if !ok {
		return 0
	}
	return max(p.DailyTokens-b.today[profile], 0)
}

// Grant is the token ceiling of a task that starts now on the profile:
// min(remaining today, run_tokens) minus what this run already spent on the
// profile (D21). At zero or below the task is skipped for the rest of the run
// and the tasks of other profiles go on.
func (b *Budget) Grant(profile string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.profiles[profile]
	if !ok {
		return 0
	}
	remaining := max(p.DailyTokens-b.today[profile], 0)
	return min(remaining, p.RunTokens) - b.run[profile]
}

// Touch records that a task started on the KB: only touched KBs get a closing
// entry, so a run that found nothing writes nothing.
func (b *Budget) Touch(kb string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.touchLocked(kb)
}

func (b *Budget) touchLocked(kb string) {
	if !b.touched[kb] {
		b.touched[kb] = true
		b.order = append(b.order, kb)
	}
}

// Charge adds tokens spent on the KB to the profile of the task; the profile's
// budget is only ever charged with that profile's tokens.
func (b *Budget) Charge(kb, profile string, tokens int) {
	if tokens <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.touchLocked(kb)
	b.run[profile] += tokens
	if b.perKB[kb] == nil {
		b.perKB[kb] = map[string]int{}
	}
	b.perKB[kb][profile] += tokens
}

// Note adds the units finished and the commits made on the KB.
func (b *Budget) Note(kb string, units, commits int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.touchLocked(kb)
	b.units[kb] += units
	b.commits[kb] += commits
}

// Entry is the closing entry of the run on one KB, without the [worker/<name>]
// prefix the server adds. The format is the contract the reader below parses.
func (b *Budget) entry(kb, stopped, summary string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "run %s day=%s", b.runID, b.day)
	profiles := make([]string, 0, len(b.perKB[kb]))
	for p, n := range b.perKB[kb] {
		if n > 0 {
			profiles = append(profiles, p)
		}
	}
	sort.Strings(profiles)
	for _, p := range profiles {
		fmt.Fprintf(&sb, " tokens.%s=%d", p, b.perKB[kb][p])
	}
	fmt.Fprintf(&sb, " units=%d commits=%d stopped=%s", b.units[kb], b.commits[kb], stopped)
	if s := oneLine(summary); s != "" {
		sb.WriteString(summarySep + s)
	}
	return sb.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)), " ")
}

// Close writes one closing entry per touched KB. A failed log_append is retried
// once and then logged: it never fails the run (its spending is then lost, like
// a crash's). It returns the KBs that got no entry.
func (b *Budget) Close(stopped, summary string) (failed []string) {
	b.mu.Lock()
	kbs := append([]string(nil), b.order...)
	entries := make(map[string]string, len(kbs))
	for _, kb := range kbs {
		entries[kb] = b.entry(kb, stopped, summary)
	}
	b.mu.Unlock()
	for _, kb := range kbs {
		args := map[string]any{"entry": entries[kb], "path": b.path()}
		var err error
		for try := 0; try < 2; try++ {
			if err = b.append(kb, args); err == nil {
				break
			}
		}
		if err != nil {
			b.logf("worker: closing entry for %s not written: %v", kb, err)
			failed = append(failed, kb)
		}
	}
	return failed
}

func (b *Budget) append(kb string, args map[string]any) error {
	out, err := b.callers(kb).CallTool("log_append", args)
	if err != nil {
		return err
	}
	if out.IsError {
		return fmt.Errorf("log_append refused: %s", oneLine(out.Text))
	}
	return nil
}

// readKB sums today's tokens per profile from the worker's entries in one KB.
// It also returns how many entries it had to ignore as malformed.
func (b *Budget) readKB(kb, day string) (map[string]int, int, error) {
	caller := b.callers(kb)
	for n := ledgerStart; ; n *= 2 {
		out, err := caller.CallTool("log_tail", map[string]any{"path": b.path(), "n": n})
		if err != nil {
			return nil, 0, err
		}
		if out.IsError {
			if strings.Contains(out.Text, "concept not found: log.md") {
				return map[string]int{}, 0, nil // the first closing entry creates the file
			}
			return nil, 0, fmt.Errorf("log_tail: %s", oneLine(out.Text))
		}
		entries := splitEntries(out.Text)
		got, bad := map[string]int{}, 0
		for _, e := range entries {
			r, ok := b.parseRun(e)
			switch {
			case !ok:
				bad++
			case r.day == day:
				for p, t := range r.tokens {
					if _, known := b.profiles[p]; known {
						got[p] = satAdd(got[p], t)
					}
				}
			}
		}
		if len(entries) < n || !mayHaveMore(entries, day, b.parseRun) {
			return got, bad, nil
		}
		if n >= ledgerCap {
			b.logf("worker: the ledger of %s still shows today at %d entries (%d unreadable): skipping the run", kb, n, bad)
			return nil, 0, ErrLedgerTooLong
		}
	}
}

// mayHaveMore reports whether a full read may have cut today's entries off: it
// has not, once any entry of the answer is dated before today (the readable
// entries come newest first). The judgement is by date over the whole answer,
// not by position: kb.LogTail puts a legacy <path>/log.md before the root-log
// entries, so the last entry returned is not necessarily the oldest. Entries
// that cannot be read tell nothing and keep the read going.
func mayHaveMore(entries []string, day string, parse func(string) (runEntry, bool)) bool {
	for _, e := range entries {
		if r, ok := parse(e); ok && r.day < day {
			return false
		}
	}
	return true
}

// splitEntries returns the first non-empty body line of each "## " entry of a
// log_tail answer, newest first. The JSON note of an empty tail has no entry.
func splitEntries(text string) []string {
	var out []string
	var in, got bool
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			in, got = true, false
			out = append(out, "")
		case in && !got && strings.TrimSpace(line) != "":
			out[len(out)-1] = strings.TrimSpace(line)
			got = true
		}
	}
	return out
}

type runEntry struct {
	day    string
	tokens map[string]int
}

// parseRun reads one closing entry. A line that is not a "[worker/<name>] run "
// entry, or is one with a missing day or a broken figure, is not ok.
func (b *Budget) parseRun(line string) (runEntry, bool) {
	rest, ok := strings.CutPrefix(line, "["+b.path()+"] run ")
	if !ok {
		return runEntry{}, false
	}
	head, _, _ := strings.Cut(rest, summarySep)
	r := runEntry{tokens: map[string]int{}}
	for _, f := range strings.Fields(head) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch {
		case k == "day":
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return runEntry{}, false
			}
			r.day = v
		case strings.HasPrefix(k, "tokens."):
			n, err := strconv.Atoi(v)
			if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(v, "-") {
				n, err = maxFigure, nil // forged: counted as spent
			}
			if err != nil || n < 0 {
				return runEntry{}, false
			}
			p := strings.TrimPrefix(k, "tokens.")
			r.tokens[p] = satAdd(r.tokens[p], min(n, maxFigure))
		}
	}
	if r.day == "" {
		return runEntry{}, false
	}
	return r, true
}
