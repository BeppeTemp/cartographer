package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// pushFlushTimeout bounds how long a sync-sensitive tool handler (D76/WP4:
// sync_check/sync_apply/sync_pull, git_conflict_resolve) waits for a pending
// async push to complete via k.FlushPush before proceeding anyway — a flush
// failure/timeout is logged and non-fatal, consistent with push errors
// elsewhere in this file.
const pushFlushTimeout = 5 * time.Second

// gitWrap wraps a write Tool so that a successful execution is followed by a git
// commit via k.CommitOp. The entire call (original handler + commit) runs under
// k.WithGitLock to serialise concurrent write operations on the same KB.
//
// Semantics:
//   - If the original handler returns a Go error or an application error
//     (res.IsError), no commit is attempted.
//   - If the commit itself fails, the error is logged to stderr but is NOT
//     propagated to the MCP client: a commit failure never turns a successful
//     operation into an error.
//   - If k.AutoCommit is false (the zero-value default), CommitOp is a no-op,
//     so existing tests that do not set AutoCommit are unaffected.
//
// Step 3 — agentic conflict handling:
//   - If SyncIn returns a *gitx.RebaseConflictError, each conflicting file is
//     registered in the KB conflict registry and marked as degraded; the write
//     is aborted with an informative errorResult.
//   - If SyncOut returns a *gitx.RebaseConflictError, conflicts are registered
//     and logged to stderr; the write result is not changed (already reported
//     as success).
func gitWrap(k *kb.KB, t Tool) Tool {
	orig := t
	t.InputSchema = withReasonProperty(t.Name, t.InputSchema)
	gated := acceptsFindings(t.Name) && writeGateActive(k)
	if gated {
		t.InputSchema = withAcceptFindingsProperty(t.Name, t.InputSchema)
	}
	t.GitWrapped = true
	t.Handler = func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		var res ToolResult
		var handlerErr error
		var accepts []acceptedFinding
		if gated {
			var perr error
			if accepts, perr = parseAcceptFindings(args); perr != nil {
				return errorResult(perr.Error()), nil
			}
		}
		var syncInDur, handlerDur, commitDur, pushDur time.Duration
		var pushAsync bool
		start := time.Now()
		k.WithGitLock(func() error { //nolint:errcheck // inner func always returns nil
			// Step 2: fetch + pull --rebase before every write operation.
			// If the KB has no remote or GitSync is false, SyncIn is a no-op.
			syncInStart := time.Now()
			_, syncErr := k.SyncIn()
			syncInDur = time.Since(syncInStart)
			if syncErr != nil {
				var rce *gitx.RebaseConflictError
				if errors.As(syncErr, &rce) {
					// Step 3: rebase conflict — register conflicts, mark concepts degraded.
					res = errorResult(conflictMessage(handleConflictError(k, rce)))
				} else {
					res = errorResult("git sync (fetch/pull) failed: " + syncErr.Error())
				}
				return nil
			}
			// SyncIn may have changed maps, journals, or concept frontmatter.
			// Recheck only after that refreshed state is installed, immediately
			// before the handler can mutate content.
			if authErr := reauthorizeUnderLock(ctx, k, orig.Name, args); authErr != nil {
				res = errorResult(authErr.Error())
				return nil
			}
			commitExternalChanges(k, orig.Name)
			gateOn := gated && writeGateActive(k) && gatePrecheck(k)
			handlerStart := time.Now()
			res, handlerErr = orig.Handler(ctx, args)
			handlerDur = time.Since(handlerStart)
			if handlerErr == nil && !res.IsError {
				// Generated map indexes (D301) follow the write in the same
				// commit. A failure is logged, never fatal: lint reports a
				// stale block and the next write retries.
				if _, regenErr := k.RegenerateIndexes(); regenErr != nil {
					fmt.Fprintf(os.Stderr, "cartographer: generated index (%s): %v\n", orig.Name, regenErr)
				}
				// Write gate (D350): after the regeneration above and the
				// handler's own repair-on-write (D349), before anything is
				// committed. A refusal has already rolled the tree back.
				var trailers []string
				if gateOn {
					out := judgeWrite(k, orig.Name, accepts)
					if out.Refusal != nil {
						res = *out.Refusal
						return nil
					}
					trailers = out.Trailers
				}
				msg := commitMessage(orig.Name, args)
				if res.CommitSubject != "" {
					msg = res.CommitSubject
				}
				if reason := commitReason(args); reason != "" {
					msg += "\n\n" + commitReasonKey + ": " + reason
				}
				for i, tr := range trailers {
					if i == 0 && commitReason(args) == "" {
						msg += "\n"
					}
					msg += "\n" + tr
				}
				commitStart := time.Now()
				p := auth.PrincipalFromContext(ctx)
				sha, commitErr := k.CommitOpAs(msg, p.AuthorName, p.AuthorEmail)
				commitDur = time.Since(commitStart)
				if commitErr == nil {
					res.CommitSHA = sha
				}
				if commitErr != nil {
					fmt.Fprintf(os.Stderr, "cartographer: git commit failed (%s): %v\n", orig.Name, commitErr)
					k.SetGitStatus("failed", fmt.Errorf("commit: %w", commitErr))
					appendSyncWarning(&res, k)
					return nil
				}
				// Step 2 / D76-WP4: push after the commit. If SyncOutDebounce
				// is set, take the push off the critical path entirely by
				// scheduling it on the per-KB async worker instead of
				// calling SyncOut inline; the worker runs under the same
				// WithGitLock (see pushworker.go) and surfaces conflicts via
				// k.OnPushConflict (wired in RegisterKBTools). Push failure —
				// sync or async — is non-fatal; Step 3 surfaces rebase
				// conflicts as degraded concepts.
				if k.SyncOutDebounce > 0 {
					k.SchedulePush()
					k.MarkPushPending()
					pushAsync = true
					appendSyncWarning(&res, k)
				} else {
					pushStart := time.Now()
					syncErr := k.SyncOut()
					pushDur = time.Since(pushStart)
					if syncErr != nil {
						var rce *gitx.RebaseConflictError
						if errors.As(syncErr, &rce) {
							n, _ := handleConflictError(k, rce)
							fmt.Fprintf(os.Stderr,
								"cartographer: git conflict during push (%s): registered %d concept(s) as degraded\n",
								orig.Name, n)
						} else {
							fmt.Fprintf(os.Stderr, "cartographer: git push failed (%s): %v\n", orig.Name, syncErr)
						}
					}
					appendSyncWarning(&res, k)
				}
			}
			return nil
		})
		total := time.Since(start)
		fmt.Fprintln(os.Stderr, formatTiming(commitMessage(orig.Name, args), syncInDur, handlerDur, commitDur, pushDur, pushAsync, total))
		return res, handlerErr
	}
	return t
}

