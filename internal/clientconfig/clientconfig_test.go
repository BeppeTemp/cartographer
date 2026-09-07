package clientconfig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/defaults"
)

func TestLoad_NotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := clientconfig.Load(dir)
	if !os.IsNotExist(err) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestMCPApprovalRoundTripAndUnknownYAMLPreservation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(clientconfig.Path(dir), []byte("server_url: http://x/mcp\ncustom_extension:\n  keep: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ApproveMCP("kb", "tools", "abc", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.ApprovedMCPHashes()["kb:kb\x00tools"]; got != "abc" {
		t.Fatalf("hash = %q", got)
	}
	data, err := os.ReadFile(clientconfig.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "custom_extension:") {
		t.Fatalf("unknown key lost: %s", data)
	}
	loaded.RevokeMCP("kb", "tools")
	loaded.RevokeMCP("kb", "tools")
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.AddAgent("claude")
	cfg.AddAgent("opencode")
	cfg.Auth = true
	cfg.KnownKBs = []string{"homelab"}

	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, clientconfig.FileName)); err != nil {
		t.Fatalf("config file not written: %v", err)
	}

	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ServerURL != cfg.ServerURL || loaded.ServerName != cfg.ServerName {
		t.Errorf("round-trip mismatch: got %+v, want %+v", loaded, cfg)
	}
	if !loaded.Auth {
		t.Error("Auth should round-trip as true")
	}
	if len(loaded.Agents) != 2 || !loaded.HasAgent("claude") || !loaded.HasAgent("opencode") {
		t.Errorf("Agents round-trip mismatch: %v", loaded.Agents)
	}
	if len(loaded.KnownKBs) != 1 || loaded.KnownKBs[0] != "homelab" {
		t.Errorf("KBs round-trip mismatch: %v", loaded.KnownKBs)
	}
}

func TestAddAgent_Dedup(t *testing.T) {
	cfg := clientconfig.Default()
	cfg.AddAgent("claude")
	cfg.AddAgent("claude")
	if len(cfg.Agents) != 1 {
		t.Errorf("expected 1 agent after dedup, got %d: %v", len(cfg.Agents), cfg.Agents)
	}
}

func TestDefault_TrustIsTrue(t *testing.T) {
	cfg := clientconfig.Default()
	if !cfg.Trust {
		t.Error("Default().Trust should be true")
	}
}

func TestLoad_TrustAbsent_DefaultsTrue(t *testing.T) {
	dir := t.TempDir()
	// Config file written before the `trust` field existed: no `trust` key at all.
	data := "server_url: https://saved.example.test/mcp\nserver_name: cartographer\nauth: false\ntoken_env: CARTOGRAPHER_TOKENS\nagents: [claude]\n"
	if err := os.WriteFile(filepath.Join(dir, clientconfig.FileName), []byte(data), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Trust {
		t.Error("Trust should default to true when the `trust` key is absent")
	}
}

func TestLoad_TrustExplicitFalse_StaysFalse(t *testing.T) {
	dir := t.TempDir()
	data := "server_url: https://saved.example.test/mcp\ntrust: false\n"
	if err := os.WriteFile(filepath.Join(dir, clientconfig.FileName), []byte(data), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Trust {
		t.Error("explicit `trust: false` must round-trip as false, not be overridden by the default")
	}
}

func TestSaveAndLoad_TrustRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.Trust = false
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Trust {
		t.Error("Trust=false should round-trip as false")
	}
}

func TestDefault_ServerURLFallsBackToLocalDefault(t *testing.T) {
	cfg := clientconfig.Default()
	if cfg.ServerURL != defaults.DefaultMCPURL {
		t.Errorf("ServerURL = %q, want %q with no env set", cfg.ServerURL, defaults.DefaultMCPURL)
	}
}

func TestDefault_ServerURLFromEnv(t *testing.T) {
	t.Setenv("CARTOGRAPHER_SERVER_URL", "https://wiki.example.com/mcp")
	cfg := clientconfig.Default()
	if cfg.ServerURL != "https://wiki.example.com/mcp" {
		t.Errorf("ServerURL = %q, want the CARTOGRAPHER_SERVER_URL value", cfg.ServerURL)
	}
}

func TestLoad_ExistingYAMLWinsOverServerURLEnv(t *testing.T) {
	t.Setenv("CARTOGRAPHER_SERVER_URL", "https://env.example.com/mcp")
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.ServerURL = "https://yaml.example.com/mcp"
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ServerURL != "https://yaml.example.com/mcp" {
		t.Errorf("ServerURL = %q, want the persisted yaml value (yaml > env precedence)", loaded.ServerURL)
	}
}

func TestLoad_Existing8080ServerURLIsPreserved(t *testing.T) {
	dir := t.TempDir()
	const existing = "http://localhost:8080/mcp"
	if err := os.WriteFile(filepath.Join(dir, clientconfig.FileName), []byte("server_url: "+existing+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ServerURL != existing {
		t.Errorf("ServerURL = %q, want preserved %q", loaded.ServerURL, existing)
	}
}

func TestDefault_SearchRootsDefaultsToDocuments(t *testing.T) {
	cfg := clientconfig.Default()
	if len(cfg.SearchRoots) != 1 || cfg.SearchRoots[0] != "~/Documents" {
		t.Errorf("SearchRoots = %v, want [~/Documents]", cfg.SearchRoots)
	}
}

func TestLoad_SearchRootsAbsent_DefaultsToDocuments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, clientconfig.FileName), []byte("server_url: http://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.SearchRoots) != 1 || loaded.SearchRoots[0] != "~/Documents" {
		t.Errorf("SearchRoots = %v, want [~/Documents]", loaded.SearchRoots)
	}
}

func TestSaveAndLoad_SearchRootsAndPathsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.SearchRoots = []string{"~/code", "/opt/repos"}
	cfg.Paths = map[string]string{"design-assets": "/mnt/shared/design"}

	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.SearchRoots) != 2 || loaded.SearchRoots[0] != "~/code" || loaded.SearchRoots[1] != "/opt/repos" {
		t.Errorf("SearchRoots round-trip mismatch: %v", loaded.SearchRoots)
	}
	if loaded.Paths["design-assets"] != "/mnt/shared/design" {
		t.Errorf("Paths round-trip mismatch: %v", loaded.Paths)
	}
}

