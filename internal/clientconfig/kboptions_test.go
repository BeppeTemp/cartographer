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
	for _, want := range []string{"# mine", "kb_options:", "artifacts: false # no skills"} {
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
