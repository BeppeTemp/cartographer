package mcpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"github.com/BeppeTemp/cartographer/internal/feedback"
)

// Report candidates (D1925): what a Surveyor noticed that a person would
// report. They ride the runs report (`report_candidates`), live in the runBook
// of the same (principal, perimeter) and die with it: memory only, bounded,
// never written to a KB or to disk.
//
// A candidate is a closed vocabulary, never a text: a kind, a tool name, a
// short code and counts. A tool's error can quote page content, so none of it
// is accepted. Ages are the server's: the Surveyor sends how long ago, the
// server dates it, so a skewed clock cannot make a candidate look recent.
//
// Two GET routes serve the Atlas, which stays read-only: the list, and a
// draft built by pure computation over one candidate (no state changes).

const (
	uiCodeFeedbackOff  = "feedback_off"
	maxCandidateAge    = 30 * 24 * 3600 // seconds
	maxCandidateCount  = int64(1) << 31
	candidateIDLen     = 16
	maxDraftIDQueryLen = 64
)

var (
	candidateToolRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	candidateCodeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	versionTextRe   = regexp.MustCompile(`^[0-9A-Za-z.+_-]{1,32}$`)
)

// reportCandidate is one deduplicated candidate as the server holds it.
type reportCandidate struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Surveyor string    `json:"surveyor"`
	KB       string    `json:"kb,omitempty"`
	Tool     string    `json:"tool,omitempty"`
	Code     string    `json:"code"`
	Count    int64     `json:"count"`
	FirstAt  time.Time `json:"first_at"`
	LastAt   time.Time `json:"last_at"`

	principal, perimeter string
}

// runsCandidate is one entry of a runs report's `report_candidates`.
type runsCandidate struct {
	Kind            string `json:"kind"`
	Tool            string `json:"tool"`
	Code            string `json:"code"`
	KB              string `json:"kb"`
	Count           int64  `json:"count"`
	FirstAgeSeconds int64  `json:"first_age_seconds"`
	LastAgeSeconds  int64  `json:"last_age_seconds"`
}

func validCandidateKind(k string) bool {
	for _, v := range componentproto.CandidateKinds() {
		if v == k {
			return true
		}
	}
	return false
}

func candidateID(principal, perimeter string, c reportCandidate) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{principal, perimeter, c.Surveyor, c.Kind, c.KB, c.Tool, c.Code}, "\x00")))
	return hex.EncodeToString(sum[:])[:candidateIDLen]
}

// acceptCandidates validates what a runs report carries and dates it. An
// entry that does not fit the vocabulary is dropped, not refused: the rest of
// the report still counts. The cap is the first componentproto.MaxCandidatesPerPost
// entries. With feedback off nothing is kept.
func (m *MultiKBServer) acceptCandidates(in []runsCandidate, principal, perimeter, surveyor string, writable func(string) bool, now time.Time) []reportCandidate {
	if m.feedback.Mode() == feedback.ModeOff {
		return nil
	}
	if len(in) > componentproto.MaxCandidatesPerPost {
		in = in[:componentproto.MaxCandidatesPerPost]
	}
	out := make([]reportCandidate, 0, len(in))
	for _, x := range in {
		if !validCandidateKind(x.Kind) || (x.Tool != "" && !candidateToolRe.MatchString(x.Tool)) || (x.KB != "" && !writable(x.KB)) {
			continue
		}
		code := x.Code
		if !candidateCodeRe.MatchString(code) {
			code = "unknown"
		}
		first := min(max(x.FirstAgeSeconds, 0), maxCandidateAge)
		last := min(max(x.LastAgeSeconds, 0), first)
		c := reportCandidate{
			Kind: x.Kind, Surveyor: surveyor, KB: x.KB, Tool: x.Tool, Code: code,
			Count:   min(max(x.Count, 1), maxCandidateCount),
			FirstAt: now.Add(-time.Duration(first) * time.Second), LastAt: now.Add(-time.Duration(last) * time.Second),
			principal: principal, perimeter: perimeter,
		}
		c.ID = candidateID(principal, perimeter, c)
		out = append(out, c)
	}
	return out
}

