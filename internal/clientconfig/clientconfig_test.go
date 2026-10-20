package clientconfig_test

import (
	"os"
	"path/filepath"
	"runtime"
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

// D906: `token` is a literal or "${NAME}"; token_env, the key it replaced, is
// honoured as the same reference until the file is realigned.
func TestToken_EnvRefLiteralAndLegacy(t *testing.T) {
	cases := []struct {
		name, yaml  string
		wantEnv     string
		wantLiteral string
	}{
		{"env ref", "token: ${CARTO_TEST_TOK}\n", "CARTO_TEST_TOK", ""},
		{"literal", "token: s3cr3t-literal\n", clientconfig.DefaultTokenEnv, "s3cr3t-literal"},
		{"legacy token_env", "token_env: OLD_TOK\n", "OLD_TOK", ""},
		{"token wins over token_env", "token: ${NEW_TOK}\ntoken_env: OLD_TOK\n", "NEW_TOK", ""},
		{"absent", "server_url: http://x\n", "", ""},
	}
	for _, c := range cases {
		dir := t.TempDir()
		writeConfigFile(t, dir, c.yaml)
		cfg, err := clientconfig.Load(dir)
		if err != nil {
			t.Fatalf("%s: Load: %v", c.name, err)
		}
		if got := cfg.TokenEnv(); got != c.wantEnv {
			t.Errorf("%s: TokenEnv() = %q, want %q", c.name, got, c.wantEnv)
		}
		if got := cfg.LiteralToken(); got != c.wantLiteral {
			t.Errorf("%s: LiteralToken() = %q, want %q", c.name, got, c.wantLiteral)
		}
	}
}

func TestDefault_TokenIsTheDefaultEnvRef(t *testing.T) {
	cfg := clientconfig.Default()
	if cfg.Token != "${CARTOGRAPHER_TOKENS}" || cfg.TokenEnv() != "CARTOGRAPHER_TOKENS" || cfg.LiteralToken() != "" {
		t.Errorf("Default token = %q (env %q, literal %q)", cfg.Token, cfg.TokenEnv(), cfg.LiteralToken())
	}
}

// A literal token is a secret in a file: Save writes it with mode 0600, and an
// env reference keeps the usual mode.
func TestSave_LiteralTokenFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits mean nothing on Windows")
	}
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.Token = "s3cr3t-literal"
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(clientconfig.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 for a file holding a literal token", fi.Mode().Perm())
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil || loaded.LiteralToken() != "s3cr3t-literal" {
		t.Errorf("literal token did not round-trip: %v, %v", loaded, err)
	}

	dir2 := t.TempDir()
	if err := clientconfig.Save(dir2, clientconfig.Default()); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(clientconfig.Path(dir2)); fi.Mode().Perm() == 0o600 {
		t.Errorf("an env reference does not need 0600, got %v", fi.Mode().Perm())
	}
}

// D906: agents and sync_timer_opt_out live in the state file; a pre-D906 file
// that still holds them in the YAML is read from there until the first save
// moves them.
func TestAgentsAndTimerOptOutLiveInTheStateFile(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://x/mcp\nagents: [claude, codex]\nsync_timer_opt_out: true\n")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasAgent("claude") || !cfg.HasAgent("codex") || !cfg.SyncTimerOptOut {
		t.Fatalf("legacy YAML values not read: %+v", cfg)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	if hasKey(raw, "agents") || hasKey(raw, "sync_timer_opt_out") {
		t.Errorf("the YAML still holds the machine-written keys:\n%s", raw)
	}
	state := readFile(t, clientconfig.StatePath(dir))
	if !strings.Contains(state, `"claude"`) || !strings.Contains(state, `"sync_timer_opt_out": true`) {
		t.Errorf("the state file lacks them:\n%s", state)
	}
	again, err := clientconfig.Load(dir)
	if err != nil || len(again.Agents) != 2 || !again.SyncTimerOptOut {
		t.Fatalf("round trip: %+v, %v", again, err)
	}

	// Every client disconnected is an empty list, not an absent one: the YAML
	// of an older version must not resurrect the agents.
	again.Agents = []string{}
	if err := clientconfig.Save(dir, again); err != nil {
		t.Fatal(err)
	}
	last, err := clientconfig.Load(dir)
	if err != nil || len(last.Agents) != 0 {
		t.Fatalf("empty agents list did not stick: %+v, %v", last, err)
	}
}