// externalChangesSubject is the subject of the commit that records changes the
// working tree held before a write began (D357).
const externalChangesSubject = "external changes"

// commitExternalChanges commits, as their own commit, whatever the working tree
// already holds when a write starts (D357). The write's commit stages
// everything (git add -A), so without this a call that changed nothing, or one
// file, would commit an editor's unrelated work under its own name, and history
// would credit a tool (or the unattended doctor) with changes it did not make.
// The commit is authored by the KB's default git identity, never the caller's,
// and says why in the Reason trailer. It runs under the git lock before the
// handler, after the pull; a KB with open conflicts is left alone (the write
// is refused as before, and a conflict marker must not be committed). A
// failure is logged and never fails the write. A clean tree, an AutoCommit-off
// KB or a non-repository make it a no-op (CommitOpAs).
func commitExternalChanges(k *kb.KB, tool string) {
	if !k.AutoCommit {
		return
	}
	if conflicts, err := k.ListConflicts(); err == nil && len(conflicts) > 0 {
		return
	}
	msg := externalChangesSubject + "\n\n" + commitReasonKey + ": uncommitted changes found before " + tool
	if _, err := k.CommitOpAs(msg, "", ""); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: commit of external changes before %s failed: %v\n", tool, err)
	}
}

// commitReasonKey is the git trailer key that carries the reason of a write
// (D272). gitx.LogNameStatus reads it back with the same key.
const commitReasonKey = "Reason"