func TestTargetDir(t *testing.T) {
	home, err := clientconfig.TargetDir()
	if err != nil {
		t.Fatalf("TargetDir(): %v", err)
	}
	realHome, _ := os.UserHomeDir()
	if home != realHome {
		t.Errorf("TargetDir() = %q, want home %q", home, realHome)
	}
}

// --- D169: per-provider KB binding ---

// TestBoundKBsThreeStates pins the rule no caller may re-derive: an absent
// entry, an entry holding an empty list and an entry holding names are three
// distinct states, and only the first one means "every known KB".
func TestBoundKBsThreeStates(t *testing.T) {
	cfg := clientconfig.Default()
	cfg.KnownKBs = []string{"alpha", "beta"}
	cfg.Clients = map[string]clientconfig.ClientBinding{
		"codex":    {KBs: []string{"beta"}},
		"opencode": {KBs: nil},
	}

	if kbs, explicit := cfg.BoundKBs("claude"); explicit || strings.Join(kbs, ",") != "alpha,beta" {
		t.Errorf("no entry: got %v explicit=%v, want [alpha beta] explicit=false", kbs, explicit)
	}
	if kbs, explicit := cfg.BoundKBs("codex"); !explicit || strings.Join(kbs, ",") != "beta" {
		t.Errorf("named entry: got %v explicit=%v, want [beta] explicit=true", kbs, explicit)
	}
	if kbs, explicit := cfg.BoundKBs("opencode"); !explicit || len(kbs) != 0 {
		t.Errorf("empty entry: got %v explicit=%v, want [] explicit=true", kbs, explicit)
	}
}

// TestBoundKBsReturnsCopy: a caller mutating the result must not corrupt the
// config it was resolved from.
func TestBoundKBsReturnsCopy(t *testing.T) {
	cfg := clientconfig.Default()
	cfg.KnownKBs = []string{"alpha"}
	kbs, _ := cfg.BoundKBs("claude")
	kbs[0] = "mutated"
	if cfg.KnownKBs[0] != "alpha" {
		t.Errorf("KnownKBs = %v, want the original [alpha]", cfg.KnownKBs)
	}
}

