package audit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func tempLog(t *testing.T) (*Log, string) {
	t.Helper()
	f, err := os.CreateTemp("", "audit-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return l, path
}

func makeEntry(tool string) Entry {
	return Entry{
		Timestamp:  time.Now(),
		Tool:       tool,
		Args:       `{"key":"value"}`,
		AgentID:    "test-agent",
		Outcome:    "ok",
		DurationMs: 10,
	}
}

func TestAppendAndTail(t *testing.T) {
	l, _ := tempLog(t)

	for _, tool := range []string{"t1", "t2", "t3"} {
		if err := l.Append(makeEntry(tool)); err != nil {
			t.Fatal(err)
		}
	}

	tail, err := l.Tail(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 3 {
		t.Fatalf("want 3 entries, got %d", len(tail))
	}
	// newest first
	if tail[0].Tool != "t3" || tail[1].Tool != "t2" || tail[2].Tool != "t1" {
		t.Errorf("wrong order: %s %s %s", tail[0].Tool, tail[1].Tool, tail[2].Tool)
	}
}

func TestHashChain(t *testing.T) {
	l, _ := tempLog(t)

	for i := 0; i < 5; i++ {
		if err := l.Append(makeEntry("tool")); err != nil {
			t.Fatal(err)
		}
	}

	// Verify first entry has PrevHash = "genesis"
	tail, err := l.Tail(5)
	if err != nil {
		t.Fatal(err)
	}
	// tail is newest-first; oldest is last
	oldest := tail[len(tail)-1]
	if oldest.PrevHash != "genesis" {
		t.Errorf("first entry PrevHash = %q, want genesis", oldest.PrevHash)
	}

	// Full chain must be valid
	idx, err := l.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if idx != -1 {
		t.Errorf("expected valid chain, got broken at index %d", idx)
	}
}

func TestVerifyTampered(t *testing.T) {
	l, path := tempLog(t)

	if err := l.Append(makeEntry("tool")); err != nil {
		t.Fatal(err)
	}

	// Tamper: change outcome in the JSONL file, leave hash unchanged
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.ReplaceAll(string(data), `"outcome":"ok"`, `"outcome":"tampered"`)
	if tampered == string(data) {
		t.Fatal("tamper replacement had no effect")
	}
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatal(err)
	}

	idx, err := l.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if idx != 0 {
		t.Errorf("expected tamper detected at index 0, got %d", idx)
	}
}

func TestCount(t *testing.T) {
	l, _ := tempLog(t)

	for i := 0; i < 5; i++ {
		if err := l.Append(makeEntry("tool")); err != nil {
			t.Fatal(err)
		}
	}

	n, err := l.Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("want 5, got %d", n)
	}
}

func TestArgsTruncation(t *testing.T) {
	l, _ := tempLog(t)

	e := makeEntry("tool")
	e.Args = strings.Repeat("a", 2048)
	if err := l.Append(e); err != nil {
		t.Fatal(err)
	}

	tail, err := l.Tail(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail[0].Args) != maxArgsLen {
		t.Errorf("expected args truncated to %d, got %d", maxArgsLen, len(tail[0].Args))
	}
}

// --- Ed25519 signing tests ---