// The removed keys are dropped by a save instead of surviving as unknown ones.
func TestSaveDropsTheKeysD906Removed(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://x/mcp\ntrust: false\nsearch_depth: 6\nwrite_findings_hook: false\nupdate:\n  policy: auto-patch\n")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	for _, key := range []string{"trust", "search_depth", "write_findings_hook", "update"} {
		if hasKey(raw, key) {
			t.Errorf("%s survived a save:\n%s", key, raw)
		}
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

var wantDefaultRoots = "~/Documents,~/src,~/code,~/projects"

func TestDefault_SearchRoots(t *testing.T) {
	cfg := clientconfig.Default()
	if got := strings.Join(cfg.SearchRoots, ","); got != wantDefaultRoots {
		t.Errorf("SearchRoots = %v, want %s", cfg.SearchRoots, wantDefaultRoots)
	}
}

// The defaults are guesses: a root that does not exist on this machine is left
// out of the scan silently, one the file names is kept (and reported by the
// scan when it is missing).
func TestScanRoots_DefaultsKeepOnlyWhatExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := clientconfig.Default().ScanRoots(); strings.Join(got, ",") != "~/src" {
		t.Errorf("ScanRoots() = %v, want only ~/src", got)
	}
	cfg := clientconfig.Default()
	cfg.SearchRoots = []string{"~/missing"}
	if got := cfg.ScanRoots(); strings.Join(got, ",") != "~/missing" {
		t.Errorf("a configured root is taken as written, got %v", got)
	}
}

// A save does not pin the defaults into the file.
func TestSave_DefaultSearchRootsAreNotWritten(t *testing.T) {
	dir := t.TempDir()
	if err := clientconfig.Save(dir, clientconfig.Default()); err != nil {
		t.Fatal(err)
	}
	if raw := readFile(t, clientconfig.Path(dir)); hasKey(raw, "search_roots") {
		t.Errorf("the defaults were written:\n%s", raw)
	}
}

func TestLoad_SearchRootsAbsent_UsesTheDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, clientconfig.FileName), []byte("server_url: http://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(loaded.SearchRoots, ","); got != wantDefaultRoots {
		t.Errorf("SearchRoots = %v, want %s", loaded.SearchRoots, wantDefaultRoots)
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
		"opencode": {KBsSet: true},
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
	if hasKey(raw, "known_kbs") {
		t.Errorf("known_kbs written to the user file, it belongs to the state file:\n%s", raw)
	}
	if state, err := os.ReadFile(clientconfig.StatePath(dir)); err != nil || !strings.Contains(string(state), `"alpha"`) {
		t.Errorf("known_kbs missing from the state file: %v\n%s", err, state)
	}
	if hasKey(raw, "kbs") {
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

// hasKey reports whether raw sets key at the top level: a commented-out line
// does not count.
func hasKey(raw, key string) bool {
	for _, l := range strings.Split(raw, "\n") {
		if strings.HasPrefix(l, key+":") {
			return true
		}
	}
	return false
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

// TestEveryYamlFieldIsWrittenBySave is the guard against the next omission: a
// field present in the YAML struct but absent from Save's literal round-trips
// through Extra and looks fine until someone changes it in code.
func TestEveryYamlFieldIsWrittenBySave(t *testing.T) {
	dir := t.TempDir()
	cfg := &clientconfig.Config{
		ServerURL: "http://example.test/mcp", ServerName: "srv", Auth: true,
		Token: "${TOK}", Agents: []string{"claude"}, KnownKBs: []string{"kb"},
		Clients:     map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"kb"}}},
		SearchRoots: []string{"~/x"},
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
		"server_url", "server_name", "auth", "token",
		"clients", "search_roots",
		"paths", "signing_keys", "mcp_approvals",
	} {
		if !hasKey(raw, key) {
			t.Errorf("Save omitted %q:\n%s", key, raw)
		}
	}
}

// TestLoad_MigratesLegacyLocalURL: a config written with the pre-#329 default
// comes back on the loopback literal the service listens on, so the next sync
// rewrites every client's MCP entry; a custom URL is left alone.
func TestLoad_MigratesLegacyLocalURL(t *testing.T) {
	cases := map[string]string{
		"http://localhost:39273/mcp":           "http://127.0.0.1:39273/mcp",
		"http://localhost:39273/mcp/alpha":     "http://127.0.0.1:39273/mcp/alpha",
		"http://localhost:8080/mcp":            "http://localhost:8080/mcp",
		"https://cartographer.example.com/mcp": "https://cartographer.example.com/mcp",
	}
	for in, want := range cases {
		dir := t.TempDir()
		writeConfigFile(t, dir, "server_url: "+in+"\n")
		cfg, err := clientconfig.Load(dir)
		if err != nil {
			t.Fatalf("Load(%s): %v", in, err)
		}
		if cfg.ServerURL != want {
			t.Errorf("Load(%s).ServerURL = %q, want %q", in, cfg.ServerURL, want)
		}
	}
}

