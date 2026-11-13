package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"github.com/BeppeTemp/cartographer/internal/feedback"
)

// feedbackFixture is the components fixture with a token that reads both KBs.
func feedbackFixture(t *testing.T) *surveyorFixture {
	t.Helper()
	f := kindFixture(t)
	w := []auth.Permission{{KB: "kb-a", Write: true}}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "surv", Principal: "surv", Kind: auth.KindSurveyor, Policy: auth.Policy{Permissions: w}},
		{Token: "reader", Principal: "reader", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a"}}}},
		{Token: "other", Principal: "other", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-b"}}}},
		{Token: "all", Principal: "all", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a"}, {KB: "kb-b"}}}},
	})
	f.h = ts.Middleware(f.multi.Handler())
	f.beatOK(t, "surv", beat(map[string]any{"version": "1.2.3"}))
	return f
}

func cand(kind, tool, code, kb string, count int64, firstAge, lastAge int64) map[string]any {
	return map[string]any{"kind": kind, "tool": tool, "code": code, "kb": kb, "count": count, "first_age_seconds": firstAge, "last_age_seconds": lastAge}
}

func (f *surveyorFixture) postCandidates(t *testing.T, cands ...map[string]any) {
	t.Helper()
	body := runsBodyOf(nil)
	body["report_candidates"] = cands
	if rr := f.postRuns("surv", body); rr.Code != http.StatusOK {
		t.Fatalf("runs %d %s", rr.Code, rr.Body)
	}
}

type candList struct {
	Candidates  []reportCandidate `json:"candidates"`
	Destination string            `json:"destination"`
}

