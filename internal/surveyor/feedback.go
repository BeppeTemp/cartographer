package surveyor

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

// Report candidates (D1925). The Surveyor notes what a person would report: a
// tool that fails in a way the Surveyor does not expect, a protocol the two
// sides cannot agree on, a result it cannot decode. It never asks anyone: the
// beater carries the notes to Meridian in the next runs report, and a person
// drafts a report from the Atlas.
//
// What a candidate holds is a closed vocabulary: a kind, a tool name, a short
// code, the KB and counts. NEVER the error text, the arguments or the result
// body (an error can quote page content, and a Surveyor writes to KBs that its
// operator may not publish). A new unexpected-answer path calls Note with those
// four words and nothing else.

var (
	candidateToolRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	candidateCodeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
)

// internalErrorCode is the code of a tool_error whose text opened with
// client.InternalErrorPrefix.
const internalErrorCode = "internal_error"

// expectedRefusals are the leading codes of an error result the Surveyor
// expects and handles: a refused write, a missing page, a conflict it retries,
// a read-only grant. They are not candidates.
var expectedRefusals = map[string]bool{
	"refused": true, "forbidden": true, "not_found": true, "stale_write": true, "conflict": true, "oversized": true,
}

// serverToolNames are the names of the tools the server registers
// (mcpserver.RegisterKBTools): a result of one of them opens with its own name
// when it refuses, so a leading token that is one is an operation, never a code.
// The list is exact, not a domain prefix, so a real code that begins with a
// domain word (`old_string_not_found`) stays a candidate.
// cmd/cartographer's test compares it to the server's registry, so a new tool
// fails there until it is added here.
var serverToolNames = []string{
	"artifact_delete", "artifact_list", "artifact_move", "artifact_read", "artifact_write", "asset_delete", "asset_list",
	"asset_read", "asset_write", "atlas_overview", "changes_since", "commit_gate", "concept_archive", "concept_batch",
	"concept_collapse", "concept_delete", "concept_expand", "concept_history", "concept_list", "concept_merge",
	"concept_move", "concept_new", "concept_patch", "concept_read", "concept_transfer", "concept_write",
	"conflict_resolve", "conflicts_list", "contradiction_report", "gate_check", "git_conflict_resolve", "graph_context",
	"graph_neighbors", "graph_path", "index_get", "index_patch", "ingest_setup", "kb_guide", "kb_repair", "kb_review",
	"kb_status", "link_suggest", "lint", "log_append", "log_tail", "map_create", "map_delete", "map_list", "map_update",
	"preset_list", "reindex", "repair_revert", "search", "secret_resolve", "secret_set", "secrets_init", "service_get", "service_list",
	"skill_install", "skill_list", "snapshot", "source_list", "source_register", "supersede", "sync_check", "sync_pull",
	"sync_status", "template_list", "validate", "work_list",
}

// subOperationNames prefix a message without being a registered tool: a check
// that refuses a write (`write_gate: refused ...`), the link families a
// concept_move or a concept_archive reports, and the Surveyor's own ingest tool.
var subOperationNames = []string{"write_gate", "rewrite_links", "inbound_links", "outbound_links", "source_fetch"}

var operationSet = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range OperationNames() {
		m[n] = true
	}
	return m
}()

// OperationNames is every leading token that names an operation: the server's
// tool names and the sub-operations. Sorted, a copy.
func OperationNames() []string {
	out := append(append([]string{}, serverToolNames...), subOperationNames...)
	sort.Strings(out)
	return out
}

// SubOperationNames is the part of OperationNames that is not a server tool.
func SubOperationNames() []string { return append([]string{}, subOperationNames...) }

func namesAnOperation(head string) bool { return operationSet[head] }

type candidateKey struct{ kind, kb, tool, code string }

type candidateEntry struct {
	count       int64
	first, last time.Time
	dirty       bool
	rev         uint64
}

// CandidateLog is the bounded, deduplicating record of report candidates. The
// zero value is not usable: NewCandidateLog.
type CandidateLog struct {
	mu  sync.Mutex
	now func() time.Time
	by  map[candidateKey]*candidateEntry
	rev uint64
}

// NewCandidateLog returns an empty log; now is time.Now when nil.
func NewCandidateLog(now func() time.Time) *CandidateLog {
	if now == nil {
		now = time.Now
	}
	return &CandidateLog{now: now, by: map[candidateKey]*candidateEntry{}}
}

// Note records one sighting. A kind, tool or code outside the vocabulary is
// dropped (a tool name that does not look like one is not recorded; a code
// that does not becomes "unknown"). A nil log records nothing.
func (l *CandidateLog) Note(kind, tool, kbName, code string) {
	if l == nil || !validKind(kind) || (tool != "" && !candidateToolRe.MatchString(tool)) {
		return
	}
	if !candidateCodeRe.MatchString(code) {
		code = "unknown"
	}
	k := candidateKey{kind, kbName, tool, code}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rev++
	e := l.by[k]
	if e == nil {
		e = &candidateEntry{first: now}
		l.by[k] = e
	}
	e.count++
	e.last, e.dirty, e.rev = now, true, l.rev
	for len(l.by) > componentproto.MaxCandidatesPerSurveyor {
		var old candidateKey
		var oldAt time.Time
		for kk, x := range l.by {
			if oldAt.IsZero() || x.last.Before(oldAt) {
				old, oldAt = kk, x.last
			}
		}
		delete(l.by, old)
	}
}