// D906: `update_check` round-trips, is absent from a default file, and the
// old `update:` block is read as update_check (its policy is ignored: a client
// never replaces itself).
func TestUpdateCheck(t *testing.T) {
	dir := t.TempDir()
	if err := clientconfig.Save(dir, clientconfig.Default()); err != nil {
		t.Fatal(err)
	}
	if raw := readFile(t, clientconfig.Path(dir)); hasKey(raw, "update_check") || hasKey(raw, "update") {
		t.Fatalf("a default config must not carry update settings:\n%s", raw)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil || !loaded.UpdateCheckEnabled() {
		t.Fatalf("defaults: %+v, %v", loaded, err)
	}
	if !(&clientconfig.Config{}).UpdateCheckEnabled() {
		t.Fatal("a zero Config must check")
	}

	loaded.UpdateCheckOff = true
	if err := clientconfig.Save(dir, loaded); err != nil {
		t.Fatal(err)
	}
	again, err := clientconfig.Load(dir)
	if err != nil || again.UpdateCheckEnabled() {
		t.Fatalf("update_check: false did not round-trip: %+v, %v", again, err)
	}

	writeConfigFile(t, dir, "server_url: http://x/mcp\nupdate:\n  check: false\n  policy: auto-patch\n")
	legacy, err := clientconfig.Load(dir)
	if err != nil || legacy.UpdateCheckEnabled() {
		t.Fatalf("legacy update.check: false not honoured: %+v, %v", legacy, err)
	}
}

// D326: usage_scan defaults to on, survives a round trip, and is written only
// when it is off, so a default machine's file does not change.
func TestUsageScan_DefaultsOnAndOnlyOffIsWritten(t *testing.T) {
	dir := t.TempDir()
	if !clientconfig.Default().UsageScan {
		t.Fatal("clientconfig.Default().UsageScan must be true")
	}
	if err := clientconfig.Save(dir, clientconfig.Default()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(clientconfig.Path(dir))
	if hasKey(string(raw), "usage_scan") {
		t.Errorf("a default config wrote usage_scan:\n%s", raw)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil || !loaded.UsageScan {
		t.Fatalf("absent key must load as on: %+v, %v", loaded, err)
	}
	loaded.UsageScan = false
	if err := clientconfig.Save(dir, loaded); err != nil {
		t.Fatal(err)
	}
	again, err := clientconfig.Load(dir)
	if err != nil || again.UsageScan {
		t.Fatalf("usage_scan: false did not round-trip: %+v, %v", again, err)
	}
	if _, stray := again.Extra["usage_scan"]; stray {
		t.Error("usage_scan leaked into Extra")
	}
}

func TestConfigDirsOnlyBindingKeepsDefaultKBs(t *testing.T) {
	dir := t.TempDir()
	body := "server_url: http://x/mcp\nclients:\n  claude:\n    config_dirs: [/a/b]  # keep\n  codex:\n    kbs: []\n"
	if err := os.WriteFile(clientconfig.Path(dir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.KnownKBs = []string{"one", "two"}
	if kbs, explicit := cfg.BoundKBs("claude"); explicit || len(kbs) != 2 {
		t.Errorf("claude with only config_dirs: kbs=%v explicit=%v, want default", kbs, explicit)
	}
	if kbs, explicit := cfg.BoundKBs("codex"); !explicit || len(kbs) != 0 {
		t.Errorf("codex kbs: []: kbs=%v explicit=%v, want explicit none", kbs, explicit)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(clientconfig.Path(dir))
	if !strings.Contains(string(data), "/a/b") || !strings.Contains(string(data), "# keep") || !strings.Contains(string(data), "kbs: []") {
		t.Errorf("round trip lost a key or comment:\n%s", data)
	}
	re, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, explicit := re.BoundKBs("claude"); explicit {
		t.Error("saving made claude an explicit binding")
	}
}

func TestConfigDirsRejectedOutsideClaude(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(clientconfig.Path(dir), []byte("clients:\n  codex:\n    config_dirs: [/a]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := clientconfig.Load(dir); err == nil || !strings.Contains(err.Error(), "config_dirs") {
		t.Fatalf("err = %v, want a config_dirs error", err)
	}
}
