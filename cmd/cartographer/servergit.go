package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// serverGitConfigFor resolves the git profile of one KB — the kbs[] override,
// then the global git: value — and, for the server profile (D117), the
// configuration kb.ConfigureServerGit mounts. It returns nil for the local
// profile. Every gap is an error naming the KB: a server asked for a review
// boundary must not fall back to direct pushes (#575).
func serverGitConfigFor(spec config.KBSpec, g config.GitConfig, name string) (*kb.ServerGitConfig, error) {
	profile := strings.ToLower(strings.TrimSpace(firstNonEmpty(spec.GitProfile, g.Profile)))
	switch profile {
	case "", "local":
		return nil, nil
	case "server":
	default:
		return nil, fmt.Errorf("KB %q: unknown git profile %q (local | server)", name, profile)
	}
	cfg := kb.ServerGitConfig{
		BaseBranch:    firstNonEmpty(spec.GitBaseBranch, g.BaseBranch),
		WorkingBranch: firstNonEmpty(spec.GitWorkingBranch, g.WorkingBranch, "cartographer/"+name),
		Owner:         firstNonEmpty(spec.GitHubOwner, g.GitHubOwner),
		Repository:    firstNonEmpty(spec.GitHubRepository, g.GitHubRepository),
	}
	forge := strings.ToLower(firstNonEmpty(spec.GitForge, g.Forge))
	apiURL := firstNonEmpty(spec.GitHubAPIURL, g.GitHubAPIURL)
	tokenEnv := firstNonEmpty(spec.GitHubTokenEnv, g.GitHubTokenEnv)
	var missing []string
	for _, f := range []struct{ key, val string }{
		{"base_branch", cfg.BaseBranch}, {"forge", forge}, {"github_owner", cfg.Owner},
		{"github_repository", cfg.Repository}, {"github_api_url", apiURL}, {"github_token_env", tokenEnv},
	} {
		if f.val == "" {
			missing = append(missing, f.key)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("KB %q: the server git profile requires %s", name, strings.Join(missing, ", "))
	}
	if forge != "github" {
		return nil, fmt.Errorf("KB %q: unsupported git forge %q (only github)", name, forge)
	}
	token := os.Getenv(tokenEnv)
	if token == "" {
		return nil, fmt.Errorf("KB %q: the server git profile reads its GitHub token from $%s, which is empty", name, tokenEnv)
	}
	cfg.Forge = &kb.GitHubForge{APIURL: apiURL, Token: token}
	return &cfg, nil
}