func validKind(kind string) bool {
	for _, k := range componentproto.CandidateKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// runsCandidate is one entry of the runs report's report_candidates. Ages, not
// times: the server dates them on its own clock.
type runsCandidate struct {
	Kind            string `json:"kind"`
	Tool            string `json:"tool,omitempty"`
	Code            string `json:"code"`
	KB              string `json:"kb,omitempty"`
	Count           int64  `json:"count"`
	FirstAgeSeconds int64  `json:"first_age_seconds"`
	LastAgeSeconds  int64  `json:"last_age_seconds"`
}

type sentCandidate struct {
	key candidateKey
	rev uint64
}

// pending returns up to componentproto.MaxCandidatesPerPost candidates the
// server has not seen, newest sighting first; all marks everything unseen
// first (the server asked for a refill). Sent confirms what was posted.
func (l *CandidateLog) pending(all bool) ([]runsCandidate, []sentCandidate) {
	if l == nil {
		return nil, nil
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	var keys []candidateKey
	for k, e := range l.by {
		if all {
			e.dirty = true
		}
		if e.dirty {
			keys = append(keys, k)
		}
	}
	// Newest sighting first; ties by the key so the order is stable.
	sort.Slice(keys, func(i, j int) bool { return newer(l.by, keys[i], keys[j]) })
	if len(keys) > componentproto.MaxCandidatesPerPost {
		keys = keys[:componentproto.MaxCandidatesPerPost]
	}
	out := make([]runsCandidate, 0, len(keys))
	sent := make([]sentCandidate, 0, len(keys))
	for _, k := range keys {
		e := l.by[k]
		out = append(out, runsCandidate{Kind: k.kind, Tool: k.tool, Code: k.code, KB: k.kb, Count: e.count,
			FirstAgeSeconds: ageSeconds(now, e.first), LastAgeSeconds: ageSeconds(now, e.last)})
		sent = append(sent, sentCandidate{k, e.rev})
	}
	return out, sent
}

func newer(by map[candidateKey]*candidateEntry, a, b candidateKey) bool {
	if !by[a].last.Equal(by[b].last) {
		return by[a].last.After(by[b].last)
	}
	return a.kind+a.kb+a.tool+a.code < b.kind+b.kb+b.tool+b.code
}

func ageSeconds(now, t time.Time) int64 { return max(int64(now.Sub(t)/time.Second), 0) }

// confirm marks what an accepted post carried as seen, unless it was noted
// again since.
func (l *CandidateLog) confirm(sent []sentCandidate) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range sent {
		if e := l.by[s.key]; e != nil && e.rev == s.rev {
			e.dirty = false
		}
	}
}

// observed is a ToolCaller that records the answers a person would report. It
// wraps the caller of one KB and changes nothing the task sees.
type observed struct {
	inner ToolCaller
	log   *CandidateLog
	kb    string
}

// ObserveCalls returns tools with every unexpected error result recorded in
// log as a report candidate for kb. The text is read to classify it and then
// forgotten.
func ObserveCalls(tools ToolCaller, log *CandidateLog, kb string) ToolCaller {
	return observed{inner: tools, log: log, kb: kb}
}

func (o observed) ListTools() ([]client.ToolInfo, error) { return o.inner.ListTools() }

func (o observed) CallTool(name string, args any) (client.ToolOutput, error) {
	out, err := o.inner.CallTool(name, args)
	if err == nil && out.IsError {
		if code, ok := unexpectedCode(name, out.Text); ok {
			o.log.Note(componentproto.CandidateToolError, name, o.kb, code)
		}
	}
	return out, err
}

// noteContract records that the result of tool did not decode as documented.
func (o observed) noteContract(tool string) {
	o.log.Note(componentproto.CandidateContractMismatch, tool, o.kb, "undecodable")
}

// NoteContract records that the result of tool could not be decoded as the
// shape the Surveyor reads. One line at each place that decodes a documented
// result. It looks through the wrappers a task's caller goes through (a guard
// with an unwrap method) to the observed caller under them; a wrapper without
// one hides the log, so a new wrapper gets an unwrap method.
func NoteContract(tools ToolCaller, tool string) {
	for i := 0; i < 8 && tools != nil; i++ {
		if o, ok := tools.(interface{ noteContract(string) }); ok {
			o.noteContract(tool)
			return
		}
		u, ok := tools.(interface{ unwrap() ToolCaller })
		if !ok {
			return
		}
		tools = u.unwrap()
	}
}

// unexpectedCode classifies the error result of a call to tool: an internal
// error is always a candidate; otherwise the leading `<code>:` token is, when it
// is shaped like a code, is not a refusal the Surveyor expects and is not the
// exact name of an operation (a tool prefixes its validation and refusal
// messages with its own name, a batch with its sub-operation's: that is a
// refusal, not a code). Any other code is a candidate, including one that merely
// begins with a domain word (`old_string_not_found`). Only the head of the text is
// read: a code the server wraps after a tool prefix (`concept_write "id": invalid
// concept: concept_too_large: ...`) or behind `forbidden:` (`template_unusable`) is
// not seen. A text with no leading code is not a candidate. Only the code leaves.
func unexpectedCode(tool, text string) (string, bool) {
	if strings.HasPrefix(text, client.InternalErrorPrefix) {
		return internalErrorCode, true
	}
	head, _, found := strings.Cut(text, ":")
	if !found || head == tool || namesAnOperation(head) || !candidateCodeRe.MatchString(head) || expectedRefusals[head] || strings.HasPrefix(head, "read_only") {
		return "", false
	}
	return head, true
}
