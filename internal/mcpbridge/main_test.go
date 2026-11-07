package mcpbridge

import (
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// TestMain: see gitxtest.QuietGit.
func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	code := m.Run()
	if elsewhereDir != "" {
		os.RemoveAll(elsewhereDir)
	}
	os.Exit(code)
}

// elsewhereDir is a folder no test runs in, bound for every provider by
// withBinding: the realistic shape of a bridge session in an unbound folder
// (a provider with no binding at all gets the HTTP-equivalent scope instead).
var elsewhereDir string

func withBinding(cfg *clientconfig.Config) *clientconfig.Config {
	if elsewhereDir == "" {
		d, err := os.MkdirTemp("", "bridge-elsewhere-")
		if err != nil {
			panic(err)
		}
		elsewhereDir = d
	}
	for _, p := range []string{"claude", "codex", "kiro", "opencode"} {
		if err := cfg.BindWorkspace(p, elsewhereDir, []string{"kb-a"}); err != nil {
			panic(err)
		}
	}
	return cfg
}
