package main

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// #575: the server profile is resolved per KB and every gap stops startup
// instead of falling back to direct pushes.
func TestServerGitConfigFor(t *testing.T) {
	full := config.GitConfig{Profile: "server", BaseBranch: "main", Forge: "github", GitHubOwner: "example-org",
		GitHubRepository: "wiki", GitHubAPIURL: "https://api.github.com", GitHubTokenEnv: "TEST_CARTO_GH_TOKEN"}

	if cfg, err := serverGitConfigFor(config.KBSpec{}, config.GitConfig{}, "kb-a"); cfg != nil || err != nil {
		t.Fatalf("local default = (%v, %v)", cfg, err)
	}
	if _, err := serverGitConfigFor(config.KBSpec{GitProfile: "srv"}, config.GitConfig{}, "kb-a"); err == nil || !strings.Contains(err.Error(), "unknown git profile") {
		t.Fatalf("unknown profile: err = %v", err)
	}

	t.Setenv("TEST_CARTO_GH_TOKEN", "")
	if _, err := serverGitConfigFor(config.KBSpec{}, full, "kb-a"); err == nil || !strings.Contains(err.Error(), "TEST_CARTO_GH_TOKEN") {
		t.Fatalf("empty token: err = %v", err)
	}
	t.Setenv("TEST_CARTO_GH_TOKEN", "secret")

	cfg, err := serverGitConfigFor(config.KBSpec{}, full, "kb-a")
	if err != nil {
		t.Fatalf("global server profile: %v", err)
	}
	if cfg.WorkingBranch != "cartographer/kb-a" || cfg.BaseBranch != "main" || cfg.Owner != "example-org" || cfg.Forge == nil {
		t.Fatalf("cfg = %+v", cfg)
	}

	// Per-KB overrides win, including turning the profile off for one KB.
	cfg, err = serverGitConfigFor(config.KBSpec{GitWorkingBranch: "bot/kb", GitHubRepository: "other"}, full, "kb-a")
	if err != nil || cfg.WorkingBranch != "bot/kb" || cfg.Repository != "other" {
		t.Fatalf("overrides = (%+v, %v)", cfg, err)
	}
	if cfg, err := serverGitConfigFor(config.KBSpec{GitProfile: "local"}, full, "kb-a"); cfg != nil || err != nil {
		t.Fatalf("per-KB local = (%v, %v)", cfg, err)
	}

	partial := full
	partial.BaseBranch, partial.GitHubOwner = "", ""
	if _, err := serverGitConfigFor(config.KBSpec{}, partial, "kb-a"); err == nil || !strings.Contains(err.Error(), "base_branch, ") || !strings.Contains(err.Error(), "github_owner") {
		t.Fatalf("missing fields: err = %v", err)
	}
	partial = full
	partial.Forge = "gitlab"
	if _, err := serverGitConfigFor(config.KBSpec{}, partial, "kb-a"); err == nil || !strings.Contains(err.Error(), "only github") {
		t.Fatalf("forge: err = %v", err)
	}
}
