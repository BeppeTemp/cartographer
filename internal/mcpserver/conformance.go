package mcpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// findingOut is a lint finding as the tools return it (D289): the optional
// machine-readable fix is omitted when the finding has none.
type findingOut struct {
	Path     string         `json:"path"`
	Check    string         `json:"check"`
	Severity string         `json:"severity"`
	Message  string         `json:"message"`
	Fix      *lint.Fix      `json:"fix,omitempty"`
	Proposal *lint.Proposal `json:"proposal,omitempty"`
}

func findingsOut(findings []lint.Finding) []findingOut {
	out := make([]findingOut, 0, len(findings))
	for _, f := range findings {
		out = append(out, findingOut{Path: f.Path, Check: f.Check, Severity: f.Severity, Message: f.Message, Fix: f.Fix, Proposal: f.Proposal})
	}
	return out
}

// rejectToolParamKeys refuses a frontmatter map that carries a write-tool
// parameter name as a key (D289): an agent that passed if_match, body... inside
// the frontmatter object, which would otherwise be persisted as a field. With
// allowNull a null value passes: in a patch it removes the key, which is the
// repair, not the mistake.
func rejectToolParamKeys(fm map[string]interface{}, allowNull bool) error {
	keys := make([]string, 0, len(fm))
	for k := range fm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if lint.IsToolParamField(k) && !(allowNull && fm[k] == nil) {
			return fmt.Errorf("frontmatter key %q is a tool parameter, not a field — pass it as a top-level argument", k)
		}
	}
	return nil
}

// findingsOrEmpty is the findings array a write answers with: always present,
// empty when there is nothing to say (D342).
func findingsOrEmpty(f []findingOut) []findingOut {
	if f == nil {
		return []findingOut{}
	}
	return f
}