func tempLogWithKey(t *testing.T, kp KeyPair) (*Log, string) {
	t.Helper()
	f, err := os.CreateTemp("", "audit-sig-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })
	l, err := OpenWithKey(path, kp)
	if err != nil {
		t.Fatal(err)
	}
	return l, path
}

func TestGenerateKeyPair(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if len(kp.Private) == 0 {
		t.Fatal("private key is empty")
	}
	if len(kp.Public) == 0 {
		t.Fatal("public key is empty")
	}

	// Round-trip via seed.
	hexSeed := kp.SeedToHex()
	if len(hexSeed) != 64 {
		t.Fatalf("expected 64-char hex seed, got %d", len(hexSeed))
	}

	kp2, err := KeyPairFromSeed(hexSeed)
	if err != nil {
		t.Fatal(err)
	}
	if kp2.SeedToHex() != hexSeed {
		t.Error("round-trip seed mismatch")
	}
}

func TestAppendWithSignature(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	l, _ := tempLogWithKey(t, kp)

	if err := l.Append(makeEntry("signed-tool")); err != nil {
		t.Fatal(err)
	}

	tail, err := l.Tail(1)
	if err != nil {
		t.Fatal(err)
	}
	if tail[0].Sig == "" {
		t.Fatal("expected non-empty Sig field after Append with key pair")
	}

	// Chain and signature must be valid.
	idx, err := l.Verify()
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if idx != -1 {
		t.Errorf("expected valid chain, got broken at index %d", idx)
	}
}

func TestVerifyInvalidSignature(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	l, path := tempLogWithKey(t, kp)

	if err := l.Append(makeEntry("tool")); err != nil {
		t.Fatal(err)
	}

	// Corrupt the Sig field in the JSONL file.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Replace a portion of the sig with 'ff' bytes — any corruption invalidates the signature.
	corrupted := strings.Replace(string(data), `"sig":"`, `"sig":"ff`, 1)
	if corrupted == string(data) {
		t.Fatal("corruption replacement had no effect")
	}
	if err := os.WriteFile(path, []byte(corrupted), 0644); err != nil {
		t.Fatal(err)
	}

	// Re-open with key to trigger signature verification.
	l2, err := OpenWithKey(path, kp)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := l2.Verify()
	if err == nil {
		t.Fatal("expected Verify to return error for invalid signature")
	}
	if idx != 0 {
		t.Errorf("expected broken entry at index 0, got %d", idx)
	}
}

func TestVerifyFullDistinguishesUnsigned(t *testing.T) {
	// Write some unsigned entries first via plain Open.
	f, err := os.CreateTemp("", "audit-vf-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	defer os.Remove(path)

	lPlain, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := lPlain.Append(makeEntry("unsigned")); err != nil {
			t.Fatal(err)
		}
	}

	// Now continue appending with signing enabled.
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lSigned, err := OpenWithKey(path, kp)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := lSigned.Append(makeEntry("signed")); err != nil {
			t.Fatal(err)
		}
	}

	count, unsigned, err := lSigned.VerifyFull()
	if err != nil {
		t.Fatalf("VerifyFull error: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 signed entries, got %d", count)
	}
	if unsigned != 2 {
		t.Errorf("expected 2 unsigned entries, got %d", unsigned)
	}
}

// --- D119: versioned events, rotation/checkpoints, queue/retry, fault injection ---

