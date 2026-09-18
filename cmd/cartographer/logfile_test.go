package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAppendLog_CreatesTheDirectoryAndAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Logs", "server.log")

	f, err := openAppendLog(path)
	if err != nil {
		t.Fatalf("openAppendLog: %v", err)
	}
	if _, err := f.WriteString("first\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Append, not truncate: a restarted service must not erase the log that
	// explains why it restarted.
	f2, err := openAppendLog(path)
	if err != nil {
		t.Fatalf("second openAppendLog: %v", err)
	}
	if _, err := f2.WriteString("second\n"); err != nil {
		t.Fatal(err)
	}
	f2.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got := string(data); got != "first\nsecond\n" {
		t.Errorf("log content = %q, want both lines in order", got)
	}
}

// A service that silently logs nowhere is the failure this flag exists to
// prevent, so an unopenable path must be an error naming it — not a warning.
func TestOpenAppendLog_UnopenablePathFails(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "notadir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "server.log")
	_, err := openAppendLog(path)
	if err == nil {
		t.Fatal("openAppendLog under a regular file should fail")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %v, want it to name the path", err)
	}
}

func TestRedirectServerLog_SendsTheStandardLoggerToTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	origOut, origFlags := log.Writer(), log.Flags()
	stdoutBefore := os.Stdout
	t.Cleanup(func() { log.SetOutput(origOut); log.SetFlags(origFlags) })

	f, err := redirectServerLog(path)
	if err != nil {
		t.Fatalf("redirectServerLog: %v", err)
	}
	log.Printf("hello from the service")
	f.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), "hello from the service") {
		t.Errorf("log file = %q, want the logged line", data)
	}
	// Stdout is untouched on purpose: in stdio transport it carries the MCP
	// protocol itself, and redirecting it would move the protocol into the log.
	if os.Stdout != stdoutBefore {
		t.Error("redirectServerLog reassigned os.Stdout, which in stdio transport is the protocol")
	}
}

func TestRedirectClientOutput_CapturesStdoutAndStderr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync.log")
	origOut, origStd, origErr := log.Writer(), os.Stdout, os.Stderr
	t.Cleanup(func() { log.SetOutput(origOut); os.Stdout, os.Stderr = origStd, origErr })

	f, err := redirectClientOutput(path)
	if err != nil {
		t.Fatalf("redirectClientOutput: %v", err)
	}
	// A sync reports itself with fmt.Print, so capturing only the log package
	// would leave the actual report in a scheduled task's void.
	if _, err := os.Stdout.WriteString("sync summary\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stderr.WriteString("sync warning\n"); err != nil {
		t.Fatal(err)
	}
	log.Print("sync log line")
	f.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	for _, want := range []string{"sync summary", "sync warning", "sync log line"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log file = %q, want it to contain %q", data, want)
		}
	}
}