// mergeCandidatesLocked upserts into the book (the Surveyor's count is cumulative,
// so it replaces ours; the earliest first sighting and the latest last one
// win) and applies the bounds: per Surveyor (64), per principal (128), then in
// all (512, from a largest holder, see below); the oldest last sighting goes
// first. The caller holds the store's lock.
func (s *surveyorStore) mergeCandidatesLocked(b *runBook, cands []reportCandidate) {
	if len(cands) == 0 {
		return
	}
	for _, c := range cands {
		held := false
		for i := range b.cands {
			if b.cands[i].ID != c.ID {
				continue
			}
			held = true
			h := &b.cands[i]
			h.Count = c.Count
			if c.FirstAt.Before(h.FirstAt) {
				h.FirstAt = c.FirstAt
			}
			if c.LastAt.After(h.LastAt) {
				h.LastAt = c.LastAt
			}
		}
		if !held {
			b.cands = append(b.cands, c)
		}
		b.evictSurveyorCandidates(c.Surveyor)
	}
	// The per-principal bound takes the holder's own oldest: a principal that
	// posts many candidates under many Surveyor ids pushes out its own.
	poster := cands[0].principal
	for s.principalCandidatesLocked(poster) > componentproto.MaxCandidatesPerPrincipal {
		s.evictOldestOfLocked(poster)
	}
	// The total bound only ever takes from a largest holder, so it is a fair
	// share: each principal keeps at least min(what it holds, its fair share of the
	// total), about MaxCandidatesTotal/N for N principals, and a principal posting
	// under many Surveyor ids cannot push another below that share. On a tie of
	// largest holders the poster pays if it is one of them, else the tied holder
	// with the oldest last sighting does, so the choice does not depend on an id
	// order or on map iteration.
	for s.candidatesLocked() > componentproto.MaxCandidatesTotal {
		s.evictOldestOfLocked(s.evictionVictimLocked(poster))
	}
}

func (s *surveyorStore) principalCandidatesLocked(principal string) (n int) {
	for _, b := range s.runs[principal] {
		n += len(b.cands)
	}
	return n
}

// evictionVictimLocked is the principal that pays for the total bound: a
// holder of the most candidates; among tied holders the poster if it is one,
// else the one whose oldest last sighting is the oldest (the smaller id when
// even those are equal).
func (s *surveyorStore) evictionVictimLocked(poster string) string {
	most := 0
	for p := range s.runs {
		most = max(most, s.principalCandidatesLocked(p))
	}
	if s.principalCandidatesLocked(poster) == most {
		return poster
	}
	victim, victimAt := "", time.Time{}
	for p := range s.runs {
		if s.principalCandidatesLocked(p) != most {
			continue
		}
		bk, i := s.oldestCandidateLocked(p)
		if bk == nil {
			continue
		}
		if at := bk.cands[i].LastAt; victim == "" || at.Before(victimAt) || (at.Equal(victimAt) && p < victim) {
			victim, victimAt = p, at
		}
	}
	return victim
}

// oldestCandidateLocked finds the principal's candidate with the oldest last sighting.
func (s *surveyorStore) oldestCandidateLocked(principal string) (*runBook, int) {
	var ob *runBook
	oi := -1
	for _, bk := range s.runs[principal] {
		for i := range bk.cands {
			if oi < 0 || bk.cands[i].LastAt.Before(ob.cands[oi].LastAt) {
				ob, oi = bk, i
			}
		}
	}
	return ob, oi
}

// evictOldestOfLocked drops the principal's candidate with the oldest last sighting.
func (s *surveyorStore) evictOldestOfLocked(principal string) {
	if ob, oi := s.oldestCandidateLocked(principal); ob != nil {
		ob.cands = append(ob.cands[:oi], ob.cands[oi+1:]...)
	}
}

