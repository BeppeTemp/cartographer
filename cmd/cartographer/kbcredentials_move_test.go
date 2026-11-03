package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

func writeKBCredential(t *testing.T, dataDir, file string) string {
	t.Helper()
	dir := filepath.Join(dataDir, "credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, file)
	if err := os.WriteFile(p, []byte("secret-"+file), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The credential files are looked up by KB name (D905): left behind, the renamed
// KB loses its key, its decryption and its manifest signature.
func TestCmdKBRename_MovesTheKBsCredentialFiles(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	withClientServerURL(t, "")
	dataDir := seedRenameKB(t, "kb-a")
	writeKBCredential(t, dataDir, "kb-a.ssh")
	writeKBCredential(t, dataDir, "kb-a.signing")
	other := writeKBCredential(t, dataDir, "kb-z.ssh")
	cfgPath := writeServerConfig(t, "http: \"127.0.0.1:39273\"\ndata: "+dataDir+"\n")

	var code int
	out := withStdout(t, func() {
		withNoGuidance(t, func() { code = cmdKBRename([]string{"kb-a", "kb-b", "--data", dataDir, "--config", cfgPath}) })
	})
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	cred := filepath.Join(dataDir, "credentials")
	for _, kind := range []string{"ssh", "signing"} {
		if _, err := os.Stat(filepath.Join(cred, "kb-b."+kind)); err != nil {
			t.Errorf("kb-b.%s missing after the rename: %v", kind, err)
		}
		if _, err := os.Stat(filepath.Join(cred, "kb-a."+kind)); !os.IsNotExist(err) {
			t.Errorf("kb-a.%s was left behind", kind)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another KB's credential was touched: %v", err)
	}
}

// A failed later step puts the credential files back with the directory.
func TestApplyKBRename_FailureRestoresTheCredentialFiles(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	dataDir := seedRenameKB(t, "kb-a")
	from := writeKBCredential(t, dataDir, "kb-a.ssh")
	to := filepath.Join(dataDir, "credentials", "kb-b.ssh")
	// An entry index the config does not have makes the config step fail.
	cfgPath := writeServerConfig(t, "kbs: []\n")
	p := &renamePlan{
		Old: "kb-a", New: "kb-b",
		OldPath: filepath.Join(dataDir, "kb-a"), NewPath: filepath.Join(dataDir, "kb-b"),
		DataDir: dataDir, ConfigPath: cfgPath, EntryIndex: 0,
		Credentials: []credMove{{From: from, To: to}},
	}
	if err := applyKBRename(p, nil); err == nil {
		t.Fatal("the failing config step was not reported")
	}
	if _, err := os.Stat(from); err != nil {
		t.Errorf("the credential file was not restored: %v", err)
	}
	if _, err := os.Stat(to); !os.IsNotExist(err) {
		t.Error("the renamed credential file survived the rollback")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "kb-a", "data", "index.md")); err != nil {
		t.Errorf("the directory was not restored: %v", err)
	}
}

func TestPlanKBRename_RefusesToOverwriteACredential(t *testing.T) {
	dataDir := seedRenameKB(t, "kb-a")
	writeKBCredential(t, dataDir, "kb-a.ssh")
	writeKBCredential(t, dataDir, "kb-b.ssh")
	if _, err := planKBRename("kb-a", "kb-b", dataDir, ""); err == nil || !strings.Contains(err.Error(), "kb-b.ssh") {
		t.Errorf("planKBRename = %v, want a refusal naming kb-b.ssh", err)
	}
}

// unmount leaves the credential files and names them.
func TestCmdKBUnmount_ListsTheCredentialFilesItLeaves(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	withClientServerURL(t, "")
	dataDir, _ := seedPushedKB(t, "kb-a")
	left := writeKBCredential(t, dataDir, "kb-a.age")
	code, out := runUnmount(t, "kb-a", "--data", dataDir, "--config", writeServerConfig(t, "data: "+dataDir+"\n"))
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, left) {
		t.Errorf("the leftover credential file is not listed:\n%s", out)
	}
	if _, err := os.Stat(left); err != nil {
		t.Errorf("unmount removed a credential file: %v", err)
	}
}

// With the KB's lock held by another holder, rename and unmount fail with the
// lock message and change nothing; once it is free they proceed.
func TestRenameAndUnmount_RespectTheKBLock(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	withClientServerURL(t, "")
	dataDir, _ := seedPushedKB(t, "kb-a")
	cfgPath := writeServerConfig(t, "data: "+dataDir+"\n")
	holder, err := kb.Open(filepath.Join(dataDir, "kb-a"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := holder.AcquireProcessLock(0, "the server")
	if err != nil {
		t.Fatal(err)
	}

	var code int
	stderr := withStderr(t, func() {
		withStdout(t, func() {
			withNoGuidance(t, func() { code = cmdKBRename([]string{"kb-a", "kb-b", "--data", dataDir, "--config", cfgPath}) })
		})
	})
	if code == 0 || !strings.Contains(stderr, "the server") {
		t.Errorf("rename with the lock held = %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "kb-a", "data", "index.md")); err != nil {
		t.Errorf("rename moved the KB despite the lock: %v", err)
	}
	stderr = withStderr(t, func() {
		code, _ = runUnmount(t, "kb-a", "--data", dataDir, "--config", cfgPath)
	})
	if code == 0 || !strings.Contains(stderr, "the server") {
		t.Errorf("unmount with the lock held = %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "kb-a")); err != nil {
		t.Errorf("unmount removed the clone despite the lock: %v", err)
	}

	release()
	withStdout(t, func() {
		withNoGuidance(t, func() { code = cmdKBRename([]string{"kb-a", "kb-b", "--data", dataDir, "--config", cfgPath}) })
	})
	if code != 0 {
		t.Errorf("rename after the lock was released = %d", code)
	}
}