func (f *surveyorFixture) candidateList(t *testing.T, token string) candList {
	t.Helper()
	rr := f.get(token, "/feedback/candidates")
	if rr.Code != http.StatusOK {
		t.Fatalf("list %d %s", rr.Code, rr.Body)
	}
	var l candList
	if err := json.Unmarshal(rr.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

type draftAnswer struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	SearchURL   string `json:"search_url"`
	BodyInURL   bool   `json:"body_in_url"`
	Destination string `json:"destination"`
}

func TestFeedbackCandidates_RecordedDatedByTheServerAndDeduplicated(t *testing.T) {
	t.Parallel()
	f := feedbackFixture(t)
	f.postCandidates(t, cand("tool_error", "concept_read", "internal_error", "kb-a", 2, 3600, 60))
	f.clock.advance(time.Minute)
	f.postCandidates(t, cand("tool_error", "concept_read", "internal_error", "kb-a", 5, 3660, 0)) // the same one, seen again
	l := f.candidateList(t, "reader")
	if len(l.Candidates) != 1 || l.Destination != "github" {
		t.Fatalf("list = %+v", l)
	}
	c := l.Candidates[0]
	now := f.clock.now()
	if c.Count != 5 || c.Kind != "tool_error" || c.KB != "kb-a" || c.Tool != "concept_read" || c.Code != "internal_error" || c.Surveyor != "w-1" {
		t.Fatalf("candidate = %+v", c)
	}
	// Dates come from the server's clock and the ages, never from the sender.
	if !c.LastAt.Equal(now) || !c.FirstAt.Equal(now.Add(-3660*time.Second)) {
		t.Fatalf("first %v last %v, now %v", c.FirstAt, c.LastAt, now)
	}
	if strings.Contains(f.get("reader", "/feedback/candidates").Body.String(), "principal") {
		t.Fatal("a principal id leaves the server")
	}
}

func TestFeedbackCandidates_VocabularyAndBoundsOfAPost(t *testing.T) {
	t.Parallel()
	f := feedbackFixture(t)
	var many []map[string]any
	for i := 0; i < componentproto.MaxCandidatesPerPost+8; i++ {
		many = append(many, cand("tool_error", "concept_read", fmt.Sprintf("code_%02d", i), "kb-a", 1, 10, 10))
	}
	many = append(many,
		cand("made_up", "concept_read", "x", "kb-a", 1, 0, 0), // an unknown kind is dropped
	)
	f.postCandidates(t, many...)
	if got := len(f.candidateList(t, "reader").Candidates); got != componentproto.MaxCandidatesPerPost {
		t.Fatalf("kept %d of an oversized post, want %d", got, componentproto.MaxCandidatesPerPost)
	}
	f = feedbackFixture(t)
	f.postCandidates(t,
		cand("made_up", "concept_read", "x", "kb-a", 1, 0, 0),
		cand("tool_error", "Not A Tool; drop table", "x", "kb-a", 1, 0, 0),
		cand("tool_error", "concept_read", "see the page: Q4 numbers", "kb-a", -4, 99999999999, 5), // a free-text code, absurd counts and ages
		cand("tool_error", "concept_read", "x", "kb-b", 1, 0, 0),                                   // a KB the token cannot write
		cand("tool_error", "concept_read", "x", "nope", 1, 0, 0),
	)
	l := f.candidateList(t, "all")
	if len(l.Candidates) != 1 {
		t.Fatalf("list = %+v, want only the entry that fits the vocabulary", l.Candidates)
	}
	c := l.Candidates[0]
	if c.Code != "unknown" || c.Count != 1 || c.LastAt.After(f.clock.now()) || c.FirstAt.Before(f.clock.now().Add(-31*24*time.Hour)) || c.LastAt.Before(c.FirstAt) {
		t.Fatalf("candidate = %+v: the free text must become unknown and the numbers must be clamped", c)
	}
}

func TestFeedbackCandidates_PerSurveyorBoundEvictsTheOldest(t *testing.T) {
	t.Parallel()
	s := &surveyorStore{}
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	mk := func(sv string, i int) reportCandidate {
		c := reportCandidate{Kind: "tool_error", Surveyor: sv, Code: fmt.Sprintf("c%d", i), Count: 1, LastAt: now.Add(time.Duration(i) * time.Second), FirstAt: now,
			principal: "p", perimeter: "per"}
		c.ID = candidateID("p", "per", c)
		return c
	}
	b := &runBook{}
	var cs []reportCandidate
	for i := 0; i < componentproto.MaxCandidatesPerSurveyor+5; i++ {
		cs = append(cs, mk("w-1", i))
	}
	s.mu.Lock()
	s.mergeCandidatesLocked(b, cs)
	s.mu.Unlock()
	if len(b.cands) != componentproto.MaxCandidatesPerSurveyor {
		t.Fatalf("a Surveyor holds %d, want %d", len(b.cands), componentproto.MaxCandidatesPerSurveyor)
	}
	for _, c := range b.cands {
		if c.Code == "c0" || c.Code == "c4" {
			t.Fatalf("%s survived: the oldest sighting goes first", c.Code)
		}
	}
}

// fillCandidates gives principal `surveyors` books of `per` candidates each, one Surveyor id per book.
func (s *surveyorStore) fillCandidates(principal string, surveyors, per int, base time.Time) {
	if s.runs == nil {
		s.runs = map[string]map[string]*runBook{}
	}
	if s.runs[principal] == nil {
		s.runs[principal] = map[string]*runBook{}
	}
	for i := 0; i < surveyors; i++ {
		bk := s.runs[principal][fmt.Sprintf("per-%d", i)]
		if bk == nil {
			bk = &runBook{}
			s.runs[principal][fmt.Sprintf("per-%d", i)] = bk
		}
		sv := fmt.Sprintf("%s-sv-%d", principal, i)
		var batch []reportCandidate
		for j := 0; j < per; j++ {
			c := reportCandidate{Kind: "tool_error", Surveyor: sv, Code: fmt.Sprintf("c%d", j), Count: 1,
				LastAt: base.Add(time.Duration(i*1000+j) * time.Second), FirstAt: base, principal: principal, perimeter: fmt.Sprintf("per-%d", i)}
			c.ID = candidateID(principal, c.perimeter, c)
			batch = append(batch, c)
		}
		s.mu.Lock()
		s.mergeCandidatesLocked(bk, batch)
		s.mu.Unlock()
	}
}

func (s *surveyorStore) heldBy(principal string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.principalCandidatesLocked(principal)
}

// One principal cannot push another's candidates out: a principal posting under
// many Surveyor ids is bounded to its own share, and the total bound takes from
// the largest holder.
func TestFeedbackCandidates_APrincipalCannotEvictAnothers(t *testing.T) {
	t.Parallel()
	s := &surveyorStore{}
	old := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	s.fillCandidates("good", 1, 10, old) // the oldest sightings of all
	s.fillCandidates("evil", 20, componentproto.MaxCandidatesPerSurveyor, old.Add(time.Hour))
	if got := s.heldBy("evil"); got != componentproto.MaxCandidatesPerPrincipal {
		t.Fatalf("evil holds %d, want its cap %d", got, componentproto.MaxCandidatesPerPrincipal)
	}
	if got := s.heldBy("good"); got != 10 {
		t.Fatalf("good holds %d after another principal's flood, want 10", got)
	}
	// Fill the total with other principals: the newcomer's 10 survive, taken from the largest holders.
	for _, p := range []string{"p1", "p2", "p3", "p4"} {
		s.fillCandidates(p, 2, componentproto.MaxCandidatesPerSurveyor, old.Add(2*time.Hour))
	}
	s.mu.Lock()
	n := s.candidatesLocked()
	s.mu.Unlock()
	if n > componentproto.MaxCandidatesTotal || s.heldBy("good") != 10 {
		t.Fatalf("total %d (max %d), good holds %d, want 10", n, componentproto.MaxCandidatesTotal, s.heldBy("good"))
	}
}

func TestFeedbackCandidates_GoWithTheDeclarationAndNeverPersist(t *testing.T) {
	t.Parallel()
	f := feedbackFixture(t)
	f.postCandidates(t, cand("tool_error", "concept_read", "internal_error", "kb-a", 1, 0, 0))
	f.clock.advance(5 * time.Minute) // the declaration goes stale: the book and its candidates with it
	if got := f.candidateList(t, "reader").Candidates; len(got) != 0 {
		t.Fatalf("candidates outlive the declaration: %+v", got)
	}
	// A new server has none: memory only.
	if got := feedbackFixture(t).candidateList(t, "reader").Candidates; len(got) != 0 {
		t.Fatalf("a fresh server holds candidates: %+v", got)
	}
}

func TestFeedbackCandidates_VisibilityFollowsTheKBsTheCallerSees(t *testing.T) {
	t.Parallel()
	f := feedbackFixture(t)
	f.postCandidates(t,
		cand("tool_error", "concept_read", "internal_error", "kb-a", 1, 0, 0),
		cand("protocol_mismatch", "", "protocol_refused", "", 1, 0, 0), // names no KB
	)
	for _, tc := range []struct {
		token string
		want  int
	}{{"all", 2}, {"reader", 0 + 1}, {"other", 0}} {
		l := f.candidateList(t, tc.token)
		if len(l.Candidates) != tc.want {
			t.Errorf("%s sees %d candidates, want %d: %+v", tc.token, len(l.Candidates), tc.want, l.Candidates)
		}
	}
	// A candidate the caller cannot see answers exactly as an unknown one does.
	id := f.candidateList(t, "all").Candidates[0].ID
	for _, tc := range []struct{ token, id string }{{"other", id}, {"all", "0000000000000000"}} {
		rr := f.get(tc.token, "/feedback/candidates/draft?id="+tc.id)
		if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), `"not_found"`) {
			t.Errorf("%s draft of %s: %d %s", tc.token, tc.id, rr.Code, rr.Body)
		}
	}
	for _, p := range []string{"/feedback/candidates", "/feedback/candidates/draft?id=x"} {
		req := httptest.NewRequest(http.MethodPost, UIAPIPrefix+p, nil)
		req.Header.Set("Authorization", "Bearer all")
		rr := httptest.NewRecorder()
		f.h.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d: the Atlas stays read-only", p, rr.Code)
		}
	}
}