func TestBindUnbindReset(t *testing.T) {
	cfg := clientconfig.Default()
	cfg.KnownKBs = []string{"alpha", "beta"}

	if err := cfg.Bind("claude", "alpha"); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	// Binding the first KB converts the provider from "all known" to "only this".
	if kbs, explicit := cfg.BoundKBs("claude"); !explicit || strings.Join(kbs, ",") != "alpha" {
		t.Fatalf("after Bind: %v explicit=%v", kbs, explicit)
	}
	if err := cfg.Bind("claude", "alpha"); err != nil {
		t.Fatalf("Bind (repeat): %v", err)
	}
	if kbs, _ := cfg.BoundKBs("claude"); len(kbs) != 1 {
		t.Errorf("re-binding the same KB duplicated it: %v", kbs)
	}

	// Unbinding the last KB must leave the entry, not restore the default.
	if err := cfg.Unbind("claude", "alpha"); err != nil {
		t.Fatalf("Unbind: %v", err)
	}
	if kbs, explicit := cfg.BoundKBs("claude"); !explicit || len(kbs) != 0 {
		t.Fatalf("after Unbind of the last KB: %v explicit=%v, want [] explicit=true", kbs, explicit)
	}

	// Reset is the only way back to the default.
	cfg.ResetBinding("claude")
	if kbs, explicit := cfg.BoundKBs("claude"); explicit || strings.Join(kbs, ",") != "alpha,beta" {
		t.Fatalf("after ResetBinding: %v explicit=%v", kbs, explicit)
	}
}

func TestUnbindUnknownPairIsNoOp(t *testing.T) {
	cfg := clientconfig.Default()
	if err := cfg.Unbind("claude", "missing"); err != nil {
		t.Fatalf("Unbind of an unbound pair: %v", err)
	}
	if _, explicit := cfg.BoundKBs("claude"); explicit {
		t.Error("Unbind created an entry for a provider that had none")
	}
}

func TestBindRejectsEmptyNames(t *testing.T) {
	cfg := clientconfig.Default()
	if err := cfg.Bind("", "alpha"); err == nil {
		t.Error("Bind with an empty provider should fail")
	}
	if err := cfg.Bind("claude", " "); err == nil {
		t.Error("Bind with a blank KB name should fail")
	}
}

// TestLoadMigratesLegacyKBsKey: a config written before D169 keeps its cached
// KB list, now under the typed KnownKBs field.
func TestLoadMigratesLegacyKBsKey(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://localhost:39273/mcp\nkbs:\n  - alpha\n  - beta\n")

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Join(cfg.KnownKBs, ",") != "alpha,beta" {
		t.Errorf("KnownKBs = %v, want [alpha beta]", cfg.KnownKBs)
	}
}

// TestLoadKnownKBsWinsOverLegacyAlias: `known_kbs: []` is a deliberately empty
// cache and must beat a stale `kbs` left by a pre-D169 client. Testing the
// empty case specifically is the point — a non-empty one would also pass with
// a plain "prefer non-empty" implementation.
func TestLoadKnownKBsWinsOverLegacyAlias(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://localhost:39273/mcp\nkbs:\n  - stale\nknown_kbs: []\n")

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.KnownKBs) != 0 {
		t.Errorf("KnownKBs = %v, want empty (known_kbs: [] must win over kbs)", cfg.KnownKBs)
	}
}

// TestSaveWritesKnownKBsNotLegacyAlias asserts on the BYTES on disk, not on a
// reloaded struct: a field omitted from Save's marshalled struct round-trips
// correctly through Extra while every programmatic change to it is silently
// lost, so only the file proves persistence.
func TestSaveWritesKnownKBsNotLegacyAlias(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://localhost:39273/mcp\nkbs:\n  - alpha\ncustom_key: keep-me\n")

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Bind("claude", "alpha"); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(clientconfig.Path(dir))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	raw := string(data)
	if !strings.Contains(raw, "known_kbs:") {
		t.Errorf("known_kbs missing from the written file:\n%s", raw)
	}
	if strings.Contains(raw, "\nkbs:") {
		t.Errorf("the legacy kbs key was written again:\n%s", raw)
	}
	if !strings.Contains(raw, "clients:") {
		t.Errorf("clients missing from the written file:\n%s", raw)
	}
	if !strings.Contains(raw, "custom_key: keep-me") {
		t.Errorf("unknown key lost across the write:\n%s", raw)
	}

	reloaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if kbs, explicit := reloaded.BoundKBs("claude"); !explicit || strings.Join(kbs, ",") != "alpha" {
		t.Errorf("binding did not survive the round trip: %v explicit=%v", kbs, explicit)
	}
}

func writeConfigFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(clientconfig.Path(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestSyncStyleSaveKeepsBindings simulates what runSync does after reconciling
// with /health — overwrite the server-owned cache, then Save — and asserts the
// user-owned bindings survive it. This is the regression the whole split of
// known_kbs from clients exists to prevent.
func TestSyncStyleSaveKeepsBindings(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.Agents = []string{"claude"}
	cfg.KnownKBs = []string{"alpha"}
	if err := cfg.Bind("claude", "alpha"); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	reloaded.KnownKBs = []string{"alpha", "beta", "gamma"} // what runSync assigns
	if err := clientconfig.Save(dir, reloaded); err != nil {
		t.Fatalf("Save after sync: %v", err)
	}

	final, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load after sync: %v", err)
	}
	if strings.Join(final.KnownKBs, ",") != "alpha,beta,gamma" {
		t.Errorf("KnownKBs = %v, want the refreshed cache", final.KnownKBs)
	}
	if kbs, explicit := final.BoundKBs("claude"); !explicit || strings.Join(kbs, ",") != "alpha" {
		t.Errorf("binding = %v explicit=%v, want [alpha] explicit=true — the sync overwrote it", kbs, explicit)
	}
}

// --- D180: every typed field must survive a write ---

// TestSearchDepthRoundTrips asserts on the BYTES, not on the reloaded struct.
// SearchDepth was read but omitted from Save's marshalled struct, and the raw
// value survived through Extra — so a reload looked correct while every
// programmatic change to the typed field was silently discarded.
func TestSearchDepthRoundTrips(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.SearchDepth = 6
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(clientconfig.Path(dir))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), "search_depth: 6") {
		t.Errorf("search_depth missing from the written file:\n%s", data)
	}

	reloaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.SearchDepth != 6 {
		t.Errorf("SearchDepth = %d, want 6", reloaded.SearchDepth)
	}
}

// TestSearchDepthChangeIsPersisted is the case Extra used to mask: the file
// already carries a value and the typed field is changed.
func TestSearchDepthChangeIsPersisted(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://localhost:39273/mcp\nsearch_depth: 4\n")

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SearchDepth != 4 {
		t.Fatalf("SearchDepth = %d, want the value from the file", cfg.SearchDepth)
	}
	cfg.SearchDepth = 8
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.SearchDepth != 8 {
		t.Errorf("SearchDepth = %d, want 8 — the change was discarded", reloaded.SearchDepth)
	}
}

// TestZeroSearchDepthWritesNoKey: zero means "use the default", so an existing
// file is not churned with a redundant key.
func TestZeroSearchDepthWritesNoKey(t *testing.T) {
	dir := t.TempDir()
	if err := clientconfig.Save(dir, clientconfig.Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(clientconfig.Path(dir))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(data), "search_depth") {
		t.Errorf("a zero SearchDepth wrote a key:\n%s", data)
	}
}

// TestEveryYamlFieldIsWrittenBySave is the guard against the next omission: a
// field present in the YAML struct but absent from Save's literal round-trips
// through Extra and looks fine until someone changes it in code.
func TestEveryYamlFieldIsWrittenBySave(t *testing.T) {
	dir := t.TempDir()
	cfg := &clientconfig.Config{
		ServerURL: "http://example.test/mcp", ServerName: "srv", Auth: true,
		TokenEnv: "TOK", Agents: []string{"claude"}, KnownKBs: []string{"kb"},
		Clients:     map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"kb"}}},
		Trust:       true,
		SearchRoots: []string{"~/x"}, SearchDepth: 5,
		Paths:       map[string]string{"n": "/p"},
		SigningKeys: map[string][]string{"kb": {strings.Repeat("ab", 32)}},
	}
	if err := cfg.ApproveMCP("kb", "srv", "hash", time.Now()); err != nil {
		t.Fatalf("ApproveMCP: %v", err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(clientconfig.Path(dir))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	raw := string(data)
	for _, key := range []string{
		"server_url", "server_name", "auth", "token_env", "agents",
		"known_kbs", "clients", "trust", "search_roots", "search_depth",
		"paths", "signing_keys", "mcp_approvals",
	} {
		if !strings.Contains(raw, key+":") {
			t.Errorf("Save omitted %q:\n%s", key, raw)
		}
	}
}