// maxCommitReasonBytes bounds the stored reason; longer text is cut at a rune
// boundary and ends with an ellipsis.
const maxCommitReasonBytes = 500

const reasonSchemaDescription = "Why (commit trailer)"

// withReasonProperty returns schema with an optional string `reason` property
// added, unless the tool already declares one (supersede, conflict_resolve keep
// their own entry). An empty schema is treated as a bare object. A schema that
// is not a JSON object panics: every wrapped tool has a literal schema, so this
// is a programmer error caught at registration.
func withReasonProperty(toolName string, schema json.RawMessage) json.RawMessage {
	top := map[string]json.RawMessage{}
	if len(schema) == 0 {
		top["type"] = json.RawMessage(`"object"`)
	} else if err := json.Unmarshal(schema, &top); err != nil || top == nil {
		panic(fmt.Sprintf("mcpserver: tool %q has an unparsable input schema: %v", toolName, err))
	}
	props := map[string]json.RawMessage{}
	if raw, ok := top["properties"]; ok {
		if err := json.Unmarshal(raw, &props); err != nil || props == nil {
			panic(fmt.Sprintf("mcpserver: tool %q has unparsable schema properties: %v", toolName, err))
		}
	}
	if _, ok := props["reason"]; ok {
		return schema
	}
	entry, _ := json.Marshal(map[string]string{"type": "string", "description": reasonSchemaDescription})
	props["reason"] = entry
	top["properties"], _ = json.Marshal(props)
	out, err := json.Marshal(top)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: tool %q: re-encode input schema: %v", toolName, err))
	}
	return out
}