func TestFeedbackDraft_CarriesTheVersionFactsAndNoFixtureName(t *testing.T) {
	t.Parallel()
	f := feedbackFixture(t)
	f.postCandidates(t, cand("tool_error", "concept_read", "internal_error", "kb-a", 3, 600, 30))
	id := f.candidateList(t, "reader").Candidates[0].ID
	rr := f.get("reader", "/feedback/candidates/draft?id="+id)
	if rr.Code != http.StatusOK {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	var d draftAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Kind != "bug" || d.Destination != "github" || d.Title != "concept_read failed with internal_error" || !d.BodyInURL {
		t.Fatalf("draft = %+v", d)
	}
	for _, want := range []string{"server version: test", "Surveyor version: 1.2.3 (component protocol 1)", "transport: HTTP", "**Minimal reproduction:**",
		"Not reproduced", "Seen 3 time(s)", "`concept_read`", "`internal_error`"} {
		if !strings.Contains(d.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, d.Body)
		}
	}
	for _, name := range []string{"kb-a", "kb-b", "team-a", "w-1", "surv", "example.com/"} { // KBs, perimeter, replica id, principal
		if strings.Contains(d.Body+d.Title+d.URL, name) {
			t.Errorf("the draft names %q", name)
		}
	}
	if !strings.HasPrefix(d.URL, "https://github.com/"+feedback.Repo+"/issues/new?template=bug.md&title=") || !strings.HasPrefix(d.SearchURL, "https://github.com/"+feedback.Repo+"/issues?q=") {
		t.Fatalf("urls = %q %q", d.URL, d.SearchURL)
	}
}

