package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/hooks"
)

func sinkRegistry(t *testing.T, blocking bool) *hooks.Registry {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "acme.token"), []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := hooks.NewRegistry([]hooks.Hook{{
		Name: "acme/siem", Point: hooks.PointAuditSink, Plugin: "acme", URL: "http://127.0.0.1:1/sink",
		Credential: "acme.token", FailurePolicy: hooks.FailOpen, Blocking: blocking,
	}}, hooks.Options{CredentialsDir: dir, Approved: func(string, string) bool { return true }, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// An audit_sink without an audit log refuses the start; with one, the sinks
// start and stop, and only the blocking ones reach the KB servers.
func TestStartAuditSinks(t *testing.T) {
	if _, err := startAuditSinks(sinkRegistry(t, false), nil, ""); err == nil || !strings.Contains(err.Error(), "keeps no audit log") {
		t.Fatalf("no audit log: %v", err)
	}
	stop, err := startAuditSinks(nil, nil, "")
	if err != nil || serverBlockingSinks != nil {
		t.Fatalf("no hooks: %v", err)
	}
	stop()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := audit.OpenWithOptions(path, audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	stop, err = startAuditSinks(sinkRegistry(t, true), l, path)
	if err != nil || len(serverBlockingSinks) != 1 {
		t.Fatalf("blocking: %v, %d", err, len(serverBlockingSinks))
	}
	stop()
	serverBlockingSinks = nil
}