// scopedFindings is the structural half of writeLintFindings alone, for a write
// that changed no concept's own content (an index_patch, D312), keeping only
// the findings of the check named: what the patch did, not what was already
// there.
func scopedFindings(k *kb.KB, ids []string, check string) []findingOut {
	scoped := make([]okf.ConceptID, len(ids))
	for i, id := range ids {
		scoped[i] = okf.ConceptID(id)
	}
	var kept []lint.Finding
	for _, f := range lint.ScopedCheck(k, scoped) {
		if f.Check == check {
			kept = append(kept, f)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return findingsOut(kept)
}

// findingsOrNil is findingsOut with nil for an empty list.
func findingsOrNil(f []lint.Finding) []findingOut {
	if len(f) == 0 {
		return nil
	}
	return findingsOut(f)
}

// writeLintFindings returns the lint findings of the concepts a write left on
// disk (written) or removed (gone), lint_ignore applied (D289, D312); gate_check's
// changed_ids path (D312) decides pass on the severities.
func writeLintFindings(k *kb.KB, written, gone []string) []lint.Finding {
	var found []lint.Finding
	scoped := make([]okf.ConceptID, 0, len(written)+len(gone))
	for _, id := range written {
		scoped = append(scoped, okf.ConceptID(id))
		if data, err := k.ReadConcept(okf.ConceptID(id)); err == nil {
			found = append(found, lint.CheckConcept(k, okf.ConceptID(id), data.Content)...)
		}
	}
	for _, id := range gone {
		scoped = append(scoped, okf.ConceptID(id))
	}
	found = append(found, lint.ScopedCheck(k, scoped)...)
	// CheckConcept and ScopedCheck can both report the same thing (the
	// frontmatter-driven part of a body check): one finding, not two.
	seen := map[string]bool{}
	uniq := found[:0]
	for _, f := range found {
		key := f.Path + "\x00" + f.Check + "\x00" + f.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		uniq = append(uniq, f)
	}
	return uniq
}

// conformanceChecks are the checks kb_status counts as conformance debt (D290):
// the D289 ones plus the structural debt that accumulates when nothing
// surfaces it. Everything else lint reports is content, not drift.
var conformanceChecks = map[string]bool{
	"nonstandard_field":         true,
	"tool_param_field":          true,
	"missing_value_contract":    true,
	"broken_link":               true,
	"broken_relation":           true,
	"link_to_retired":           true,
	"index_lists_retired":       true,
	"map_misfit":                true,
	"prose_value":               true,
	"invalid_field_value":       true,
	"stale_open":                true,
	"closed_with_open_items":    true,
	"legacy_archive_descriptor": true,
}

// doctorMarker is the text a log.md entry carries when the kb-doctor skill
// closed a session: kb_status reads the date of the newest one.
const doctorMarker = "kb-doctor"

var logDateRe = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2})`)

// lastDoctorDate returns the date (YYYY-MM-DD) of the newest log.md entry whose
// text contains doctorMarker, or "".
func lastDoctorDate(k *kb.KB) string {
	tail, err := k.LogTail("", 1<<20)
	if err != nil {
		return ""
	}
	newest := ""
	for _, block := range strings.Split("\n"+tail, "\n## ")[1:] {
		heading, _, _ := strings.Cut(block, "\n")
		m := logDateRe.FindStringSubmatch("## " + heading)
		if m == nil || !strings.Contains(block, doctorMarker) {
			continue
		}
		if m[1] > newest {
			newest = m[1]
		}
	}
	return newest
}

// doctorDue says whether a kb-doctor session is due (D299): the KB's interval
// has passed since the last one, or there never was one. An interval of 0
// means the operator turned the proposal off.
func doctorDue(lastDoctor string, intervalDays int, now time.Time) bool {
	if intervalDays <= 0 {
		return false
	}
	t, err := time.Parse("2006-01-02", lastDoctor)
	return err != nil || !now.Before(t.AddDate(0, 0, intervalDays))
}

// summarizeConformance turns the findings and the review items the caller may
// see into the kb_status conformance object. A pure function of its inputs, so
// the doctor_suggested truth table is testable without a KB.
//
// doctor_suggested is debt && due (D299): any lint finding the caller can
// see or any review item, once the KB's doctor interval has passed. Time, not
// the first warning, decides, so the flag does not stay on for good on a real
// KB. Every finding counts, not only the conformance checks (D336): the
// Health panel shows them all, every one has a way out, and a doctor session is
// done only at zero — a debt that never calls the doctor never gets there.
//
// repairable counts, per check, every finding kb_repair can fix — conformance
// check or not (D331): fixable alone hid stringified_list or title_h1_mismatch
// from a doctor session planning its mechanical pass.
func summarizeConformance(findings []lint.Finding, reviewTotal int, lastDoctor string, intervalDays int, now time.Time) map[string]interface{} {
	bySev := map[string]int{}
	acceptability := map[string]string{}
	fixable := 0
	repairable := map[string]int{}
	for _, f := range findings {
		if f.Fix != nil {
			repairable[f.Check]++
		}
		if !conformanceChecks[f.Check] {
			continue
		}
		acceptability[f.Check] = lint.CheckAcceptability(f.Check)
		bySev[f.Severity]++
		if f.Fix != nil {
			fixable++
		}
	}
	out := map[string]interface{}{
		"findings":         bySev,
		"fixable":          fixable,
		"repairable":       repairable,
		"acceptability":    acceptability,
		"lint_findings":    len(findings),
		"doctor_suggested": (len(findings)+reviewTotal) > 0 && doctorDue(lastDoctor, intervalDays, now),
	}
	if lastDoctor != "" {
		out["last_doctor"] = lastDoctor
		if t, err := time.Parse("2006-01-02", lastDoctor); err == nil && intervalDays > 0 {
			out["next_doctor"] = t.AddDate(0, 0, intervalDays).Format("2006-01-02")
		}
	}
	return out
}

// conformanceCache caches the whole-KB lint findings and the last-doctor date
// keyed on the graph view generation (D294). The cached slice is the unfiltered
// output of lint.Run; per-caller visibility (D226) is applied on every read, so
// the cache never leaks a hidden concept.
type conformanceCache struct {
	mu sync.Mutex

	// lint findings, cached on graph generation.
	gen      uint64
	stamp    string
	cached   bool // true once findings has been set (findings may be nil = no issues)
	findings []lint.Finding

	// lastDoctorDate, cached on the root log file's mtime+size.
	doctorDate string
	logMtime   int64
	logSize    int64

	// clock replaces time.Now for the lint history in tests.
	clock func() time.Time

	// lintCalls counts how many times lint.Run was actually called (for tests).
	lintCalls int

	// review work list (D298), cached on the same key as findings.
	reviewGen    uint64
	reviewStamp  string
	reviewCached bool
	review       []lint.ReviewItem

	// whole-KB read cost (D301), cached on the graph generation alone: it
	// reads nothing but concept files.
	readCostGen    uint64
	readCostCached bool
	readCost       kb.ReadCost

	// work view (D302), cached on the same key as findings.
	workGen    uint64
	workStamp  string
	workCached bool
	work       []lint.WorkEntry
}

// workEntries returns the whole-KB work view (D302), unfiltered, on the lint
// cache key: the open statuses come from map contracts, which the stamp
// covers. Visibility is the caller's (visibleWork).
func (cc *conformanceCache) workEntries(k *kb.KB) ([]lint.WorkEntry, error) {
	gen, err := k.GraphGeneration()
	if err != nil {
		return nil, err
	}
	// Age and staleness move with the calendar, not with the files: the day
	// is part of the key, so an idle KB does not serve yesterday's ages.
	day := lint.Now().UTC().Format("2006-01-02")
	stamp := lintInputsStamp(k) + "|" + day
	cc.mu.Lock()
	if cc.workCached && gen == cc.workGen && stamp == cc.workStamp {
		out := cc.work
		cc.mu.Unlock()
		return out, nil
	}
	cc.mu.Unlock()
	entries, err := lint.Work(k)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []lint.WorkEntry{}
	}
	if genAfter, gerr := k.GraphGeneration(); gerr == nil && genAfter == gen && lintInputsStamp(k)+"|"+day == stamp {
		cc.mu.Lock()
		cc.workGen, cc.workStamp, cc.work, cc.workCached = gen, stamp, entries, true
		cc.mu.Unlock()
	}
	return entries, nil
}

// readCostFor returns kb_status.read_cost for a caller. The whole-KB answer
// is cached on the graph generation; a narrowed caller's is computed on its
// visible graph every time, since percentiles over a filtered cache entry
// would still count the hidden concepts (D226).
func (cc *conformanceCache) readCostFor(k *kb.KB, include func(string) bool, whole bool) (kb.ReadCost, error) {
	if !whole {
		return k.ReadCost(include)
	}
	gen, err := k.GraphGeneration()
	if err != nil {
		return kb.ReadCost{}, err
	}
	cc.mu.Lock()
	if cc.readCostCached && cc.readCostGen == gen {
		out := cc.readCost
		cc.mu.Unlock()
		return out, nil
	}
	cc.mu.Unlock()
	rc, err := k.ReadCost(nil)
	if err != nil {
		return kb.ReadCost{}, err
	}
	if genAfter, gerr := k.GraphGeneration(); gerr == nil && genAfter == gen {
		cc.mu.Lock()
		cc.readCostGen, cc.readCost, cc.readCostCached = gen, rc, true
		cc.mu.Unlock()
	}
	return rc, nil
}

// reviewItems returns the whole-KB review work list (D298), unfiltered, on the
// same cache key as lintFindings: kb_status and kb_review share one
// computation per generation. Visibility is the caller's (lint.FilterReview).
func (cc *conformanceCache) reviewItems(k *kb.KB) ([]lint.ReviewItem, error) {
	gen, err := k.GraphGeneration()
	if err != nil {
		return nil, err
	}
	stamp := lintInputsStamp(k)
	cc.mu.Lock()
	if cc.reviewCached && gen == cc.reviewGen && stamp == cc.reviewStamp {
		out := cc.review
		cc.mu.Unlock()
		return out, nil
	}
	cc.mu.Unlock()
	findings, err := cc.lintFindings(k)
	if err != nil {
		return nil, err
	}
	items, err := lint.Review(k, findings)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []lint.ReviewItem{}
	}
	if genAfter, gerr := k.GraphGeneration(); gerr == nil && genAfter == gen && lintInputsStamp(k) == stamp {
		cc.mu.Lock()
		cc.reviewGen, cc.reviewStamp, cc.review, cc.reviewCached = gen, stamp, items, true
		cc.mu.Unlock()
	}
	return items, nil
}

// lintFindings returns the whole-KB lint findings, reusing the cache while
// the KB's inputs are unchanged. The key is the graph generation plus a stamp
// of every non-concept input lint reads (map descriptors and indexes, assets,
// glossary.yaml, paths.yaml, hooks, templates): the graph cache tracks concept
// files only, so a map_update or an out-of-band contract edit would otherwise
// serve stale findings. The result is never nil, so callers can tell "cached,
// no findings" from "not computed".
func (cc *conformanceCache) lintFindings(k *kb.KB) ([]lint.Finding, error) {
	gen, err := k.GraphGeneration()
	if err != nil {
		return nil, err
	}
	stamp := lintInputsStamp(k)
	cc.mu.Lock()
	if cc.cached && gen == cc.gen && stamp == cc.stamp {
		out := cc.findings
		cc.mu.Unlock()
		return out, nil
	}
	cc.mu.Unlock()
	findings, err := lint.Run(k, "", false)
	if err != nil {
		return nil, err
	}
	if findings == nil {
		findings = []lint.Finding{}
	}
	// A write that landed while lint ran would make these findings older
	// than the state the new key describes: return them, but cache only when
	// the inputs did not move underneath.
	genAfter, gerr := k.GraphGeneration()
	if gerr == nil && genAfter == gen && lintInputsStamp(k) == stamp {
		cc.mu.Lock()
		cc.gen, cc.stamp, cc.findings, cc.cached = gen, stamp, findings, true
		cc.lintCalls++
		cc.mu.Unlock()
		// D370: one sample a day of the unfiltered totals, taken where the
		// whole-KB lint is recomputed and cached.
		now := time.Now()
		if cc.clock != nil {
			now = cc.clock()
		}
		_ = recordLintSample(k, newLintSample(k, findings, now), now)
	} else {
		cc.mu.Lock()
		cc.lintCalls++
		cc.mu.Unlock()
	}
	return findings, nil
}

// lintInputsStamp fingerprints (path, size, mtime) of every regular file in
// the KB root that is not a concept body and not under .git. Concept bodies
// are covered by the graph generation; everything else lint reads is here.
// A stat walk, no reads.
func lintInputsStamp(k *kb.KB) string {
	root := filepath.Dir(k.DataRoot())
	h := sha256.New()
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		name := d.Name()
		// The lint history is written on every recompute (D370): counting it
		// would invalidate the cache it was written from. The rest of
		// .cartographer stays in: usage.json feeds artifact_unused.
		if strings.HasPrefix(name, lintHistoryName) {
			return nil
		}
		if strings.HasSuffix(name, ".md") && name != "index.md" && name != "_map.md" && name != "_archive.md" && strings.HasPrefix(filepath.ToSlash(rel), "data/") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// doctorDate returns the cached last-doctor date, re-reading the log only when
// the file's mtime or size has changed.
func (cc *conformanceCache) cachedDoctorDate(k *kb.KB) string {
	logPath := filepath.Join(k.DataRoot(), "log.md")
	info, err := os.Stat(logPath)
	if err != nil {
		return lastDoctorDate(k)
	}
	mtime := info.ModTime().UnixNano()
	size := info.Size()
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if mtime == cc.logMtime && size == cc.logSize {
		return cc.doctorDate
	}
	cc.mu.Unlock()
	date := lastDoctorDate(k)
	cc.mu.Lock()
	cc.doctorDate = date
	cc.logMtime = mtime
	cc.logSize = size
	return date
}

// LintCalls returns how many times lint.Run was invoked (for tests).
func (cc *conformanceCache) LintCalls() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.lintCalls
}