func TestFeedbackDraft_TheRedactionPassCatchesWhatTheTextCouldCarry(t *testing.T) {
	t.Parallel()
	// A code and a version are validated shapes, yet either can spell a name: the
	// net replaces a KB name, and the request host, wherever they land.
	c := reportCandidate{Kind: "tool_error", Tool: "concept_read", Code: "workkb", Count: 1, FirstAt: time.Unix(1700000000, 0).UTC(), LastAt: time.Unix(1700000000, 0).UTC()}
	f := draftFacts{serverVersion: "dev", surveyorVersion: "node1.corp.internal", protocol: 1}
	rep := redactedCandidateReport(c, f, feedback.Known{KBNames: []string{"workkb"}, ServerHosts: []string{"node1.corp.internal:8443"}})
	if strings.Contains(rep.Title+rep.Body, "workkb") || strings.Contains(rep.Body, "corp.internal") {
		t.Fatalf("a name survived:\n%s\n%s", rep.Title, rep.Body)
	}
	if !strings.Contains(rep.Title, "kb-a") || !strings.Contains(rep.Body, "server version: dev") {
		t.Fatalf("the facts must survive: %s\n%s", rep.Title, rep.Body)
	}
}

func TestFeedbackDraft_EveryKindHasATitleAndTheTemplateHeadings(t *testing.T) {
	t.Parallel()
	for kind, want := range map[string]string{
		componentproto.CandidateToolError:        "concept_read failed with c",
		componentproto.CandidateContractMismatch: "Surveyor and server disagree on concept_read",
		componentproto.CandidateProtocolMismatch: "Surveyor and server disagree on the component protocol",
	} {
		rep := candidateReport(reportCandidate{Kind: kind, Tool: "concept_read", Code: "c", Count: 1}, draftFacts{})
		if rep.Title != want || rep.Kind != "bug" {
			t.Errorf("%s: title %q", kind, rep.Title)
		}
		for _, h := range []string{"**Version**", "**What happened, and what you expected:**", "**Minimal reproduction:**"} {
			if !strings.Contains(rep.Body, h) {
				t.Errorf("%s: no heading %s", kind, h)
			}
		}
	}
}

func TestFeedbackOff_DropsTheFieldAndAnswersBothRoutes404(t *testing.T) {
	t.Parallel()
	f := feedbackFixture(t)
	off, err := feedback.ParseDestination("off")
	if err != nil {
		t.Fatal(err)
	}
	f.multi.SetFeedback(off)
	f.postCandidates(t, cand("tool_error", "concept_read", "internal_error", "kb-a", 1, 0, 0)) // accepted, 200
	for _, p := range []string{"/feedback/candidates", "/feedback/candidates/draft?id=x"} {
		rr := f.get("all", p)
		if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), uiCodeFeedbackOff) {
			t.Errorf("%s with feedback off: %d %s", p, rr.Code, rr.Body)
		}
	}
	// Nothing was kept: switching it on again shows an empty list.
	f.multi.SetFeedback(feedback.Destination{})
	if got := f.candidateList(t, "all").Candidates; len(got) != 0 {
		t.Fatalf("candidates were kept while off: %+v", got)
	}
}

func TestFeedbackDraft_CustomDestinationBuildsTheTemplateURLAndNoSearchLink(t *testing.T) {
	t.Parallel()
	f := feedbackFixture(t)
	d, err := feedback.ParseDestination("https://tracker.example.com/new?kind={kind}&t={title}&b={body}")
	if err != nil {
		t.Fatal(err)
	}
	f.multi.SetFeedback(d)
	f.postCandidates(t, cand("contract_mismatch", "concept_list", "undecodable", "kb-a", 1, 0, 0))
	id := f.candidateList(t, "all").Candidates[0].ID
	var out draftAnswer
	rr := f.get("all", "/feedback/candidates/draft?id="+id)
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || rr.Code != 200 {
		t.Fatalf("%d %s %v", rr.Code, rr.Body, err)
	}
	if out.Destination != "custom" || !strings.HasPrefix(out.URL, "https://tracker.example.com/new?kind=bug&t=") || out.SearchURL != "" {
		t.Fatalf("draft = %+v", out)
	}
}

