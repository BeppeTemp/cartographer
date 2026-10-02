package provisioning_test

// Kiro's hook registration (D300): one Cartographer-owned standalone hook file,
// ~/.kiro/hooks/cartographer.json, beside every file of the user's.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func kiroHooksFile(baseDir string) string {
	return filepath.Join(baseDir, ".kiro", "hooks", "cartographer.json")
}

func applyKiroHooks(t *testing.T, kbRoot, baseDir string, lock provisioning.Lock) provisioning.AppliedResult {
	t.Helper()
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderKiro, BaseDir: baseDir, Lock: lock,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res
}

// kiroHookDoc decodes cartographer.json and checks it against the schema the
// KAS loader validates (2.27.0: version "v1", at least one entry, each with a
// name, a trigger and a command action; timeout a non-negative integer of
// seconds, enabled a boolean). A file the loader rejects loads no hook at all
// and nothing reports it, so the shape is pinned here.
func kiroHookDoc(t *testing.T, baseDir string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(kiroHooksFile(baseDir))
	if err != nil {
		t.Fatalf("read cartographer.json: %v", err)
	}
	var doc struct {
		Version string           `json:"version"`
		Hooks   []map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("cartographer.json: %v\n%s", err, data)
	}
	if doc.Version != "v1" || len(doc.Hooks) == 0 {
		t.Fatalf("not a v1 hook file with entries: %s", data)
	}
	allowed := map[string]bool{"name": true, "description": true, "trigger": true, "matcher": true, "action": true, "timeout": true, "enabled": true}
	for _, h := range doc.Hooks {
		for k := range h {
			if !allowed[k] {
				t.Errorf("entry carries %q, a key the v1 schema does not define: %v", k, h)
			}
		}
		if n, _ := h["name"].(string); n == "" {
			t.Errorf("entry without a name: %v", h)
		}
		if tr, _ := h["trigger"].(string); tr == "" {
			t.Errorf("entry without a trigger: %v", h)
		}
		action, _ := h["action"].(map[string]any)
		if action["type"] != "command" || action["command"] == "" || action["command"] == nil {
			t.Errorf("entry action is not a command: %v", h)
		}
		if to, ok := h["timeout"].(float64); !ok || to < 0 || to != float64(int(to)) {
			t.Errorf("timeout must be a non-negative integer of seconds: %v", h["timeout"])
		}
		if _, ok := h["enabled"].(bool); !ok {
			t.Errorf("enabled must be a boolean: %v", h["enabled"])
		}
	}
	return doc.Hooks
}

func TestApply_Kiro_Hook_RegistersInOwnedFile(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "SessionStart", "", "./notify.sh")
	writeHookKB(t, kbRoot, "audit", "Stop", "", "./notify.sh")
	baseDir := t.TempDir()

	res := applyKiroHooks(t, kbRoot, baseDir, provisioning.Lock{})
	first, err := os.ReadFile(kiroHooksFile(baseDir))
	if err != nil {
		t.Fatal(err)
	}
	applyKiroHooks(t, kbRoot, baseDir, res.NewLock)
	second, err := os.ReadFile(kiroHooksFile(baseDir))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("re-apply rewrote different bytes:\n%s\n---\n%s", first, second)
	}

	hooks := kiroHookDoc(t, baseDir)
	var names []string
	for _, h := range hooks {
		names = append(names, h["name"].(string))
	}
	if strings.Join(names, ",") != "audit,notify" {
		t.Fatalf("entries = %v, want one per hook, sorted", names)
	}
	notify := hooks[1]
	if notify["trigger"] != "SessionStart" {
		t.Errorf("trigger = %v", notify["trigger"])
	}
	want := filepath.Join(baseDir, ".kiro", "hooks", "cartographer", "notify", "notify.sh")
	if got := notify["action"].(map[string]any)["command"]; got != want {
		t.Errorf("command = %v, want %s", got, want)
	}
	// The registration file is not a managed file of each hook: pruning one
	// hook by path would delete every other hook's registration with it.
	for _, mf := range res.NewLock.Managed {
		if filepath.ToSlash(mf.Path) == ".kiro/hooks/cartographer.json" {
			t.Errorf("cartographer.json recorded as a managed file: %+v", mf)
		}
	}
}

