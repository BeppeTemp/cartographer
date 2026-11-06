package clientconfig_test

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// The per-provider and per-KB keys (D1731): the old maps and
// the groups that replaced them.

const flatProvidersYAML = `server_url: http://127.0.0.1:39273/mcp
# which KBs
clients:
  claude:
    kbs: [kb-a, work-kb]
    config_dirs: [/opt/claude-b]
    access: read-min
  codex:
    kbs: []
scopes:
  claude: workspace
unbound_access:
  default: write
  codex: read
workspaces:
  claude:
    - path: /opt/work
      kbs: [work-kb]
kb_options:
  work-kb:
    access: read
    artifacts: false
signing_keys:
  kb-a: [0123abcd]
identities:
  default: User <user@example.com>
  work-kb: User <user@work.example.com>
`

const groupedProvidersYAML = `server:
  url: http://127.0.0.1:39273/mcp
defaults:
  unbound_access: write
providers:
  claude:
    kbs: [kb-a, work-kb]
    scope: workspace
    config_dirs: [/opt/claude-b]
    access: read
    tools: minimal
    workspaces:
      - path: /opt/work
        kbs: [work-kb]
  codex:
    kbs: []
    unbound_access: read
knowledge_bases:
  work-kb:
    access: read
    artifacts: false
    identity: User <user@work.example.com>
  kb-a:
    signing_keys: [0123abcd]
  default:
    identity: User <user@example.com>
`

func assertProviderValues(t *testing.T, cfg *clientconfig.Config) {
	t.Helper()
	claude := cfg.Clients["claude"]
	if strings.Join(claude.KBs, ",") != "kb-a,work-kb" || strings.Join(claude.ConfigDirs, ",") != "/opt/claude-b" || claude.Access != clientconfig.AccessReadMin {
		t.Errorf("claude binding = %+v", claude)
	}
	if codex := cfg.Clients["codex"]; !codex.Explicit() || len(codex.KBs) != 0 {
		t.Errorf("codex binding = %+v, want explicit and empty", codex)
	}
	if cfg.Scopes["claude"] != "workspace" || cfg.UnboundModes["default"] != "write" || cfg.UnboundModes["codex"] != "read" {
		t.Errorf("scopes %v unbound %v", cfg.Scopes, cfg.UnboundModes)
	}
	if w := cfg.Workspaces["claude"]; len(w) != 1 || w[0].Path != "/opt/work" || strings.Join(w[0].KBs, ",") != "work-kb" {
		t.Errorf("workspaces = %+v", cfg.Workspaces)
	}
	if cfg.KBAccess("work-kb") != clientconfig.KBAccessRead || cfg.KBArtifacts("work-kb") {
		t.Errorf("work-kb options = %+v", cfg.KBOptions["work-kb"])
	}
	if strings.Join(cfg.SigningKeys["kb-a"], ",") != "0123abcd" || cfg.Identities["default"] != "User <user@example.com>" || cfg.Identities["work-kb"] != "User <user@work.example.com>" {
		t.Errorf("keys %v identities %v", cfg.SigningKeys, cfg.Identities)
	}
}

func TestLoadReadsBothProviderLayouts(t *testing.T) {
	for name, body := range map[string]string{"flat": flatProvidersYAML, "grouped": groupedProvidersYAML} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfigFile(t, dir, body)
			cfg, err := clientconfig.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			assertProviderValues(t, cfg)
			if readFile(t, clientconfig.Path(dir)) != body {
				t.Error("Load rewrote the file")
			}
		})
	}
}

// The migration moves each value, splits read-min, keeps a person's note and
// the original, and runs once.
func TestRealignFileMovesTheProviderAndKBKeys(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, flatProvidersYAML)
	rep, backup, err := clientconfig.RealignFile(dir, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" || readFile(t, backup) != flatProvidersYAML {
		t.Errorf("backup %q does not hold the original", backup)
	}
	if got := strings.Join(rep.Lines(), "\n"); !strings.Contains(got, "access: read with tools: minimal") {
		t.Errorf("the report does not mention the split:\n%s", got)
	}
	raw := readFile(t, clientconfig.Path(dir))
	for _, key := range []string{"clients", "scopes", "unbound_access", "workspaces", "kb_options", "signing_keys", "identities"} {
		if hasKey(raw, key) {
			t.Errorf("%s is still at the root:\n%s", key, raw)
		}
	}
	for _, want := range []string{"# which KBs", "\nproviders:\n", "\nknowledge_bases:\n", "\ndefaults:\n", "    tools: minimal", "    scope: workspace", "    identity: User <user@work.example.com>"} {
		if !strings.Contains(raw, want) {
			t.Errorf("%q missing:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "read-min") {
		t.Errorf("read-min survived:\n%s", raw)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertProviderValues(t, cfg)
	if again, backup, err := clientconfig.RealignFile(dir, "1.0.0"); err != nil || !again.Empty() || backup != "" {
		t.Errorf("second run: %v %q %v", again.Lines(), backup, err)
	}
}

// A save over a flat file writes the groups (read-min split included), and the
// grouped file round-trips unchanged.
func TestSaveWritesTheProviderGroups(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, flatProvidersYAML)
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	for _, key := range []string{"clients", "scopes", "kb_options", "identities"} {
		if hasKey(raw, key) {
			t.Errorf("%s survived the save:\n%s", key, raw)
		}
	}
	reloaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertProviderValues(t, reloaded)
	if err := clientconfig.Save(dir, reloaded); err != nil || readFile(t, clientconfig.Path(dir)) != raw {
		t.Errorf("a second save changed the file (err %v)", err)
	}
}

func TestProviderLayoutRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"conflict":  {"clients:\n  claude:\n    kbs: [a]\nproviders:\n  claude:\n    kbs: [b]\n", "clients and providers are both set"},
		"scope":     {"scopes:\n  claude: workspace\nproviders:\n  claude:\n    scope: provider\n", "scopes.claude and providers.claude.scope"},
		"lone read": {"providers:\n  claude:\n    access: read\n", "providers.claude"},
		"dir":       {"providers:\n  codex:\n    config_dirs: [/x]\n", "providers.codex.config_dirs"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfigFile(t, dir, tc.body)
			if _, err := clientconfig.Load(dir); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Load error = %v, want %q", err, tc.want)
			}
		})
	}
}