func tempLogWithOptions(t *testing.T, opts Options) (*Log, string) {
	t.Helper()
	f, err := os.CreateTemp("", "audit-opts-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })
	l, err := OpenWithOptions(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	return l, path
}

func TestVersionedEventsPairAndVerify(t *testing.T) {
	l, path := tempLog(t)

	attempt, err := l.AppendEvent(Entry{
		RequestID: "req-1", Phase: PhaseAttempt, Tool: "concept_read",
		Transport: "stdio", PrincipalID: "agent-1", Resources: map[string]string{"id": "notes/example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Version != VersionV2 {
		t.Errorf("want version %d, got %d", VersionV2, attempt.Version)
	}
	if attempt.ID == "" {
		t.Error("expected a generated event ID")
	}

	if _, err := l.AppendEvent(Entry{
		RequestID: "req-1", Phase: PhaseCompletion, Tool: "concept_read", Outcome: OutcomeSuccess, DurationMs: 5,
	}); err != nil {
		t.Fatal(err)
	}

	sum, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}
	if sum.Valid != 2 {
		t.Errorf("want 2 valid entries, got %d", sum.Valid)
	}
	if sum.Incomplete != 0 {
		t.Errorf("want 0 incomplete, got %d", sum.Incomplete)
	}
}

func TestVersionedAttemptRejectsOutcome(t *testing.T) {
	l, _ := tempLog(t)
	if _, err := l.AppendEvent(Entry{RequestID: "r", Phase: PhaseAttempt, Tool: "t", Outcome: OutcomeSuccess}); err == nil {
		t.Fatal("expected an attempt phase carrying an outcome to be rejected")
	}
}

func TestVersionedIncompleteAttemptFailsVerify(t *testing.T) {
	l, path := tempLog(t)
	if _, err := l.AppendEvent(Entry{RequestID: "req-2", Phase: PhaseAttempt, Tool: "concept_read"}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(path, nil); err == nil {
		t.Fatal("expected verification to fail for an attempt with no matching completion")
	}
}

func TestRotationCheckpointAndCheckpointOnlyVerification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "archive")
	l, err := OpenWithOptions(path, Options{MaxBytes: 1, ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := l.Append(makeEntry("tool")); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	segs, err := os.ReadDir(archive)
	if err != nil {
		t.Fatal(err)
	}
	var jsonlCount int
	for _, e := range segs {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			jsonlCount++
		}
	}
	if jsonlCount != 2 {
		t.Fatalf("want 2 archived segments, got %d", jsonlCount)
	}

	sum, err := VerifyAudit(path, archive, nil)
	if err != nil {
		t.Fatalf("VerifyAudit: %v", err)
	}
	if sum.Retained != 2 {
		t.Errorf("want 2 retained segments, got %d", sum.Retained)
	}
	if sum.CheckpointOnly != 0 {
		t.Errorf("want 0 checkpoint-only segments, got %d", sum.CheckpointOnly)
	}
	if sum.Valid != 3 {
		t.Errorf("want 3 valid entries total, got %d", sum.Valid)
	}

	// Simulate retention having deleted the oldest segment: its chain
	// evidence must survive purely via the checkpoint index.
	idxBefore, err := loadCheckpointIndex(checkpointPath(path, archive))
	if err != nil {
		t.Fatal(err)
	}
	if len(idxBefore.Segments) != 2 {
		t.Fatalf("want 2 checkpoints, got %d", len(idxBefore.Segments))
	}
	if err := os.Remove(filepath.Join(archive, idxBefore.Segments[0].Name)); err != nil {
		t.Fatal(err)
	}

	sum2, err := VerifyAudit(path, archive, nil)
	if err != nil {
		t.Fatalf("VerifyAudit after simulated retention: %v", err)
	}
	if sum2.CheckpointOnly != 1 {
		t.Errorf("want 1 checkpoint-only segment, got %d", sum2.CheckpointOnly)
	}
	if sum2.Retained != 1 {
		t.Errorf("want 1 still-retained segment, got %d", sum2.Retained)
	}
}

func TestVerifyAuditRequiresKeyWhenCheckpointSigned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "archive")
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	l, err := OpenWithKeyAndOptions(path, kp, Options{MaxBytes: 1, ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := l.Append(makeEntry("tool")); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := VerifyAudit(path, archive, nil); err == nil {
		t.Fatal("expected verification without the public key to fail for a signing-required checkpoint")
	}
	if _, err := VerifyAudit(path, archive, kp.Public); err != nil {
		t.Fatalf("VerifyAudit with correct key: %v", err)
	}
}

// TestRotationCheckpointFailurePreservesActiveSegment is a fault-injection
// test for the ordering guarantee documented on rotateLocked: if the
// checkpoint index write fails, the active segment must be left completely
// untouched (never renamed, never truncated) so no audit data is ever lost.
func TestRotationCheckpointFailurePreservesActiveSegment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "archive")

	l, err := OpenWithOptions(path, Options{MaxBytes: 1, ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(makeEntry("first")); err != nil {
		t.Fatal(err) // no rotation yet: the active file starts empty
	}

	// Force the checkpoint index write to fail on the next rotation:
	// pre-create a directory where atomicWrite needs to rename a temp file
	// into place.
	if err := os.MkdirAll(checkpointPath(path, archive), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := l.Append(makeEntry("second")); err == nil {
		t.Fatal("expected rotation to fail because the checkpoint index path is a directory")
	}

	n, err := l.Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("active segment must be untouched after a failed checkpoint write, want 1 entry, got %d", n)
	}
	entries, err := os.ReadDir(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			t.Fatalf("no segment should have been renamed into the archive, found %q", e.Name())
		}
	}
}

// TestRetentionOnlyDeletesCheckpointedSegments is a white-box test of
// retainLocked: an aged-out file that has no corresponding checkpoint entry
// must never be deleted, regardless of age.
func TestRetentionOnlyDeletesCheckpointedSegments(t *testing.T) {
	dir := t.TempDir()
	const checkpointedName = "checkpointed.jsonl"
	const orphanName = "orphan.jsonl"
	for _, name := range []string{checkpointedName, orphanName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{checkpointedName, orphanName} {
		if err := os.Chtimes(filepath.Join(dir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}

	l := &Log{
		opts:  Options{RetentionDays: 1},
		index: checkpointIndex{Version: 1, Segments: []Checkpoint{{Name: checkpointedName}}},
	}
	l.retainLocked(dir)

	if _, err := os.Stat(filepath.Join(dir, checkpointedName)); !os.IsNotExist(err) {
		t.Error("checkpointed, aged-out segment should have been deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, orphanName)); err != nil {
		t.Errorf("un-checkpointed segment must never be deleted by retention: %v", err)
	}
}

// TestAppendPartialWriteRollback injects a partial write followed by a
// failure into appendDurable and asserts the file is truncated back to its
// pre-append size (rollback), leaving the chain valid and appendable.
func TestAppendPartialWriteRollback(t *testing.T) {
	l, path := tempLog(t)
	if err := l.Append(makeEntry("first")); err != nil {
		t.Fatal(err)
	}
	sizeBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	orig := writeAllFunc
	writeAllFunc = func(f *os.File, b []byte) error {
		if len(b) > 5 {
			_, _ = f.Write(b[:5]) // simulate a short write before the failure
		}
		return errors.New("injected partial write failure")
	}
	if err := l.Append(makeEntry("second")); err == nil {
		t.Fatal("expected the injected write failure to surface as an error")
	}
	writeAllFunc = orig

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != sizeBefore.Size() {
		t.Errorf("expected rollback to truncate back to %d bytes, file is %d bytes", sizeBefore.Size(), fi.Size())
	}

	if err := l.Append(makeEntry("third")); err != nil {
		t.Fatalf("append after rollback should succeed: %v", err)
	}
	idx, err := l.Verify()
	if err != nil || idx != -1 {
		t.Fatalf("expected a valid chain after rollback, got idx=%d err=%v", idx, err)
	}
}

// TestAppendRetrySucceedsAfterTransientFailure exercises the bounded
// retry-with-backoff loop: the first two write attempts fail transiently,
// the third succeeds, and the append must ultimately succeed without
// dropping the event.
func TestAppendRetrySucceedsAfterTransientFailure(t *testing.T) {
	l, _ := tempLogWithOptions(t, Options{RetryAttempts: 3, RetryBackoff: time.Millisecond})

	var mu sync.Mutex
	calls := 0
	orig := writeAllFunc
	writeAllFunc = func(f *os.File, b []byte) error {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n < 3 {
			return errors.New("transient write failure")
		}
		return orig(f, b)
	}
	defer func() { writeAllFunc = orig }()

	if err := l.Append(makeEntry("retried")); err != nil {
		t.Fatalf("expected append to succeed after retries, got %v", err)
	}
	n, err := l.Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("want 1 entry after successful retry, got %d", n)
	}
	if !l.State().Healthy {
		t.Error("expected log to be healthy after a retry that ultimately succeeded")
	}
}

// TestQueueCapacityRejectsOverflow is a white-box test of the bounded
// admission counter: once queueDepth reaches QueueCapacity, an append is
// rejected outright (visible overload) rather than growing unbounded.
func TestQueueCapacityRejectsOverflow(t *testing.T) {
	l, _ := tempLog(t)

	l.mu.Lock()
	l.queueDepth = l.opts.QueueCapacity
	l.mu.Unlock()

	if err := l.Append(makeEntry("overflow")); err == nil || !strings.Contains(err.Error(), "queue full") {
		t.Fatalf("expected a queue-full error, got %v", err)
	}

	l.mu.Lock()
	l.queueDepth = 0
	l.mu.Unlock()

	if err := l.Append(makeEntry("after-recovery")); err != nil {
		t.Fatalf("expected append to succeed once the queue has room again: %v", err)
	}
}

// TestBestEffortModeDoesNotBlockOnFailure asserts that a failed append in
// best_effort mode is dropped and counted, but never renders the sink
// not-ready — an MCP call must keep proceeding even while audit writes fail.
func TestBestEffortModeDoesNotBlockOnFailure(t *testing.T) {
	l, _ := tempLogWithOptions(t, Options{Mode: "best_effort"})

	orig := writeAllFunc
	writeAllFunc = func(f *os.File, b []byte) error { return errors.New("disk full") }
	defer func() { writeAllFunc = orig }()

	if err := l.Append(makeEntry("x")); err == nil {
		t.Fatal("expected the append to fail")
	}
	st := l.State()
	if st.Healthy {
		t.Error("expected unhealthy after a failed append")
	}
	if !st.Ready {
		t.Error("best_effort mode must remain ready even while unhealthy")
	}
	if st.Dropped != 1 {
		t.Errorf("want 1 dropped event, got %d", st.Dropped)
	}
}

// TestRequiredModeBlocksAndRecoversAutomatically asserts that required mode
// reports not-ready while unhealthy (the documented block/recovery path),
// and recovers automatically the moment a subsequent append succeeds.
func TestRequiredModeBlocksAndRecoversAutomatically(t *testing.T) {
	l, _ := tempLogWithOptions(t, Options{Mode: "required"})

	orig := writeAllFunc
	writeAllFunc = func(f *os.File, b []byte) error { return errors.New("disk full") }
	if err := l.Append(makeEntry("x")); err == nil {
		t.Fatal("expected the append to fail")
	}
	writeAllFunc = orig

	st := l.State()
	if st.Ready {
		t.Error("required mode must report not-ready while unhealthy")
	}

	if err := l.Append(makeEntry("y")); err != nil {
		t.Fatalf("expected the recovery append to succeed: %v", err)
	}
	st = l.State()
	if !st.Ready || !st.Healthy {
		t.Error("expected automatic recovery after a successful append")
	}
}

func TestSegmentsListsArchiveThenActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "archive")
	if err := os.MkdirAll(archive, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b.jsonl", "a.jsonl"} {
		if err := os.WriteFile(filepath.Join(archive, name), nil, 0o640); err != nil {
			t.Fatal(err)
		}
	}

	segs, err := Segments(path, archive)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(archive, "a.jsonl"), filepath.Join(archive, "b.jsonl"), path}
	if len(segs) != len(want) {
		t.Fatalf("want %v, got %v", want, segs)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Errorf("segment %d = %q, want %q", i, segs[i], want[i])
		}
	}
}
