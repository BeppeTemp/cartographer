package provisioning

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// Kiro CLI 3.x upgrades a V2 agent in place, adds `kiroUpgrade` and keeps the
// original as <file>.bak (#1717). The file is ours, upgraded: not drift, and a
// sync must not write the V2 bytes back (the loop: upgrade, drift, rewrite,
// upgrade...). One upgraded from anything else is still drift.
// upgradedFrom is what Kiro makes of the V2 bytes: every key kept, plus the
// derived `permissions` and the `kiroUpgrade` block.
func upgradedFrom(t *testing.T, v2 []byte) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(v2, &m); err != nil {
		t.Fatalf("agent is not JSON: %v\n%s", err, v2)
	}
	m["permissions"] = map[string]any{"rules": []any{}}
	m["kiroUpgrade"] = map[string]any{"revision": 2, "fingerprint": "sha256:aa", "source": "sha256:bb"}
	out, _ := json.MarshalIndent(m, "", "  ")
	return append(out, '\n')
}

func kiroAgentApplied(t *testing.T) (baseDir string, m Manifest, applied AppliedResult, kbRoot, agentFile string) {
	t.Helper()
	baseDir, m, applied, kbRoot = driftKB(t, func(o *ApplyOptions) { o.Provider = configurator.ProviderKiro })
	agentFile = filepath.Join(baseDir, ".kiro", "agents", "reviewer.json")
	if _, err := os.Stat(agentFile); err != nil {
		t.Fatalf("kiro agent not materialised: %v", err)
	}
	return
}

// simulateKiroUpgrade does what Kiro does: backup of the bytes on disk, then the
// upgraded file in their place.
func simulateKiroUpgrade(t *testing.T, agentFile string, bak []byte) []byte {
	t.Helper()
	cur, _ := os.ReadFile(agentFile)
	up := upgradedFrom(t, cur)
	if err := os.WriteFile(agentFile+".bak", bak, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentFile, up, 0o644); err != nil {
		t.Fatal(err)
	}
	return up
}

func TestVerifyManaged_KiroUpgradedFromOurBytesIsNotDrift(t *testing.T) {
	baseDir, m, applied, kbRoot, agentFile := kiroAgentApplied(t)
	ours, _ := os.ReadFile(agentFile)
	up := simulateKiroUpgrade(t, agentFile, ours)

	if f, ok := findingFor(VerifyManaged(applied.NewLock, configurator.ProviderKiro, baseDir), "agent", "reviewer"); ok {
		t.Fatalf("an agent Kiro upgraded from our bytes is reported: %+v", f)
	}

	// And a sync leaves it alone, the lock keeping our hash.
	again, err := Apply(m, ApplyOptions{ApprovedMCP: approveAll(m), KBRoots: map[string]string{"wiki": kbRoot},
		Provider: configurator.ProviderKiro, BaseDir: baseDir, Lock: applied.NewLock})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, _ := os.ReadFile(agentFile); string(got) != string(up) {
		t.Fatalf("sync rewrote the upgraded agent:\n%s", got)
	}
	for i, mf := range again.NewLock.Managed {
		if mf.Kind == "agent" && mf.MaterializedHash != applied.NewLock.Managed[i].MaterializedHash {
			t.Errorf("the lock lost our hash as the source: %+v", mf)
		}
	}
}

