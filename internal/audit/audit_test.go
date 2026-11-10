package audit

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
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
	l, err := OpenWithOptions(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return l, path
}

// appendOne writes one tool call (attempt and completion) with a fresh
// request ID.
func appendOne(l *Log, tool string) error {
	id := newID()
	if _, err := l.AppendEvent(Entry{RequestID: id, Phase: PhaseAttempt, Tool: tool}); err != nil {
		return err
	}
	_, err := l.AppendEvent(Entry{RequestID: id, Phase: PhaseCompletion, Tool: tool, Outcome: OutcomeSuccess})
	return err
}

// countEntries is the number of entries of the active segment.
func countEntries(l *Log) (int, error) {
	es, err := l.TailFunc(math.MaxInt32, nil)
	return len(es), err
}

// verifyLog is the production verifier on one active file, the way the
// offline command runs it without an archive.
func verifyLog(path string, public ed25519.PublicKey) (Summary, error) {
	return VerifyAudit(path, "", public, VerifyOptions{})
}

func TestAppendAndTail(t *testing.T) {
	l, _ := tempLog(t)

	for _, tool := range []string{"t1", "t2", "t3"} {
		if err := appendOne(l, tool); err != nil {
			t.Fatal(err)
		}
	}

	tail, err := l.TailFunc(3, func(e Entry) bool { return e.Phase == PhaseCompletion })
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

// TailFunc reads backwards across chunk boundaries and stops at the page: a
// filter that matches one entry in many still fills it, newest first.
func TestTailFuncAcrossChunks(t *testing.T) {
	l, path := tempLog(t)
	// Lines of ~300 bytes: 600 of them span several 64 KiB chunks.
	for i := 0; i < 600; i++ {
		e := Entry{RequestID: newID(), Phase: PhaseAttempt, Tool: fmt.Sprintf("t%03d", i)}
		e.KB = "busy"
		if i%50 == 0 {
			e.KB = "quiet"
		}
		e.Resources = map[string]string{"note": strings.Repeat("x", 200)}
		if _, err := l.AppendEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	if st, _ := os.Stat(path); st.Size() < 2*tailChunk {
		t.Fatalf("fixture too small to cross chunks: %d bytes", st.Size())
	}
	quiet, err := l.TailFunc(5, func(e Entry) bool { return e.KB == "quiet" })
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range quiet {
		got = append(got, e.Tool)
	}
	if want := "t550 t500 t450 t400 t350"; strings.Join(got, " ") != want {
		t.Fatalf("quiet page = %v, want %s", got, want)
	}
	all, err := l.TailFunc(1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 600 || all[0].Tool != "t599" || all[599].Tool != "t000" {
		t.Fatalf("whole tail: %d entries, first %s, last %s", len(all), all[0].Tool, all[len(all)-1].Tool)
	}
}

func TestHashChain(t *testing.T) {
	l, path := tempLog(t)

	for i := 0; i < 5; i++ {
		if err := appendOne(l, "tool"); err != nil {
			t.Fatal(err)
		}
	}

	// Verify first entry has PrevHash = "genesis"
	tail, err := l.TailFunc(100, nil)
	if err != nil {
		t.Fatal(err)
	}
	// tail is newest-first; oldest is last
	oldest := tail[len(tail)-1]
	if oldest.PrevHash != "genesis" {
		t.Errorf("first entry PrevHash = %q, want genesis", oldest.PrevHash)
	}

	// Full chain must be valid
	if _, err := verifyLog(path, nil); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyTampered(t *testing.T) {
	l, path := tempLog(t)

	if err := appendOne(l, "tool"); err != nil {
		t.Fatal(err)
	}

	// Tamper: change outcome in the JSONL file, leave hash unchanged
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.ReplaceAll(string(data), `"outcome":"success"`, `"outcome":"tampered"`)
	if tampered == string(data) {
		t.Fatal("tamper replacement had no effect")
	}
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := verifyLog(path, nil); err == nil || !strings.Contains(err.Error(), "chain broken at entry 1") {
		t.Errorf("expected the tamper detected at entry 1, got %v", err)
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
	l, err := OpenWithKeyAndOptions(path, kp, Options{})
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
	l, path := tempLogWithKey(t, kp)

	if err := appendOne(l, "signed-tool"); err != nil {
		t.Fatal(err)
	}

	tail, err := l.TailFunc(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tail[0].Sig == "" {
		t.Fatal("expected non-empty Sig field after Append with key pair")
	}

	// Chain and signature must be valid.
	if _, err := verifyLog(path, kp.Public); err != nil {
		t.Fatalf("verify returned error: %v", err)
	}
}

func TestVerifyInvalidSignature(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	l, path := tempLogWithKey(t, kp)

	if err := appendOne(l, "tool"); err != nil {
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
	_, err = verifyLog(path, kp.Public)
	if err == nil || !strings.Contains(err.Error(), "invalid signature at entry 0") {
		t.Fatalf("expected an invalid signature at entry 0, got %v", err)
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

	lPlain, err := OpenWithOptions(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := appendOne(lPlain, "unsigned"); err != nil {
			t.Fatal(err)
		}
	}

	// Now continue appending with signing enabled.
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lSigned, err := OpenWithKeyAndOptions(path, kp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := appendOne(lSigned, "signed"); err != nil {
			t.Fatal(err)
		}
	}

	sum, err := verifyLog(path, kp.Public)
	if err != nil {
		t.Fatalf("verify error: %v", err)
	}
	if signed := sum.Valid - sum.Unsigned; signed != 6 {
		t.Errorf("expected 6 signed entries (3 calls), got %d", signed)
	}
	if sum.Unsigned != 4 {
		t.Errorf("expected 4 unsigned entries (2 calls), got %d", sum.Unsigned)
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

	sum, err := verifyLog(path, nil)
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
	if _, err := verifyLog(path, nil); err == nil {
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
		if err := appendOne(l, "tool"); err != nil {
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
	if jsonlCount != 5 { // MaxBytes 1: every event after the first rotates
		t.Fatalf("want 5 archived segments, got %d", jsonlCount)
	}

	sum, err := VerifyAudit(path, archive, nil, VerifyOptions{})
	if err != nil {
		t.Fatalf("VerifyAudit: %v", err)
	}
	if sum.Retained != 5 {
		t.Errorf("want 5 retained segments, got %d", sum.Retained)
	}
	if sum.CheckpointOnly != 0 {
		t.Errorf("want 0 checkpoint-only segments, got %d", sum.CheckpointOnly)
	}
	if sum.Valid != 6 {
		t.Errorf("want 6 valid entries total, got %d", sum.Valid)
	}

	// Simulate retention having deleted the oldest segment: its chain
	// evidence must survive purely via the checkpoint index.
	idxBefore, err := loadCheckpointIndex(checkpointPath(path, archive))
	if err != nil {
		t.Fatal(err)
	}
	if len(idxBefore.Segments) != 5 {
		t.Fatalf("want 5 checkpoints, got %d", len(idxBefore.Segments))
	}
	if err := os.Remove(filepath.Join(archive, idxBefore.Segments[0].Name)); err != nil {
		t.Fatal(err)
	}

	sum2, err := VerifyAudit(path, archive, nil, VerifyOptions{})
	if err != nil {
		t.Fatalf("VerifyAudit after simulated retention: %v", err)
	}
	if sum2.CheckpointOnly != 1 {
		t.Errorf("want 1 checkpoint-only segment, got %d", sum2.CheckpointOnly)
	}
	if sum2.Retained != 4 {
		t.Errorf("want 4 still-retained segment, got %d", sum2.Retained)
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
		if err := appendOne(l, "tool"); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := VerifyAudit(path, archive, nil, VerifyOptions{}); err == nil {
		t.Fatal("expected verification without the public key to fail for a signing-required checkpoint")
	}
	if _, err := VerifyAudit(path, archive, kp.Public, VerifyOptions{}); err != nil {
		t.Fatalf("VerifyAudit with correct key: %v", err)
	}
}

// appendCall records one completed tool call as an attempt/completion pair.
func appendCall(t *testing.T, l *Log, id string) {
	t.Helper()
	if _, err := l.AppendEvent(Entry{RequestID: id, Phase: PhaseAttempt, Tool: "concept_read"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.AppendEvent(Entry{RequestID: id, Phase: PhaseCompletion, Tool: "concept_read", Outcome: OutcomeSuccess}); err != nil {
		t.Fatal(err)
	}
}

// legacyLog writes two unsigned calls, as a server without a seed did before
// D905, and returns the active log size.
func legacyLog(t *testing.T, path, archive string) int64 {
	t.Helper()
	l, err := OpenWithOptions(path, Options{ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	appendCall(t, l, "old-1")
	appendCall(t, l, "old-2")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

// TestVerifyAuditAcceptsAnUnsignedPrefix: a log kept without a seed is signed
// from the D905 upgrade on. The first signature covers the hash chain, so the
// old entries are authenticated through it, and a rotation after the upgrade
// must not turn them into unsigned events of a signing-required segment. An
// unsigned entry after a signed one still is one.
func TestVerifyAuditAcceptsAnUnsignedPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "archive")
	legacyLog(t, path, archive)

	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := OpenWithKeyAndOptions(path, kp, Options{MaxBytes: 1, ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	appendCall(t, signed, "new-1")
	appendCall(t, signed, "new-2")
	if err := signed.Close(); err != nil {
		t.Fatal(err)
	}

	sum, err := VerifyAudit(path, archive, kp.Public, VerifyOptions{})
	if err != nil {
		t.Fatalf("VerifyAudit after the upgrade: %v", err)
	}
	if sum.Unsigned != 4 || sum.Retained == 0 {
		t.Errorf("summary = %+v, want the 4 legacy entries unsigned and the log rotated", sum)
	}

	downgraded, err := OpenWithOptions(path, Options{ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	appendCall(t, downgraded, "after")
	if err := downgraded.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAudit(path, archive, kp.Public, VerifyOptions{}); err == nil || !strings.Contains(err.Error(), "unsigned event") {
		t.Errorf("VerifyAudit after an unsigned run = %v, want the unsigned event reported", err)
	}
}

// TestVerifyAuditRequiresSignaturesAfterASigningCheckpoint: the rotation runs
// before the append, so the first one after the upgrade can archive a segment
// holding legacy entries only. Its checkpoint still says a key was loaded, so
// an unsigned chain continuing from it (here: the signed active file replaced
// by forged entries) is refused even though no signed entry precedes it.
func TestVerifyAuditRequiresSignaturesAfterASigningCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "archive")
	size := legacyLog(t, path, archive)

	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := OpenWithKeyAndOptions(path, kp, Options{MaxBytes: size, ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	appendCall(t, signed, "new")
	if err := signed.Close(); err != nil {
		t.Fatal(err)
	}
	if sum, err := VerifyAudit(path, archive, kp.Public, VerifyOptions{}); err != nil || sum.Retained != 1 || sum.Unsigned != 4 {
		t.Fatalf("VerifyAudit = %+v, %v; want one archived legacy segment and a valid log", sum, err)
	}

	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	forged, err := OpenWithOptions(path, Options{ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	appendCall(t, forged, "forged")
	if err := forged.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAudit(path, archive, kp.Public, VerifyOptions{}); err == nil || !strings.Contains(err.Error(), "unsigned event") {
		t.Errorf("VerifyAudit of a forged unsigned tail = %v, want the unsigned event reported", err)
	}
}

// TestVerifyAuditRefusesAnUnsignedRunBetweenSignedOnes: a keyed run, an
// unsigned one and a keyed one again, all before the first rotation. The
// archived segment holds an unsigned event after a signed one: a downgrade,
// not a legacy prefix.
func TestVerifyAuditRefusesAnUnsignedRunBetweenSignedOnes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "archive")
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range []*KeyPair{&kp, nil} {
		l, err := open(path, key, Options{ArchiveDir: archive})
		if err != nil {
			t.Fatal(err)
		}
		appendCall(t, l, fmt.Sprintf("run-%d", i))
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	l, err := OpenWithKeyAndOptions(path, kp, Options{MaxBytes: 1, ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	appendCall(t, l, "run-2")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAudit(path, archive, kp.Public, VerifyOptions{}); err == nil || !strings.Contains(err.Error(), "unsigned event") {
		t.Errorf("VerifyAudit = %v, want the unsigned run reported", err)
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
	if _, err := l.AppendEvent(Entry{RequestID: "r1", Phase: PhaseAttempt, Tool: "first"}); err != nil {
		t.Fatal(err) // no rotation yet: the active file starts empty
	}

	// Force the checkpoint index write to fail on the next rotation:
	// pre-create a directory where atomicWrite needs to rename a temp file
	// into place.
	if err := os.MkdirAll(checkpointPath(path, archive), 0o750); err != nil {
		t.Fatal(err)
	}

	if _, err := l.AppendEvent(Entry{RequestID: "r2", Phase: PhaseAttempt, Tool: "second"}); err == nil {
		t.Fatal("expected rotation to fail because the checkpoint index path is a directory")
	}

	n, err := countEntries(l)
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
	if err := appendOne(l, "first"); err != nil {
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
	if err := appendOne(l, "second"); err == nil {
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

	if err := appendOne(l, "third"); err != nil {
		t.Fatalf("append after rollback should succeed: %v", err)
	}
	if _, err := verifyLog(path, nil); err != nil {
		t.Fatalf("expected a valid chain after rollback, got %v", err)
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

	if err := appendOne(l, "retried"); err != nil {
		t.Fatalf("expected append to succeed after retries, got %v", err)
	}
	n, err := countEntries(l)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("want 2 entries (attempt, completion) after successful retry, got %d", n)
	}
	if !l.State().Healthy {
		t.Error("expected log to be healthy after a retry that ultimately succeeded")
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

	if err := appendOne(l, "x"); err == nil {
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
	if err := appendOne(l, "x"); err == nil {
		t.Fatal("expected the append to fail")
	}
	writeAllFunc = orig

	st := l.State()
	if st.Ready {
		t.Error("required mode must report not-ready while unhealthy")
	}

	if err := appendOne(l, "y"); err != nil {
		t.Fatalf("expected the recovery append to succeed: %v", err)
	}
	st = l.State()
	if !st.Ready || !st.Healthy {
		t.Error("expected automatic recovery after a successful append")
	}
}

// TestResultBytesKeepsOldHashesAndIsCovered pins D301: an event without
// result_bytes hashes exactly as before the field existed, a log mixing old
// and new events verifies, and a tampered result_bytes breaks the chain.
func TestResultBytesKeepsOldHashesAndIsCovered(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	old := Entry{Version: VersionV2, ID: "e1", Timestamp: ts, Phase: PhaseCompletion, RequestID: "r", Tool: "t", Outcome: OutcomeSuccess, PrevHash: "p"}
	pre := `{"version":2,"id":"e1","timestamp":"2026-01-02T03:04:05Z","phase":"completion","request_id":"r","principal_id":"","transport":"","kb":"","tool":"t","read_only":false,"outcome":"success","duration_ms":0,"prev_hash":"p"}`
	sum := sha256.Sum256([]byte(pre))
	if got := computeHash(old); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash of an event without result_bytes changed: %s", got)
	}

	l, path := tempLog(t)
	for i, rb := range []int64{0, 42} {
		req := fmt.Sprintf("req-%d", i)
		if _, err := l.AppendEvent(Entry{RequestID: req, Phase: PhaseAttempt, Tool: "concept_read"}); err != nil {
			t.Fatal(err)
		}
		if _, err := l.AppendEvent(Entry{RequestID: req, Phase: PhaseCompletion, Tool: "concept_read", Outcome: OutcomeSuccess, ResultBytes: rb}); err != nil {
			t.Fatal(err)
		}
	}
	if s, err := verifyLog(path, nil); err != nil || s.Valid != 4 {
		t.Fatalf("mixed log: %+v %v", s, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"result_bytes":42`) {
		t.Fatal("result_bytes not written")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), `"result_bytes":42`, `"result_bytes":7`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := verifyLog(path, nil); err == nil && s.Valid == 4 {
		t.Fatal("tampered result_bytes still verifies")
	}
}

// SingleWriter keeps a second writer off the log, which would fork the chain it
// holds in memory, until the first closes it.
func TestSingleWriterLock(t *testing.T) {
	shortLockWait(t, 200*time.Millisecond)
	path := filepath.Join(t.TempDir(), "audit.log")
	first, err := OpenWithOptions(path, Options{SingleWriter: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWithOptions(path, Options{SingleWriter: true}); !errors.Is(err, ErrLocked) {
		t.Fatalf("second writer: err = %v, want ErrLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := OpenWithOptions(path, Options{SingleWriter: true})
	if err != nil {
		t.Fatalf("after Close: %v", err)
	}
	again.Close()
}

// TestVerifyAuditRefusesAnUnsignedEntryAfterTheFirstSignature: a log that never
// rotated has no checkpoint, yet an unsigned entry after a signed one is still
// a downgrade. The unsigned prefix before the first signature stays accepted.
func TestVerifyAuditRefusesAnUnsignedEntryAfterTheFirstSignature(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range []*KeyPair{nil, &kp} {
		l, err := open(path, key, Options{})
		if err != nil {
			t.Fatal(err)
		}
		appendCall(t, l, fmt.Sprintf("run-%d", i))
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := VerifyAudit(path, "", kp.Public, VerifyOptions{}); err != nil {
		t.Fatalf("unsigned prefix then signed = %v, want accepted", err)
	}
	l, err := open(path, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	appendCall(t, l, "run-2")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAudit(path, "", kp.Public, VerifyOptions{}); err == nil || !strings.Contains(err.Error(), "unsigned event") {
		t.Errorf("VerifyAudit = %v, want the unsigned entry reported", err)
	}
}

func shortLockWait(t *testing.T, d time.Duration) {
	t.Helper()
	old := LockWait
	LockWait = d
	t.Cleanup(func() { LockWait = old })
}

// A writer started while its predecessor is still exiting waits for the lock;
// one that stays held past the wait is refused.
func TestSingleWriterLockWaitsForTheExitingHolder(t *testing.T) {
	shortLockWait(t, 5*time.Second)
	path := filepath.Join(t.TempDir(), "audit.log")
	first, err := OpenWithOptions(path, Options{SingleWriter: true})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		first.Close()
	}()
	second, err := OpenWithOptions(path, Options{SingleWriter: true})
	if err != nil {
		t.Fatalf("open while the holder exits: %v", err)
	}
	second.Close()

	held, err := OpenWithOptions(path, Options{SingleWriter: true})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	LockWait = 250 * time.Millisecond
	start := time.Now()
	if _, err := OpenWithOptions(path, Options{SingleWriter: true}); !errors.Is(err, ErrLocked) {
		t.Fatalf("held throughout: err = %v, want ErrLocked", err)
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Errorf("gave up after %v, before the wait", time.Since(start))
	}
}

// unsignedLog writes a log that never had a key: what a rewrite that
// recomputed the chain and dropped every signature leaves on disk, since the
// signature is not part of the hash.
func unsignedLog(t *testing.T, path, archive string) []Entry {
	t.Helper()
	legacyLog(t, path, archive)
	es, err := readEntries(path)
	if err != nil || len(es) == 0 {
		t.Fatalf("readEntries = %v, %v", es, err)
	}
	return es
}

// TestVerifyAuditRefusesALogWithNoSignedEntry: with a key, a log in which
// nothing is signed and no start is declared cannot be authenticated. Without
// a key the chain alone is checked, as before.
func TestVerifyAuditRefusesALogWithNoSignedEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "archive")
	es := unsignedLog(t, path, archive)
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	sum, err := VerifyAudit(path, archive, kp.Public, VerifyOptions{})
	if err == nil || !strings.Contains(err.Error(), "no signed entry") || sum.Corrupt != 1 {
		t.Errorf("VerifyAudit with a key = %+v, %v; want the no signed entry refusal", sum, err)
	}
	if _, err := VerifyAudit(path, archive, nil, VerifyOptions{}); err != nil {
		t.Errorf("VerifyAudit without a key = %v, want the chain accepted", err)
	}

	before := VerifyOptions{SignedSince: es[0].Timestamp.Add(-time.Second)}
	if _, err := VerifyAudit(path, archive, kp.Public, before); err == nil || !strings.Contains(err.Error(), "unsigned event") {
		t.Errorf("VerifyAudit with SignedSince before the log = %v, want the unsigned event reported", err)
	}
	after := VerifyOptions{SignedSince: es[len(es)-1].Timestamp.Add(time.Second)}
	if _, err := VerifyAudit(path, archive, kp.Public, after); err != nil {
		t.Errorf("VerifyAudit with SignedSince after the log = %v, want it accepted", err)
	}
}

// TestVerifyAuditSignedSinceOnAnUpgradedLog: the legacy prefix is accepted
// with no declared start, with one at the upgrade, and refused when the
// operator claims the log was signed before the legacy entries were written.
func TestVerifyAuditSignedSinceOnAnUpgradedLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "archive")
	legacy := unsignedLog(t, path, archive)
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := OpenWithKeyAndOptions(path, kp, Options{ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	appendCall(t, signed, "new")
	if err := signed.Close(); err != nil {
		t.Fatal(err)
	}
	all, err := readEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	upgrade := all[len(legacy)].Timestamp

	if _, err := VerifyAudit(path, archive, kp.Public, VerifyOptions{}); err != nil {
		t.Errorf("no declared start = %v, want accepted", err)
	}
	if _, err := VerifyAudit(path, archive, kp.Public, VerifyOptions{SignedSince: upgrade}); err != nil {
		t.Errorf("SignedSince at the upgrade = %v, want accepted", err)
	}
	early := VerifyOptions{SignedSince: legacy[0].Timestamp.Add(-time.Second)}
	if _, err := VerifyAudit(path, archive, kp.Public, early); err == nil || !strings.Contains(err.Error(), "unsigned event") {
		t.Errorf("SignedSince before the legacy entries = %v, want the unsigned event reported", err)
	}
}

// TestVerifyAuditAcceptsAnEmptyLogWithAKey: no entry, nothing to authenticate.
func TestVerifyAuditAcceptsAnEmptyLogWithAKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAudit(path, filepath.Join(dir, "archive"), kp.Public, VerifyOptions{}); err != nil {
		t.Errorf("VerifyAudit of an empty log = %v, want accepted", err)
	}
}

// A signed line is verified as the bytes on disk, not as the values a JSON
// decoder extracts from them: anything the hash does not cover, and any
// alternative spelling of a covered key, fails verification.
func TestVerifyRefusesALineThatIsNotTheWrittenForm(t *testing.T) {
	forge := func(t *testing.T, edit func(line string) string) {
		t.Helper()
		kp, _ := GenerateKeyPair()
		path := filepath.Join(t.TempDir(), "audit.jsonl")
		l, err := OpenWithKeyAndOptions(path, kp, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.AppendEvent(Entry{RequestID: "r", Phase: PhaseAttempt, Tool: "concept_read", PrincipalID: "alice"}); err != nil {
			t.Fatal(err)
		}
		if _, err := l.AppendEvent(Entry{RequestID: "r", Phase: PhaseCompletion, Tool: "concept_read", PrincipalID: "alice", Outcome: OutcomeSuccess}); err != nil {
			t.Fatal(err)
		}
		l.Close()
		if _, err := VerifyAudit(path, "", kp.Public, VerifyOptions{}); err != nil {
			t.Fatalf("untouched log: %v", err)
		}
		data, _ := os.ReadFile(path)
		lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		forged := edit(lines[0])
		if forged == lines[0] {
			t.Fatal("edit had no effect")
		}
		lines[0] = forged
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyAudit(path, "", kp.Public, VerifyOptions{}); err == nil {
			t.Error("VerifyAudit accepts a forged line")
		}
		if _, err := verifyLog(path, kp.Public); err == nil {
			t.Error("VerifyFile accepts a forged line")
		}
	}
	cases := map[string]func(string) string{
		"args added":       func(l string) string { return strings.Replace(l, `{"version":2`, `{"version":2,"args":"x"`, 1) },
		"agent_id added":   func(l string) string { return strings.Replace(l, `"tool":`, `"agent_id":"mallory","tool":`, 1) },
		"case-variant key": func(l string) string { return strings.TrimSuffix(l, "}") + `,"PRINCIPAL_ID":"alice"}` },
		"forged value then original under another case": func(l string) string {
			return strings.Replace(strings.TrimSuffix(l, "}"), `"principal_id":"alice"`, `"principal_id":"mallory"`, 1) + `,"PRINCIPAL_ID":"alice"}`
		},
		"duplicate key":         func(l string) string { return strings.TrimSuffix(l, "}") + `,"tool":"concept_read"}` },
		"unknown key":           func(l string) string { return strings.TrimSuffix(l, "}") + `,"note":"x"}` },
		"CR before the newline": func(l string) string { return l + "\r" },
		"timestamp at another offset": func(l string) string {
			m := regexp.MustCompile(`"timestamp":"([^"]+)"`).FindStringSubmatch(l)
			ts, err := time.Parse(time.RFC3339Nano, m[1])
			if err != nil {
				t.Fatal(err)
			}
			moved := ts.In(time.FixedZone("", 2*3600)).Format(time.RFC3339Nano)
			return strings.Replace(l, m[1], moved, 1)
		},
		"whitespace": func(l string) string { return strings.Replace(l, `"version":2,`, `"version": 2,`, 1) },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) { forge(t, edit) })
	}
}

// Lines written by earlier releases keep verifying: the first release's
// layout (args, agent_id and duration_ms always present) and the layout of a
// legacy entry written through the deprecated Append.
func TestVerifyAcceptsLinesOfEarlierReleases(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	e1 := Entry{Timestamp: ts, Tool: "concept_read", Args: "{}", AgentID: "a", Outcome: "ok", PrevHash: "genesis"}
	e1.Hash = computeHash(e1)
	e2 := Entry{Timestamp: ts, Tool: "lint", Outcome: "ok", PrevHash: e1.Hash}
	e2.Hash = computeHash(e2)
	old1 := fmt.Sprintf(`{"timestamp":"2026-01-02T03:04:05Z","tool":"concept_read","args":"{}","agent_id":"a","outcome":"ok","duration_ms":0,"prev_hash":"genesis","hash":%q}`, e1.Hash)
	old2 := fmt.Sprintf(`{"timestamp":"2026-01-02T03:04:05Z","tool":"lint","args":"","agent_id":"","outcome":"ok","duration_ms":0,"prev_hash":%q,"hash":%q}`, e1.Hash, e2.Hash)
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, []byte(old1+"\n"+old2+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := verifyLog(path, nil); err != nil || s.Valid != 2 {
		t.Fatalf("first-release layout: %+v %v", s, err)
	}
	if s, err := VerifyAudit(path, "", nil, VerifyOptions{}); err != nil || s.Valid != 2 {
		t.Fatalf("first-release layout, VerifyAudit: %+v %v", s, err)
	}
}

// A v2 event carries no legacy-only field, whatever its hash says.
func TestValidV2OutcomeRefusesLegacyFields(t *testing.T) {
	if validV2Outcome(Entry{Phase: PhaseAttempt, Args: "x"}) || validV2Outcome(Entry{Phase: PhaseAttempt, AgentID: "x"}) {
		t.Fatal("a v2 event with args or agent_id is accepted")
	}
}

// A legacy (version 0) line is hashed over six fields only: a signed one must
// not gain any field of the later eras, nor a timestamp at another offset.
func TestVerifyRefusesAnnotatedLegacyLines(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	e := Entry{Timestamp: ts, Tool: "lint", Outcome: "ok", PrevHash: "genesis"}
	e.Hash = computeHash(e)
	line := func(extra string) string {
		return fmt.Sprintf(`{"timestamp":"2026-01-02T03:04:05Z",%s"tool":"lint","outcome":"ok","prev_hash":"genesis","hash":%q}`, extra, e.Hash)
	}
	check := func(l string, wantOK bool) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "audit.jsonl")
		if err := os.WriteFile(path, []byte(l+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := verifyLog(path, nil)
		if (err == nil) != wantOK {
			t.Fatalf("%s: err=%v, want ok=%v", l, err, wantOK)
		}
	}
	check(line(""), true)
	check(line(`"kb":"kb-a",`), false)
	check(line(`"principal_id":"mallory",`), false)
	check(line(`"id":"x","phase":"attempt",`), false)
	check(line(`"read_only":true,`), false)
	check(line(`"resources":{"id":"x"},`), false)
	// Same instant, another offset.
	check(strings.Replace(line(""), "2026-01-02T03:04:05Z", "2026-01-02T05:04:05+02:00", 1), false)
}

// ev appends one event; with MaxBytes 1 every event after the first rotates.
func ev(l *Log) error {
	_, err := l.AppendEvent(Entry{RequestID: newID(), Phase: PhaseAttempt, Tool: "t"})
	return err
}

// verifyChain verifies the whole chain; the single-phase events of these tests
// are attempts with no completion, which is the only finding tolerated.
func verifyChain(path, archive string, pub ed25519.PublicKey) (Summary, error) {
	sum, err := VerifyAudit(path, archive, pub, VerifyOptions{})
	if err != nil && strings.Contains(err.Error(), "without completion") && sum.Corrupt == 0 {
		return sum, nil
	}
	return sum, err
}

func rotatingLog(t *testing.T, kp *KeyPair) (*Log, string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "archive")
	o := Options{MaxBytes: 1, ArchiveDir: archive}
	var l *Log
	var err error
	if kp != nil {
		l, err = OpenWithKeyAndOptions(path, *kp, o)
	} else {
		l, err = OpenWithOptions(path, o)
	}
	if err != nil {
		t.Fatal(err)
	}
	return l, path, archive
}

// A rename that fails must leave neither a phantom checkpoint in the index
// file nor a changed in-memory index, so a later rotation verifies (#1500).
func TestRotationRenameFailureLeavesNoPhantomCheckpoint(t *testing.T) {
	l, path, archive := rotatingLog(t, nil)
	if err := ev(l); err != nil {
		t.Fatal(err)
	}
	orig := renameFunc
	renameFunc = func(string, string) error { return errors.New("injected rename failure") }
	for i := 0; i < 3; i++ {
		if err := ev(l); err == nil {
			t.Fatal("expected the rotation to fail")
		}
	}
	renameFunc = orig
	if n := len(l.index.Segments); n != 0 || l.sequence != 0 {
		t.Fatalf("in-memory state moved: %d segments, sequence %d", n, l.sequence)
	}
	if _, err := os.Stat(checkpointPath(path, archive)); !os.IsNotExist(err) {
		t.Fatalf("a phantom checkpoint index was left behind: %v", err)
	}
	if err := ev(l); err != nil {
		t.Fatal(err)
	}
	if len(l.index.Segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(l.index.Segments))
	}
	if _, err := verifyChain(path, archive, nil); err != nil {
		t.Fatalf("verify after recovery: %v", err)
	}
}

// With a previous checkpoint, the failed rotation restores that index file.
func TestRotationRenameFailureRestoresThePreviousIndex(t *testing.T) {
	l, path, archive := rotatingLog(t, nil)
	for i := 0; i < 2; i++ {
		if err := ev(l); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(checkpointPath(path, archive))
	if err != nil {
		t.Fatal(err)
	}
	orig := renameFunc
	renameFunc = func(string, string) error { return errors.New("injected rename failure") }
	_ = ev(l)
	renameFunc = orig
	after, _ := os.ReadFile(checkpointPath(path, archive))
	if string(before) != string(after) {
		t.Fatalf("index changed by a failed rotation:\n%s\n%s", before, after)
	}
	if err := ev(l); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyChain(path, archive, nil); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// An archive_dir on another volume fails the rename with EXDEV: the segment is
// copied, the active log truncated, and the chain still verifies.
func TestRotationFallsBackToCopyOnEXDEV(t *testing.T) {
	l, path, archive := rotatingLog(t, nil)
	orig := renameFunc
	renameFunc = func(a, b string) error {
		return &os.LinkError{Op: "rename", Old: a, New: b, Err: syscall.EXDEV}
	}
	defer func() { renameFunc = orig }()
	for i := 0; i < 3; i++ {
		if err := ev(l); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if len(l.index.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(l.index.Segments))
	}
	var n int
	ents, _ := os.ReadDir(archive)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			n++
		} else if strings.HasPrefix(e.Name(), ".audit-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if n != 2 {
		t.Fatalf("archived segments = %d, want 2", n)
	}
	if _, err := verifyChain(path, archive, nil); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// A crash between the durable index write and the rename leaves a checkpoint
// whose segment is missing while the active log still holds its entries: open
// drops the entry (re-signed) and the log verifies.
func TestOpenReconcilesTheCrashWindowOfARotation(t *testing.T) {
	for _, signed := range []bool{false, true} {
		var kp *KeyPair
		var pub ed25519.PublicKey
		if signed {
			k, err := GenerateKeyPair()
			if err != nil {
				t.Fatal(err)
			}
			kp, pub = &k, k.Public
		}
		l, path, archive := rotatingLog(t, kp)
		if err := ev(l); err != nil {
			t.Fatal(err)
		}
		entries, err := readEntries(path)
		if err != nil {
			t.Fatal(err)
		}
		// Simulate the crash: write the index as rotateLocked would, never rename.
		cp := Checkpoint{Name: "missing.000001.jsonl", Start: entries[0].Timestamp, End: entries[0].Timestamp, Count: 1,
			PrevHash: entries[0].PrevHash, FirstHash: entries[0].Hash, LastHash: entries[0].Hash, SigningRequired: signed}
		idx := checkpointIndex{Version: 1, Segments: []Checkpoint{cp}}
		if signed {
			idx.Sig = hex.EncodeToString(ed25519.Sign(kp.Private, checkpointBytes(idx)))
		}
		data, _ := json.Marshal(idx)
		if err := os.MkdirAll(archive, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(checkpointPath(path, archive), data, 0o640); err != nil {
			t.Fatal(err)
		}
		l.Close()

		var l2 *Log
		o := Options{MaxBytes: 1, ArchiveDir: archive, SingleWriter: true}
		if signed {
			l2, err = OpenWithKeyAndOptions(path, *kp, o)
		} else {
			l2, err = OpenWithOptions(path, o)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l2.Close() })
		if len(l2.index.Segments) != 0 || l2.sequence != 0 {
			t.Fatalf("signed=%v: index not reconciled: %+v", signed, l2.index)
		}
		if err := ev(l2); err != nil {
			t.Fatal(err)
		}
		if _, err := verifyChain(path, archive, pub); err != nil {
			t.Fatalf("signed=%v: verify: %v", signed, err)
		}
	}
}

// crashState leaves the index a rotation wrote durably for the one entry in
// the active log, with the segment copied to the archive (withSegment) or not.
func crashState(t *testing.T, path, archive string, kp *KeyPair, withSegment bool) []byte {
	t.Helper()
	entries, err := readEntries(path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("active entries = %d, %v", len(entries), err)
	}
	e := entries[0]
	cp := Checkpoint{Name: "seg.000001.jsonl", Start: e.Timestamp, End: e.Timestamp, Count: 1,
		PrevHash: e.PrevHash, FirstHash: e.Hash, LastHash: e.Hash, SigningRequired: kp != nil}
	idx := checkpointIndex{Version: 1, Segments: []Checkpoint{cp}}
	if kp != nil {
		idx.Sig = hex.EncodeToString(ed25519.Sign(kp.Private, checkpointBytes(idx)))
	}
	data, _ := json.Marshal(idx)
	if err := os.MkdirAll(archive, 0o750); err != nil {
		t.Fatal(err)
	}
	if withSegment {
		raw, _ := os.ReadFile(path)
		if err := os.WriteFile(filepath.Join(archive, cp.Name), raw, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(checkpointPath(path, archive), data, 0o640); err != nil {
		t.Fatal(err)
	}
	return data
}

func reopen(t *testing.T, path, archive string, kp *KeyPair, single bool) *Log {
	t.Helper()
	o := Options{MaxBytes: 1, ArchiveDir: archive, SingleWriter: single}
	var l *Log
	var err error
	if kp != nil {
		l, err = OpenWithKeyAndOptions(path, *kp, o)
	} else {
		l, err = OpenWithOptions(path, o)
	}
	if err != nil {
		t.Fatal(err)
	}
	// A SingleWriter log holds <path>.lock open; Windows cannot remove it
	// while the handle lives (#1680).
	t.Cleanup(func() { l.Close() })
	return l
}

// A crash after the EXDEV copy and before the truncate leaves the segment
// archived and the active log holding the same entries: open finishes the
// rotation instead of archiving them twice.
func TestOpenFinishesAnInterruptedCopyRotation(t *testing.T) {
	for _, signed := range []bool{false, true} {
		var kp *KeyPair
		var pub ed25519.PublicKey
		if signed {
			k, _ := GenerateKeyPair()
			kp, pub = &k, k.Public
		}
		l, path, archive := rotatingLog(t, kp)
		if err := ev(l); err != nil {
			t.Fatal(err)
		}
		l.Close()
		crashState(t, path, archive, kp, true)
		l2 := reopen(t, path, archive, kp, true)
		if n, _ := countEntries(l2); n != 0 {
			t.Fatalf("signed=%v: active log still holds %d entries", signed, n)
		}
		if len(l2.index.Segments) != 1 {
			t.Fatalf("signed=%v: segments = %d, want 1", signed, len(l2.index.Segments))
		}
		if err := ev(l2); err != nil {
			t.Fatal(err)
		}
		if _, err := verifyChain(path, archive, pub); err != nil {
			t.Fatalf("signed=%v: verify: %v", signed, err)
		}
	}
}

// An index whose signature does not verify is never re-signed: a forged last
// entry must not launder a tampered index into a signed one (#1500 review).
func TestOpenDoesNotReconcileAnIndexItCannotVerify(t *testing.T) {
	k, _ := GenerateKeyPair()
	kp := &k
	l, path, archive := rotatingLog(t, kp)
	for i := 0; i < 2; i++ {
		if err := ev(l); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()
	raw, err := os.ReadFile(checkpointPath(path, archive))
	if err != nil {
		t.Fatal(err)
	}
	var idx checkpointIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	entries, _ := readEntries(path)
	idx.Segments[0].Count += 7 // tamper with an earlier checkpoint
	idx.Segments = append(idx.Segments, Checkpoint{Name: "missing.000002.jsonl", FirstHash: entries[0].Hash,
		LastHash: entries[0].Hash, PrevHash: idx.Segments[0].LastHash, Count: 1, SigningRequired: true})
	forged, _ := json.Marshal(idx) // the old signature no longer matches
	if err := os.WriteFile(checkpointPath(path, archive), forged, 0o640); err != nil {
		t.Fatal(err)
	}
	l2 := reopen(t, path, archive, kp, true)
	l2.Close()
	after, _ := os.ReadFile(checkpointPath(path, archive))
	if string(after) != string(forged) {
		t.Fatal("the unverified index was rewritten (re-signed)")
	}
	if _, err := VerifyAudit(path, archive, k.Public, VerifyOptions{}); err == nil {
		t.Fatal("verify accepted the tampered index")
	}
}

// Without the writer lock another process may be mid-rotation: open repairs
// nothing.
func TestOpenWithoutTheWriterLockDoesNotReconcile(t *testing.T) {
	l, path, archive := rotatingLog(t, nil)
	if err := ev(l); err != nil {
		t.Fatal(err)
	}
	l.Close()
	data := crashState(t, path, archive, nil, false)
	reopen(t, path, archive, nil, false).Close()
	after, _ := os.ReadFile(checkpointPath(path, archive))
	if string(after) != string(data) {
		t.Fatal("the index was reconciled without the writer lock")
	}
}

// A forged keyless index plus a junk file at the archive path must not
// truncate the active log: the archived segment has to equal it.
func TestOpenDoesNotTruncateWhenTheArchivedSegmentDiffers(t *testing.T) {
	l, path, archive := rotatingLog(t, nil)
	if err := ev(l); err != nil {
		t.Fatal(err)
	}
	l.Close()
	crashState(t, path, archive, nil, true)
	if err := os.WriteFile(filepath.Join(archive, "seg.000001.jsonl"), []byte("junk\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	reopen(t, path, archive, nil, true).Close()
	after, _ := os.ReadFile(path)
	if len(before) == 0 || string(before) != string(after) {
		t.Fatal("the active log was truncated against a segment that does not match it")
	}
}

// TestAttributionFieldsKeepOldHashes pins D1894: a record without
// principal_kind and via hashes as before they existed, a log mixing old and
// new records verifies, and a tampered via breaks the chain.
func TestAttributionFieldsKeepOldHashes(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	old := Entry{Version: VersionV2, ID: "e1", Timestamp: ts, Phase: PhaseCompletion, RequestID: "r", Tool: "t", Outcome: OutcomeSuccess, PrevHash: "p", ResultBytes: 5}
	pre := `{"version":2,"id":"e1","timestamp":"2026-01-02T03:04:05Z","phase":"completion","request_id":"r","principal_id":"","transport":"","kb":"","tool":"t","read_only":false,"outcome":"success","duration_ms":0,"prev_hash":"p","result_bytes":5}`
	sum := sha256.Sum256([]byte(pre))
	if got := computeHash(old); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash of an old record changed: %s", got)
	}
	l, path := tempLog(t)
	for i, via := range []string{"", "sso-token"} {
		req := fmt.Sprintf("req-%d", i)
		if _, err := l.AppendEvent(Entry{RequestID: req, Phase: PhaseAttempt, Tool: "t", PrincipalID: "user:a", PrincipalKind: "human", Via: via}); err != nil {
			t.Fatal(err)
		}
		if _, err := l.AppendEvent(Entry{RequestID: req, Phase: PhaseCompletion, Tool: "t", Outcome: OutcomeSuccess, PrincipalID: "user:a", PrincipalKind: "human", Via: via}); err != nil {
			t.Fatal(err)
		}
	}
	if s, err := verifyLog(path, nil); err != nil || s.Valid != 4 {
		t.Fatalf("mixed log: %+v %v", s, err)
	}
	data, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(data), `"via":"sso-token"`, `"via":"other"`, 1)), 0o600)
	if s, err := verifyLog(path, nil); err == nil && s.Valid == 4 {
		t.Fatal("tampered via still verifies")
	}
}