// The draft route is part of the Atlas and not of the public API: it is not
// served on /api/v1 (TestAPIContract_RoutesAndDocumentAgree pins the document).
func TestFeedbackRoutes_AreInternalTier(t *testing.T) {
	t.Parallel()
	seen := 0
	for _, rt := range apiRoutes {
		if strings.HasPrefix(rt.Pattern, "/feedback/") {
			seen++
			if rt.Tier != tierInternal {
				t.Errorf("%s is %s, want internal", rt.Pattern, rt.Tier)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("%d feedback routes, want 2", seen)
	}
}

// Five principals, one flooding under many Surveyor ids: the flooder pays for its
// own excess and the others keep what they held (below their fair share of the 512).
func TestFeedbackCandidates_FairShareAgainstAFlood(t *testing.T) {
	t.Parallel()
	s := &surveyorStore{}
	base := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	others := []string{"a", "b", "c", "d"}
	for _, p := range others {
		s.fillCandidates(p, 2, 50, base) // 100 each, the oldest sightings
	}
	s.fillCandidates("flood", 10, componentproto.MaxCandidatesPerSurveyor, base.Add(time.Hour))
	for _, p := range others {
		if got := s.heldBy(p); got != 100 {
			t.Fatalf("%s holds %d after a flood, want its 100 (fair share is %d)", p, got, componentproto.MaxCandidatesTotal/5)
		}
	}
	s.mu.Lock()
	n := s.candidatesLocked()
	s.mu.Unlock()
	if n != componentproto.MaxCandidatesTotal || s.heldBy("flood") != componentproto.MaxCandidatesTotal-400 {
		t.Fatalf("total %d, flood holds %d, want %d and %d", n, s.heldBy("flood"), componentproto.MaxCandidatesTotal, componentproto.MaxCandidatesTotal-400)
	}
}

// Four principals tied at the cap and a newcomer below them: the newcomer is never
// the victim, and the tied holder paying is the one with the oldest sighting, the
// smaller id when those are equal.
func TestFeedbackCandidates_TieOfLargestHoldersIsDeterministic(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		olders string // the principal whose sightings are older, "" for none
		victim string
	}{{"oldest sighting", "p3", "p3"}, {"equal sightings: smaller id", "", "p1"}} {
		s := &surveyorStore{}
		for _, p := range []string{"p4", "p2", "p3", "p1"} {
			at := base.Add(time.Hour)
			if p == tc.olders {
				at = base
			}
			s.fillCandidates(p, 2, componentproto.MaxCandidatesPerSurveyor, at)
		}
		s.fillCandidates("q", 1, 1, base.Add(2*time.Hour))
		for _, p := range []string{"p1", "p2", "p3", "p4"} {
			want := componentproto.MaxCandidatesPerPrincipal
			if p == tc.victim {
				want--
			}
			if got := s.heldBy(p); got != want {
				t.Errorf("%s: %s holds %d, want %d", tc.name, p, got, want)
			}
		}
		if got := s.heldBy("q"); got != 1 {
			t.Errorf("%s: the newcomer holds %d, want 1", tc.name, got)
		}
	}
}

// The poster is among the largest holders: it pays first, whatever the ids and the ages.
func TestFeedbackCandidates_APosterAmongTheLargestPaysFirst(t *testing.T) {
	t.Parallel()
	s := &surveyorStore{}
	base := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	s.fillCandidates("a", 2, componentproto.MaxCandidatesPerSurveyor, base) // the oldest, the smallest id
	s.fillCandidates("z", 2, componentproto.MaxCandidatesPerSurveyor, base.Add(time.Hour))
	s.fillCandidates("m", 1, 10, base.Add(time.Hour))
	s.mu.Lock()
	defer s.mu.Unlock()
	if got := s.evictionVictimLocked("z"); got != "z" {
		t.Errorf("a poster tied for the most pays first, victim %q", got)
	}
	if got := s.evictionVictimLocked("m"); got != "a" {
		t.Errorf("a smaller poster is not the victim: want the oldest of the tied, got %q", got)
	}
}
