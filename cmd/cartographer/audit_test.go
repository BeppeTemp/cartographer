package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/audit"
)

func TestRunAuditVerifyExitCodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := audit.OpenWithOptions(path, audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.AppendEvent(audit.Entry{RequestID: "r", Phase: audit.PhaseAttempt, Tool: "concept_read"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.AppendEvent(audit.Entry{RequestID: "r", Phase: audit.PhaseCompletion, Tool: "concept_read", Outcome: audit.OutcomeSuccess}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	kp, err := audit.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	pub := hex.EncodeToString(kp.Public)
	later := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	cases := []struct {
		name string
		f    auditFlags
		want int
	}{
		{"no key, chain only", auditFlags{log: path}, 0},
		{"bad timestamp", auditFlags{log: path, publicKey: pub, signedSince: "yesterday"}, 2},
		{"signed-since without a key", auditFlags{log: path, signedSince: later}, 2},
		{"key and an unsigned log", auditFlags{log: path, publicKey: pub}, 1},
		{"key and a declared start after the log", auditFlags{log: path, publicKey: pub, signedSince: later}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := runAuditVerify(c.f); code != c.want {
				t.Errorf("exit = %d, want %d", code, c.want)
			}
		})
	}
}

// A stdout that cannot be written (a closed pipe, a full disk) fails the
// export: a truncated document must not look successful (#1500).
func TestAuditExportToAClosedStdoutFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := audit.OpenWithOptions(path, audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.AppendEvent(audit.Entry{RequestID: "r", Phase: audit.PhaseAttempt, Tool: "concept_read"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.AppendEvent(audit.Entry{RequestID: "r", Phase: audit.PhaseCompletion, Tool: "concept_read", Outcome: audit.OutcomeSuccess}); err != nil {
		t.Fatal(err)
	}
	l.Close()

	closed, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	saved := os.Stdout
	os.Stdout = closed
	defer func() { os.Stdout = saved }()
	if code := cmdAuditExport([]string{"--log", path}); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}
