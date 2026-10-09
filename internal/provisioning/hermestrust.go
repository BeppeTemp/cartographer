package provisioning

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// hermestrust.go (D364) — Hermes loads a project's skills only after the
// directory is trusted.
//
// Probed on v0.21.5: `.hermes/skills/<n>/` in a project is absent from the
// session until `hermes skills trust <dir>` has run, which records the
// directory under `skills.trusted_project_dirs` in $HERMES_HOME/config.yaml.
// As with Codex (CodexProjectTrusted), the files can be right and the
// projection still inactive, so the client reports it inactive with that
// command as the hint. config.yaml is operator-owned (D141): this only reads.

// HermesConfigPath is where the trust record is read from, given Hermes' home.
func HermesConfigPath(hermesHome string) string {
	return filepath.Join(hermesHome, "config.yaml")
}

// HermesProjectTrusted reports whether $HERMES_HOME/config.yaml lists
// workspaceDir in skills.trusted_project_dirs. A missing, unreadable or
// unparsable config is "not trusted": absence of evidence is never trust, and
// a projection that cannot tell reports itself inactive rather than failing.
func HermesProjectTrusted(hermesHome, workspaceDir string) bool {
	data, err := os.ReadFile(HermesConfigPath(hermesHome))
	if err != nil {
		return false
	}
	var cfg struct {
		Skills struct {
			TrustedProjectDirs []string `yaml:"trusted_project_dirs"`
		} `yaml:"skills"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return false
	}
	want := filepath.Clean(workspaceDir)
	for _, dir := range cfg.Skills.TrustedProjectDirs {
		if dir != "" && filepath.Clean(dir) == want {
			return true
		}
	}
	return false
}
