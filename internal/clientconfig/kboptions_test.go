package clientconfig_test

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func TestKBArtifactsDefaultsToTrue(t *testing.T) {
	var nilCfg *clientconfig.Config
	if !nilCfg.KBArtifacts("kb-a") {
		t.Error("nil config must sync artifacts")
	}
	cfg := clientconfig.Default()
	if !cfg.KBArtifacts("kb-a") || !cfg.KBArtifacts("") {
		t.Error("absent map must sync artifacts")
	}
	cfg.KBOptions = map[string]clientconfig.KBOption{"kb-a": {}}
	if !cfg.KBArtifacts("kb-a") {
		t.Error("an entry without the key must sync artifacts")
	}
	cfg.SetKBArtifacts("kb-a", false)
	if cfg.KBArtifacts("kb-a") || !cfg.KBArtifacts("kb-b") || !cfg.KBArtifacts("") {
		t.Error("false applies to kb-a only")
	}
}

func TestSetKBArtifactsTrueCleansUp(t *testing.T) {
	cfg := clientconfig.Default()
	cfg.SetKBArtifacts("kb-a", false)
	cfg.SetKBArtifacts("kb-b", false)
	cfg.SetKBArtifacts("kb-a", true)
	if _, ok := cfg.KBOptions["kb-a"]; ok || len(cfg.KBOptions) != 1 {
		t.Errorf("entry not removed: %v", cfg.KBOptions)
	}
	cfg.SetKBArtifacts("kb-b", true)
	if cfg.KBOptions != nil {
		t.Errorf("map not nil: %v", cfg.KBOptions)
	}
}

func TestKBOptionsRoundTripAndHandEditSurvives(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "# mine\nserver_url: http://127.0.0.1:39273/mcp\nserver_name: cartographer\nkb_options:\n  work-kb:\n    artifacts: false # no skills\nsearch_roots:\n  - ~/Documents\n")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KBArtifacts("work-kb") {
		t.Fatal("artifacts: false not read")
	}
	if err := cfg.Bind("claude", "work-kb"); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	for _, want := range []string{"# mine", "knowledge_bases:", "artifacts: false # no skills"} {
		if !strings.Contains(raw, want) {
			t.Errorf("lost %q:\n%s", want, raw)
		}
	}
}

func TestKBOptionsEmptyIsNotWritten(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.SetKBArtifacts("kb-a", false)
	cfg.SetKBArtifacts("kb-a", true)
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if raw := readFile(t, clientconfig.Path(dir)); strings.Contains(raw, "kb_options:\n") && !strings.Contains(raw, "# kb_options") {
		t.Errorf("empty kb_options written:\n%s", raw)
	}
}

func TestKBOptionsLoadErrors(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://127.0.0.1:39273/mcp\nkb_options:\n  work-kb:\n    artifacts: maybe\n")
	if _, err := clientconfig.Load(dir); err == nil {
		t.Error("a string artifacts must be a load error")
	}
	writeConfigFile(t, dir, "server_url: http://127.0.0.1:39273/mcp\nkb_options:\n  \"\":\n    artifacts: false\n")
	if _, err := clientconfig.Load(dir); err == nil || !strings.Contains(err.Error(), ".cartographer.yaml") {
		t.Errorf("empty KB name: %v", err)
	}
}