// evictSurveyorCandidates keeps one Surveyor to its bound, oldest last sighting out.
func (b *runBook) evictSurveyorCandidates(surveyor string) {
	for {
		n, oldest := 0, -1
		for i := range b.cands {
			if b.cands[i].Surveyor != surveyor {
				continue
			}
			n++
			if oldest < 0 || b.cands[i].LastAt.Before(b.cands[oldest].LastAt) {
				oldest = i
			}
		}
		if n <= componentproto.MaxCandidatesPerSurveyor {
			return
		}
		b.cands = append(b.cands[:oldest], b.cands[oldest+1:]...)
	}
}

func (s *surveyorStore) candidatesLocked() (n int) {
	for _, m := range s.runs {
		for _, b := range m {
			n += len(b.cands)
		}
	}
	return n
}

// allCandidates returns every candidate held by a live book, newest last
// sighting first.
func (s *surveyorStore) allCandidates() []reportCandidate {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	out := []reportCandidate{}
	for _, m := range s.runs {
		for _, b := range m {
			out = append(out, b.cands...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastAt.Equal(out[j].LastAt) {
			return out[i].LastAt.After(out[j].LastAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// draftFacts are the declared facts a draft cites: the server's version and
// the Surveyor's, as its live declaration states them.
type draftFacts struct {
	serverVersion, surveyorVersion string
	protocol                       int
}

func (s *surveyorStore) factsFor(c reportCandidate) draftFacts {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := draftFacts{}
	for _, d := range s.by[c.principal] {
		if d.Perimeter == c.perimeter && d.Surveyor == c.Surveyor {
			f.protocol = d.Protocol
			if versionTextRe.MatchString(d.Version) {
				f.surveyorVersion = d.Version
			}
		}
	}
	return f
}

// candidateVisible is the visibility rule of the components view: a candidate
// of a KB is for whoever sees that KB whole; one that names no KB (a protocol
// disagreement) is for whoever sees every mounted KB whole, so no scoped token
// learns of a Surveyor that serves KBs it cannot see.
func (m *MultiKBServer) candidateVisible(ctx requestContext, c reportCandidate) bool {
	if c.KB != "" {
		srv, ok := m.servers[c.KB]
		return ok && srv.kbRef != nil && WholeVisible(ctx, srv.kbRef, false)
	}
	for _, srv := range m.servers {
		if srv.kbRef != nil && !WholeVisible(ctx, srv.kbRef, false) {
			return false
		}
	}
	return true
}

func writeFeedbackOff(w http.ResponseWriter) {
	writeUIError(w, http.StatusNotFound, uiCodeFeedbackOff, "feedback reports are switched off by the operator of this server", "")
}

// GET /feedback/candidates: the candidates the caller may see, newest first.
func (m *MultiKBServer) uiFeedbackCandidates(w http.ResponseWriter, r *http.Request) {
	if m.feedback.Mode() == feedback.ModeOff {
		writeFeedbackOff(w)
		return
	}
	out := []reportCandidate{}
	for _, c := range m.surveyors.allCandidates() {
		if m.candidateVisible(r.Context(), c) {
			out = append(out, c)
		}
	}
	writeUIJSON(w, http.StatusOK, map[string]any{"candidates": out, "destination": string(m.feedback.Mode())})
}

// GET /feedback/candidates/draft?id=: the redacted draft of one candidate and
// the URL that files it. Pure computation: nothing is stored, sent or changed.
// A candidate the caller cannot see answers the 404 of an unknown one.
func (m *MultiKBServer) uiFeedbackDraft(w http.ResponseWriter, r *http.Request) {
	if m.feedback.Mode() == feedback.ModeOff {
		writeFeedbackOff(w)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" || len(id) > maxDraftIDQueryLen {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "id is required", "id")
		return
	}
	var c reportCandidate
	found := false
	for _, x := range m.surveyors.allCandidates() {
		if x.ID == id && m.candidateVisible(r.Context(), x) {
			c, found = x, true
			break
		}
	}
	if !found {
		writeUINotFound(w)
		return
	}
	facts := m.surveyors.factsFor(c)
	facts.serverVersion = m.version
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	var hosts []string
	if r.Host != "" {
		hosts = append(hosts, r.Host)
	}
	if u, err := url.Parse(resourceBaseURL(r)); err == nil && u.Host != "" {
		hosts = append(hosts, u.Host)
	}
	rep := redactedCandidateReport(c, facts, feedback.Known{KBNames: names, ServerHosts: hosts})
	u, bodyInURL := m.feedback.SubmitURL(rep)
	writeUIJSON(w, http.StatusOK, map[string]any{
		"title": rep.Title, "body": rep.Body, "kind": rep.Kind, "url": u,
		"search_url": m.feedback.SearchURL(rep.Title), "body_in_url": bodyInURL,
		"destination": string(m.feedback.Mode()),
	})
}

// redactedCandidateReport builds the bug draft of a candidate and passes it
// through feedback.Redact as a net: the text names no KB by construction (a
// candidate's KB is "a KB"), and the pass catches whatever a code or a version
// might still carry.
func redactedCandidateReport(c reportCandidate, f draftFacts, k feedback.Known) feedback.Report {
	rep := candidateReport(c, f)
	rep.Title, _ = feedback.Redact(rep.Title, k)
	rep.Body, _ = feedback.Redact(rep.Body, k)
	return rep
}

// candidateReport is the draft of one candidate, in the headings of the bug
// template. Pure over the candidate and the declared facts. The reproduction is
// "not reproduced": a draft never invents steps.
func candidateReport(c reportCandidate, f draftFacts) feedback.Report {
	tool := "a tool"
	if c.Tool != "" {
		tool = "`" + c.Tool + "`"
	}
	var title, what string
	switch c.Kind {
	case componentproto.CandidateToolError:
		title = fmt.Sprintf("%s failed with %s", orWord(c.Tool, "a tool"), c.Code)
		what = fmt.Sprintf("A Surveyor called %s and the server answered with an error coded `%s`, which the Surveyor does not treat as an expected refusal. "+
			"Expected: the call to succeed, or to be refused with a code the Surveyor handles.", tool, c.Code)
	case componentproto.CandidateContractMismatch:
		title = "Surveyor and server disagree on " + orWord(c.Tool, "a tool")
		what = fmt.Sprintf("A Surveyor read the result of %s and could not decode it as the shape it reads. Expected: the documented shape.", tool)
	default:
		title = "Surveyor and server disagree on the component protocol"
		what = fmt.Sprintf("A Surveyor and the server could not agree on a component protocol (`%s`). Expected: a protocol within N-1..N+1 of each other.", c.Code)
	}
	var b strings.Builder
	b.WriteString("**Version** (`cartographer version`) and **transport** (stdio / HTTP):\n\n")
	b.WriteString("- server version: " + orWord(f.serverVersion, "unknown") + "\n")
	b.WriteString("- Surveyor version: " + orWord(f.surveyorVersion, "unknown"))
	if f.protocol > 0 {
		b.WriteString(fmt.Sprintf(" (component protocol %d)", f.protocol))
	}
	b.WriteString("\n- transport: HTTP\n\n**What happened, and what you expected:**\n\n")
	b.WriteString(what + "\n")
	b.WriteString(fmt.Sprintf("Seen %d time(s) between %s and %s (UTC).\n\n", c.Count, c.FirstAt.UTC().Format(time.RFC3339), c.LastAt.UTC().Format(time.RFC3339)))
	b.WriteString("**Minimal reproduction:**\n\nNot reproduced: this draft was written from a Surveyor's record of what it saw, not from a session.\n")
	return feedback.Report{Kind: "bug", Title: title, Body: b.String()}
}

func orWord(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