// The report of a client that still holds the flat file shows the groups.
func TestGroupedShowsTheCurrentLayout(t *testing.T) {
	out := string(clientconfig.Grouped([]byte(flatProvidersYAML)))
	if !strings.Contains(out, "providers:") || hasKey(out, "scopes") {
		t.Errorf("Grouped left the flat layout:\n%s", out)
	}
	if got := clientconfig.Grouped([]byte("server_url: [\n")); string(got) != "server_url: [\n" {
		t.Errorf("unparsable bytes changed: %q", got)
	}
}

// A KB is named after its folder, so a name can hold a dot or be "*": the
// migration carries it like any other, addressed literally (D1731).
const flatDottedYAML = `kb_options:
  notes.v2:
    access: read
  "*":
    artifacts: false
identities:
  notes.v2: User <user@example.com>
  "*": User <user@work.example.com>
signing_keys:
  notes.v2: [0123abcd]
  "*": [4567ef01]
`

const groupedDottedYAML = `knowledge_bases:
  notes.v2:
    access: read
    identity: User <user@example.com>
    signing_keys: [0123abcd]
  "*":
    artifacts: false
    identity: User <user@work.example.com>
    signing_keys: [4567ef01]
`

func assertDottedValues(t *testing.T, cfg *clientconfig.Config) {
	t.Helper()
	if cfg.KBOptions["notes.v2"].Access != "read" {
		t.Errorf("KBOptions[notes.v2] = %+v", cfg.KBOptions["notes.v2"])
	}
	if o := cfg.KBOptions["*"]; o.Artifacts == nil || *o.Artifacts {
		t.Errorf("KBOptions[*] = %+v", o)
	}
	if cfg.Identities["notes.v2"] != "User <user@example.com>" || cfg.Identities["*"] != "User <user@work.example.com>" {
		t.Errorf("Identities = %v", cfg.Identities)
	}
	if k := cfg.SigningKeys["notes.v2"]; len(k) != 1 || k[0] != "0123abcd" {
		t.Errorf("SigningKeys[notes.v2] = %v", k)
	}
	if k := cfg.SigningKeys["*"]; len(k) != 1 || k[0] != "4567ef01" {
		t.Errorf("SigningKeys[*] = %v", k)
	}
}

func TestDottedKBNamesLoadInBothLayouts(t *testing.T) {
	for name, body := range map[string]string{"flat": flatDottedYAML, "grouped": groupedDottedYAML} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfigFile(t, dir, body)
			cfg, err := clientconfig.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			assertDottedValues(t, cfg)
		})
	}
}

func TestDottedKBNamesMigrateAndSave(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, flatDottedYAML)
	if _, _, err := clientconfig.RealignFile(dir, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	for _, key := range []string{"kb_options", "identities", "signing_keys"} {
		if hasKey(raw, key) {
			t.Errorf("%s is still at the root:\n%s", key, raw)
		}
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertDottedValues(t, cfg)
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	saved := readFile(t, clientconfig.Path(dir))
	again, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load after Save: %v\n%s", err, saved)
	}
	assertDottedValues(t, again)
	if err := clientconfig.Save(dir, again); err != nil || readFile(t, clientconfig.Path(dir)) != saved {
		t.Errorf("a second save changed the file (err %v)", err)
	}
	// A flat file saved without a migration writes the groups too.
	writeConfigFile(t, dir, flatDottedYAML)
	cfg, err = clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if again, err := clientconfig.Load(dir); err != nil {
		t.Fatal(err)
	} else {
		assertDottedValues(t, again)
	}
}

// The same name at the old and the new path with different values is refused
// like any other.
func TestDottedKBNameConflict(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "identities:\n  notes.v2: A <a@example.com>\nknowledge_bases:\n  notes.v2:\n    identity: B <b@example.com>\n")
	if _, err := clientconfig.Load(dir); err == nil || !strings.Contains(err.Error(), "notes.v2") {
		t.Errorf("Load error = %v", err)
	}
}
