package audit

import (
	"os"
	"strings"
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