func TestKBAccessDefaultsToWriteAndWriteEqualsAbsent(t *testing.T) {
	var nilCfg *clientconfig.Config
	if nilCfg.KBAccess("kb-a") != "write" || nilCfg.ReadOnlyKBs() != nil {
		t.Error("nil config must be read-write")
	}
	cfg := clientconfig.Default()
	if err := cfg.SetKBAccess("kb-b", "read"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetKBAccess("kb-a", "read"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.ReadOnlyKBs(), ","); got != "kb-a,kb-b" {
		t.Errorf("ReadOnlyKBs = %q, want sorted", got)
	}
	if cfg.KBAccess("kb-a") != "read" || cfg.KBAccess("kb-c") != "write" || cfg.KBAccess("") != "write" {
		t.Error("read applies to the named KBs only")
	}
	// write removes the key, the entry and the map; a sibling option survives.
	cfg.SetKBArtifacts("kb-a", false)
	_ = cfg.SetKBAccess("kb-a", "write")
	_ = cfg.SetKBAccess("kb-b", "write")
	if o, ok := cfg.KBOptions["kb-a"]; !ok || o.Access != "" || cfg.KBArtifacts("kb-a") {
		t.Errorf("sibling option lost: %+v", cfg.KBOptions)
	}
	if _, ok := cfg.KBOptions["kb-b"]; ok {
		t.Error("an entry holding only write must be dropped")
	}
	if err := cfg.SetKBAccess("kb-a", "rw"); err == nil {
		t.Error("an invalid access must be refused")
	}
}

func TestKBAccessRoundTripAndLoadErrors(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://127.0.0.1:39273/mcp\nkb_options:\n  work-kb:\n    access: read # reference\n  kb-a:\n    access: write\n")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.ReadOnlyKBs(), ",") != "work-kb" {
		t.Errorf("ReadOnlyKBs = %v", cfg.ReadOnlyKBs())
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if raw := readFile(t, clientconfig.Path(dir)); !strings.Contains(raw, "access: read # reference") {
		t.Errorf("lost the key or its comment:\n%s", raw)
	}
	writeConfigFile(t, dir, "server_url: http://127.0.0.1:39273/mcp\nkb_options:\n  work-kb:\n    access: readonly\n")
	_, err = clientconfig.Load(dir)
	if err == nil || !strings.Contains(err.Error(), ".cartographer.yaml") || !strings.Contains(err.Error(), "knowledge_bases.work-kb.access") ||
		!strings.Contains(err.Error(), `"read"`) || !strings.Contains(err.Error(), `"write"`) {
		t.Errorf("invalid value: %v", err)
	}
}

// AC-transv-1
func TestKBTransversalRoundTrip(t *testing.T) {
	var nilCfg *clientconfig.Config
	if nilCfg.KBTransversal("kb-a") {
		t.Error("nil config is not transversal")
	}
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://127.0.0.1:39273/mcp\nknowledge_bases:\n  kb-a:\n    transversal: true # everywhere\n")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.KBTransversal("kb-a") || cfg.KBTransversal("kb-b") || cfg.KBTransversal("") {
		t.Fatal("transversal not read for kb-a only")
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if raw := readFile(t, clientconfig.Path(dir)); !strings.Contains(raw, "transversal: true # everywhere") {
		t.Errorf("lost the key or its comment:\n%s", raw)
	}
	// off removes the key, the entry and the map; a sibling option survives.
	cfg.SetKBArtifacts("kb-a", false)
	cfg.SetKBTransversal("kb-a", false)
	if o := cfg.KBOptions["kb-a"]; o.Transversal || cfg.KBArtifacts("kb-a") {
		t.Errorf("sibling option lost: %+v", o)
	}
	cfg.SetKBArtifacts("kb-a", true)
	if cfg.KBOptions != nil {
		t.Errorf("map not nil: %v", cfg.KBOptions)
	}
	cfg.SetKBTransversal("kb-b", true)
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.SetKBTransversal("kb-b", false)
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if raw := readFile(t, clientconfig.Path(dir)); strings.Contains(raw, "\n  kb-b:") {
		t.Errorf("false left the entry:\n%s", raw)
	}
	writeConfigFile(t, dir, "server_url: http://127.0.0.1:39273/mcp\nknowledge_bases:\n  kb-a:\n    transversal: maybe\n")
	if _, err := clientconfig.Load(dir); err == nil {
		t.Error("a non-boolean transversal must be a load error")
	}
}

// AC-transv-2
func TestTransversalKBsByScope(t *testing.T) {
	var nilCfg *clientconfig.Config
	if nilCfg.TransversalKBs("claude") != nil {
		t.Error("nil config has none")
	}
	cfg := clientconfig.Default()
	cfg.SetKBTransversal("kb-b", true)
	cfg.SetKBTransversal("kb-a", true)
	if got := cfg.TransversalKBs("claude"); got != nil {
		t.Errorf("provider scope = %v, want none", got)
	}
	if err := cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.TransversalKBs("claude"), ","); got != "kb-a,kb-b" {
		t.Errorf("workspace scope = %q, want sorted", got)
	}
	if got := cfg.TransversalKBs("codex"); got != nil {
		t.Errorf("another provider in provider scope = %v", got)
	}
	cfg.KnownKBs = []string{"kb-a"}
	if got := strings.Join(cfg.TransversalKBs("claude"), ","); got != "kb-a" {
		t.Errorf("restricted to the mounted KBs = %q", got)
	}
	// The flag never widens a provider's binding.
	if kbs, explicit := cfg.BoundOwnKBs("claude"); explicit || len(kbs) != 1 {
		t.Errorf("binding changed: %v %v", kbs, explicit)
	}
}

// AC-transv-3: the flat kb_options layout is a migration input and does not
// accept the key; the client loader has no unknown-key finding, so what is
// pinned is that it is not applied as a setting.
func TestLegacyKBOptionsRejectTransversal(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://127.0.0.1:39273/mcp\nkb_options:\n  kb-a:\n    transversal: true\n")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KBTransversal("kb-a") {
		t.Error("the flat kb_options layout must not set transversal")
	}
	// The warning every command prints once, and the realignment that drops the
	// flag, are in kboptions_legacy_test.go.
}

func TestRenameKBOption(t *testing.T) {
	off := false
	c := &clientconfig.Config{KBOptions: map[string]clientconfig.KBOption{
		"a": {Artifacts: &off, Access: clientconfig.KBAccessRead, Transversal: true},
	}}
	if !c.RenameKBOption("a", "b") {
		t.Fatal("RenameKBOption reported no entry")
	}
	if _, ok := c.KBOptions["a"]; ok {
		t.Errorf("old entry stays: %+v", c.KBOptions)
	}
	if !c.KBTransversal("b") || c.KBAccess("b") != clientconfig.KBAccessRead || c.KBArtifacts("b") {
		t.Errorf("b = %+v, want every option moved", c.KBOptions["b"])
	}
	if c.RenameKBOption("a", "c") {
		t.Error("a missing entry reports true")
	}

	// An entry the new name has wins; the old one goes.
	c = &clientconfig.Config{KBOptions: map[string]clientconfig.KBOption{"a": {Transversal: true}, "b": {Access: clientconfig.KBAccessRead}}}
	c.RenameKBOption("a", "b")
	if c.KBTransversal("b") || c.KBAccess("b") != clientconfig.KBAccessRead || len(c.KBOptions) != 1 {
		t.Errorf("collision: %+v, want only b with access read", c.KBOptions)
	}

	// An empty new name drops the entry and the map once empty.
	c = &clientconfig.Config{KBOptions: map[string]clientconfig.KBOption{"a": {Transversal: true}}}
	c.RenameKBOption("a", "")
	if c.KBOptions != nil {
		t.Errorf("drop: %+v, want nil", c.KBOptions)
	}
}