// commitReason extracts the optional `reason` argument of a write and
// normalises it for a commit trailer: trimmed, newlines and whitespace runs
// collapsed to one space, cut to maxCommitReasonBytes at a rune boundary with
// an ellipsis. It returns "" when there is no usable reason.
func commitReason(args json.RawMessage) string {
	var p struct {
		Reason any `json:"reason"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return ""
	}
	raw, _ := p.Reason.(string)
	return normalizeReason(raw)
}

// normalizeReason is the shared normalisation of a free-text reason: whitespace
// collapsed, cut to maxCommitReasonBytes at a rune boundary with an ellipsis
// (the Reason trailer, D272, and accept_findings reasons, D350).
func normalizeReason(raw string) string {
	reason := strings.Join(strings.Fields(raw), " ")
	if len(reason) > maxCommitReasonBytes {
		cut := maxCommitReasonBytes
		for cut > 0 && !utf8.RuneStart(reason[cut]) {
			cut--
		}
		reason = strings.TrimRight(reason[:cut], " ") + "…"
	}
	return reason
}

func appendSyncWarning(res *ToolResult, k *kb.KB) {
	s := k.GitStatusSnapshot()
	if s.State != "failed" && s.State != "pending" && s.State != "degraded" {
		return
	}
	b, _ := json.Marshal(map[string]any{"sync_state": s.State, "last_error": s.LastError})
	res.Content = append(res.Content, ContentBlock{Type: "text", Text: string(b)})
}

// readSyncWrap keeps a Git-synchronised KB fresh for its readers without
// making them wait (#361, D258). When the SyncIn freshness window has expired,
// the read is served at once from the local clone and a SyncIn is started in
// the background; the change it pulls is visible from the next call on. Sync
// failures stay non-fatal, as they always were for reads: a conflict is
// registered and its concepts degraded, any other error is logged. Writes do
// not come through here: gitWrap still syncs them first, since a write must
// not commit on a stale base (D237).
func readSyncWrap(k *kb.KB, t Tool) Tool {
	orig := t
	t.Handler = func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		// The checks are lock-free and can race another SyncIn; SyncIn repeats
		// them under the lock, which turns the race into a no-op.
		if k.SyncInDue() && !k.ReadFetchBackingOff() {
			startReadRefresh(k, orig.Name)
		}
		return orig.Handler(ctx, args)
	}
	return t
}

// readRefresh is one KB's background read-side SyncIn. running coalesces
// them: reads arriving while one is in flight do not queue another fetch
// behind the git lock, they are served from the clone that fetch will update.
type readRefresh struct {
	running atomic.Bool
	wg      sync.WaitGroup
}

// readRefreshes maps *kb.KB to its *readRefresh. KBs live as long as the
// server, so entries are never removed.
var readRefreshes sync.Map

func readRefreshFor(k *kb.KB) *readRefresh {
	v, _ := readRefreshes.LoadOrStore(k, &readRefresh{})
	return v.(*readRefresh)
}

func startReadRefresh(k *kb.KB, op string) {
	r := readRefreshFor(k)
	if !r.running.CompareAndSwap(false, true) {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer r.running.Store(false)
		start := time.Now()
		var fetchRan bool
		_ = k.WithGitLock(func() error { // inner func always returns nil
			// A fetch that failed while this one waited for the lock starts
			// the backoff: do not stack a second bounded fetch on it (#348).
			if k.ReadFetchBackingOff() {
				return nil
			}
			var syncErr error
			fetchRan, syncErr = k.SyncIn()
			if syncErr != nil {
				var rce *gitx.RebaseConflictError
				if errors.As(syncErr, &rce) {
					n, _ := handleConflictError(k, rce)
					fmt.Fprintf(os.Stderr,
						"cartographer: git conflict during read sync (%s): registered %d concept(s) as degraded\n",
						op, n)
				} else {
					fmt.Fprintf(os.Stderr, "cartographer: git sync failed during read (%s): %v\n", op, syncErr)
				}
			}
			return nil
		})
		if fetchRan {
			d := time.Since(start)
			fmt.Fprintln(os.Stderr, formatTiming(op+" (background sync)", d, 0, 0, 0, false, d))
		}
	}()
}

// formatTiming renders a single greppable timing line for a write operation.
// Durations are rounded to whole milliseconds; phases that were skipped are
// passed in as 0. When pushAsync is true (D76/WP4, SyncOutDebounce > 0), the
// push field reads "async" instead of a duration: the push was scheduled on
// the per-KB worker rather than awaited inline, so there is no meaningful
// elapsed time to report here.
func formatTiming(op string, syncIn, handler, commit, push time.Duration, pushAsync bool, total time.Duration) string {
	pushField := fmt.Sprintf("%dms", push.Milliseconds())
	if pushAsync {
		pushField = "async"
	}
	return fmt.Sprintf(
		"cartographer: timing op=%q sync_in=%dms handler=%dms commit=%dms push=%s total=%dms",
		op, syncIn.Milliseconds(), handler.Milliseconds(), commit.Milliseconds(), pushField, total.Milliseconds(),
	)
}

// handleConflictError registers each conflicting concept in the KB conflict registry
// and marks it as degraded. Best-effort: errors are logged to stderr.
// Returns the number of concept IDs successfully identified in the conflict,
// and the reserved files (log.md, index.md, _map.md, _archive.md) in it.
//
// Reserved files are not concepts (D311). Alongside a concept they stay out of
// the registry: FinalizeConflicts resolves them automatically with the
// concepts. Alone, they reach here only when the server's own reconciliation
// (kb.pullRebase) failed, and are registered with kind "reserved" and their
// path as concept_id so git_conflict_resolve can settle them — registering
// nothing would leave the KB with no tool-level way out.
func handleConflictError(k *kb.KB, rce *gitx.RebaseConflictError) (int, []string) {
	now := time.Now().UTC().Format(time.RFC3339)
	n := 0
	var reserved, concepts []string
	conceptIDs := map[string]string{}
	for _, file := range rce.Files {
		if conceptID, ok := kb.GitPathToConceptID(file); ok {
			concepts = append(concepts, file)
			conceptIDs[file] = conceptID
		} else if kb.IsReservedPath(file) {
			reserved = append(reserved, file)
		}
	}
	register := concepts
	if len(concepts) == 0 {
		register = reserved
	}
	for _, file := range register {
		conceptID, kind := conceptIDs[file], ""
		if conceptID == "" {
			conceptID, kind = file, kb.ConflictKindReserved
		}
		c := kb.Conflict{
			ConceptID:  conceptID,
			Path:       file,
			LocalSHA:   rce.LocalSHA,
			RemoteSHA:  rce.RemoteSHA,
			Branch:     rce.Branch,
			Files:      rce.Files,
			DetectedAt: now,
			Kind:       kind,
		}
		if state := k.ServerGitStatus(); state.Profile == "server" {
			c.BaseBranch, c.WorkingBranch, c.PRNumber, c.PRURL = state.BaseBranch, state.WorkingBranch, state.PRNumber, state.PRURL
		}
		if err := k.RegisterConflict(c); err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: register conflict %q: %v\n", conceptID, err)
		}
		if kind == kb.ConflictKindReserved {
			continue
		}
		if err := k.MarkDegraded(conceptID); err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: mark degraded %q: %v\n", conceptID, err)
		}
		n++
	}
	return n, reserved
}

// conflictMessage is the error a write returns when its SyncIn hit a rebase
// conflict on n concepts and the given reserved files. It never suggests a
// git command: every way out is a tool (D311).
func conflictMessage(n int, reserved []string) string {
	if len(reserved) == 0 {
		return fmt.Sprintf("git conflict detected and registered on %d concept(s); "+
			"use the conflicts_list tool and kb-conflict-resolve skill to resolve", n)
	}
	files := strings.Join(reserved, ", ")
	if n == 0 {
		return fmt.Sprintf("git conflict on reserved file(s) only (%s); auto-resolution failed — "+
			"resolve with git_conflict_resolve (concept_id: the file path, strategy: union|ours|theirs; union only for log.md)", files)
	}
	return fmt.Sprintf("git conflict detected and registered on %d concept(s); "+
		"%d reserved file(s) also in conflict (%s) — these are auto-resolved when the concept conflicts are resolved; "+
		"use the conflicts_list tool and kb-conflict-resolve skill to resolve the concept conflicts", n, len(reserved), files)
}

// byteBudget renders a byte limit the way an operator reads it, so a tool
// description and the constant it documents can never disagree: the figures in
// every description are interpolated from the constants the handlers check
// (D161).
func byteBudget(n int) string {
	switch {
	case n%(1024*1024) == 0:
		return fmt.Sprintf("%d MiB", n/(1024*1024))
	case n%1024 == 0:
		return fmt.Sprintf("%d KiB", n/1024)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// commitSubjectFields names, per tool, the argument fields that identify the
// resource a write touched, in the order they appear in the commit subject
// (joined with "/"). It exists because the fallback list below is keyed on the
// concept tools' vocabulary: the artifact tools identify their target with
// "path" and the asset tools with "concept_id" plus "path", so without an entry
// here every one of those writes committed as the bare tool name and the git
// history could not distinguish them.
//
// Deliberately not shared with audit.go's auditResourceFields: that list is a
// leakage allow-list answering "which arguments may be recorded at all", and
// its artifact entries name fields those tools do not accept.
var commitSubjectFields = map[string][]string{
	"artifact_write":  {"path"},
	"artifact_delete": {"path"},
	"asset_write":     {"concept_id", "path"},
	"asset_delete":    {"concept_id", "path"},
}

// commitSubjectFallback is the priority order used for any tool with no
// commitSubjectFields entry: the first non-empty string field wins.
var commitSubjectFallback = []string{"id", "name", "source_id", "contradiction_id"}

// commitMessage builds a human-readable commit message from the tool name and
// the arguments that identify what the write touched: the per-tool fields in
// commitSubjectFields when the tool has an entry, otherwise the first
// recognisable identifier in commitSubjectFallback.
// Falls back to the tool name alone when no identifying argument is present.
func commitMessage(toolName string, args json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return toolName
	}
	stringArg := func(key string) string {
		s, _ := m[key].(string)
		return s
	}
	if fields, ok := commitSubjectFields[toolName]; ok {
		var parts []string
		for _, key := range fields {
			if s := stringArg(key); s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			return fmt.Sprintf("%s: %s", toolName, strings.Join(parts, "/"))
		}
		return toolName
	}
	for _, key := range commitSubjectFallback {
		if s := stringArg(key); s != "" {
			return fmt.Sprintf("%s: %s", toolName, s)
		}
	}
	return toolName
}