func TestVerifyManaged_KiroUpgradedFromForeignSourceIsDrift(t *testing.T) {
	cases := map[string]func(t *testing.T, agentFile string){
		"foreign backup": func(t *testing.T, f string) {
			simulateKiroUpgrade(t, f, []byte(`{"name":"reviewer","prompt":"someone else"}`))
		},
		"no backup": func(t *testing.T, f string) {
			cur, _ := os.ReadFile(f)
			if err := os.WriteFile(f, upgradedFrom(t, cur), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"backup is a link": func(t *testing.T, f string) {
			ours, _ := os.ReadFile(f)
			target := filepath.Join(t.TempDir(), "orig")
			os.WriteFile(target, ours, 0o644)
			if err := os.Symlink(target, f+".bak"); err != nil {
				t.Skipf("symlink: %v", err)
			}
			cur, _ := os.ReadFile(f)
			os.WriteFile(f, upgradedFrom(t, cur), 0o644)
		},
		"no kiroUpgrade block": func(t *testing.T, f string) {
			ours, _ := os.ReadFile(f)
			os.WriteFile(f+".bak", ours, 0o644)
			os.WriteFile(f, []byte(`{"name":"reviewer","prompt":"edited"}`), 0o644)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			baseDir, m, applied, kbRoot, agentFile := kiroAgentApplied(t)
			before, _ := os.ReadFile(agentFile)
			setup(t, agentFile)
			after, _ := os.ReadFile(agentFile)
			if f, ok := findingFor(VerifyManaged(applied.NewLock, configurator.ProviderKiro, baseDir), "agent", "reviewer"); !ok || f.Reason != DriftModified {
				t.Fatalf("expected drift, got %+v", f)
			}
			if _, err := Apply(m, ApplyOptions{ApprovedMCP: approveAll(m), KBRoots: map[string]string{"wiki": kbRoot},
				Provider: configurator.ProviderKiro, BaseDir: baseDir, Lock: applied.NewLock}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got, _ := os.ReadFile(agentFile); string(got) == string(after) && string(after) != string(before) {
				t.Errorf("a foreign upgrade was not healed")
			}
		})
	}
}

func kiroUpgradedLock(t *testing.T) (baseDir string, m Manifest, kbRoot, agentFile string, lock Lock) {
	t.Helper()
	baseDir, m, applied, kbRoot, agentFile := kiroAgentApplied(t)
	ours, _ := os.ReadFile(agentFile)
	simulateKiroUpgrade(t, agentFile, ours)
	again, err := Apply(m, ApplyOptions{ApprovedMCP: approveAll(m), KBRoots: map[string]string{"wiki": kbRoot},
		Provider: configurator.ProviderKiro, BaseDir: baseDir, Lock: applied.NewLock})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return baseDir, m, kbRoot, agentFile, again.NewLock
}

func agentEntry(t *testing.T, l Lock) ManagedFile {
	t.Helper()
	for _, mf := range l.Managed {
		if mf.Kind == "agent" && mf.Name == "reviewer" {
			return mf
		}
	}
	t.Fatal("no agent entry in the lock")
	return ManagedFile{}
}

// The first sync that accepts the upgrade records the file's hash; the file is
// then bound to it.
func TestApply_KiroUpgradeRecordsTheUpgradedHash(t *testing.T) {
	_, _, _, agentFile, lock := kiroUpgradedLock(t)
	data, _ := os.ReadFile(agentFile)
	if got, want := agentEntry(t, lock).UpgradedHash, agentBytesHash("reviewer", data); got == "" || got != want {
		t.Fatalf("UpgradedHash = %q, want %q", got, want)
	}
}

// A hand edit (or anyone widening permissions) after the upgrade was accepted is
// drift and is healed, even though the .bak still matches.
func TestVerifyManaged_KiroEditAfterAcceptanceIsDrift(t *testing.T) {
	baseDir, m, kbRoot, agentFile, lock := kiroUpgradedLock(t)
	if _, ok := findingFor(VerifyManaged(lock, configurator.ProviderKiro, baseDir), "agent", "reviewer"); ok {
		t.Fatal("the accepted upgrade is reported")
	}
	edited := `{"name":"reviewer","permissions":{"rules":[{"allow":"*"}]},"kiroUpgrade":{"revision":2}}`
	if err := os.WriteFile(agentFile, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, ok := findingFor(VerifyManaged(lock, configurator.ProviderKiro, baseDir), "agent", "reviewer"); !ok || f.Reason != DriftModified {
		t.Fatalf("expected drift, got %+v", f)
	}
	if _, err := Apply(m, ApplyOptions{ApprovedMCP: approveAll(m), KBRoots: map[string]string{"wiki": kbRoot},
		Provider: configurator.ProviderKiro, BaseDir: baseDir, Lock: lock}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, _ := os.ReadFile(agentFile); string(got) == edited {
		t.Error("the edited upgrade was not healed")
	}
}

// Kiro keeps every V2 key of the .bak; a different copy is an edit even before
// any hash is recorded.
func TestVerifyManaged_KiroWidenedRestrictionIsDrift(t *testing.T) {
	cases := map[string]struct{ bak, upgraded string }{
		"widened": {
			`{"name":"reviewer","allowedTools":["fs_read"]}`,
			`{"name":"reviewer","allowedTools":["fs_read","execute_bash"],"kiroUpgrade":{"revision":2}}`},
		"added": {
			`{"name":"reviewer"}`,
			`{"name":"reviewer","toolsSettings":{"x":1},"kiroUpgrade":{"revision":2}}`},
		"kept": {
			`{"name":"reviewer","allowedTools":["fs_read"],"toolsSettings":{"a":{"b":1}}}`,
			`{"name":"reviewer","toolsSettings":{"a":{"b":1}},"allowedTools":["fs_read"],"permissions":{"rules":[]},"kiroUpgrade":{"revision":2}}`},
		"prompt":     {`{"name":"r","prompt":"a"}`, `{"name":"r","prompt":"b","kiroUpgrade":{}}`},
		"mcpServers": {`{"name":"r","mcpServers":{"a":{"command":"x"}}}`, `{"name":"r","mcpServers":{"a":{"command":"y"}},"kiroUpgrade":{}}`},
		"removed":    {`{"name":"r","hooks":{"a":[]}}`, `{"name":"r","kiroUpgrade":{}}`},
		"tools":      {`{"name":"r","tools":["a"]}`, `{"name":"r","tools":["*"],"kiroUpgrade":{}}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := kiroKeysEqual([]byte(c.bak), []byte(c.upgraded)); got != (name == "kept") {
				t.Errorf("kiroKeysEqual = %v", got)
			}
		})
	}
	baseDir, _, applied, _, agentFile := kiroAgentApplied(t)
	ours, _ := os.ReadFile(agentFile)
	// Same bak as the lock, but the upgraded file carries an allowedTools the
	// original did not have.
	os.WriteFile(agentFile+".bak", ours, 0o644)
	widened := map[string]any{}
	json.Unmarshal(upgradedFrom(t, ours), &widened)
	widened["allowedTools"] = []any{"*"}
	wb, _ := json.Marshal(widened)
	os.WriteFile(agentFile, wb, 0o644)
	if f, ok := findingFor(VerifyManaged(applied.NewLock, configurator.ProviderKiro, baseDir), "agent", "reviewer"); !ok || f.Reason != DriftModified {
		t.Fatalf("expected drift, got %+v", f)
	}
}

// A lockfile written before the field reads and writes back as it was.
func TestLock_UpgradedHashIsOptional(t *testing.T) {
	old := `{"kind":"agent","name":"reviewer","path":".kiro/agents/reviewer.json","content_hash":"h","materialized_hash":"m"}`
	var mf ManagedFile
	if err := json.Unmarshal([]byte(old), &mf); err != nil || mf.UpgradedHash != "" {
		t.Fatalf("old entry: %v %+v", err, mf)
	}
	out, _ := json.Marshal(mf)
	if string(out) != old {
		t.Errorf("round trip changed the entry:\n%s", out)
	}
}

// Kiro never overwrites a backup: the original of a later upgrade is in
// <file>.bak.1, and the stale .bak must not hide it.
func TestVerifyManaged_KiroNumberedBackupIsOurs(t *testing.T) {
	baseDir, _, applied, _, agentFile := kiroAgentApplied(t)
	ours, _ := os.ReadFile(agentFile)
	os.WriteFile(agentFile+".bak", []byte(`{"name":"reviewer","prompt":"an older source"}`), 0o644)
	os.WriteFile(agentFile+".bak.1", ours, 0o644)
	os.WriteFile(agentFile, upgradedFrom(t, ours), 0o644)
	if f, ok := findingFor(VerifyManaged(applied.NewLock, configurator.ProviderKiro, baseDir), "agent", "reviewer"); ok {
		t.Fatalf("an upgrade whose original is .bak.1 is reported: %+v", f)
	}
	// A look-alike name is not a backup.
	os.Remove(agentFile + ".bak.1")
	os.WriteFile(agentFile+".bakx", ours, 0o644)
	if _, ok := findingFor(VerifyManaged(applied.NewLock, configurator.ProviderKiro, baseDir), "agent", "reviewer"); !ok {
		t.Fatal(".bakx counted as a backup")
	}
}

// Every key of the upgraded file but permissions and kiroUpgrade is the
// backup's: a changed prompt or mcpServers before the hash is recorded is drift,
// and sync must not pin it.
func TestVerifyManaged_KiroChangedKeyBeforeRecordingIsDrift(t *testing.T) {
	for _, key := range []string{"prompt", "mcpServers", "tools", "hooks"} {
		t.Run(key, func(t *testing.T) {
			baseDir, _, applied, _, agentFile := kiroAgentApplied(t)
			ours, _ := os.ReadFile(agentFile)
			os.WriteFile(agentFile+".bak", ours, 0o644)
			doc := map[string]any{}
			json.Unmarshal(upgradedFrom(t, ours), &doc)
			doc[key] = map[string]any{"changed": true}
			b, _ := json.Marshal(doc)
			os.WriteFile(agentFile, b, 0o644)
			if f, ok := findingFor(VerifyManaged(applied.NewLock, configurator.ProviderKiro, baseDir), "agent", "reviewer"); !ok || f.Reason != DriftModified {
				t.Fatalf("expected drift, got %+v", f)
			}
			if got := recordKiroUpgrade(agentEntry(t, applied.NewLock), agentFile); got.UpgradedHash != "" {
				t.Error("a changed file was pinned")
			}
		})
	}
}

// A backup over the size cap is not read, and only the first few numbered
// backups are looked at.
func TestKiroBackups_AreBounded(t *testing.T) {
	baseDir, _, applied, _, agentFile := kiroAgentApplied(t)
	ours, _ := os.ReadFile(agentFile)
	big := append(append([]byte{}, ours...), make([]byte, kiroBackupMaxBytes)...)
	os.WriteFile(agentFile+".bak", big, 0o644)
	os.WriteFile(agentFile, upgradedFrom(t, ours), 0o644)
	if got := kiroBackups(agentFile); len(got) != 0 {
		t.Fatalf("an oversized backup is listed: %v", got)
	}
	if _, ok := findingFor(VerifyManaged(applied.NewLock, configurator.ProviderKiro, baseDir), "agent", "reviewer"); !ok {
		t.Fatal("an oversized backup was accepted")
	}
	os.Remove(agentFile + ".bak")
	for i := 1; i <= kiroBackupMaxNumbered+5; i++ {
		os.WriteFile(fmt.Sprintf("%s.bak.%d", agentFile, i), ours, 0o644)
	}
	if got := kiroBackups(agentFile); len(got) != kiroBackupMaxNumbered {
		t.Fatalf("listed %d numbered backups, want %d", len(got), kiroBackupMaxNumbered)
	}
}