// A hook file of the user's, beside ours, is never written or pruned, and the
// directory holding it survives a full disconnect.
func TestApply_Kiro_Hook_UserFilesUntouchedAndDisconnectRemovesOurs(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "SessionStart", "", "./notify.sh")
	baseDir := t.TempDir()
	hooksDir := filepath.Join(baseDir, ".kiro", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(hooksDir, "mine.json")
	userBody := `{"version":"v1","hooks":[{"name":"mine","trigger":"Stop","action":{"type":"command","command":"say done"}}]}`
	if err := os.WriteFile(userFile, []byte(userBody), 0o600); err != nil {
		t.Fatal(err)
	}

	res := applyKiroHooks(t, kbRoot, baseDir, provisioning.Lock{})
	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderKiro, res.NewLock, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(kiroHookDoc(t, baseDir)); got != 2 {
		t.Fatalf("want the KB hook and the bootstrap hook registered, got %d entries", got)
	}

	if _, err := provisioning.PruneManaged(lock.Managed, baseDir, false); err != nil {
		t.Fatalf("PruneManaged: %v", err)
	}
	if _, err := os.Stat(kiroHooksFile(baseDir)); !os.IsNotExist(err) {
		t.Errorf("disconnect must remove cartographer.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "cartographer")); !os.IsNotExist(err) {
		t.Errorf("disconnect must remove the hook files' directory: %v", err)
	}
	data, err := os.ReadFile(userFile)
	if err != nil || string(data) != userBody {
		t.Errorf("user hook file changed or removed: %q, %v", data, err)
	}
	if info, err := os.Stat(userFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("user hook file mode changed: %v, %v", info, err)
	}
}

// Pruning one hook keeps every other hook's entry in the shared file.
func TestApply_Kiro_Hook_RemovedKeepsOtherEntries(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "SessionStart", "", "./notify.sh")
	writeHookKB(t, kbRoot, "audit", "UserPromptSubmit", "", "./notify.sh")
	baseDir := t.TempDir()
	res := applyKiroHooks(t, kbRoot, baseDir, provisioning.Lock{})

	if err := os.RemoveAll(filepath.Join(kbRoot, "hooks", "audit")); err != nil {
		t.Fatal(err)
	}
	res2 := applyKiroHooks(t, kbRoot, baseDir, res.NewLock)
	if len(res2.Pruned) == 0 {
		t.Fatal("expected the removed hook to be pruned")
	}
	hooks := kiroHookDoc(t, baseDir)
	if len(hooks) != 1 || hooks[0]["name"] != "notify" {
		t.Errorf("entries after prune = %v, want only notify", hooks)
	}
}

// An event Kiro is not declared to fire is installed but never registered,
// with a warning: claiming a tool hook nobody probed would be a guess (D50).
func TestApply_Kiro_Hook_UnreachedEventIsNotRegistered(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "shell", "./notify.sh")
	baseDir := t.TempDir()
	res := applyKiroHooks(t, kbRoot, baseDir, provisioning.Lock{})
	if _, err := os.Stat(kiroHooksFile(baseDir)); !os.IsNotExist(err) {
		t.Errorf("no registration expected for an unreached event: %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		found = found || (strings.Contains(w, "guard") && strings.Contains(w, "not fired by this client"))
	}
	if !found {
		t.Errorf("want a warning naming the hook, got %v", res.Warnings)
	}
}

// D148: a symlinked ~/.kiro/hooks is refused rather than written through.
func TestApply_Kiro_Hook_RefusesSymlinkedHooksDir(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "SessionStart", "", "./notify.sh")
	baseDir := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(baseDir, ".kiro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(baseDir, ".kiro", "hooks")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderKiro, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	_, _ = provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderKiro, provisioning.Lock{}, false)
	entries, err := os.ReadDir(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		t.Errorf("wrote through the symlink into %v", names)
	}
}

func TestHookRegistrations_KiroCountsOwnedEntries(t *testing.T) {
	baseDir := t.TempDir()
	if _, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderKiro, provisioning.Lock{}, false); err != nil {
		t.Fatal(err)
	}
	managed, stray, err := provisioning.HookRegistrations(baseDir, configurator.ProviderKiro, provisioning.BootstrapHookName)
	if err != nil || managed != 1 || stray != 0 {
		t.Errorf("HookRegistrations = %d, %d, %v; want 1, 0, nil", managed, stray, err)
	}
	if got := provisioning.HookRegistrationFile(configurator.ProviderKiro); filepath.ToSlash(got) != ".kiro/hooks/cartographer.json" {
		t.Errorf("HookRegistrationFile = %q", got)
	}
}
